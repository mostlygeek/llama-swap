//go:build !embed_ui

package server

import (
	"io/fs"
	"net/http"
)

// uiFS serves no files when the binary is built without the `embed_ui` tag.
// This is the default for `go test` and plain `go build`, which keeps those
// commands from requiring a populated ui_dist directory. Release builds pass
// `-tags embed_ui` (see the Makefile) to embed the real UI via embed.go.
var uiFS http.FileSystem = http.FS(emptyUIFS{})

// emptyUIFS is an fs.FS that contains no files.
type emptyUIFS struct{}

func (emptyUIFS) Open(string) (fs.File, error) { return nil, fs.ErrNotExist }
