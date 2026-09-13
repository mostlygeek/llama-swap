//go:build embed_playground

package main

import _ "embed"

//go:embed dist/index.html
var playgroundIndex []byte

//go:embed dist/main.wasm.gz
var playgroundWasm []byte
