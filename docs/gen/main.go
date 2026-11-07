// Command gen copies the config schema and the Docs Agent system prompt into
// docs/. Run it with `go generate ./docs`.
package main

import (
	"log"
	"os"

	"github.com/mostlygeek/llama-swap/docs/internal/docsgen"
)

func main() {
	// go generate runs this from the docs directory.
	files, err := docsgen.Build("..")
	if err != nil {
		log.Fatal(err)
	}
	for name, data := range files {
		if err := os.WriteFile(name, data, 0o644); err != nil {
			log.Fatal(err)
		}
	}
}
