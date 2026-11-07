package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/mostlygeek/llama-swap/docbot"
)

func TestGensource_ArchivesDirectory(t *testing.T) {
	out := filepath.Join(t.TempDir(), "src.tar.gz")
	if err := run(out, "../.."); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	src, err := docbot.SourceFromArchive(data)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := src.Open("docbot/docbot.go"); err != nil {
		t.Errorf("archive is missing docbot/docbot.go: %v", err)
	}
	if _, err := src.Open("docbot/docbot_test.go"); err == nil {
		t.Error("archive includes a test file")
	}

	// A second run with the same source leaves the file untouched.
	before, _ := os.Stat(out)
	if err := run(out, "../.."); err != nil {
		t.Fatal(err)
	}
	after, _ := os.Stat(out)
	if !after.ModTime().Equal(before.ModTime()) {
		t.Error("unchanged archive was rewritten")
	}
}

// In llama-swap's own module, `go list -m` finds the main module itself.
func TestGensource_FindsModuleDir(t *testing.T) {
	dir, err := moduleDir()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "go.mod")); err != nil {
		t.Errorf("moduleDir() = %s: %v", dir, err)
	}
}
