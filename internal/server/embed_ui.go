//go:build embed_ui

package server

import (
	"embed"
	"io/fs"
	"net/http"
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