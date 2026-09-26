//go:build !js || !wasm

// This is the native half of the Tailcat Playground; main_js.go, sharing this
// directory, is the js/wasm half that runs in the browser. The build
// constraint above is what keeps their two main()s apart: main_js.go only
// compiles under js/wasm, this only compiles everywhere else, so a given
// build always sees exactly one of them (it is the same constraint
// cmd/tailcat-playground-wasm's old main_notjs.go stub carried, which this
// file replaced when the two packages merged).
//
// The tailcat-playground-server binary serves the split packaging of the
// Tailcat Playground page (index.html + main.wasm.gz) from memory, so the page
// can be used without a web server of your own. Browsers refuse to fetch the
// module beside a file:// page, which is why the split pair needs one.
//
// Both files are embedded at build time; see `make tailcat-playground`, which
// copies them into dist/ here before building this. The embed only compiles
// under the embed_playground tag so `go build ./...` does not fail when dist/
// is absent; without the tag the binary just explains how to build itself.
package main

import (
	"flag"
	"fmt"
	"net/http"
	"os"
	"strconv"
)

func main() {
	listen := flag.String("listen", "127.0.0.1:8090", "address to serve the page on")
	flag.Parse()

	if playgroundIndex == nil {
		fmt.Fprintln(os.Stderr, "tailcat-playground-server was built without the page; run `make tailcat-playground`")
		os.Exit(1)
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /{$}", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Write(playgroundIndex)
	})
	// Served as opaque bytes, not with Content-Encoding: gzip. The page fetches
	// this and decompresses it itself (ui/src/tailcat/wasm.ts), so a transparent
	// browser decode would hand it 27MB it then fails to gunzip again. The
	// length is what lets the page show download progress.
	mux.HandleFunc("GET /main.wasm.gz", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/octet-stream")
		w.Header().Set("Content-Length", strconv.Itoa(len(playgroundWasm)))
		w.Write(playgroundWasm)
	})

	fmt.Printf("Tailcat Playground at http://%s/\n", *listen)
	if err := http.ListenAndServe(*listen, mux); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
