// Command gensource writes llama-swap's source code to an archive for
// docbot's code__* tools. See the docbot package documentation.
//
// Run it from a module that requires llama-swap, usually through
// go:generate. It archives the llama-swap version that module's go.mod
// selects, or the directory given with -dir.
package main

import (
	"bytes"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/mostlygeek/llama-swap/docbot"
	"github.com/mostlygeek/llama-swap/internal/mcptools/sourcecode"
)

func main() {
	out := flag.String("o", "", "archive to write (required)")
	dir := flag.String("dir", "", "llama-swap source directory (default: the version in go.mod)")
	flag.Parse()

	if *out == "" {
		fmt.Fprintln(os.Stderr, "gensource: -o is required")
		flag.Usage()
		os.Exit(2)
	}

	if err := run(*out, *dir); err != nil {
		fmt.Fprintln(os.Stderr, "gensource:", err)
		os.Exit(1)
	}
}

func run(out, dir string) error {
	if dir == "" {
		var err error
		if dir, err = moduleDir(); err != nil {
			return err
		}
	}

	var buf bytes.Buffer
	if err := sourcecode.WriteArchive(&buf, os.DirFS(dir)); err != nil {
		return fmt.Errorf("archiving %s: %w", dir, err)
	}

	// Leave an unchanged archive alone, so its modification time does not
	// change and rebuilds stay quiet.
	if old, err := os.ReadFile(out); err == nil && bytes.Equal(old, buf.Bytes()) {
		return nil
	}
	return os.WriteFile(out, buf.Bytes(), 0o644)
}

// moduleDir asks the go command where the llama-swap module selected by the
// current module lives: the module cache, or a local directory under a
// replace directive.
func moduleDir() (string, error) {
	cmd := exec.Command("go", "list", "-m", "-f", "{{.Dir}}", docbot.ModulePath)
	cmd.Stderr = os.Stderr
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("finding %s: %w", docbot.ModulePath, err)
	}
	dir := strings.TrimSpace(string(out))
	if dir == "" {
		return "", fmt.Errorf("%s is not downloaded; run `go mod download %s`", docbot.ModulePath, docbot.ModulePath)
	}
	return dir, nil
}
