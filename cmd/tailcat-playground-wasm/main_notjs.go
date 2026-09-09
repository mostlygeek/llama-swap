//go:build !js || !wasm

// This stub exists so `go build ./...`, `go test ./...` and staticcheck do not
// fail this package with "build constraints exclude all Go files" on the hosts
// llama-swap is actually developed on. The real program is main_js.go; build
// it with `make tailcat-playground`.
package main

import (
	"fmt"
	"os"
)

func main() {
	fmt.Fprintln(os.Stderr, "tailcat-playground-wasm builds for js/wasm only; run `make tailcat-playground`")
	os.Exit(1)
}
