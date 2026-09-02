package server

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"
)

func TestServer_SelectEncoding(t *testing.T) {
	cases := []struct {
		accept   string
		encoding string
		ext      string
	}{
		{"", "", ""},
		{"gzip", "gzip", ".gz"},
		{"gzip, deflate, br", "br", ".br"},
		{"deflate", "", ""},
		{"br;q=1.0, gzip;q=0.8", "br", ".br"},
	}
	for _, c := range cases {
		enc, ext := selectEncoding(c.accept)
		if enc != c.encoding || ext != c.ext {
			t.Errorf("selectEncoding(%q) = (%q, %q), want (%q, %q)", c.accept, enc, ext, c.encoding, c.ext)
		}
	}
}

func uiTestFS() http.FileSystem {
	return http.FS(fstest.MapFS{
		"index.html":  {Data: []byte("<html>app</html>")},
		"app.js":      {Data: []byte("plain")},
		"app.js.br":   {Data: []byte("brotli")},
		"app.js.gz":   {Data: []byte("gzipped")},
		"favicon.ico": {Data: []byte("icon")},
	})
}

func serveUIRequest(t *testing.T, path, acceptEncoding string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, path, nil)
	if acceptEncoding != "" {
		req.Header.Set("Accept-Encoding", acceptEncoding)
	}
	w := httptest.NewRecorder()
	serveUI(uiTestFS(), w, req)
	return w
}

func TestServer_ServeUI_File(t *testing.T) {
	w := serveUIRequest(t, "/ui/app.js", "")
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	if w.Body.String() != "plain" {
		t.Errorf("body = %q, want plain", w.Body.String())
	}
}

func TestServer_ServeUI_Brotli(t *testing.T) {
	w := serveUIRequest(t, "/ui/app.js", "gzip, br")
	if got := w.Header().Get("Content-Encoding"); got != "br" {
		t.Fatalf("Content-Encoding = %q, want br", got)
	}
	if w.Body.String() != "brotli" {
		t.Errorf("body = %q, want brotli", w.Body.String())
	}
}

func TestServer_ServeUI_IndexAndRoot(t *testing.T) {
	for _, path := range []string{"/ui/", "/ui/index.html"} {
		w := serveUIRequest(t, path, "")
		if w.Code != http.StatusOK || w.Body.String() != "<html>app</html>" {
			t.Errorf("%s: status=%d body=%q", path, w.Code, w.Body.String())
		}
	}
}

func TestServer_ServeUI_SPAFallback(t *testing.T) {
	w := serveUIRequest(t, "/ui/models", "")
	if w.Code != http.StatusOK || w.Body.String() != "<html>app</html>" {
		t.Errorf("SPA fallback: status=%d body=%q", w.Code, w.Body.String())
	}
}

func TestServer_ServeUI_MissingFile(t *testing.T) {
	w := serveUIRequest(t, "/ui/missing.js", "")
	if w.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404", w.Code)
	}
}

// TestServer_EmbeddedUIAssets exercises the real embedded uiFS to verify the
// committed no-npm UI is embedded and served. It guards the //go:embed all:
// directive (underscore-prefixed files like __selftest.html are only embedded
// with the all: prefix) and that real ES modules carry a JS MIME type.
func TestServer_EmbeddedUIAssets(t *testing.T) {
	cases := []struct {
		path        string
		wantType    string // substring expected in Content-Type, "" to skip
		description string
	}{
		{"/ui/index.html", "text/html", "SPA entry point"},
		{"/ui/js/main.js", "javascript", "ES module bootstrap"},
		{"/ui/css/app.css", "text/css", "core stylesheet"},
		{"/ui/__selftest.html", "text/html", "underscore-prefixed file (needs all: embed)"},
	}
	for _, c := range cases {
		req := httptest.NewRequest(http.MethodGet, c.path, nil)
		w := httptest.NewRecorder()
		serveUI(uiFS, w, req)
		if w.Code != http.StatusOK {
			t.Errorf("%s (%s): status = %d, want 200", c.path, c.description, w.Code)
			continue
		}
		if w.Body.Len() == 0 {
			t.Errorf("%s (%s): empty body", c.path, c.description)
		}
		if c.wantType != "" && !strings.Contains(w.Header().Get("Content-Type"), c.wantType) {
			t.Errorf("%s: Content-Type = %q, want substring %q", c.path, w.Header().Get("Content-Type"), c.wantType)
		}
	}
}

// TestServer_RootAssets verifies the site-root files referenced by index.html
// and the web app manifest (served outside /ui/) resolve from the embedded FS.
func TestServer_RootAssets(t *testing.T) {
	s := &Server{}
	for _, asset := range rootUIAssets {
		req := httptest.NewRequest(http.MethodGet, "/"+asset, nil)
		w := httptest.NewRecorder()
		s.handleRootAsset(w, req)
		if w.Code != http.StatusOK {
			t.Errorf("/%s: status = %d, want 200", asset, w.Code)
		}
		if w.Body.Len() == 0 {
			t.Errorf("/%s: empty body", asset)
		}
	}
}
