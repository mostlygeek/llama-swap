//go:build embed_ui

package server

import (
	"embed"
	"io/fs"
	"net/http"
)

// uiStaticFS holds the embedded UI build. The build is produced into ui_dist by
// the Makefile's `ui` target. This file is only compiled when the `embed_ui`
// build tag is set, so the embed directive requires ui_dist to be populated —
// the Makefile build targets pass `-tags embed_ui` after building the UI.
//
//go:embed all:ui_dist
var uiStaticFS embed.FS

// uiFS is the embedded UI rooted at ui_dist.
var uiFS = func() http.FileSystem {
	sub, err := fs.Sub(uiStaticFS, "ui_dist")
	if err != nil {
		panic(err)
	}
	return http.FS(sub)
}()
