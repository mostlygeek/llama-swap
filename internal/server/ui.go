package server

import (
	"embed"
	"io/fs"
	"net/http"
	"path"
	"strings"
)

// uiStaticFS holds the embedded UI build. The build is copied into ui_dist by
// the Makefile's `ui` target; placeholder.txt keeps the embed valid before a
// build has run.
//
//go:embed ui_dist
var uiStaticFS embed.FS

// uiFS is the embedded UI rooted at ui_dist.
var uiFS = func() http.FileSystem {
	sub, err := fs.Sub(uiStaticFS, "ui_dist")
	if err != nil {
		panic(err)
	}
	return http.FS(sub)
}()

// selectEncoding chooses the best pre-compressed encoding the client accepts.
// It returns the encoding ("br" or "gzip") and the matching file extension.
func selectEncoding(acceptEncoding string) (encoding, ext string) {
	if acceptEncoding == "" {
		return "", ""
	}
	for _, part := range strings.Split(acceptEncoding, ",") {
		if strings.TrimSpace(strings.SplitN(part, ";", 2)[0]) == "br" {
			return "br", ".br"
		}
	}
	for _, part := range strings.Split(acceptEncoding, ",") {
		if strings.TrimSpace(strings.SplitN(part, ";", 2)[0]) == "gzip" {
			return "gzip", ".gz"
		}
	}
	return "", ""
}

// serveCompressedFile serves name from fsys, preferring a pre-compressed
// sibling (name+".br" / name+".gz") when the client accepts it. It returns an
// error without writing a response when name cannot be served, so callers can
// fall back (e.g. SPA routing).
func serveCompressedFile(fsys http.FileSystem, w http.ResponseWriter, r *http.Request, name string) error {
	if encoding, ext := selectEncoding(r.Header.Get("Accept-Encoding")); encoding != "" {
		if cf, err := fsys.Open(name + ext); err == nil {
			defer cf.Close()
			if stat, err := cf.Stat(); err == nil && !stat.IsDir() {
				w.Header().Set("Content-Encoding", encoding)
				w.Header().Add("Vary", "Accept-Encoding")
				http.ServeContent(w, r, name, stat.ModTime(), cf)
				return nil
			}
		}
	}

	file, err := fsys.Open(name)
	if err != nil {
		return err
	}
	defer file.Close()

	stat, err := file.Stat()
	if err != nil {
		return err
	}
	if stat.IsDir() {
		return fs.ErrNotExist
	}

	http.ServeContent(w, r, name, stat.ModTime(), file)
	return nil
}

// handleUI serves the embedded SPA under /ui/.
func (s *Server) handleUI(w http.ResponseWriter, r *http.Request) {
	serveUI(uiFS, w, r)
}

// serveUI serves the SPA from fsys. Real files are served with compression
// support; unknown paths without a file extension fall back to index.html so
// client-side routing works.
func serveUI(fsys http.FileSystem, w http.ResponseWriter, r *http.Request) {
	name := strings.TrimPrefix(r.URL.Path, "/ui/")
	if name == "" {
		name = "index.html"
	}

	if err := serveCompressedFile(fsys, w, r, name); err != nil {
		if strings.Contains(path.Base(name), ".") {
			http.NotFound(w, r)
			return
		}
		if err := serveCompressedFile(fsys, w, r, "index.html"); err != nil {
			http.NotFound(w, r)
		}
	}
}

// handleFavicon serves /favicon.ico from the embedded UI build.
func (s *Server) handleFavicon(w http.ResponseWriter, r *http.Request) {
	if err := serveCompressedFile(uiFS, w, r, "favicon.ico"); err != nil {
		http.NotFound(w, r)
	}
}
