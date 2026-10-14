//go:build !embed_playground

package main

// Nil without the embed_playground tag; main() refuses to serve an empty page.
var playgroundIndex, playgroundWasm []byte
