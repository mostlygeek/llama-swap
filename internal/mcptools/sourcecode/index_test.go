package sourcecode

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// writeTree creates files under a fresh directory and returns it.
func writeTree(t *testing.T, files map[string]string) string {
	t.Helper()
	root := t.TempDir()
	for name, content := range files {
		p := filepath.Join(root, filepath.FromSlash(name))
		require.NoError(t, os.MkdirAll(filepath.Dir(p), 0o755))
		require.NoError(t, os.WriteFile(p, []byte(content), 0o644))
	}
	return root
}

func testIndex(t *testing.T) *Index {
	t.Helper()
	root := writeTree(t, map[string]string{
		"internal/process/process.go": "package process\n\nfunc (p *Process) Stop() {\n\tif p.ttl > 0 { stop(); stop() }\n}\n",
		"internal/config/config.go":   "package config\n\n// GlobalTTL is the default ttl.\nGlobalTTL int `yaml:\"globalTTL\"`\n",
		"docs/kb/guides/ttl.md":       "# TTL\n\nSet `ttl` to unload.\r\nwindows line\r\n",
		"ui/src/app.ts":               "export const ttl = 1;\n",
		"node_modules/dep/index.js":   "ttl\n",
		".git/config":                 "ttl\n",
		"assets/logo.png":             "\x89PNG\x00\x00ttl",
	})
	require.NoError(t, os.WriteFile(filepath.Join(filepath.Dir(root), "escape.txt"), []byte("outside\n"), 0o644))
	idx, err := LoadIndex(root)
	require.NoError(t, err)
	return idx
}

func TestSourceCode_FindSkipsGitAndDependencies(t *testing.T) {
	idx := testIndex(t)
	files, err := idx.Find("", "")
	require.NoError(t, err)
	assert.Equal(t, []string{
		"assets/logo.png",
		"docs/kb/guides/ttl.md",
		"internal/config/config.go",
		"internal/process/process.go",
		"ui/src/app.ts",
	}, files)

	files, err = idx.Find("internal", "*.go")
	require.NoError(t, err)
	assert.Equal(t, []string{"internal/config/config.go", "internal/process/process.go"}, files)

	files, err = idx.Find("internal", "config/*.go")
	require.NoError(t, err)
	assert.Equal(t, []string{"internal/config/config.go"}, files, "a glob with / is relative to path")

	_, err = idx.Find("nope", "*.go")
	assert.ErrorContains(t, err, "path not found: nope")
}

func TestSourceCode_List(t *testing.T) {
	idx := testIndex(t)
	entries, err := idx.List("")
	require.NoError(t, err)
	assert.Equal(t, []string{"assets/", "docs/", "internal/", "ui/"}, entries, ".git and node_modules are left out")

	entries, err = idx.List("internal/config")
	require.NoError(t, err)
	assert.Equal(t, []string{"config.go"}, entries)

	for _, bad := range []string{"nope", "internal/config/config.go"} {
		_, err = idx.List(bad)
		assert.Error(t, err, bad)
	}

	entries, err = idx.List("../")
	require.NoError(t, err)
	assert.Equal(t, []string{"assets/", "docs/", "internal/", "ui/"}, entries, "a path above the root is clamped to it")
}

func TestSourceCode_SearchCountsEachLineOnce(t *testing.T) {
	idx := testIndex(t)
	res, err := idx.Search(SearchOptions{Pattern: "stop", MaxMatches: 10})
	require.NoError(t, err)

	require.Len(t, res.Matches, 1, "two hits on one line are one match")
	assert.Equal(t, "internal/process/process.go", res.Matches[0].Path)
	assert.Equal(t, 4, res.Matches[0].Line)
	assert.Equal(t, 1, res.TotalMatches)
	assert.Equal(t, 1, res.FilesMatched)
	assert.Equal(t, 4, res.FilesSearched, "the binary file is not searched")
}

func TestSourceCode_SearchOptions(t *testing.T) {
	idx := testIndex(t)

	tests := []struct {
		name  string
		opts  SearchOptions
		files int
		total int
	}{
		{"case sensitive", SearchOptions{Pattern: "TTL"}, 2, 3},
		{"ignore case", SearchOptions{Pattern: "ttl", IgnoreCase: true}, 4, 6},
		{"regex", SearchOptions{Pattern: `func \(p \*Process\)`}, 1, 1},
		{"literal", SearchOptions{Pattern: "(p *Process)", Literal: true}, 1, 1},
		{"line anchors", SearchOptions{Pattern: "^package"}, 2, 2},
		{"directory", SearchOptions{Pattern: "ttl", IgnoreCase: true, Path: "internal"}, 2, 3},
		{"file", SearchOptions{Pattern: "ttl", Path: "ui/src/app.ts"}, 1, 1},
		{"basename glob", SearchOptions{Pattern: "ttl", IgnoreCase: true, Glob: "*.go"}, 2, 3},
		{"path glob", SearchOptions{Pattern: "ttl", IgnoreCase: true, Glob: "docs/**/*.md"}, 1, 2},
		{"alternatives", SearchOptions{Pattern: "ttl", Glob: "*.{md,ts}"}, 2, 2},
		{"path and glob", SearchOptions{Pattern: "ttl", IgnoreCase: true, Path: "internal", Glob: "process/*.go"}, 1, 1},
		{"empty match", SearchOptions{Pattern: "x*", Path: "ui"}, 1, 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tt.opts.MaxMatches = 100
			res, err := idx.Search(tt.opts)
			require.NoError(t, err)
			assert.Equal(t, tt.files, res.FilesMatched, "files")
			assert.Equal(t, tt.total, res.TotalMatches, "lines")
		})
	}
}

func TestSourceCode_SearchContextAndLimit(t *testing.T) {
	idx := testIndex(t)

	res, err := idx.Search(SearchOptions{Pattern: "if p.ttl", Literal: true, Context: 2, MaxMatches: 10})
	require.NoError(t, err)
	require.Len(t, res.Matches, 1)
	m := res.Matches[0]
	assert.Equal(t, []string{"", "func (p *Process) Stop() {"}, m.Before)
	assert.Equal(t, []string{"}"}, m.After, "context stops at the last line")

	res, err = idx.Search(SearchOptions{Pattern: "ttl", IgnoreCase: true, MaxMatches: 2})
	require.NoError(t, err)
	assert.Len(t, res.Matches, 2)
	assert.Equal(t, 6, res.TotalMatches, "the total counts past the limit")
}

func TestSourceCode_SearchRejectsBadInput(t *testing.T) {
	idx := testIndex(t)
	_, err := idx.Search(SearchOptions{Pattern: "(", MaxMatches: 1})
	assert.Error(t, err)
	_, err = idx.Search(SearchOptions{Pattern: "x", Glob: "*.{go", MaxMatches: 1})
	assert.Error(t, err)
	_, err = idx.Search(SearchOptions{Pattern: "x", Path: "missing", MaxMatches: 1})
	assert.ErrorContains(t, err, "path not found")
}

func TestSourceCode_Read(t *testing.T) {
	idx := testIndex(t)

	got, err := idx.Read("docs/kb/guides/ttl.md", 3, 10)
	require.NoError(t, err)
	assert.Equal(t, 3, got.Offset)
	assert.Equal(t, 4, got.Total)
	assert.Equal(t, []string{"Set `ttl` to unload.", "windows line"}, got.Lines, "CRLF and the final newline are stripped")

	got, err = idx.Read("./internal/config/config.go", 0, 1)
	require.NoError(t, err, "a leading ./ is tolerated")
	assert.Equal(t, []string{"package config"}, got.Lines)

	got, err = idx.Read(`internal\config\config.go`, 1, 1)
	require.NoError(t, err, "Windows separators are accepted")
	assert.Equal(t, "internal/config/config.go", got.Path)

	_, err = idx.Read("internal/config/config.go", 5, 1)
	assert.ErrorContains(t, err, "offset 5 is beyond the end of the file (4 lines total)")

	for _, escape := range []string{"../escape.txt", `..\escape.txt`, `internal\..\..\escape.txt`, "/etc/passwd", `C:\Windows\win.ini`, ".git/config"} {
		_, err = idx.Read(escape, 1, 1)
		assert.ErrorIs(t, err, errNoFile, "%q must not be readable", escape)
	}

	_, err = idx.Read("internal/config/missing.go", 1, 1)
	assert.ErrorIs(t, err, errNoFile)
	assert.Equal(t, []string{"internal/config/config.go"}, idx.Suggest("pkg/config.go", 5))
}

func TestSourceCode_Selector(t *testing.T) {
	idx := &Index{root: writeTree(t, map[string]string{"internal/a/b.go": "", "ui/src/a.ts": ""})}
	tests := []struct {
		dir, glob, path string
		want            bool
	}{
		{"", "", "a/b.go", true},
		{"internal", "", "internal/x.go", true},
		{"internal/", "", "internal/x.go", true},
		{"internal", "", "internalx/x.go", false},
		{"", "*.go", "a/b/c.go", true},
		{"", "*_test.go", "a/b_test.go", true},
		{"", "*_test.go", "a/b.go", false},
		{"", "internal/*.go", "internal/a/b.go", false},
		{"", "internal/**/*.go", "internal/a/b.go", true},
		{"", "internal/**/*.go", "internal/b.go", true},
		{"internal", "a/*.go", "internal/a/b.go", true},
		{"", "ui/**", "ui/src/a.ts", true},
		{"", "file?.go", "file1.go", true},
		{"", "[ab].go", "a.go", true},
		{"ui/src/a.ts", "", "ui/src/a.ts", true},
		{"ui/src/a.ts", "*.go", "ui/src/a.ts", false},
	}
	for _, tt := range tests {
		keep, err := idx.selector(tt.dir, tt.glob)
		require.NoError(t, err)
		assert.Equal(t, tt.want, keep(tt.path), "%q %q vs %q", tt.dir, tt.glob, tt.path)
	}
}

func TestSourceCode_RefForVersion(t *testing.T) {
	assert.Equal(t, "v266", RefForVersion("v266"))
	assert.Equal(t, "main", RefForVersion("v266-3-gabc123"))
	assert.Equal(t, "main", RefForVersion("0"))
	assert.Equal(t, "main", RefForVersion("devel"))
	assert.False(t, strings.ContainsAny(safeDirName("feature/x y"), "/ "))
}

func TestSourceCode_SymlinksCannotLeaveTheCheckout(t *testing.T) {
	outside := writeTree(t, map[string]string{"secret.txt": "secret\n"})
	root := writeTree(t, map[string]string{"a.go": "package a\n"})
	if err := os.Symlink(outside, filepath.Join(root, "link")); err != nil {
		t.Skipf("cannot create a symlink here: %v", err)
	}
	idx, err := LoadIndex(root)
	require.NoError(t, err)

	_, err = idx.Read("link/secret.txt", 1, 1)
	assert.ErrorIs(t, err, errNoFile)
	_, err = idx.List("link")
	assert.Error(t, err)
	_, err = idx.Search(SearchOptions{Pattern: "secret", Path: "link", MaxMatches: 1})
	assert.Error(t, err)

	res, err := idx.Search(SearchOptions{Pattern: "secret", MaxMatches: 1})
	require.NoError(t, err)
	assert.Zero(t, res.TotalMatches, "a whole-tree search does not follow the link")
}
