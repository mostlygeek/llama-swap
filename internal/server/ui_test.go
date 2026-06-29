package server

import (
	"errors"
	"io/fs"
	"net/http"
	"net/http/httptest"
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

// Tests for embed_notag.go: emptyUIFS and uiFS behavior when built without
// the embed_ui tag (which is the default for `go test`).

func TestEmptyUIFS_Open_ReturnsErrNotExist(t *testing.T) {
	paths := []string{
		"",
		"/",
		"index.html",
		"/index.html",
		"favicon.ico",
		"assets/app.js",
		"deep/nested/path/file.css",
		"file.with.many.dots.txt",
	}
	e := emptyUIFS{}
	for _, p := range paths {
		t.Run(p, func(t *testing.T) {
			f, err := e.Open(p)
			if f != nil {
				f.Close()
				t.Errorf("Open(%q): got non-nil file, want nil", p)
			}
			if !errors.Is(err, fs.ErrNotExist) {
				t.Errorf("Open(%q): err = %v, want fs.ErrNotExist", p, err)
			}
		})
	}
}

func TestEmptyUIFS_ImplementsFSInterface(t *testing.T) {
	// Compile-time assertion: emptyUIFS must satisfy fs.FS.
	var _ fs.FS = emptyUIFS{}
}

func TestUiFS_NotNil(t *testing.T) {
	// uiFS is set at package init time; it must never be nil.
	if uiFS == nil {
		t.Fatal("uiFS is nil")
	}
}

func TestUiFS_OpenReturnsErrorForAnyPath(t *testing.T) {
	// When built without embed_ui, uiFS wraps emptyUIFS and must return an
	// error for every path — no real files are embedded.
	paths := []string{
		"/",
		"/index.html",
		"/favicon.ico",
		"/app.js",
		"/nonexistent-file.txt",
	}
	for _, p := range paths {
		t.Run(p, func(t *testing.T) {
			f, err := uiFS.Open(p)
			if err == nil {
				if f != nil {
					f.Close()
				}
				t.Errorf("uiFS.Open(%q): expected error, got nil", p)
			}
		})
	}
}

func TestUiFS_ServeUIWithEmptyFS_Returns404(t *testing.T) {
	// serveUI uses the package-level uiFS. When built without embed_ui the
	// filesystem is empty, so every path — including the SPA root — should
	// result in a 404 (not a server error).
	paths := []string{
		"/ui/",
		"/ui/index.html",
		"/ui/missing.js",
		"/ui/some/spa/route",
	}
	for _, p := range paths {
		t.Run(p, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, p, nil)
			w := httptest.NewRecorder()
			serveUI(uiFS, w, req)
			if w.Code != http.StatusNotFound {
				t.Errorf("serveUI(uiFS, %q): status = %d, want 404", p, w.Code)
			}
		})
	}
}

func TestEmptyUIFS_OpenOnlyReturnsErrNotExist_NotOtherErrors(t *testing.T) {
	// Regression: Open must return exactly fs.ErrNotExist (or wrap it), not a
	// different error like io.EOF or a permission error.
	e := emptyUIFS{}
	_, err := e.Open("any-path")
	if err == nil {
		t.Fatal("expected an error, got nil")
	}
	if !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("error = %v; want errors.Is(err, fs.ErrNotExist) == true", err)
	}
}
