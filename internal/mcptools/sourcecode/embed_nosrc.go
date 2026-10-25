//go:build !embed_src

package sourcecode

import "io/fs"

// Embedded is nil when the binary is built without the `embed_src` tag. This is
// the default for `go test` and plain `go build`, which keeps those commands
// from requiring a populated _src directory. Release builds pass
// `-tags embed_src` (see the Makefile) to embed the source via embed.go.
var Embedded fs.FS
