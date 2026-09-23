// Package tailcat isolates llama-swap from Tailcat's unstable API.
// No package outside this adapter and config validation should need to know
// how Tailcat represents listeners, clients, regions, or connection blobs.
package tailcat

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	tailcatlib "github.com/tailscale/tailcat"
	"tailscale.com/tailcfg"
	"tailscale.com/types/key"
	"tailscale.com/wgengine/filter"
)

const (
	HTTPPort                uint16 = 80
	serverDrainTimeout             = time.Second
	serverReadHeaderTimeout        = time.Second
)

// PrivateKey is the adapter-owned representation of a validated Tailcat key
// file. Keeping the concrete Tailcat type private localizes upstream API churn.
type PrivateKey struct {
	value tailcatlib.PrivateKey
}

func LoadPrivateKey(path string, requireRegion bool) (*PrivateKey, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read %q: %w", path, err)
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	var pk tailcatlib.PrivateKey
	if err := dec.Decode(&pk); err != nil {
		return nil, fmt.Errorf("parse %q as Tailcat PrivateKey JSON: %w", path, err)
	}
	var trailing any
	if err := dec.Decode(&trailing); err != io.EOF {
		if err == nil {
			return nil, fmt.Errorf("parse %q as Tailcat PrivateKey JSON: trailing JSON value", path)
		}
		return nil, fmt.Errorf("parse %q as Tailcat PrivateKey JSON: %w", path, err)
	}
	if pk.Private.IsZero() {
		return nil, fmt.Errorf("parse %q as Tailcat PrivateKey JSON: private key is missing", path)
	}
	if pk.Public.ServerPublic.NodePublic.IsZero() || pk.Private.Public() != pk.Public.ServerPublic.NodePublic {
		return nil, fmt.Errorf("parse %q as Tailcat PrivateKey JSON: public key does not match private key", path)
	}
	if !pk.Public.ServerDiscoPublic.Equal(tailcatlib.DiscoPublicForNode(pk.Private)) {
		return nil, fmt.Errorf("parse %q as Tailcat PrivateKey JSON: discovery public key does not match private key", path)
	}
	if requireRegion && pk.Public.RegionID == 0 && len(pk.Public.Region) == 0 {
		return nil, fmt.Errorf("parse %q as Tailcat server key: relay region is missing", path)
	}
	if _, err := tailcatlib.ParseAddr(pk.Public.Addr()); err != nil {
		return nil, fmt.Errorf("parse %q as Tailcat PrivateKey JSON: invalid public connection data: %w", path, err)
	}
	return &PrivateKey{value: pk}, nil
}

func (p *PrivateKey) Identity() string {
	if p == nil {
		return ""
	}
	return p.value.Private.Public().String()
}

func (p *PrivateKey) ConnectionBlob() string {
	if p == nil {
		return ""
	}
	return string(p.value.Public.Addr())
}

func ValidateNodePublic(raw string) (string, error) {
	var public key.NodePublic
	if err := public.UnmarshalText([]byte(raw)); err != nil || public.IsZero() {
		if err == nil {
			err = errors.New("zero key")
		}
		return "", err
	}
	return public.String(), nil
}

func ValidateConnectionBlob(blob string) error {
	_, err := tailcatlib.ParseAddr(tailcatlib.Addr(blob))
	return err
}

func ConnectionDestination(blob string) (string, error) {
	ci, err := tailcatlib.ParseAddr(tailcatlib.Addr(blob))
	if err != nil {
		return "", err
	}
	return ci.ServerPublic.NodePublic.String(), nil
}

type Logger interface {
	Debugf(string, ...any)
	Warnf(string, ...any)
}

func logFunc(logger Logger, prefix string) func(string, ...any) {
	return func(format string, args ...any) {
		if logger != nil {
			logger.Debugf(prefix+format, args...)
		}
	}
}

type sourceContextKey struct{}

// sourcePrefix marks a request source as a Tailcat node key.
const sourcePrefix = "tc:"

// SourceFromContext returns trusted listener metadata attached by this
// adapter. It cannot be influenced through HTTP forwarding headers.
func SourceFromContext(ctx context.Context) (string, bool) {
	source, ok := ctx.Value(sourceContextKey{}).(string)
	return source, ok && source != ""
}

// ContextWithNodeKey attaches an authenticated client node key to ctx. The
// listener calls it for every accepted Tailcat connection.
func ContextWithNodeKey(ctx context.Context, nodeKey string) context.Context {
	return context.WithValue(ctx, sourceContextKey{}, sourcePrefix+nodeKey)
}

// NodeKeyFromContext returns the authenticated client node key attached by
// the listener, in canonical "nodekey:..." form.
func NodeKeyFromContext(ctx context.Context) (string, bool) {
	source, ok := SourceFromContext(ctx)
	if !ok {
		return "", false
	}
	nodeKey, ok := strings.CutPrefix(source, sourcePrefix)
	return nodeKey, ok && nodeKey != ""
}

type authenticatedConn struct {
	net.Conn
	nodeKey string
}

// authenticatedListener wraps Tailcat's port listener and tags each accepted
// connection with the client's node key before net/http sees it.
type authenticatedListener struct {
	net.Listener
	server  *tailcatlib.Server
	runtime *Server
}

func (l *authenticatedListener) Accept() (net.Conn, error) {
	for {
		conn, err := l.Listener.Accept()
		if err != nil {
			return nil, err
		}
		public, ok := resolveRemoteNodeKey(l.server, conn.LocalAddr(), conn.RemoteAddr())
		if !ok {
			if logger := l.runtime.currentLogger(); logger != nil {
				logger.Warnf("tailcat server: rejecting connection with unresolved identity from %v", conn.RemoteAddr())
			}
			conn.Close()
			continue
		}
		return &authenticatedConn{Conn: conn, nodeKey: public.String()}, nil
	}
}

// ServerOptions configures a Tailcat listener. Client authorization is not
// enforced here: the listener accepts any client holding the connection token
// and tags requests with the client's node key (see NodeKeyFromContext) so the
// HTTP handler can apply an allowlist that changes on config reload.
type ServerOptions struct {
	PrivateKey *PrivateKey
	Handler    http.Handler
	Logger     Logger
}

// Server bridges Tailcat port 80 into a standard net/http server.
type Server struct {
	tailcat *tailcatlib.Server
	http    *http.Server
	blob    string
	closed  atomic.Bool
	logger  atomic.Pointer[Logger]
}

type ephemeralServerIdentity struct {
	private             key.NodePrivate
	presharedKey        tailcatlib.PresharedKey
	disablePresharedKey bool
	region              *tailcfg.DERPRegion
	blob                string
}

var processServerIdentity struct {
	sync.Mutex
	value *ephemeralServerIdentity
}

// Start starts a Tailcat listener. A nil PrivateKey uses one generated
// once per process and retains the selected region across listener restarts.
func Start(ctx context.Context, opts ServerOptions) (*Server, error) {
	if opts.Handler == nil {
		return nil, errors.New("tailcat handler is required")
	}

	identity, err := resolveServerIdentity(ctx, opts.PrivateKey)
	if err != nil {
		return nil, err
	}

	runtime := &Server{blob: identity.blob}
	runtime.SetLogger(opts.Logger)
	tc := &tailcatlib.Server{
		Key:                 identity.private,
		PresharedKey:        identity.presharedKey,
		DisablePresharedKey: identity.disablePresharedKey,
		Region:              identity.region,
		ServedTCPPorts:      []filter.PortRange{{First: HTTPPort, Last: HTTPPort}},
		Logf: func(format string, args ...any) {
			if logger := runtime.currentLogger(); logger != nil {
				logger.Debugf("tailcat server: "+format, args...)
			}
		},
	}

	// Listen starts the Tailcat server and claims the HTTP port.
	tcListener, err := tc.Listen(ctx, "tcp", fmt.Sprintf(":%d", HTTPPort))
	if err != nil {
		tc.Close()
		return nil, fmt.Errorf("start Tailcat server: %w", err)
	}
	ln := &authenticatedListener{Listener: tcListener, server: tc, runtime: runtime}
	runtime.tailcat = tc

	// Tailcat's server token embeds the resolved relay. Stable key files keep
	// their original compact token (including a fixed RegionID); ephemeral
	// identities cache the first resolved token for process-lifetime reloads.
	if opts.PrivateKey == nil {
		processServerIdentity.Lock()
		if processServerIdentity.value == nil {
			resolved, parseErr := tailcatlib.ParseAddr(tc.TailcatAddr())
			if parseErr == nil && len(resolved.Region) > 0 {
				processServerIdentity.value = &ephemeralServerIdentity{
					private:      identity.private,
					presharedKey: identity.presharedKey,
					region:       resolved.Region[0],
					blob:         string(tc.TailcatAddr()),
				}
				runtime.blob = processServerIdentity.value.blob
			}
		}
		processServerIdentity.Unlock()
	}
	if runtime.blob == "" {
		runtime.blob = string(tc.TailcatAddr())
	}
	runtime.http = newHTTPServer(opts)
	go func() {
		err := runtime.http.Serve(ln)
		if logger := runtime.currentLogger(); err != nil && !errors.Is(err, http.ErrServerClosed) && !errors.Is(err, net.ErrClosed) && logger != nil {
			logger.Warnf("tailcat HTTP server stopped: %v", err)
		}
	}()
	return runtime, nil
}

func newHTTPServer(opts ServerOptions) *http.Server {
	return &http.Server{
		Handler:           opts.Handler,
		ReadHeaderTimeout: serverReadHeaderTimeout,
		ConnContext: func(ctx context.Context, conn net.Conn) context.Context {
			if authenticated, ok := conn.(*authenticatedConn); ok {
				return ContextWithNodeKey(ctx, authenticated.nodeKey)
			}
			return ctx
		},
	}
}

func resolveServerIdentity(ctx context.Context, saved *PrivateKey) (ephemeralServerIdentity, error) {
	if saved != nil {
		ci := saved.value.Public
		if err := ci.Expand(ctx, tailcatlib.ExpandForServer); err != nil {
			return ephemeralServerIdentity{}, fmt.Errorf("resolve Tailcat server relay: %w", err)
		}
		if len(ci.Region) == 0 {
			return ephemeralServerIdentity{}, errors.New("resolve Tailcat server relay: no region selected")
		}
		blob := ""
		if saved.value.Public.RegionID != -1 {
			blob = string(saved.value.Public.Addr())
		}
		return ephemeralServerIdentity{
			private:             saved.value.Private,
			presharedKey:        saved.value.Public.PresharedKey,
			disablePresharedKey: saved.value.Public.PresharedKey.IsZero(),
			region:              ci.Region[0],
			blob:                blob,
		}, nil
	}

	processServerIdentity.Lock()
	defer processServerIdentity.Unlock()
	if cached := processServerIdentity.value; cached != nil {
		return *cached, nil
	}
	private := key.NewNode()
	presharedKey := tailcatlib.NewPresharedKey()
	ci := tailcatlib.ConnInfo{
		ServerPublic:      tailcatlib.NodePublic{NodePublic: private.Public()},
		ServerDiscoPublic: tailcatlib.DiscoPublicForNode(private),
		PresharedKey:      presharedKey,
		RegionID:          -1,
	}
	if err := ci.Expand(ctx, tailcatlib.ExpandForServer); err != nil {
		return ephemeralServerIdentity{}, fmt.Errorf("select Tailcat server relay: %w", err)
	}
	if len(ci.Region) == 0 {
		return ephemeralServerIdentity{}, errors.New("select Tailcat server relay: no region selected")
	}
	return ephemeralServerIdentity{
		private:      private,
		presharedKey: presharedKey,
		region:       ci.Region[0],
	}, nil
}

// resolveRemoteNodeKey returns the node key WireGuard authenticated for an
// accepted connection, using Tailcat's own client registry via PeerEnv.
func resolveRemoteNodeKey(server *tailcatlib.Server, local, remote net.Addr) (key.NodePublic, bool) {
	var zero key.NodePublic
	for _, kv := range server.PeerEnv(local, remote) {
		raw, ok := strings.CutPrefix(kv, "TAILCAT_PEER_KEY=")
		if !ok {
			continue
		}
		var public key.NodePublic
		if err := public.UnmarshalText([]byte(raw)); err != nil || public.IsZero() {
			return zero, false
		}
		return public, true
	}
	return zero, false
}

// SetLogger replaces the transport logger of a running listener. A nil logger
// silences Tailcat diagnostics. It lets config reloads toggle tailcat.debug.
func (s *Server) SetLogger(logger Logger) {
	if s == nil {
		return
	}
	if logger == nil {
		s.logger.Store(nil)
		return
	}
	s.logger.Store(&logger)
}

func (s *Server) currentLogger() Logger {
	if p := s.logger.Load(); p != nil {
		return *p
	}
	return nil
}

func (s *Server) Address() string {
	if s == nil {
		return ""
	}
	return s.blob
}

// Close stops accepting requests, drains HTTP and Tailcat TCP state, then
// closes the WireGuard engine and relay connection.
func (s *Server) Close(ctx context.Context) error {
	if s == nil || !s.closed.CompareAndSwap(false, true) {
		return nil
	}
	var errs []error
	if s.http != nil {
		if err := s.http.Shutdown(ctx); err != nil {
			errs = append(errs, err)
		}
	}
	if s.tailcat != nil {
		drainCtx, cancel := context.WithTimeout(ctx, serverDrainTimeout)
		err := s.tailcat.DrainTCP(drainCtx)
		cancel()
		if err != nil &&
			!errors.Is(err, context.DeadlineExceeded) &&
			!errors.Is(err, context.Canceled) {
			errs = append(errs, err)
		}
		if err := s.tailcat.Close(); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

// Client is a reusable outbound Tailcat identity and network stack.
type Client struct {
	client *tailcatlib.Client
	closed atomic.Bool
	used   atomic.Bool
}

var processPeerKeys struct {
	sync.Mutex
	keys map[string]key.NodePrivate
}

func NewClient(peerID, blob string, saved *PrivateKey, logger Logger) *Client {
	var private key.NodePrivate
	if saved != nil {
		private = saved.value.Private
	} else {
		processPeerKeys.Lock()
		if processPeerKeys.keys == nil {
			processPeerKeys.keys = make(map[string]key.NodePrivate)
		}
		private = processPeerKeys.keys[peerID]
		if private.IsZero() {
			private = key.NewNode()
			processPeerKeys.keys[peerID] = private
		}
		processPeerKeys.Unlock()
	}
	return &Client{client: &tailcatlib.Client{
		Server: tailcatlib.Addr(blob),
		Key:    private,
		Logf:   logFunc(logger, "tailcat client "+peerID+": "),
	}}
}

func (c *Client) DialContext(ctx context.Context, _, _ string) (net.Conn, error) {
	if c == nil || c.closed.Load() {
		return nil, net.ErrClosed
	}
	conn, err := c.client.DialTCPPort(ctx, HTTPPort)
	if err == nil {
		c.used.Store(true)
	}
	return conn, err
}

// PublicKey returns the canonical node-key string without exposing Tailcat's
// concrete key type outside the adapter.
func (c *Client) PublicKey() string { return c.client.PublicKey().String() }

func (c *Client) Close(ctx context.Context) error {
	if c == nil || !c.closed.CompareAndSwap(false, true) {
		return nil
	}
	var errs []error
	if c.used.Load() {
		if err := c.client.DrainTCP(ctx); err != nil && ctx.Err() == nil {
			errs = append(errs, err)
		}
	}
	if err := c.client.Close(); err != nil {
		errs = append(errs, err)
	}
	return errors.Join(errs...)
}

// CloseWithTimeout is convenient for router shutdown paths that own only a
// duration budget rather than a context.
func (c *Client) CloseWithTimeout(timeout time.Duration) error {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	return c.Close(ctx)
}
