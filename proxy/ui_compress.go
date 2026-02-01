package proxy

import (
	"net/http"
	"strings"
)

// compressedFileSystem wraps a http.FileSystem to serve pre-compressed files.
// It checks for .br (brotli) and .gz (gzip) versions of files and serves them
// with appropriate Content-Encoding headers when the client supports them.
type compressedFileSystem struct {
	fs http.FileSystem
}

// NewCompressedFileSystem creates a new compressed file system wrapper
func NewCompressedFileSystem(fs http.FileSystem) http.FileSystem {
	return &compressedFileSystem{fs: fs}
}

// Open opens a file from the filesystem. If the request has Accept-Encoding
// headers and a pre-compressed version exists, it returns that instead.
func (cfs *compressedFileSystem) Open(name string) (http.File, error) {
	return cfs.fs.Open(name)
}

// selectEncoding chooses the best encoding based on Accept-Encoding header
// Returns the encoding ("br", "gzip", or "") and the corresponding file extension
func selectEncoding(acceptEncoding string) (encoding, ext string) {
	if acceptEncoding == "" {
		return "", ""
	}

	// Check for brotli first (preferred for better compression)
	if strings.Contains(acceptEncoding, "br") {
		return "br", ".br"
	}

	// Fall back to gzip
	if strings.Contains(acceptEncoding, "gzip") {
		return "gzip", ".gz"
	}

	return "", ""
}

// ServeCompressedFile serves a file with compression support.
// It checks for pre-compressed versions and serves them with proper headers.
func ServeCompressedFile(fs http.FileSystem, w http.ResponseWriter, r *http.Request, name string) {
	encoding, ext := selectEncoding(r.Header.Get("Accept-Encoding"))

	// Try to serve compressed version if client supports it
	if encoding != "" {
		if cf, err := fs.Open(name + ext); err == nil {
			defer cf.Close()

			// Verify it's a regular file (not a directory)
			if stat, err := cf.Stat(); err == nil && !stat.IsDir() {
				// Set the content encoding header
				w.Header().Set("Content-Encoding", encoding)
				w.Header().Add("Vary", "Accept-Encoding")

				// Get original file info for content type detection
				origFile, err := fs.Open(name)
				if err == nil {
					origFile.Close()
				}

				// Serve the compressed file
				http.ServeContent(w, r, name, stat.ModTime(), cf)
				return
			}
		}
	}

	// Fall back to serving the uncompressed file
	file, err := fs.Open(name)
	if err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}
	defer file.Close()

	stat, err := file.Stat()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	if stat.IsDir() {
		http.Error(w, "is a directory", http.StatusForbidden)
		return
	}

	http.ServeContent(w, r, name, stat.ModTime(), file)
}
