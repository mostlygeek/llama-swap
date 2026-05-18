package router

import (
	"context"
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

	"github.com/mostlygeek/llama-swap/internal/logmon"
	"github.com/mostlygeek/llama-swap/proxy/config"
)

type peerMember struct {
	peerID       string
	reverseProxy *httputil.ReverseProxy
	apiKey       string
}

type PeerRouter struct {
	config config.PeerDictionaryConfig
	logger *logmon.Monitor
	peers  map[string]*peerMember

	shutdownCtx  context.Context
	shutdownFn   context.CancelFunc
	shuttingDown atomic.Bool
	inflight     sync.WaitGroup
}

func NewPeer(peers config.PeerDictionaryConfig, logger *logmon.Monitor) (*PeerRouter, error) {
	modelMap := make(map[string]*peerMember)

	peerIDs := make([]string, 0, len(peers))
	for peerID := range peers {
		peerIDs = append(peerIDs, peerID)
	}
	sort.Strings(peerIDs)

	for _, peerID := range peerIDs {
		peer := peers[peerID]

		peerTransport := &http.Transport{
			Proxy: http.ProxyFromEnvironment,
			DialContext: (&net.Dialer{
				Timeout:   time.Duration(peer.Timeouts.Connect) * time.Second,
				KeepAlive: time.Duration(peer.Timeouts.KeepAlive) * time.Second,
			}).DialContext,
			TLSHandshakeTimeout:   time.Duration(peer.Timeouts.TLSHandshake) * time.Second,
			ResponseHeaderTimeout: time.Duration(peer.Timeouts.ResponseHeader) * time.Second,
			ExpectContinueTimeout: time.Duration(peer.Timeouts.ExpectContinue) * time.Second,
			ForceAttemptHTTP2:     true,
			MaxIdleConns:          100,
			MaxIdleConnsPerHost:   10,
			IdleConnTimeout:       time.Duration(peer.Timeouts.IdleConn) * time.Second,
		}

		reverseProxy := httputil.NewSingleHostReverseProxy(peer.ProxyURL)
		reverseProxy.Transport = peerTransport

		originalDirector := reverseProxy.Director
		reverseProxy.Director = func(req *http.Request) {
			originalDirector(req)
			req.Host = req.URL.Host
		}

		reverseProxy.ModifyResponse = func(resp *http.Response) error {
			if strings.Contains(strings.ToLower(resp.Header.Get("Content-Type")), "text/event-stream") {
				resp.Header.Set("X-Accel-Buffering", "no")
			}
			return nil
		}

		reverseProxy.ErrorHandler = func(w http.ResponseWriter, r *http.Request, err error) {
			logger.Warnf("peer %s: proxy error: %v", peerID, err)
			errMsg := fmt.Sprintf("peer proxy error: %v", err)
			if runtime.GOOS == "darwin" && strings.Contains(err.Error(), "connect: no route to host") {
				errMsg += " (hint: on macOS, check System Settings > Privacy & Security > Local Network permissions)"
			}
			http.Error(w, errMsg, http.StatusBadGateway)
		}

		pp := &peerMember{
			peerID:       peerID,
			reverseProxy: reverseProxy,
			apiKey:       peer.ApiKey,
		}

		for _, modelID := range peer.Models {
			if _, found := modelMap[modelID]; found {
				logger.Warnf("peer %s: model %s already mapped to another peer, skipping", peerID, modelID)
				continue
			}
			modelMap[modelID] = pp
		}
	}

	shutdownCtx, shutdownFn := context.WithCancel(context.Background())

	return &PeerRouter{
		config:      peers,
		logger:      logger,
		peers:       modelMap,
		shutdownCtx: shutdownCtx,
		shutdownFn:  shutdownFn,
	}, nil
}

func (r *PeerRouter) Shutdown(timeout time.Duration) error {
	if !r.shuttingDown.CompareAndSwap(false, true) {
		return fmt.Errorf("shutdown already in progress")
	}

	if timeout == 0 {
		r.shutdownFn()
		r.inflight.Wait()
		return nil
	}

	done := make(chan struct{})
	go func() {
		r.inflight.Wait()
		close(done)
	}()

	select {
	case <-done:
		return nil
	case <-time.After(timeout):
		r.shutdownFn()
		r.inflight.Wait()
		return fmt.Errorf("shutdown timed out after %v", timeout)
	}
}

func (r *PeerRouter) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	if r.shuttingDown.Load() {
		SendError(w, req, fmt.Errorf("peer proxy is shutting down"))
		return
	}
	r.inflight.Add(1)
	defer r.inflight.Done()

	model, err := FetchModel(req)
	if err != nil {
		SendError(w, req, err)
		return
	}

	pp, found := r.peers[model]
	if !found {
		r.logger.Warnf("peer model not found: %s", model)
		SendError(w, req, ErrNoPeerModelFound)
		return
	}

	if pp.apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+pp.apiKey)
		req.Header.Set("x-api-key", pp.apiKey)
	}

	// Cancel the proxy request when the client disconnects or shutdown times out.
	// AfterFunc links both parent contexts to our child without a goroutine leak.
	ctx, cancel := context.WithCancel(context.Background())
	stopReq := context.AfterFunc(req.Context(), cancel)
	stopShutdown := context.AfterFunc(r.shutdownCtx, cancel)
	req = req.WithContext(ctx)

	pp.reverseProxy.ServeHTTP(w, req)

	stopShutdown()
	stopReq()
	cancel()
}
