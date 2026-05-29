package server

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/mostlygeek/llama-swap/internal/chain"
	"github.com/mostlygeek/llama-swap/internal/config"
	"github.com/mostlygeek/llama-swap/internal/logmon"
	"github.com/mostlygeek/llama-swap/internal/router"
)

// NewLoggers builds the proxy, upstream, and combined (mux) log monitors,
// wiring each one's output per the logToStdout config value. The proxy and
// upstream monitors write into muxlog (rather than os.Stdout directly) so
// muxlog accumulates a combined history for the /logs endpoints, while each
// monitor keeps its own per-source history and event subscribers.
//
// Behaviour matches the legacy ProxyManager:
//
//   - none:     everything discarded
//   - both:     proxy + upstream both routed to muxlog -> stdout
//   - upstream: only upstream routed to muxlog -> stdout; proxy discarded
//   - proxy:    only proxy routed to muxlog -> stdout; upstream discarded
//
// An empty or unrecognised value behaves like "proxy".
func NewLoggers(logToStdout string) (muxlog, proxylog, upstreamlog *logmon.Monitor) {
	switch logToStdout {
	case config.LogToStdoutNone:
		muxlog = logmon.NewWriter(io.Discard)
		proxylog = logmon.NewWriter(io.Discard)
		upstreamlog = logmon.NewWriter(io.Discard)
	case config.LogToStdoutBoth:
		muxlog = logmon.NewWriter(os.Stdout)
		proxylog = logmon.NewWriter(muxlog)
		upstreamlog = logmon.NewWriter(muxlog)
	case config.LogToStdoutUpstream:
		muxlog = logmon.NewWriter(os.Stdout)
		proxylog = logmon.NewWriter(io.Discard)
		upstreamlog = logmon.NewWriter(muxlog)
	default:
		// config.LogToStdoutProxy, and the fallback for an unset value.
		muxlog = logmon.NewWriter(os.Stdout)
		proxylog = logmon.NewWriter(muxlog)
		upstreamlog = logmon.NewWriter(io.Discard)
	}
	return muxlog, proxylog, upstreamlog
}

// handleLogs serves the historical proxy/upstream log. HTML clients are
// redirected to the UI.
func (s *Server) handleLogs(w http.ResponseWriter, r *http.Request) {
	if strings.Contains(r.Header.Get("Accept"), "text/html") {
		http.Redirect(w, r, "/ui/", http.StatusFound)
		return
	}
	w.Header().Set("Content-Type", "text/plain")
	w.Write(s.muxlog.GetHistory())
}

// getLogger resolves a log monitor by id. An empty id maps to the combined
// muxlog; "proxy" and "upstream" select the respective monitors.
func (s *Server) getLogger(logMonitorID string) (*logmon.Monitor, error) {
	switch logMonitorID {
	case "":
		return s.muxlog, nil
	case "proxy":
		return s.proxylog, nil
	case "upstream":
		return s.upstreamlog, nil
	default:
		if _, modelID, _, found := findModelInPath(s.cfg, "/"+logMonitorID); found {
			if log, ok := s.local.ProcessLogger(modelID); ok {
				return log, nil
			}
		}
		return nil, fmt.Errorf("invalid logger. Use 'proxy', 'upstream' or a model's ID")
	}
}

// handleLogStream tails a log monitor: it writes the history then streams live
// log data until the client disconnects or the server shuts down.
func (s *Server) handleLogStream(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/plain")
	w.Header().Set("Transfer-Encoding", "chunked")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	// prevent nginx from buffering streamed logs
	w.Header().Set("X-Accel-Buffering", "no")

	logMonitorID := strings.TrimPrefix(r.PathValue("logMonitorID"), "/")
	// Strip a query string if it leaked into the path segment.
	if idx := strings.Index(logMonitorID, "?"); idx != -1 {
		logMonitorID = logMonitorID[:idx]
	}

	logger, err := s.getLogger(logMonitorID)
	if err != nil {
		router.SendResponse(w, r, http.StatusBadRequest, err.Error())
		return
	}

	flusher, ok := w.(http.Flusher)
	if !ok {
		router.SendResponse(w, r, http.StatusInternalServerError, "streaming unsupported")
		return
	}

	_, skipHistory := r.URL.Query()["no-history"]
	if !skipHistory {
		if history := logger.GetHistory(); len(history) != 0 {
			w.Write(history)
			flusher.Flush()
		}
	}

	sendChan := make(chan []byte, 10)
	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()
	cancelSub := logger.OnLogData(func(data []byte) {
		select {
		case sendChan <- data:
		case <-ctx.Done():
		default:
		}
	})
	defer cancelSub()

	for {
		select {
		case <-r.Context().Done():
			return
		case <-s.shutdownCtx.Done():
			return
		case data := <-sendChan:
			w.Write(data)
			flusher.Flush()
		}
	}
}

// requestLogPathSkips lists path prefixes excluded from the access log because
// they are polled frequently and would drown out useful entries.
var requestLogPathSkips = []string{"/wol-health", "/api/performance", "/metrics"}

// statusRecorder wraps an http.ResponseWriter to capture the response status
// code and the number of body bytes written, so the access log can report
// them. Flush is forwarded so streaming handlers (SSE) still work.
type statusRecorder struct {
	http.ResponseWriter
	status int
	size   int
}

func (sr *statusRecorder) WriteHeader(code int) {
	sr.status = code
	sr.ResponseWriter.WriteHeader(code)
}

func (sr *statusRecorder) Write(b []byte) (int, error) {
	n, err := sr.ResponseWriter.Write(b)
	sr.size += n
	return n, err
}

func (sr *statusRecorder) Flush() {
	if f, ok := sr.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

// clientIP resolves the originating client address, preferring proxy headers
// over the raw connection address.
func clientIP(r *http.Request) string {
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		if first, _, found := strings.Cut(xff, ","); found {
			return strings.TrimSpace(first)
		}
		return strings.TrimSpace(xff)
	}
	if xr := r.Header.Get("X-Real-IP"); xr != "" {
		return strings.TrimSpace(xr)
	}
	if host, _, err := net.SplitHostPort(r.RemoteAddr); err == nil {
		return host
	}
	return r.RemoteAddr
}

// CreateRequestLogMiddleware returns middleware that records one access-log
// line per request to proxylog, in the legacy format:
//
//	clientIP "METHOD PATH PROTO" status bodySize "UA" duration
//
// Frequently-polled health/metrics paths are skipped. The path is captured
// before next runs because /upstream rewrites the request URL in place.
func CreateRequestLogMiddleware(proxylog *logmon.Monitor) chain.Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			for _, prefix := range requestLogPathSkips {
				if strings.HasPrefix(r.URL.Path, prefix) {
					next.ServeHTTP(w, r)
					return
				}
			}

			start := time.Now()
			ip, method, path, proto, ua := clientIP(r), r.Method, r.URL.Path, r.Proto, r.UserAgent()

			rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
			next.ServeHTTP(rec, r)

			proxylog.Infof("Request %s \"%s %s %s\" %d %d \"%s\" %v",
				ip, method, path, proto, rec.status, rec.size, ua, time.Since(start))
		})
	}
}
