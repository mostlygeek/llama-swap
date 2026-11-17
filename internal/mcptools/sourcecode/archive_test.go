package sourcecode

import (
	"bytes"
	"io/fs"
	"os"
	"os/exec"
	"sort"
	"strings"
	"testing"
	"testing/fstest"
)

func TestArchive_RoundTripIsDeterministic(t *testing.T) {
	src := fstest.MapFS{
		"main.go":                    {Data: []byte("package main\n")},
		"main_test.go":               {Data: []byte("package main\n")},
		"internal/a/a.go":            {Data: []byte("package a\n")},
		"ui/src/app.ts":              {Data: []byte("export {}\n")},
		"ui/src/app.test.ts":         {Data: []byte("test\n")},
		"ui/src/App.svelte":          {Data: []byte("<p/>\n")},
		"ui/node_modules/x/index.ts": {Data: []byte("dep\n")},
		"internal/x/_src/copy.go":    {Data: []byte("package copy\n")},
		"docs/README.md":             {Data: []byte("# docs\n")},
		"scripts/tool.ts":            {Data: []byte("not under ui\n")},
		".github/workflows/ci.go":    {Data: []byte("hidden\n")},
	}

	var a, b bytes.Buffer
	if err := WriteArchive(&a, src); err != nil {
		t.Fatal(err)
	}
	if err := WriteArchive(&b, src); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(a.Bytes(), b.Bytes()) {
		t.Error("two archives of the same files differ")
	}

	got, err := ReadArchive(a.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	fs.WalkDir(got, ".", func(name string, d fs.DirEntry, err error) error {
		if !d.IsDir() {
			names = append(names, name)
		}
		return nil
	})
	want := "internal/a/a.go main.go ui/src/App.svelte ui/src/app.ts"
	if strings.Join(names, " ") != want {
		t.Errorf("files = %v, want %s", names, want)
	}
}

// The archive must hold the same files as `make src`, which lists them with
// git. Skipped outside a git checkout.
func TestArchive_MatchesMakeSrc(t *testing.T) {
	out, err := exec.Command("git", "-C", "../../..", "ls-files", "--cached", "--others", "--exclude-standard", "--",
		"*.go", "ui/*.ts", "ui/*.svelte", ":(exclude)*_test.go", ":(exclude)*.test.ts").Output()
	if err != nil {
		t.Skipf("git ls-files: %v", err)
	}
	want := strings.Fields(string(out))
	sort.Strings(want)

	var buf bytes.Buffer
	if err := WriteArchive(&buf, os.DirFS("../../..")); err != nil {
		t.Fatal(err)
	}
	got, err := ReadArchive(buf.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	fs.WalkDir(got, ".", func(name string, d fs.DirEntry, err error) error {
		if !d.IsDir() {
			names = append(names, name)
		}
		return nil
	})

	sort.Strings(names)
	if strings.Join(names, "\n") != strings.Join(want, "\n") {
		missing, extra := diff(want, names), diff(names, want)
		t.Errorf("archive differs from git ls-files\nmissing: %v\nextra: %v", missing, extra)
	}
}

func diff(a, b []string) []string {
	in := map[string]bool{}
	for _, s := range b {
		in[s] = true
	}
	var out []string
	for _, s := range a {
		if !in[s] {
			out = append(out, s)
		}
	}
	return out
}
