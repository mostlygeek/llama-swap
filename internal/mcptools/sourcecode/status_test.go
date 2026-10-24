package sourcecode

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSourceCode_StatusAndUpdate(t *testing.T) {
	u := newUpstream(t, "v1", map[string]string{"a.go": "package a\n// first\n"})
	first := u.git("rev-parse", "HEAD")
	p := u.provider("v1")
	p.version, p.buildCommit = "v1", first[:7]

	res := call(t, p, "status", `{}`)
	assert.False(t, res.isError, res.text)
	assert.Contains(t, res.text, "checkout: none")
	assert.Contains(t, res.text, "repository: v1 is at commit "+first[:12])
	assert.Contains(t, res.text, "Nothing is downloaded yet. Call code__update")
	_, exists := p.fetcher.existing()
	assert.False(t, exists, "status never downloads")

	res = call(t, p, "update", `{}`)
	require.False(t, res.isError, res.text)
	assert.Equal(t, "Downloaded v1 at commit "+first[:12]+".", res.text)

	res = call(t, p, "status", `{}`)
	assert.Contains(t, res.text, "checkout: v1 at commit "+first[:12])
	assert.Contains(t, res.text, "The checkout is up to date")
	assert.Contains(t, res.text, "The checkout is the commit this server was built from.")

	res = call(t, p, "update", `{}`)
	assert.Equal(t, "Already up to date: v1 at commit "+first[:12]+".", res.text)

	second := u.advance("v1", "a.go", "package a\n// second\n")
	res = call(t, p, "status", `{}`)
	assert.Contains(t, res.text, "out of date: v1 has moved from "+first[:12]+" to "+second[:12]+". Call code__update")

	res = call(t, p, "update", `{}`)
	require.False(t, res.isError, res.text)
	assert.Equal(t, "Updated v1 from commit "+first[:12]+" to "+second[:12]+".", res.text)
	res = call(t, p, "grep", `{"pattern":"second"}`)
	assert.Contains(t, res.text, "a.go:2: // second", "searches see the new checkout")

	res = call(t, p, "status", `{}`)
	assert.Contains(t, res.text, "up to date")
	assert.Contains(t, res.text, "This server was built from commit "+first[:7]+", not "+second[:12])

	res = call(t, p, "update", `{"force":true}`)
	assert.Equal(t, "Downloaded v1 again; it is still at commit "+second[:12]+".", res.text)
}

func TestSourceCode_StatusWhenRepositoryUnreachable(t *testing.T) {
	u := newUpstream(t, "v1", map[string]string{"a.go": "package a\n"})
	p := u.provider("v1")
	call(t, p, "update", `{}`)
	require.NoError(t, os.RemoveAll(u.dir))

	res := call(t, p, "status", `{}`)
	assert.False(t, res.isError)
	assert.Contains(t, res.text, "repository: could not check")
	assert.Contains(t, res.text, "still usable")

	res = call(t, p, "update", `{}`)
	assert.True(t, res.isError)
	assert.Contains(t, res.text, "set force=true")

	res = call(t, p, "update", `{"force":true}`)
	assert.True(t, res.isError)
	assert.Contains(t, res.text, "The previous checkout is still in use.")
	res = call(t, p, "grep", `{"pattern":"package"}`)
	assert.False(t, res.isError, "a failed refresh keeps the old checkout: %s", res.text)
}

func TestSourceCode_StatusReportsUnreadableCommit(t *testing.T) {
	// A checkout directory that is not a git checkout, as an earlier build
	// of these tools could leave behind.
	u := newUpstream(t, "v1", map[string]string{"a.go": "package a\n"})
	p := u.provider("v1")
	dir, _ := p.fetcher.existing()
	require.NoError(t, os.MkdirAll(dir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "a.go"), []byte("package a\n"), 0o644))

	res := call(t, p, "status", `{}`)
	assert.Contains(t, res.text, "at commit unknown")
	assert.Contains(t, res.text, "could not be read")
	assert.Contains(t, res.text, "force=true")

	res = call(t, p, "update", `{}`)
	require.False(t, res.isError, res.text)
	assert.Contains(t, res.text, "Updated v1 from commit unknown to ")
}

func TestSourceCode_CompareBuild(t *testing.T) {
	const commitA = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	const commitB = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	p := &Provider{version: "v1"}
	for _, tt := range []struct{ build, want string }{
		{commitA[:7], "is the commit this server was built from"},
		{commitA[:7] + "+", "uncommitted changes"},
		{commitB[:7], "not aaaaaaaaaaaa"},
		{"", ""},
		{"not-a-hash", ""},
	} {
		p.buildCommit = tt.build
		got := p.compareBuild(commitA)
		if tt.want == "" {
			assert.Empty(t, got, tt.build)
		} else {
			assert.Contains(t, got, tt.want, tt.build)
		}
	}
	p.version, p.buildCommit = "0", commitB[:7]
	assert.Empty(t, p.compareBuild(commitA), "a plain go build has a placeholder commit")
}
