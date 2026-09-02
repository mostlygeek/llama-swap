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
	"net/netip"
	"os"
	"reflect"
	"slices"
	"sync"
	"sync/atomic"
	"time"
	"unsafe"

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
	if _, err := tailcatlib.ParseConnBlob(pk.Public.ConnBlob()); err != nil {
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
	return string(p.value.Public.ConnBlob())
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
	_, err := tailcatlib.ParseConnBlob(tailcatlib.ConnBlob(blob))
	return err
}

func ConnectionDestination(blob string) (string, error) {
	ci, err := tailcatlib.ParseConnBlob(tailcatlib.ConnBlob(blob))
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

// SourceFromContext returns trusted listener metadata attached by this
// adapter. It cannot be influenced through HTTP forwarding headers.
func SourceFromContext(ctx context.Context) (string, bool) {
	source, ok := ctx.Value(sourceContextKey{}).(string)
	return source, ok && source != ""
}

type authenticatedConn struct {
	net.Conn
	source string
}

type channelListener struct {
	connections chan net.Conn
	done        chan struct{}
	closeOnce   sync.Once
}

func newChannelListener() *channelListener {
	return &channelListener{connections: make(chan net.Conn), done: make(chan struct{})}
}

func (l *channelListener) Accept() (net.Conn, error) {
	select {
	case conn := <-l.connections:
		return conn, nil
	case <-l.done:
		return nil, net.ErrClosed
	}
}

func (l *channelListener) deliver(conn net.Conn) bool {
	select {
	case l.connections <- conn:
		return true
	case <-l.done:
		return false
	}
}

func (l *channelListener) Close() error {
	l.closeOnce.Do(func() { close(l.done) })
	return nil
}

func (l *channelListener) Addr() net.Addr { return tailcatAddr("tailcat:80") }

type tailcatAddr string

func (a tailcatAddr) Network() string { return "tailcat" }
func (a tailcatAddr) String() string  { return string(a) }

type ServerOptions struct {
	PrivateKey     *PrivateKey
	AllowedClients []string
	Handler        http.Handler
	Logger         Logger
}

// Server bridges Tailcat port 80 into a standard net/http server.
type Server struct {
	tailcat *tailcatlib.Server
	http    *http.Server
	ln      *channelListener
	blob    string
	closed  atomic.Bool
}

type ephemeralServerIdentity struct {
	private key.NodePrivate
	region  *tailcfg.DERPRegion
	blob    string
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

	private, region, blob, err := resolveServerIdentity(ctx, opts.PrivateKey)
	if err != nil {
		return nil, err
	}

	ln := newChannelListener()
	runtime := &Server{ln: ln, blob: blob}
	allowed := make([]key.NodePublic, 0, len(opts.AllowedClients))
	for _, raw := range opts.AllowedClients {
		var public key.NodePublic
		if err := public.UnmarshalText([]byte(raw)); err != nil {
			ln.Close()
			return nil, fmt.Errorf("parse allowed Tailcat client: %w", err)
		}
		allowed = append(allowed, public)
	}
	tc := &tailcatlib.Server{
		Key:            private,
		Region:         region,
		AllowedClients: allowed,
		ServedTCPPorts: []filter.PortRange{{First: HTTPPort, Last: HTTPPort}},
		Logf:           logFunc(opts.Logger, "tailcat server: "),
	}
	tc.OnTCP = func(port uint16) func(net.Conn) {
		if port != HTTPPort {
			return nil
		}
		return func(conn net.Conn) {
			public, ok := resolveRemoteNodeKey(tc, conn.RemoteAddr())
			if !ok {
				if opts.Logger != nil {
					opts.Logger.Warnf("tailcat server: rejecting connection with unresolved identity from %v", conn.RemoteAddr())
				}
				conn.Close()
				return
			}
			authenticated := &authenticatedConn{Conn: conn, source: "tc:" + public.String()}
			if !ln.deliver(authenticated) {
				conn.Close()
			}
		}
	}

	if err := tc.Start(); err != nil {
		ln.Close()
		return nil, fmt.Errorf("start Tailcat server: %w", err)
	}
	runtime.tailcat = tc

	// Tailcat's server token embeds the resolved relay. Stable key files keep
	// their original compact token (including a fixed RegionID); ephemeral
	// identities cache the first resolved token for process-lifetime reloads.
	if opts.PrivateKey == nil {
		processServerIdentity.Lock()
		if processServerIdentity.value == nil {
			resolved, parseErr := tailcatlib.ParseConnBlob(tc.ConnBlob())
			if parseErr == nil && len(resolved.Region) > 0 {
				processServerIdentity.value = &ephemeralServerIdentity{
					private: private,
					region:  resolved.Region[0],
					blob:    string(tc.ConnBlob()),
				}
				runtime.blob = processServerIdentity.value.blob
			}
		}
		processServerIdentity.Unlock()
	}
	if runtime.blob == "" {
		runtime.blob = string(tc.ConnBlob())
	}
	runtime.http = newHTTPServer(opts)
	go func() {
		if err := runtime.http.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) && !errors.Is(err, net.ErrClosed) && opts.Logger != nil {
			opts.Logger.Warnf("tailcat HTTP server stopped: %v", err)
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
				return context.WithValue(ctx, sourceContextKey{}, authenticated.source)
			}
			return ctx
		},
	}
}

func resolveServerIdentity(ctx context.Context, saved *PrivateKey) (key.NodePrivate, *tailcfg.DERPRegion, string, error) {
	if saved != nil {
		ci := saved.value.Public
		if err := ci.Expand(ctx, tailcatlib.ExpandForServer); err != nil {
			return key.NodePrivate{}, nil, "", fmt.Errorf("resolve Tailcat server relay: %w", err)
		}
		if len(ci.Region) == 0 {
			return key.NodePrivate{}, nil, "", errors.New("resolve Tailcat server relay: no region selected")
		}
		blob := ""
		if saved.value.Public.RegionID != -1 {
			blob = string(saved.value.Public.ConnBlob())
		}
		return saved.value.Private, ci.Region[0], blob, nil
	}

	processServerIdentity.Lock()
	defer processServerIdentity.Unlock()
	if cached := processServerIdentity.value; cached != nil {
		return cached.private, cached.region, cached.blob, nil
	}
	private := key.NewNode()
	ci := tailcatlib.ConnInfo{
		ServerPublic:      tailcatlib.NodePublic{NodePublic: private.Public()},
		ServerDiscoPublic: tailcatlib.DiscoPublicForNode(private),
		RegionID:          -1,
	}
	if err := ci.Expand(ctx, tailcatlib.ExpandForServer); err != nil {
		return key.NodePrivate{}, nil, "", fmt.Errorf("select Tailcat server relay: %w", err)
	}
	if len(ci.Region) == 0 {
		return key.NodePrivate{}, nil, "", errors.New("select Tailcat server relay: no region selected")
	}
	return private, ci.Region[0], "", nil
}

func resolveRemoteNodeKey(server *tailcatlib.Server, addr net.Addr) (key.NodePublic, bool) {
	var zero key.NodePublic
	host, _, err := net.SplitHostPort(addr.String())
	if err != nil {
		return zero, false
	}
	ip, err := netip.ParseAddr(host)
	if err != nil {
		return zero, false
	}
	status := server.Status()
	if status != nil {
		for public, peer := range status.Peer {
			if peer != nil && slices.Contains(peer.TailscaleIPs, ip) {
				return public, true
			}
		}
	}

	// Tailcat v0.4.0's public Status method creates an ipnstate builder with
	// peer collection disabled, so Status().Peer is always empty even after a
	// successful meow handshake. Until Tailcat exposes the authenticated key
	// on OnTCP (or fixes Status), read its authenticated client registry under
	// its own mutex. This dependency is deliberately isolated here and guarded
	// by the exact v0.4.0 module pin and the local-DERP integration test.
	if public, ok := tailcatV040RemoteNodeKey(server, ip); ok {
		return public, true
	}
	return zero, false
}

func tailcatV040RemoteNodeKey(server *tailcatlib.Server, ip netip.Addr) (key.NodePublic, bool) {
	var zero key.NodePublic
	serverValue := reflect.ValueOf(server)
	if !serverValue.IsValid() || serverValue.IsNil() {
		return zero, false
	}
	lbPointer := serverValue.Elem().FieldByName("lb")
	if !lbPointer.IsValid() || lbPointer.IsNil() {
		return zero, false
	}
	lb := lbPointer.Elem()
	mutexValue := lb.FieldByName("mu")
	clients := lb.FieldByName("clients")
	if !mutexValue.IsValid() || !mutexValue.CanAddr() || !clients.IsValid() || clients.Kind() != reflect.Map {
		return zero, false
	}

	mutex := (*sync.Mutex)(unsafe.Pointer(mutexValue.UnsafeAddr()))
	mutex.Lock()
	defer mutex.Unlock()
	iter := clients.MapRange()
	for iter.Next() {
		nodeValue := iter.Value()
		if nodeValue.Kind() != reflect.Pointer || nodeValue.IsNil() {
			continue
		}
		node := (*tailcfg.Node)(unsafe.Pointer(nodeValue.Pointer()))
		for _, prefix := range node.Addresses {
			if prefix.Addr() == ip {
				return node.Key, true
			}
		}
	}
	return zero, false
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
		Server: tailcatlib.ConnBlob(blob),
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
