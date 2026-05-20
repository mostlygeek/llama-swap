package server

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"

	"github.com/mostlygeek/llama-swap/internal/config"
	"github.com/mostlygeek/llama-swap/internal/logmon"
	"github.com/mostlygeek/llama-swap/internal/router"
)

// muxlog creates a combined log for logging to stdout depending the configuration
// mostly for backwards compatibility with the /log endpoints
func muxlog(proxyConfig config.Config, proxylog *logmon.Monitor, upstreamlog *logmon.Monitor) (*logmon.Monitor, error) {
	var muxlog *logmon.Monitor
	switch proxyConfig.LogToStdout {
	case config.LogToStdoutNone:
		muxlog = logmon.NewWriter(io.Discard)
	case config.LogToStdoutBoth:
		muxlog = logmon.NewWriter(os.Stdout)
	case config.LogToStdoutUpstream:
		muxlog = logmon.NewWriter(os.Stdout)
	default:
		// same as config.LogToStdoutProxy
		// helpful because some old tests create a config.Config directly and it
		// may not have LogToStdout set explicitly
		muxlog = logmon.NewWriter(os.Stdout)
	}

	return muxlog, nil
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
		return nil, fmt.Errorf("invalid logger. Use 'proxy' or 'upstream'")
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
