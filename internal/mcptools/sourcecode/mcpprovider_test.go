package sourcecode

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func noGit() (string, error) { return "", exec.ErrNotFound }

// upstream is a local git repository standing in for GitHub.
type upstream struct {
	t       *testing.T
	dir     string
	gitPath string
}

// newUpstream commits files and tags the commit with an annotated tag, the
// kind llama-swap releases use.
func newUpstream(t *testing.T, tag string, files map[string]string) *upstream {
	t.Helper()
	gitPath, err := exec.LookPath("git")
	if err != nil {
		t.Skip("git is not installed")
	}
	u := &upstream{t: t, dir: writeTree(t, files), gitPath: gitPath}
	u.git("init", "--quiet")
	u.git("add", ".")
	u.git("commit", "--quiet", "-m", "init")
	u.git("tag", "--annotate", "-m", tag, tag)
	return u
}

func (u *upstream) git(args ...string) string {
	u.t.Helper()
	cmd := exec.CommandContext(u.t.Context(), u.gitPath, args...)
	cmd.Dir = u.dir
	cmd.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@example.com",
		"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@example.com")
	out, err := cmd.CombinedOutput()
	require.NoError(u.t, err, string(out))
	return strings.TrimSpace(string(out))
}

// advance commits a change to path and moves tag onto it, returning the new
// commit.
func (u *upstream) advance(tag, path, content string) string {
	u.t.Helper()
	require.NoError(u.t, os.WriteFile(filepath.Join(u.dir, filepath.FromSlash(path)), []byte(content), 0o644))
	u.git("commit", "--quiet", "-am", "next")
	u.git("tag", "--force", "--annotate", "-m", tag, tag)
	return u.git("rev-parse", "HEAD")
}

// provider fetches from u. A plain path rather than a file:// URL, which has
// no portable form for a Windows path; git warns that --depth is ignored
// locally and clones.
func (u *upstream) provider(ref string) *Provider {
	return NewProvider(Options{RepoURL: u.dir, Ref: ref, CacheDir: u.t.TempDir()})
}

func newTestProvider(t *testing.T, repoURL, ref string) *Provider {
	t.Helper()
	p := NewProvider(Options{RepoURL: repoURL, Ref: ref, CacheDir: t.TempDir()})
	p.fetcher.lookGit = noGit
	return p
}

type callResult struct {
	text    string
	isError bool
	trunc   bool
}

func call(t *testing.T, p *Provider, name, args string) callResult {
	t.Helper()
	var parsed map[string]json.RawMessage
	require.NoError(t, json.Unmarshal([]byte(args), &parsed))
	res, err := p.Call(context.Background(), name, parsed)
	require.NoError(t, err)
	return callResult{res.Content, res.IsError, res.Truncated}
}

func TestSourceCode_GitShallowClone(t *testing.T) {
	u := newUpstream(t, "v7", map[string]string{"internal/x.go": "package x\n\nconst Answer = 42\n"})
	p := u.provider("v7")

	res := call(t, p, "grep", `{"pattern":"Answer"}`)
	require.False(t, res.isError, res.text)
	assert.Equal(t, "internal/x.go:3: const Answer = 42", res.text)

	res = call(t, p, "read", `{"path":"internal/x.go"}`)
	assert.Contains(t, res.text, "/blob/v7/internal/x.go#L1-L3")

	res = call(t, p, "ls", `{}`)
	assert.Equal(t, "internal/", res.text, "git's own files are not listed")
}

func TestSourceCode_CheckoutIsReusedUntilRefreshed(t *testing.T) {
	u := newUpstream(t, "v1", map[string]string{"a.go": "one\n"})
	f := u.provider("v1").fetcher

	dir, err := f.checkout(context.Background(), false)
	require.NoError(t, err)
	u.advance("v1", "a.go", "two\n")

	_, err = f.checkout(context.Background(), false)
	require.NoError(t, err)
	data, _ := os.ReadFile(filepath.Join(dir, "a.go"))
	assert.Equal(t, "one\n", string(data), "a checkout on disk is reused")

	_, err = f.checkout(context.Background(), true)
	require.NoError(t, err)
	data, _ = os.ReadFile(filepath.Join(dir, "a.go"))
	assert.Equal(t, "two\n", string(data))

	entries, err := os.ReadDir(f.cacheDir)
	require.NoError(t, err)
	require.Len(t, entries, 1, "the swap leaves neither the scratch clone nor the old checkout behind")
	assert.Equal(t, "v1", entries[0].Name())
}

func TestSourceCode_StaleFetchesAreRemoved(t *testing.T) {
	u := newUpstream(t, "v1", map[string]string{"a.go": "package a\n"})
	f := u.provider("v1").fetcher
	stale := filepath.Join(f.cacheDir, ".fetch-old")
	fresh := filepath.Join(f.cacheDir, ".fetch-new")
	require.NoError(t, os.MkdirAll(stale, 0o755))
	require.NoError(t, os.MkdirAll(fresh, 0o755))
	old := time.Now().Add(-2 * fetchTimeout)
	require.NoError(t, os.Chtimes(stale, old, old))

	_, err := f.checkout(context.Background(), false)
	require.NoError(t, err)
	assert.NoDirExists(t, stale)
	assert.DirExists(t, fresh, "a clone that may still be running is left alone")
}

func TestSourceCode_NoGitTellsTheAgent(t *testing.T) {
	p := newTestProvider(t, DefaultRepoURL, "v1")

	for name, args := range map[string]string{"grep": `{"pattern":"x"}`, "update": `{}`} {
		res := call(t, p, name, args)
		assert.True(t, res.isError, name)
		assert.Contains(t, res.text, "git is not installed", name)
		assert.Contains(t, res.text, "Tell the user to install git", name)
	}

	res := call(t, p, "status", `{}`)
	assert.False(t, res.isError)
	assert.Contains(t, res.text, "git: not installed")
	assert.Contains(t, res.text, "Tell the user to install git")
}

func TestSourceCode_CloneFailureIsAToolError(t *testing.T) {
	gitPath, err := exec.LookPath("git")
	if err != nil {
		t.Skip("git is not installed")
	}
	p := NewProvider(Options{RepoURL: filepath.Join(t.TempDir(), "missing"), Ref: "v1", CacheDir: t.TempDir()})
	var attempts atomic.Int32
	p.fetcher.lookGit = func() (string, error) { attempts.Add(1); return gitPath, nil }

	res := call(t, p, "grep", `{"pattern":"x"}`)
	assert.True(t, res.isError)
	assert.Contains(t, res.text, "not available")
	assert.Contains(t, res.text, "git clone of")

	// A failure is remembered for a while rather than retried on every call.
	call(t, p, "ls", `{}`)
	assert.Equal(t, int32(1), attempts.Load())

	p.now = func() time.Time { return time.Now().Add(2 * retryAfter) }
	call(t, p, "ls", `{}`)
	assert.Equal(t, int32(2), attempts.Load())
}

func TestSourceCode_StillLoading(t *testing.T) {
	release := make(chan struct{})
	p := newTestProvider(t, DefaultRepoURL, "v1")
	p.fetcher.lookGit = func() (string, error) { <-release; return noGit() }
	// Let the background load finish before its cache directory is removed;
	// Windows will not delete a directory another goroutine is writing into.
	t.Cleanup(func() {
		p.mu.Lock()
		done := p.loading
		p.mu.Unlock()
		close(release)
		if done != nil {
			<-done
		}
	})

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	res, err := p.Call(ctx, "grep", map[string]json.RawMessage{"pattern": json.RawMessage(`"x"`)})
	require.NoError(t, err)
	assert.True(t, res.IsError)
	assert.Contains(t, res.Content, "still downloading")
}

func TestSourceCode_UnknownTool(t *testing.T) {
	p := newTestProvider(t, "http://127.0.0.1:0", "v1")
	_, err := p.Call(context.Background(), "nope", nil)
	assert.Error(t, err)
}

// readyProvider is a provider whose index is already loaded from files.
func readyProvider(t *testing.T, files map[string]string) *Provider {
	t.Helper()
	idx, err := LoadIndex(writeTree(t, files))
	require.NoError(t, err)
	p := newTestProvider(t, DefaultRepoURL, "v266")
	p.index = idx
	return p
}

func TestSourceCode_GrepOutput(t *testing.T) {
	p := readyProvider(t, map[string]string{
		"a.go": "one\nttl here\nthree\nfour\nttl again\n",
		"b.go": "ttl\n",
		"c.go": strings.Repeat("x", 600) + "ttl\n",
	})

	res := call(t, p, "grep", `{"pattern":"ttl","context":1,"limit":"2"}`)
	require.False(t, res.isError, res.text)
	assert.Equal(t, "a.go-1- one\na.go:2: ttl here\na.go-3- three\na.go-4- four\na.go:5: ttl again\n\n"+
		"[2 matches limit reached (4 matching lines in all). Use limit=4 for more, or refine pattern]", res.text)
	assert.True(t, res.trunc)

	res = call(t, p, "grep", `{"pattern":"ttl","path":"c.go"}`)
	assert.Contains(t, res.text, "c.go:1: "+strings.Repeat("x", 500)+"... [truncated]")
	assert.Contains(t, res.text, "[Some lines truncated to 500 chars. Use code__read to see full lines]")

	res = call(t, p, "grep", `{"pattern":"TTL","ignore_case":"true","glob":"b.*"}`)
	assert.Equal(t, "b.go:1: ttl", res.text, "a stringified boolean is accepted")
	assert.False(t, res.trunc)

	res = call(t, p, "grep", `{"pattern":"nothing"}`)
	assert.False(t, res.isError, "no matches is an answer, not an error")
	assert.Equal(t, "No matches found", res.text)

	res = call(t, p, "grep", `{"pattern":"ttl","path":"missing/"}`)
	assert.True(t, res.isError)
	assert.Contains(t, res.text, "path not found: missing")

	res = call(t, p, "grep", `{"pattern":"("}`)
	assert.True(t, res.isError)
}

func TestSourceCode_GrepByteLimit(t *testing.T) {
	files := map[string]string{}
	for i := range 400 {
		files[fmt.Sprintf("f%03d.go", i)] = "match " + strings.Repeat("y", 60) + "\n"
	}
	p := readyProvider(t, files)

	res := call(t, p, "grep", `{"pattern":"match","limit":400}`)
	assert.LessOrEqual(t, len(res.text), 16*1024)
	assert.True(t, strings.HasSuffix(res.text, "[16KB limit reached]"), res.text[len(res.text)-80:])
}

func TestSourceCode_ReadOutput(t *testing.T) {
	var long strings.Builder
	for i := 1; i <= 1000; i++ {
		fmt.Fprintf(&long, "line %d %s\n", i, strings.Repeat("y", 60))
	}
	p := readyProvider(t, map[string]string{
		"internal/a.go": "package a\nfunc A() {}\nfunc B() {}\n",
		"long.txt":      long.String(),
	})

	res := call(t, p, "read", `{"path":"internal/a.go","offset":2}`)
	require.False(t, res.isError, res.text)
	assert.Equal(t, "https://github.com/mostlygeek/llama-swap/blob/v266/internal/a.go#L2-L3\nL2: func A() {}\nL3: func B() {}", res.text)
	assert.False(t, res.trunc)

	res = call(t, p, "read", `{"path":"internal/a.go","limit":1}`)
	assert.Equal(t, "https://github.com/mostlygeek/llama-swap/blob/v266/internal/a.go#L1-L1\nL1: package a\n\n"+
		"[2 more lines in file. Use offset=2 to continue.]", res.text)

	// The byte cap stops it before the line cap; the offset it reports must
	// pick up exactly where it stopped.
	res = call(t, p, "read", `{"path":"long.txt"}`)
	assert.True(t, res.trunc)
	assert.LessOrEqual(t, len(res.text), 16*1024)
	var first, last, total, next int
	_, err := fmt.Sscanf(res.text[strings.LastIndex(res.text, "[Showing"):],
		"[Showing lines %d-%d of %d (16KB limit). Use offset=%d to continue.]", &first, &last, &total, &next)
	require.NoError(t, err, res.text[strings.LastIndex(res.text, "\n\n"):])
	assert.Equal(t, []int{1, 1000, last + 1}, []int{first, total, next})
	assert.Contains(t, res.text, fmt.Sprintf("L%d: line %d ", last, last))
	assert.NotContains(t, res.text, fmt.Sprintf("L%d: ", next))

	res = call(t, p, "read", `{"path":"internal/a.go","offset":9}`)
	assert.True(t, res.isError)
	assert.Contains(t, res.text, "offset 9 is beyond the end of the file (3 lines total)")

	res = call(t, p, "read", `{"path":"a.go"}`)
	assert.True(t, res.isError)
	assert.Contains(t, res.text, "file not found: a.go. Did you mean one of: internal/a.go?")
}

func TestSourceCode_FindAndLsOutput(t *testing.T) {
	files := map[string]string{"README.md": "x\n", "ui/src/a.ts": "a\n"}
	for i := range 5 {
		files[fmt.Sprintf("internal/pkg%d/f.go", i)] = "package p\n"
	}
	p := readyProvider(t, files)

	res := call(t, p, "ls", `{}`)
	require.False(t, res.isError, res.text)
	assert.Equal(t, "internal/\nREADME.md\nui/", res.text)

	res = call(t, p, "ls", `{"path":"internal","limit":2}`)
	assert.Equal(t, "pkg0/\npkg1/\n\n[2 entries limit reached. Use limit=4 for more]", res.text)

	res = call(t, p, "ls", `{"path":"nope"}`)
	assert.True(t, res.isError)

	res = call(t, p, "find", `{"pattern":"*.go","limit":2}`)
	assert.Equal(t, "internal/pkg0/f.go\ninternal/pkg1/f.go\n\n[2 results limit reached. Use limit=4 for more, or refine pattern]", res.text)

	res = call(t, p, "find", `{"pattern":"src/*.ts","path":"ui"}`)
	assert.Equal(t, "ui/src/a.ts", res.text)

	res = call(t, p, "find", `{"pattern":"*.rs"}`)
	assert.Equal(t, "No files found matching pattern", res.text)
}

func TestSourceCode_ToolAnnotations(t *testing.T) {
	p := newTestProvider(t, DefaultRepoURL, "v266")
	tools, err := p.Tools(context.Background())
	require.NoError(t, err)
	require.Len(t, tools, 6)
	for _, tool := range tools {
		// update replaces the local checkout; everything else only reads.
		assert.Equal(t, tool.Name != "update", tool.Annotations.ReadOnlyHint, tool.Name)
		if tool.Name != "status" {
			assert.Contains(t, tool.Description, "v266", "%s names the version it works on", tool.Name)
		}
	}
}

func TestSourceCode_ReadReturnsLongLinesWhole(t *testing.T) {
	long := strings.Repeat("x", 600) + "END"
	huge := strings.Repeat("é", 12*1024) // 24KB, over the result limit
	p := readyProvider(t, map[string]string{"a.go": long + "\n" + huge + "\nlast\n"})

	res := call(t, p, "grep", `{"pattern":"END"}`)
	assert.Contains(t, res.text, "Use code__read to see full lines")
	res = call(t, p, "read", `{"path":"a.go","limit":1}`)
	assert.Contains(t, res.text, "L1: "+long+"\n", "a line grep clipped is whole here")

	res = call(t, p, "read", `{"path":"a.go","offset":2}`)
	assert.True(t, res.trunc, "a cut line marks the result truncated")
	assert.LessOrEqual(t, len(res.text), 16*1024)
	assert.True(t, utf8.ValidString(res.text), "the cut does not split a character")
	assert.Contains(t, res.text, "... [truncated]")
	assert.Contains(t, res.text, "[Line 2 is longer than the 16KB limit and was cut. Showing lines 2-2 of 3 (16KB limit). Use offset=3 to continue.]")
}

func TestSourceCode_GrepLimitIsCapped(t *testing.T) {
	var many strings.Builder
	for range 1500 {
		many.WriteString("m\n")
	}
	p := readyProvider(t, map[string]string{"a.go": many.String()})

	res := call(t, p, "grep", `{"pattern":"m","limit":100000}`)
	assert.Contains(t, res.text, "[1000 matches limit reached (1500 matching lines in all). Refine the pattern, path or glob to see the rest")

	res = call(t, p, "grep", `{"pattern":"m","limit":600}`)
	assert.Contains(t, res.text, "Use limit=1000 for more", "the suggestion stays within the cap")
}
