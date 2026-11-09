package server

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A client that sends Expect: 100-continue (curl does for bodies over ~1 MiB)
// makes httputil.ReverseProxy relay the upstream's 100 Continue through
// WriteHeader before the real status. The response recorders must not mistake
// it for the final status. See #1316.
func TestProxy_ExpectContinueKeepsUpstreamStatus(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// reading the body makes net/http send 100 Continue
		_, _ = io.Copy(io.Discard, r.Body)
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte("bad request"))
	}))
	defer upstream.Close()

	upstreamURL, err := url.Parse(upstream.URL)
	require.NoError(t, err)
	proxy := httputil.NewSingleHostReverseProxy(upstreamURL)

	wrappers := map[string]func(http.ResponseWriter, *http.Request) http.ResponseWriter{
		"statusRecorder": func(w http.ResponseWriter, _ *http.Request) http.ResponseWriter {
			return &statusRecorder{ResponseWriter: w}
		},
		"responseBodyCopier": func(w http.ResponseWriter, _ *http.Request) http.ResponseWriter {
			return newBodyCopier(w)
		},
		"inflightResponseWriter": func(w http.ResponseWriter, r *http.Request) http.ResponseWriter {
			tracker := newInflightTracker()
			return &inflightResponseWriter{ResponseWriter: w, tracker: tracker, id: tracker.Add(r, func() {})}
		},
	}

	for name, wrap := range wrappers {
		for _, expect := range []string{"", "100-continue"} {
			t.Run(name+"/expect="+expect, func(t *testing.T) {
				front := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					proxy.ServeHTTP(wrap(w, r), r)
				}))
				defer front.Close()

				req, err := http.NewRequest(http.MethodPost, front.URL, bytes.NewReader(make([]byte, 2<<20)))
				require.NoError(t, err)
				if expect != "" {
					req.Header.Set("Expect", expect)
				}
				client := &http.Client{Transport: &http.Transport{ExpectContinueTimeout: time.Second}}
				resp, err := client.Do(req)
				require.NoError(t, err)
				defer resp.Body.Close()

				body, _ := io.ReadAll(resp.Body)
				assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
				assert.Equal(t, "bad request", string(body))
			})
		}
	}
}

func TestProxy_RecordersIgnoreInformationalStatus(t *testing.T) {
	t.Run("statusRecorder", func(t *testing.T) {
		rec := &statusRecorder{ResponseWriter: httptest.NewRecorder()}
		rec.WriteHeader(http.StatusContinue)
		assert.False(t, rec.WroteHeader())
		rec.WriteHeader(http.StatusInternalServerError)
		assert.Equal(t, http.StatusInternalServerError, rec.status)
	})

	t.Run("responseBodyCopier", func(t *testing.T) {
		rec := newBodyCopier(httptest.NewRecorder())
		rec.WriteHeader(http.StatusContinue)
		assert.False(t, rec.WroteHeader())
		rec.WriteHeader(http.StatusInternalServerError)
		assert.Equal(t, http.StatusInternalServerError, rec.status)
	})

	t.Run("deferredUpgradeWriter", func(t *testing.T) {
		w := &deferredUpgradeWriter{header: http.Header{}}
		w.WriteHeader(http.StatusContinue)
		assert.False(t, w.WroteHeader())
		w.WriteHeader(http.StatusBadGateway)
		assert.Equal(t, http.StatusBadGateway, w.status)
	})

	t.Run("discardResponseWriter", func(t *testing.T) {
		w := &discardResponseWriter{}
		w.WriteHeader(http.StatusContinue)
		w.WriteHeader(http.StatusBadGateway)
		assert.Equal(t, http.StatusBadGateway, w.status)
	})
}
