package sourcecode

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"testing/fstest"
	"unicode/utf8"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

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

func TestSourceCode_NotEmbeddedTellsTheAgent(t *testing.T) {
	p := NewProvider(Options{Ref: "v266"})

	tools, err := p.Tools(context.Background())
	require.NoError(t, err)
	assert.Len(t, tools, 4, "the tools are listed so a call can explain")

	for name, args := range map[string]string{
		"grep": `{"pattern":"x"}`, "read": `{"path":"a.go"}`, "find": `{"pattern":"*.go"}`, "ls": `{}`,
	} {
		res := call(t, p, name, args)
		assert.True(t, res.isError, name)
		assert.Contains(t, res.text, "not embedded in this build", name)
		assert.Contains(t, res.text, "embed_src", name)
	}
}

func TestSourceCode_EmptySourceIsNotEmbedded(t *testing.T) {
	p := NewProvider(Options{Source: fstest.MapFS{}})
	res := call(t, p, "ls", `{}`)
	assert.True(t, res.isError)
	assert.Contains(t, res.text, "not embedded in this build")
}

func TestSourceCode_UnknownTool(t *testing.T) {
	p := NewProvider(Options{})
	_, err := p.Call(context.Background(), "nope", nil)
	assert.Error(t, err)
}

// readyProvider is a provider serving files.
func readyProvider(t *testing.T, files map[string]string) *Provider {
	t.Helper()
	p := NewProvider(Options{Source: mapFS(files), Ref: "v266"})
	require.NotNil(t, p.index)
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
	p := readyProvider(t, map[string]string{"a.go": "package a\n"})
	tools, err := p.Tools(context.Background())
	require.NoError(t, err)
	require.Len(t, tools, 4)
	for _, tool := range tools {
		assert.True(t, tool.Annotations.ReadOnlyHint, tool.Name)
		assert.True(t, tool.Annotations.IdempotentHint, tool.Name)
		assert.Contains(t, tool.Description, "v266", "%s names the version it works on", tool.Name)
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
