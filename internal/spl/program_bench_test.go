package spl

import (
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

// benchDenial keeps Run's result observable so the compiler cannot elide the
// call being measured.
var benchDenial *Denial

// benchProgram keeps Parse's result observable for the same reason.
var benchProgram *Program

// benchmarkPrograms are the program shapes measured against one body. Baseline
// is an empty program: its numbers are the loop, the body reset and the call
// overhead, so subtract it to read the other rows.
var benchmarkPrograms = []struct {
	name string
	src  string
}{
	{"Baseline", ""},
	{
		"NoMatch",
		`set a to 1 when model = "no-such-model"
set b to 2 when temperature > 100
set c to 3 when messages[0].role = "system"`,
	},
	{
		"Defaults",
		`default temperature to 0.2
default top_p to 0.95
set chat_template_kwargs to {"reasoning_effort": "high"}`,
	},
	{
		"Regex",
		`set a to 1 when request.model matches /coder/
set b to 2 when messages[0].content matches /message 0/
set c to 3 when request.path matches /^\/v1\/chat/`,
	},
	{
		"NegativeIndex",
		`set a to 1 when messages[-1].role = "user"
set messages[-1].name to "last"`,
	},
	{
		"ContextWrites",
		`set context.workload to "coding"
set context.tier to "gold" when auth.key is present
default context.session to "none"`,
	},
	{
		// The deny fires on the first clause, so the two writes below it never
		// run: this is the cost of rejecting a request.
		"Deny",
		`deny 403 "Model is disabled" when request.model = "qwen-coder"
default temperature to 0.2
default top_p to 0.95`,
	},
	{
		// The hooks.on_request example from docs/config.example.yaml, followed
		// by the kind of clauses a model's filters.policy carries.
		"Realistic",
		`deny 400 "messages is required"
  when messages is missing

apply authenticated
  when request.model matches /^private-/

apply coding-defaults
  when request.model matches /coder/

default context.workload to "default"
remove reasoning_effort
default max_tokens to 4096`,
	},
}

// BenchmarkProgram_Run measures Run for each program shape against a short
// conversation. Both time and allocations are reported.
func BenchmarkProgram_Run(b *testing.B) {
	lib := benchLibrary(b)
	body := benchBody(8, 200)

	for _, tt := range benchmarkPrograms {
		prog := benchParse(b, tt.src)
		b.Run(tt.name, func(b *testing.B) {
			req := benchRequest()
			scratch := make([]byte, 0, len(body)*2)
			b.ReportAllocs()
			for b.Loop() {
				// Run rewrites Body in place, so every iteration starts from
				// the original bytes. The scratch buffer has room for the
				// whole body, so the reset itself never allocates.
				req.Body = append(scratch[:0], body...)
				denial, err := prog.Run(lib, req)
				if err != nil {
					b.Fatal(err)
				}
				benchDenial = denial
			}
		})
	}
}

// BenchmarkProgram_RunBodySize measures how Run scales with the size of the
// request body. gjson and sjson scan the body, and a negative index also
// materializes the array it indexes into, so the two shapes are expected to
// degrade differently as a conversation grows.
func BenchmarkProgram_RunBodySize(b *testing.B) {
	shapes := []struct {
		name string
		src  string
	}{
		{"Defaults", "default temperature to 0.2\nset top_p to 0.95"},
		{"NegativeIndex", `set a to 1 when messages[-1].role = "user"`},
	}

	for _, shape := range shapes {
		prog := benchParse(b, shape.src)
		for _, messages := range []int{4, 32, 256} {
			body := benchBody(messages, 200)
			name := fmt.Sprintf("%s/Messages_%d", shape.name, messages)
			b.Run(name, func(b *testing.B) {
				req := benchRequest()
				scratch := make([]byte, 0, len(body)*2)
				b.SetBytes(int64(len(body)))
				b.ReportAllocs()
				for b.Loop() {
					req.Body = append(scratch[:0], body...)
					denial, err := prog.Run(nil, req)
					if err != nil {
						b.Fatal(err)
					}
					benchDenial = denial
				}
			})
		}
	}
}

// BenchmarkProgram_RunParallel checks that a Program really is usable from
// many goroutines at once, as the package documents, and that doing so does
// not serialize on shared state.
func BenchmarkProgram_RunParallel(b *testing.B) {
	lib := benchLibrary(b)
	body := benchBody(8, 200)
	var src string
	for _, tt := range benchmarkPrograms {
		if tt.name == "Realistic" {
			src = tt.src
		}
	}
	prog := benchParse(b, src)

	b.ReportAllocs()
	b.RunParallel(func(pb *testing.PB) {
		req := benchRequest()
		scratch := make([]byte, 0, len(body)*2)
		for pb.Next() {
			req.Body = append(scratch[:0], body...)
			if _, err := prog.Run(lib, req); err != nil {
				b.Error(err)
				return
			}
		}
	})
}

// BenchmarkProgram_Parse measures the compile cost paid once per program when
// a configuration loads.
func BenchmarkProgram_Parse(b *testing.B) {
	var realistic string
	for _, tt := range benchmarkPrograms {
		if tt.name == "Realistic" {
			realistic = tt.src
		}
	}

	sources := []struct {
		name string
		src  string
	}{
		{"Realistic", realistic},
		{"Clauses_200", benchGeneratedProgram(200)},
	}

	for _, tt := range sources {
		b.Run(tt.name, func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				prog, err := Parse(tt.src)
				if err != nil {
					b.Fatal(err)
				}
				benchProgram = prog
			}
		})
	}
}

// TestSPL_BenchmarkFixtures keeps the benchmark honest. Each row above is only
// meaningful if it exercises what its name claims, and the Run loop only
// measures steady-state work if resetting the body really restores it. A row
// whose condition quietly stops matching would still report a number, so the
// intent is asserted here rather than left to inspection.
func TestSPL_BenchmarkFixtures(t *testing.T) {
	body := benchBody(8, 200)
	require.True(t, gjson.ValidBytes(body), "generated body must be valid JSON")
	require.Len(t, gjson.GetBytes(body, "messages").Array(), 8)
	assert.Equal(t, "user", gjson.GetBytes(body, "messages.7.role").String(),
		"a chat request ends with the user's turn")
	assert.Equal(t, "assistant", gjson.GetBytes(body, "messages.6.role").String())
	assert.Len(t, gjson.GetBytes(body, "messages.0.content").String(), 200)

	lib := benchLibrary(t)
	run := func(t *testing.T, src string) ([]byte, map[string]string, *Denial) {
		t.Helper()
		req := benchRequest()
		req.Body = append([]byte(nil), body...)
		denial, err := benchParse(t, src).Run(lib, req)
		require.NoError(t, err)
		return req.Body, req.Context, denial
	}

	checks := map[string]func(t *testing.T, out []byte, ctx map[string]string, denial *Denial){
		"Baseline": func(t *testing.T, out []byte, _ map[string]string, denial *Denial) {
			assert.Nil(t, denial)
			assert.Equal(t, string(body), string(out), "an empty program changes nothing")
		},
		"NoMatch": func(t *testing.T, out []byte, _ map[string]string, denial *Denial) {
			assert.Nil(t, denial)
			assert.Equal(t, string(body), string(out), "no condition should match")
		},
		"Defaults": func(t *testing.T, out []byte, _ map[string]string, _ *Denial) {
			assert.Equal(t, 0.7, gjson.GetBytes(out, "temperature").Float(), "request value wins")
			assert.Equal(t, 0.95, gjson.GetBytes(out, "top_p").Float())
			assert.Equal(t, "high", gjson.GetBytes(out, "chat_template_kwargs.reasoning_effort").String())
		},
		"Regex": func(t *testing.T, out []byte, _ map[string]string, _ *Denial) {
			for _, key := range []string{"a", "b", "c"} {
				assert.True(t, gjson.GetBytes(out, key).Exists(), "regex %q should match", key)
			}
		},
		"NegativeIndex": func(t *testing.T, out []byte, _ map[string]string, _ *Denial) {
			assert.True(t, gjson.GetBytes(out, "a").Exists(), "the last message is a user turn")
			assert.Equal(t, "last", gjson.GetBytes(out, "messages.7.name").String())
		},
		"ContextWrites": func(t *testing.T, out []byte, ctx map[string]string, _ *Denial) {
			assert.Equal(t, map[string]string{
				"workload": "coding",
				"tier":     "gold",
				"session":  "none",
			}, ctx)
			assert.Equal(t, string(body), string(out), "context writes leave the body alone")
		},
		"Deny": func(t *testing.T, out []byte, _ map[string]string, denial *Denial) {
			require.NotNil(t, denial, "the deny must fire, or this row measures the writes below it")
			assert.Equal(t, 403, denial.Status)
			assert.Equal(t, string(body), string(out), "nothing after the deny runs")
		},
		"Realistic": func(t *testing.T, out []byte, ctx map[string]string, denial *Denial) {
			require.Nil(t, denial)
			assert.Equal(t, 0.95, gjson.GetBytes(out, "top_p").Float(), "coding-defaults applied")
			assert.Equal(t, int64(4096), gjson.GetBytes(out, "max_tokens").Int())
			assert.Equal(t, "coding", ctx["workload"])
		},
	}

	for _, tt := range benchmarkPrograms {
		t.Run(tt.name, func(t *testing.T) {
			check, ok := checks[tt.name]
			require.True(t, ok, "every benchmark program needs a fixture check")
			out, ctx, denial := run(t, tt.src)
			check(t, out, ctx, denial)

			// The Run loop resets the body between iterations, so the same
			// program over a restored body must produce the same result every
			// time. Without this, later iterations would measure a body that
			// earlier ones already rewrote.
			again, _, _ := run(t, tt.src)
			assert.Equal(t, string(out), string(again), "runs over a reset body must match")
		})
	}

	assert.Equal(t, 200, benchParse(t, benchGeneratedProgram(200)).Len())
}

// benchParse compiles src or fails the benchmark.
func benchParse(tb testing.TB, src string) *Program {
	tb.Helper()
	prog, err := Parse(src)
	if err != nil {
		tb.Fatalf("parsing benchmark program: %v", err)
	}
	return prog
}

// benchLibrary builds the policies the Realistic program applies.
func benchLibrary(tb testing.TB) *Library {
	tb.Helper()
	lib, err := NewLibrary(map[string]string{
		"coding-defaults": `default temperature to 0.2
default top_p to 0.95
set context.workload to "coding"`,
		"authenticated": `deny 401 "API key required"
  when auth.key is missing`,
	}, []string{"model"})
	if err != nil {
		tb.Fatalf("building benchmark library: %v", err)
	}
	return lib
}

// benchRequest mirrors what CreateFilterMiddleware builds per request: a
// resolved model name, the request line, an API key and an allocated metadata
// map.
func benchRequest() *Request {
	return &Request{
		Context: make(map[string]string),
		APIKey:  "benchmark-key",
		Model:   "qwen-coder",
		Path:    "/v1/chat/completions",
		Method:  http.MethodPost,
		Header:  http.Header{"X-Session-Id": []string{"bench"}},
	}
}

// benchBody builds a chat completions request with the given number of
// messages, each carrying contentLen characters of deterministic text.
func benchBody(messages, contentLen int) []byte {
	var sb strings.Builder
	sb.WriteString(`{"model":"qwen-coder","temperature":0.7,"stream":false,"messages":[`)
	for i := range messages {
		if i > 0 {
			sb.WriteByte(',')
		}
		// Count roles backwards so the conversation always ends with a user
		// turn, the way a real chat completion request arrives.
		role := "user"
		if (messages-1-i)%2 == 1 {
			role = "assistant"
		}
		content := fmt.Sprintf("%s message %d: ", role, i)
		if pad := contentLen - len(content); pad > 0 {
			content += strings.Repeat("x", pad)
		}
		fmt.Fprintf(&sb, `{"role":%q,"content":%q}`, role, content)
	}
	sb.WriteString(`]}`)
	return []byte(sb.String())
}

// benchGeneratedProgram builds a program with the given number of clauses,
// cycling through the action and condition forms.
func benchGeneratedProgram(clauses int) string {
	var sb strings.Builder
	for i := range clauses {
		switch i % 4 {
		case 0:
			fmt.Fprintf(&sb, "default field_%d to %d\n", i, i)
		case 1:
			fmt.Fprintf(&sb, "set field_%d to \"value %d\"\n  when model matches /coder/\n", i, i)
		case 2:
			fmt.Fprintf(&sb, "remove field_%d\n  when field_%d is present and temperature < 1\n", i, i)
		case 3:
			fmt.Fprintf(&sb, "set context.key_%d to true\n  when field_%d in [1, 2, \"three\"]\n", i, i)
		}
	}
	return sb.String()
}
