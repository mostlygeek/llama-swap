//go:build embed_src

package sourcecode

import (
	"embed"
	"io/fs"
)

// srcFS holds the source copied into _src by the Makefile's `src` target. This
// file is only compiled when the `embed_src` build tag is set, so the embed
// directive requires _src to be populated. The leading underscore keeps the go
// command from treating the copied .go files as packages.
//
//go:embed _src
var srcFS embed.FS

// Embedded is the source tree rooted at _src.
var Embedded = func() fs.FS {
	sub, err := fs.Sub(srcFS, "_src")
	if err != nil {
		panic(err)
	}
	return sub
}()
