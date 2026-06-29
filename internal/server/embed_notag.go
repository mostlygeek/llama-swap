//go:build !embed_ui

package server

import (
	"io/fs"
	"net/http"
)

// emptyUIFS is a no-op fs.FS used when the binary is built without the
// embed_ui build tag. Every Open call returns fs.ErrNotExist.
type emptyUIFS struct{}

func (emptyUIFS) Open(_ string) (fs.File, error) {
	return nil, fs.ErrNotExist
}

// uiFS wraps emptyUIFS so that the UI endpoints return 404 when no UI assets
// are embedded (the default for `go test` and development builds).
var uiFS http.FileSystem = http.FS(emptyUIFS{})