package router

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httputil"
	"runtime"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/mostlygeek/llama-swap/internal/config"
	"github.com/mostlygeek/llama-swap/internal/hint"
	"github.com/mostlygeek/llama-swap/internal/logmon"
	"github.com/mostlygeek/llama-swap/internal/swaputil"
	"github.com/mostlygeek/llama-swap/internal/tailcat"
)

var _ PeerRouter = (*Peer)(nil)

type peerMember struct {
	peerID       string
	reverseProxy *httputil.ReverseProxy
	transport    *http.Transport
	tailcat      *tailcat.Client
	apiKey       string
	// upstream is the provider behind the peer, guessed once from its proxy
	// host. hint.Unknown when no detection rule matches.
	upstream hint.Upstream
	// refresh hands the peer's catalog loop a manual refresh. It holds at
	// most one, the pending one, so sends never block. nil for peers
	// without model discovery.
	refresh chan *catalogRefresh
	// pending is the manual refresh waiting for or being served by the
	// catalog loop; concurrent callers share it. Guarded by refreshMu.
	refreshMu sync.Mutex
	pending   *catalogRefresh
}

type Peer struct {
	cfg     config.Config
	logger  *logmon.Monitor
	byID    map[string]*peerMember
	members []*peerMember

	catalogMu     sync.Mutex
	catalogStatus map[string]*PeerCatalogStatus
	// minManualRefresh is how soon after a catalog fetch a manual refresh
	// may fetch again. Sooner requests get the last fetch's result.
	minManualRefresh time.Duration

	shutdownCtx  context.Context
	shutdownFn   context.CancelFunc
	shuttingDown atomic.Bool
	inflight     sync.WaitGroup
}

func NewPeer(cfg config.Config, logger *logmon.Monitor) (*Peer, error) {
	if err := config.ValidatePeerNamespace(cfg); err != nil {
		return nil, err
	}

	peers := cfg.Peers
	byID := make(map[string]*peerMember, len(peers))

	peerIDs := make([]string, 0, len(peers))
	tailcatClients := make(map[string]*tailcat.Client)
	members := make([]*peerMember, 0, len(peers))
	for peerID := range peers {
		peerIDs = append(peerIDs, peerID)
	}
	sort.Strings(peerIDs)

	for _, peerID := range peerIDs {
		peer := peers[peerID]
		var tailcatClient *tailcat.Client
		proxyFromEnvironment := http.ProxyFromEnvironment
		dialContext := (&net.Dialer{
			Timeout:   time.Duration(peer.Timeouts.Connect) * time.Second,
			KeepAlive: time.Duration(peer.Timeouts.KeepAlive) * time.Second,
		}).DialContext
		if _, blob, privateKey, found := peer.Tailcat(); found {
			clientKey := "ephemeral:" + peerID
			if privateKey != nil {
				clientKey = privateKey.Identity() + ":" + blob
			}
			tailcatClient = tailcatClients[clientKey]
			if tailcatClient == nil {
				tailcatClient = tailcat.NewClient(peerID, blob, privateKey, logger)
				tailcatClients[clientKey] = tailcatClient
			}
			proxyFromEnvironment = nil
			dialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
				if peer.Timeouts.Connect > 0 {
					var cancel context.CancelFunc
					ctx, cancel = context.WithTimeout(ctx, time.Duration(peer.Timeouts.Connect)*time.Second)
					defer cancel()
				}
				return tailcatClient.DialContext(ctx, network, address)
			}
		}

		// Tailcat needs a new dial per request so a restarted server cannot
		// strand an HTTP request on a connection from the old server.
		peerTransport := &http.Transport{
			Proxy:                 proxyFromEnvironment,
			DialContext:           dialContext,
			DisableKeepAlives:     tailcatClient != nil,
			TLSHandshakeTimeout:   time.Duration(peer.Timeouts.TLSHandshake) * time.Second,
			ResponseHeaderTimeout: time.Duration(peer.Timeouts.ResponseHeader) * time.Second,
			ExpectContinueTimeout: time.Duration(peer.Timeouts.ExpectContinue) * time.Second,
			ForceAttemptHTTP2:     true,
			MaxIdleConns:          100,
			MaxIdleConnsPerHost:   10,
			IdleConnTimeout:       time.Duration(peer.Timeouts.IdleConn) * time.Second,
		}

		reverseProxy := &httputil.ReverseProxy{
			Transport: peerTransport,
			Rewrite: func(r *httputil.ProxyRequest) {
				r.SetURL(peer.ProxyURL)
				r.Out.Host = r.Out.URL.Host
				// The peer is a server-to-server hop. Some APIs (Anthropic)
				// reject requests carrying a browser Origin; see issue #1214.
				swaputil.StripBrowserRequestHeaders(r.Out.Header)
			},
		}

		reverseProxy.ModifyResponse = func(resp *http.Response) error {
			// The peer is another llama-swap that already applied its own CORS
			// policy. ReverseProxy adds rather than replaces headers, so keep
			// only this instance's; see issue #85.
			swaputil.StripUpstreamCORSHeaders(resp.Header)

			if strings.Contains(strings.ToLower(resp.Header.Get("Content-Type")), "text/event-stream") {
				resp.Header.Set("X-Accel-Buffering", "no")
			}
			return nil
		}

		reverseProxy.ErrorHandler = func(w http.ResponseWriter, r *http.Request, err error) {
			// A cancelled request is not a peer failure, so keep it out of the
			// warning stream whether or not the sentinel applies below.
			if errors.Is(err, context.Canceled) || r.Context().Err() != nil {
				logger.Debugf("peer %s: request cancelled: %v", peerID, err)
			} else {
				logger.Warnf("peer %s: proxy error: %v", peerID, err)
			}

			// Only a client that actually hung up gets the recorded-only
			// sentinel (#1029). A request cancelled server-side still has a
			// client waiting for an answer.
			if swaputil.MarkClientClosed(w, r) || swaputil.ResponseStarted(w) {
				return
			}

			errMsg := fmt.Sprintf("peer proxy error: %v", err)
			if runtime.GOOS == "darwin" && strings.Contains(err.Error(), "connect: no route to host") {
				errMsg += " (hint: on macOS, check System Settings > Privacy & Security > Local Network permissions)"
			}
			swaputil.SendResponse(w, r, http.StatusBadGateway, errMsg)
		}

		pp := &peerMember{
			peerID:       peerID,
			reverseProxy: reverseProxy,
			transport:    peerTransport,
			tailcat:      tailcatClient,
			apiKey:       peer.ApiKey,
			upstream:     hint.ForURL(peer.ProxyURL),
		}
		if peer.Discovery() {
			pp.refresh = make(chan *catalogRefresh, 1)
		}
		members = append(members, pp)
		byID[peerID] = pp
	}

	shutdownCtx, shutdownFn := context.WithCancel(context.Background())

	r := &Peer{
		cfg:         cfg,
		logger:      logger,
		byID:        byID,
		members:     members,
		shutdownCtx: shutdownCtx,
		shutdownFn:  shutdownFn,

		minManualRefresh: peerCatalogMinManualRefresh,
	}
	return r, nil
}

func (r *Peer) Handles(model string) bool {
	_, _, ok := r.route(model)
	return ok
}

// route resolves a model name to the peer member serving it and the peer's
// own model ID. It reads the config's peer index, which catalog refreshes
// update while the router runs.
func (r *Peer) route(model string) (*peerMember, string, bool) {
	peerID, modelID, found := r.cfg.ResolvePeerModel(model)
	if !found {
		return nil, "", false
	}
	member, ok := r.byID[peerID]
	return member, modelID, ok
}

func (r *Peer) Shutdown(timeout time.Duration) error {
	if !r.shuttingDown.CompareAndSwap(false, true) {
		return fmt.Errorf("shutdown already in progress")
	}

	if timeout == 0 {
		r.shutdownFn()
		r.inflight.Wait()
		return r.closeTransports(time.Second)
	}

	deadline := time.Now().Add(timeout)
	deadlineCtx, cancelDeadline := context.WithDeadline(context.Background(), deadline)
	defer cancelDeadline()

	done := make(chan struct{})
	go func() {
		r.inflight.Wait()
		close(done)
	}()

	select {
	case <-done:
		r.shutdownFn()
		return r.closeTransports(max(time.Until(deadline), 0))
	case <-deadlineCtx.Done():
		r.shutdownFn()
		select {
		case <-done:
		case <-deadlineCtx.Done():
		}
		return errors.Join(fmt.Errorf("peer shutdown timed out after %v", timeout), r.closeTransports(max(time.Until(deadline), 0)))
	}
}

func (r *Peer) closeTransports(timeout time.Duration) error {
	type closeResult struct {
		index int
		err   error
	}

	deadline := time.Now().Add(timeout)
	closedTailcat := make(map[*tailcat.Client]struct{})
	errs := make([]error, len(r.members))
	done := make(chan closeResult, len(r.members))
	for i, member := range r.members {
		closeTailcat := false
		if member.tailcat != nil {
			if _, closed := closedTailcat[member.tailcat]; !closed {
				closedTailcat[member.tailcat] = struct{}{}
				closeTailcat = true
			}
		}

		go func() {
			var err error
			member.transport.CloseIdleConnections()
			if closeTailcat {
				if closeErr := member.tailcat.CloseWithTimeout(max(time.Until(deadline), 0)); closeErr != nil {
					err = fmt.Errorf("peer %s: %w", member.peerID, closeErr)
				}
			}
			done <- closeResult{index: i, err: err}
		}()
	}

	timer := time.NewTimer(max(time.Until(deadline), 0))
	defer timer.Stop()
	for range r.members {
		select {
		case result := <-done:
			errs[result.index] = result.err
		case <-timer.C:
			return errors.Join(errs...)
		}
	}
	return errors.Join(errs...)
}

func (r *Peer) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	if r.shuttingDown.Load() {
		swaputil.SendError(w, req, fmt.Errorf("peer proxy is shutting down"))
		return
	}
	r.inflight.Add(1)
	defer r.inflight.Done()

	data, err := swaputil.FetchContext(req, r.cfg)
	if err != nil {
		swaputil.SendError(w, req, err)
		return
	}

	pp, peerModelID, found := r.route(data.ModelID)
	if !found {
		r.logger.Warnf("peer model not found: %s", data.ModelID)
		swaputil.SendError(w, req, ErrNoPeerModelFound)
		return
	}

	r.logger.Debugf("peer: routing model %s to peer %s as %s", data.ModelID, pp.peerID, peerModelID)

	if h := hint.From(req.Context()); h != nil {
		h.Route = hint.RoutePeer
		h.PeerID = pp.peerID
		h.Upstream = pp.upstream
	}

	if data.Model != peerModelID {
		req, err = swaputil.ReplaceRequestModel(req, data.Model, peerModelID)
		if err != nil {
			swaputil.SendResponse(w, req, http.StatusBadRequest, err.Error())
			return
		}
	}

	if pp.apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+pp.apiKey)
		req.Header.Set("x-api-key", pp.apiKey)
	}

	// Cancel the proxy request when the client disconnects or shutdown times out.
	// Deriving from the request covers the client half directly and keeps the
	// request's context values — notably the client context that tells a real
	// disconnect apart from a server-side cancel. AfterFunc links the unrelated
	// shutdown context in without a goroutine leak.
	ctx, cancel := context.WithCancel(req.Context())
	stopShutdown := context.AfterFunc(r.shutdownCtx, cancel)
	req = req.WithContext(ctx)

	pp.reverseProxy.ServeHTTP(w, req)

	stopShutdown()
	cancel()
}
