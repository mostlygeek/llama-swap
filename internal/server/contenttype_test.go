package server

import (
	"bytes"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/mostlygeek/llama-swap/internal/swaputil"
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
			req.ContentLength = swaputil.MaxRequestBodySize + 1
			w := httptest.NewRecorder()
			s.ServeHTTP(w, req)
			require.Equal(t, http.StatusRequestEntityTooLarge, w.Code, w.Body.String())
			assert.Contains(t, gjson.Get(w.Body.String(), "error.message").String(), "250 MB")
		})
	}
}
