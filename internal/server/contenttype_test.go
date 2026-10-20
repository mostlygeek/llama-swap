package server

import (
	"bytes"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/mostlygeek/llama-swap/internal/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestServer_ContentType_JSONRoutes(t *testing.T) {
	const chatBody = `{"model":"m","messages":[{"role":"user","content":"hi"}]}`

	tests := []struct {
		name        string
		contentType string // "-" leaves the header unset
		body        string
		wantStatus  int
		wantErr     string
	}{
		{"declared json", "application/json", chatBody, http.StatusOK, ""},
		{"curl default urlencoded", "application/x-www-form-urlencoded", chatBody, http.StatusOK, ""},
		{"no content type", "-", chatBody, http.StatusOK, ""},
		{"text/plain", "text/plain", chatBody, http.StatusOK, ""},
		{"leading whitespace", "-", "\n  " + chatBody, http.StatusOK, ""},
		{"form encoded model", "application/x-www-form-urlencoded", "model=m", http.StatusOK, ""},
		{"empty body", "-", "", http.StatusBadRequest, "request body is empty"},
		{"truncated json", "application/x-www-form-urlencoded", `{"model":"m"`, http.StatusBadRequest, "not valid JSON"},
		{"plain text", "text/plain", "hello", http.StatusBadRequest, "send JSON with Content-Type: application/json"},
		{"json without model", "-", `{"messages":[]}`, http.StatusNotFound, "no model id could be identified"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			local := newStubRouter([]string{"m"}, "")
			var gotContentType, gotBody string
			local.serveHTTP = func(w http.ResponseWriter, r *http.Request) {
				gotContentType = r.Header.Get("Content-Type")
				b, _ := io.ReadAll(r.Body)
				gotBody = string(b)
				w.WriteHeader(http.StatusOK)
			}
			s := newTestServer(local, newStubRouter(nil, ""))

			req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(tt.body))
			if tt.contentType != "-" {
				req.Header.Set("Content-Type", tt.contentType)
			}
			w := httptest.NewRecorder()
			s.ServeHTTP(w, req)

			require.Equal(t, tt.wantStatus, w.Code, w.Body.String())
			if tt.wantErr != "" {
				assert.Contains(t, gjson.Get(w.Body.String(), "error.message").String(), tt.wantErr)
				return
			}
			assert.Equal(t, tt.body, gotBody, "body must reach the upstream unchanged")
			if strings.HasPrefix(strings.TrimSpace(tt.body), "{") {
				assert.Equal(t, "application/json", gotContentType)
			}
		})
	}
}

func TestServer_ContentType_FormRoutes(t *testing.T) {
	s := newTestServer(newStubRouter([]string{"m"}, "ok"), newStubRouter(nil, ""))

	t.Run("multipart passes", func(t *testing.T) {
		var buf bytes.Buffer
		mw := multipart.NewWriter(&buf)
		require.NoError(t, mw.WriteField("model", "m"))
		require.NoError(t, mw.Close())

		req := httptest.NewRequest(http.MethodPost, "/v1/audio/transcriptions", &buf)
		req.Header.Set("Content-Type", mw.FormDataContentType())
		w := httptest.NewRecorder()
		s.ServeHTTP(w, req)
		assert.Equal(t, http.StatusOK, w.Code, w.Body.String())
	})

	t.Run("json body names the expected type", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "/v1/audio/transcriptions", strings.NewReader(`{"model":"m"}`))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		s.ServeHTTP(w, req)
		require.Equal(t, http.StatusBadRequest, w.Code, w.Body.String())
		assert.Contains(t, gjson.Get(w.Body.String(), "error.message").String(), "expects multipart/form-data")
	})
}

func TestServer_ContentType_GetRoutesUntouched(t *testing.T) {
	s := newTestServer(newStubRouter([]string{"m"}, "ok"), newStubRouter(nil, ""))
	w := httptest.NewRecorder()
	s.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/props?model=m", nil))
	assert.Equal(t, http.StatusOK, w.Code, w.Body.String())
}

func TestServer_ContentType_BodyTooLarge(t *testing.T) {
	s := newTestServer(newStubRouter([]string{"m"}, "ok"), newStubRouter(nil, ""))

	for _, contentType := range []string{"application/json", "text/plain"} {
		t.Run(contentType, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{"model":"m"}`))
			req.Header.Set("Content-Type", contentType)
			req.ContentLength = config.Config{}.MaxRequestBodyBytes() + 1
			w := httptest.NewRecorder()
			s.ServeHTTP(w, req)
			require.Equal(t, http.StatusRequestEntityTooLarge, w.Code, w.Body.String())
			assert.Contains(t, gjson.Get(w.Body.String(), "error.message").String(), "250 MB")
		})
	}
}

func TestServer_ContentType_StreamedBodyTooLarge(t *testing.T) {
	const maxBytes = 1 << 20
	// No Content-Length, so only the reader-side cap can catch it.
	r := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", io.LimitReader(zeroReader{}, maxBytes+1))
	r.ContentLength = -1
	r.Header.Set("Content-Type", "text/plain")

	bodyErr := prepareModelBody(httptest.NewRecorder(), r, maxBytes)
	require.NotNil(t, bodyErr)
	assert.Equal(t, http.StatusRequestEntityTooLarge, bodyErr.status)
	assert.Contains(t, bodyErr.message, "1 MB")
}

func TestServer_ContentType_ConfiguredBodyLimit(t *testing.T) {
	const limit = 1 << 20
	s := newTestServerWithConfig(config.Config{MaxRequestBodyMB: 1}, newStubRouter([]string{"m"}, "ok"), newStubRouter(nil, ""))

	t.Run("under the limit", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{"model":"m"}`))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		s.ServeHTTP(w, req)
		assert.Equal(t, http.StatusOK, w.Code, w.Body.String())
	})

	t.Run("Content-Length over the limit", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{"model":"m"}`))
		req.Header.Set("Content-Type", "application/json")
		req.ContentLength = limit + 1
		w := httptest.NewRecorder()
		s.ServeHTTP(w, req)
		require.Equal(t, http.StatusRequestEntityTooLarge, w.Code, w.Body.String())
		assert.Contains(t, gjson.Get(w.Body.String(), "error.message").String(), "1 MB")
	})

	// Without a Content-Length only reading the body finds the overrun. JSON and
	// multipart bodies are first read by the model lookup, not the
	// content-type check, and must still get a 413 rather than a 404.
	for _, contentType := range []string{"text/plain", "application/json", "multipart/form-data; boundary=x"} {
		t.Run("streamed body over the limit/"+contentType, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", io.LimitReader(zeroReader{}, limit+1))
			req.ContentLength = -1
			req.Header.Set("Content-Type", contentType)
			w := httptest.NewRecorder()
			s.ServeHTTP(w, req)
			require.Equal(t, http.StatusRequestEntityTooLarge, w.Code, w.Body.String())
			assert.Contains(t, gjson.Get(w.Body.String(), "error.message").String(), "1 MB")
		})
	}
}

func TestServer_ContentType_StreamedBodyTooLargeWithSelectors(t *testing.T) {
	// The selector middleware reads the body before the request context does
	// and ignores its read error; the 413 must survive that first read.
	cfg := config.Config{
		MaxRequestBodyMB: 1,
		Models:           map[string]config.ModelConfig{"m": {}},
		Selectors: map[string]config.SelectorConfig{
			"pool": {Strategy: config.SelectorStrategyPin, Targets: []string{"m"}},
		},
	}
	s := newTestServerWithConfig(cfg, newStubRouter([]string{"m"}, "ok"), newStubRouter(nil, ""))

	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", io.LimitReader(zeroReader{}, 1<<20+1))
	req.ContentLength = -1
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	s.ServeHTTP(w, req)
	require.Equal(t, http.StatusRequestEntityTooLarge, w.Code, w.Body.String())
}

type zeroReader struct{}

func (zeroReader) Read(p []byte) (int, error) { clear(p); return len(p), nil }
