package process

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/mostlygeek/llama-swap/internal/logmon"
	"github.com/mostlygeek/llama-swap/internal/swaputil"
)

// markerRecorder stands in for the server package's response recorders: it
// implements swaputil.StatusMarker so the proxy error handler can record a
// status without writing it to the client.
type markerRecorder struct {
	http.ResponseWriter
	status      int
	marked      int
	wroteHeader bool
}

func newMarkerRecorder() *markerRecorder {
	return &markerRecorder{ResponseWriter: httptest.NewRecorder(), status: http.StatusOK}
}

func (m *markerRecorder) WriteHeader(code int) {
	if m.wroteHeader {
		return
	}
	m.wroteHeader = true
	m.status = code
	m.ResponseWriter.WriteHeader(code)
}

func (m *markerRecorder) Write(b []byte) (int, error) {
	if !m.wroteHeader {
		m.WriteHeader(http.StatusOK)
	}
	return m.ResponseWriter.Write(b)
}

func (m *markerRecorder) MarkStatus(code int) {
	m.status = code
	m.marked = code
}

func (m *markerRecorder) WroteHeader() bool { return m.wroteHeader }

// TestProcessCommand_ProxyErrorHandler covers the secondary case in #1029: once
// the model is loaded, a client hanging up mid-request used to surface as a
// 502, blaming a healthy upstream. Cancellation must record the client-closed
// sentinel instead, while genuine upstream failures keep their 502.
func TestProcessCommand_ProxyErrorHandler(t *testing.T) {
	t.Run("client disconnect records the sentinel without writing", func(t *testing.T) {
		logger := logmon.NewWriter(io.Discard)
		handler := newProxyErrorHandler("m", logger)

		// The client connection itself goes away.
		clientCtx, cancel := context.WithCancel(context.Background())
		ctx := swaputil.SetClientContext(clientCtx, clientCtx)
		r := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil).WithContext(ctx)
		cancel()

		rec := newMarkerRecorder()
		handler(rec, r, context.Canceled)

		if rec.marked != swaputil.StatusClientClosedRequest {
			t.Errorf("marked status = %d, want %d", rec.marked, swaputil.StatusClientClosedRequest)
		}
		if rec.wroteHeader {
			t.Error("nothing should be written to a client that already hung up")
		}
		if line := string(logger.GetHistory()); strings.Contains(line, "proxy error") {
			t.Errorf("client disconnect should not be logged as a proxy error: %q", line)
		}
	})

	t.Run("upstream failure still answers 502", func(t *testing.T) {
		logger := logmon.NewWriter(io.Discard)
		handler := newProxyErrorHandler("m", logger)

		r := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
		rec := newMarkerRecorder()
		handler(rec, r, fmt.Errorf("dial tcp 127.0.0.1:9999: connect: connection refused"))

		if rec.status != http.StatusBadGateway {
			t.Errorf("status = %d, want %d", rec.status, http.StatusBadGateway)
		}
		if !rec.wroteHeader {
			t.Error("a real upstream failure must still be answered")
		}
		if line := string(logger.GetHistory()); !strings.Contains(line, "proxy error") {
			t.Errorf("upstream failure should be logged: %q", line)
		}
	})

	// A request cancelled server-side (an operator cancelling it from the UI,
	// or shutdown) still has a connected client. Recording it as a client
	// disconnect would misattribute it, and returning without writing would
	// let net/http finalize an empty 200 that tells the caller it succeeded.
	t.Run("server-side cancel still answers the connected client", func(t *testing.T) {
		logger := logmon.NewWriter(io.Discard)
		handler := newProxyErrorHandler("m", logger)

		// The client is alive; the inflight tracker's derived context is what
		// gets cancelled, exactly as POST /api/inflight/{id}/cancel does.
		clientCtx := context.Background()
		ctx, cancel := context.WithCancel(swaputil.SetClientContext(clientCtx, clientCtx))
		r := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil).WithContext(ctx)
		cancel()

		rec := newMarkerRecorder()
		handler(rec, r, context.Canceled)

		if rec.marked == swaputil.StatusClientClosedRequest {
			t.Error("a server-side cancel must not be recorded as a client disconnect")
		}
		if rec.status != http.StatusBadGateway {
			t.Errorf("status = %d, want %d: a connected client must get a real response", rec.status, http.StatusBadGateway)
		}
		if line := string(logger.GetHistory()); strings.Contains(line, "proxy error") {
			t.Errorf("a cancel is not an upstream fault: %q", line)
		}
	})

	t.Run("health check poll is not logged as a proxy error", func(t *testing.T) {
		logger := logmon.NewWriter(io.Discard)
		handler := newProxyErrorHandler("m", logger)

		ctx := context.WithValue(context.Background(), healthCheckKey{}, true)
		r := httptest.NewRequest(http.MethodGet, "/health", nil).WithContext(ctx)
		rec := newMarkerRecorder()
		handler(rec, r, fmt.Errorf("dial tcp 127.0.0.1:9999: connect: connection refused"))

		// The loop keys off the 502 to decide the model is not ready yet.
		if rec.status != http.StatusBadGateway {
			t.Errorf("status = %d, want %d", rec.status, http.StatusBadGateway)
		}
		if line := string(logger.GetHistory()); strings.Contains(line, "proxy error") {
			t.Errorf("a booting upstream should not log a proxy error per poll: %q", line)
		}
	})

	t.Run("response already started keeps its status", func(t *testing.T) {
		logger := logmon.NewWriter(io.Discard)
		handler := newProxyErrorHandler("m", logger)

		ctx, cancel := context.WithCancel(context.Background())
		r := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil).WithContext(ctx)
		cancel()

		// A streamed response that already flushed headers before the client
		// left: marking it 499 would misreport bytes the client did receive.
		rec := newMarkerRecorder()
		rec.Write([]byte("data: {}\n\n"))
		handler(rec, r, context.Canceled)

		if rec.status != http.StatusOK {
			t.Errorf("status = %d, want %d", rec.status, http.StatusOK)
		}
	})
}
