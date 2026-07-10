package server

import (
	"bufio"
	"bytes"
	"compress/flate"
	"compress/gzip"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/mostlygeek/llama-swap/internal/config"
	"github.com/mostlygeek/llama-swap/internal/logmon"
)

func TestServer_DecompressBody(t *testing.T) {
	plain := []byte("hello world")

	var gz bytes.Buffer
	gw := gzip.NewWriter(&gz)
	gw.Write(plain)
	gw.Close()

	var fl bytes.Buffer
	fw, _ := flate.NewWriter(&fl, flate.DefaultCompression)
	fw.Write(plain)
	fw.Close()

	cases := []struct {
		name     string
		body     []byte
		encoding string
	}{
		{"plain", plain, ""},
		{"gzip", gz.Bytes(), "gzip"},
		{"deflate", fl.Bytes(), "deflate"},
		{"unknown passthrough", plain, "br"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := decompressBody(c.body, c.encoding)
			if err != nil {
				t.Fatalf("decompressBody: %v", err)
			}
			if !bytes.Equal(got, plain) {
				t.Errorf("got %q, want %q", got, plain)
			}
		})
	}
}

func TestServer_FilterAcceptEncoding(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"", ""},
		{"gzip, deflate, br", "gzip, deflate"},
		{"br, zstd", ""},
		{"gzip;q=1.0", "gzip;q=1.0"},
	}
	for _, c := range cases {
		if got := filterAcceptEncoding(c.in); got != c.want {
			t.Errorf("filterAcceptEncoding(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestServer_BodyCopier_Flush(t *testing.T) {
	bc := newBodyCopier(httptest.NewRecorder())
	bc.Write([]byte("data"))
	bc.Flush()
	if bc.Status() != http.StatusOK {
		t.Errorf("status = %d, want 200", bc.Status())
	}
}

// hijackRecorder is an httptest.ResponseRecorder that also implements
// http.Hijacker, returning a pipe so Hijack forwarding can be exercised.
type hijackRecorder struct {
	*httptest.ResponseRecorder
	conn net.Conn
}

func (h *hijackRecorder) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	return h.conn, bufio.NewReadWriter(bufio.NewReader(h.conn), bufio.NewWriter(h.conn)), nil
}

func TestServer_BodyCopier_Hijack(t *testing.T) {
	t.Run("forwards to underlying hijacker", func(t *testing.T) {
		client, server := net.Pipe()
		defer client.Close()
		defer server.Close()

		bc := newBodyCopier(&hijackRecorder{httptest.NewRecorder(), server})
		conn, _, err := bc.Hijack()
		if err != nil {
			t.Fatalf("Hijack: %v", err)
		}
		if conn != server {
			t.Errorf("Hijack returned unexpected conn")
		}
	})

	t.Run("errors when underlying writer is not a hijacker", func(t *testing.T) {
		bc := newBodyCopier(httptest.NewRecorder())
		if _, _, err := bc.Hijack(); err == nil {
			t.Error("expected error hijacking a non-Hijacker ResponseWriter")
		}
	})
}

func TestServer_BodyCopier_SkipsBufferingOnUpgrade(t *testing.T) {
	rec := httptest.NewRecorder()
	bc := newBodyCopier(rec)
	bc.WriteHeader(http.StatusSwitchingProtocols)
	bc.Write([]byte("websocket frame bytes"))

	if bc.body.Len() != 0 {
		t.Errorf("upgrade body buffered = %q, want empty", bc.body.Bytes())
	}
	if got := rec.Body.String(); got != "websocket frame bytes" {
		t.Errorf("client body = %q, want %q", got, "websocket frame bytes")
	}
}

func TestServer_HeaderMapAndRedact(t *testing.T) {
	h := http.Header{
		"Content-Type":  {"application/json"},
		"Authorization": {"Bearer secret"},
		"X-Api-Key":     {"key123"},
	}
	m := headerMap(h)
	if m["Content-Type"] != "application/json" {
		t.Errorf("Content-Type = %q", m["Content-Type"])
	}

	redactHeaders(m)
	if m["Authorization"] != "[REDACTED]" || m["X-Api-Key"] != "[REDACTED]" {
		t.Errorf("sensitive headers not redacted: %v", m)
	}
	if m["Content-Type"] != "application/json" {
		t.Error("non-sensitive header should not be redacted")
	}
}

func TestServer_StripVersionPrefix(t *testing.T) {
	r := httptest.NewRequest(http.MethodGet, "/v/v1/chat", nil)
	stripVersionPrefix(r)
	if r.URL.Path != "/v1/chat" {
		t.Errorf("path = %q, want /v1/chat", r.URL.Path)
	}

	r2 := httptest.NewRequest(http.MethodGet, "/v1/chat", nil)
	stripVersionPrefix(r2)
	if r2.URL.Path != "/v1/chat" {
		t.Errorf("path = %q, want unchanged", r2.URL.Path)
	}
}

func TestServer_CloseStreams(t *testing.T) {
	s := newTestServer(newStubRouter(nil, ""), newStubRouter(nil, ""))
	s.CloseStreams()
	select {
	case <-s.shutdownCtx.Done():
	default:
		t.Error("CloseStreams did not cancel shutdown context")
	}
	s.CloseStreams() // idempotent
}

func TestServer_HandleUIAndFavicon(t *testing.T) {
	s := newTestServer(newStubRouter(nil, ""), newStubRouter(nil, ""))

	for _, path := range []string{"/ui/", "/favicon.ico"} {
		w := httptest.NewRecorder()
		s.ServeHTTP(w, httptest.NewRequest(http.MethodGet, path, nil))
		// Tests build without the `embed_ui` tag, so uiFS is empty and these
		// resolve to 404 — the handlers still execute end to end.
		if w.Code != http.StatusOK && w.Code != http.StatusNotFound {
			t.Errorf("%s: status = %d", path, w.Code)
		}
	}
}

func TestServer_HandleAPIUnloadAll(t *testing.T) {
	local := newStubRouter([]string{"m1"}, "")
	s := newTestServer(local, newStubRouter(nil, ""))

	w := httptest.NewRecorder()
	s.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/api/models/unload", nil))

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d", w.Code)
	}
	if local.unloadCalls.Load() != 1 {
		t.Errorf("unloadCalls = %d, want 1", local.unloadCalls.Load())
	}
	if len(local.unloadModels) != 0 {
		t.Errorf("unloadModels = %v, want empty for unload all", local.unloadModels)
	}
	if local.unloadTimeout != 0 {
		t.Errorf("unloadTimeout = %v, want 0 (use configured timeouts)", local.unloadTimeout)
	}
}

func TestServer_HandleAPIUnloadModel(t *testing.T) {
	local := newStubRouter([]string{"m1"}, "")
	s := newTestServer(local, newStubRouter(nil, ""))
	s.cfg = config.Config{Models: map[string]config.ModelConfig{"m1": {}}}

	t.Run("known model", func(t *testing.T) {
		w := httptest.NewRecorder()
		s.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/api/models/unload/m1", nil))
		if w.Code != http.StatusOK {
			t.Errorf("status = %d, want 200", w.Code)
		}
		if len(local.unloadModels) != 1 || local.unloadModels[0] != "m1" {
			t.Errorf("unloadModels = %v, want [m1]", local.unloadModels)
		}
		if local.unloadTimeout != 0 {
			t.Errorf("unloadTimeout = %v, want 0 (use configured timeouts)", local.unloadTimeout)
		}
	})

	t.Run("unknown model 404", func(t *testing.T) {
		w := httptest.NewRecorder()
		s.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/api/models/unload/nope", nil))
		if w.Code != http.StatusNotFound {
			t.Errorf("status = %d, want 404", w.Code)
		}
	})
}

func TestServer_HandleAPICapture(t *testing.T) {
	s := newTestServer(newStubRouter(nil, ""), newStubRouter(nil, ""))
	s.metrics = newTestMetricsMonitor(t, logmon.NewWriter(io.Discard), 100, 5)
	s.metrics.addCapture(ReqRespCapture{ID: 42, ReqPath: "/v1/chat/completions"})

	t.Run("found", func(t *testing.T) {
		w := httptest.NewRecorder()
		s.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/captures/42", nil))
		if w.Code != http.StatusOK {
			t.Fatalf("status = %d", w.Code)
		}
		if !bytes.Contains(w.Body.Bytes(), []byte("/v1/chat/completions")) {
			t.Errorf("body = %q", w.Body.String())
		}
	})

	t.Run("not found", func(t *testing.T) {
		w := httptest.NewRecorder()
		s.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/captures/999", nil))
		if w.Code != http.StatusNotFound {
			t.Errorf("status = %d, want 404", w.Code)
		}
	})

	t.Run("invalid id", func(t *testing.T) {
		w := httptest.NewRecorder()
		s.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/captures/abc", nil))
		if w.Code != http.StatusBadRequest {
			t.Errorf("status = %d, want 400", w.Code)
		}
	})
}
