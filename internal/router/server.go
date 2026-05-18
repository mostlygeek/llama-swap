package router

import (
	"errors"
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/mostlygeek/llama-swap/internal/logmon"
	"github.com/mostlygeek/llama-swap/proxy/config"
)

// Server implement http.Handler to route requests to the appropriate router
// based on the model in the request. The server will also handle the
// /upstream endpoint for proxying requests to the upstream server.
type Server struct {
	cfg    config.Config
	logger *logmon.Monitor
	peer   Router
	router Router
}

func NewServer(cfg config.Config, proxylog *logmon.Monitor, upstreamlog *logmon.Monitor) (*Server, error) {
	s := &Server{
		cfg:    cfg,
		logger: proxylog,
	}

	if cfg.Matrix != nil {
		m, err := NewMatrix(cfg, proxylog, upstreamlog)
		if err != nil {
			return nil, fmt.Errorf("creating matrix router: %w", err)
		}
		s.router = m
	} else {
		g, err := NewGroup(cfg, proxylog, upstreamlog)
		if err != nil {
			return nil, fmt.Errorf("creating group router: %w", err)
		}
		s.router = g
	}

	p, err := NewPeer(cfg, proxylog)
	if err != nil {
		return nil, fmt.Errorf("creating peer router: %w", err)
	}
	s.peer = p

	return s, nil
}

func (s *Server) Handles(model string) bool {
	return s.router.Handles(model) || s.peer.Handles(model)
}

func (s *Server) Shutdown(timeout time.Duration) error {
	var wg sync.WaitGroup
	defer wg.Wait()

	var errmu sync.Mutex
	var errs []error

	for _, r := range []Router{s.router, s.peer} {
		if r == nil {
			continue
		}

		wg.Add(1)
		go func(r Router) {
			defer wg.Done()
			err := r.Shutdown(timeout)
			errmu.Lock()
			errs = append(errs, err)
			errmu.Unlock()
		}(r)
	}

	return errors.Join(errs...)
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {

	_, model, err := FetchModel(r, s.cfg)
	if err != nil {
		SendError(w, r, ErrNoModelInContext)
		return
	}

	if s.peer.Handles(model) {
		s.peer.ServeHTTP(w, r)
		return
	} else if s.router.Handles(model) {
		s.router.ServeHTTP(w, r)
		return
	} else {
		SendError(w, r, ErrNoRouterFound)
		return
	}
}
