package spl

import (
	"net/http"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

// run parses src and evaluates it against body, returning the new body.
func run(t *testing.T, src, body string, lib *Library) (string, *Denial) {
	t.Helper()
	prog, err := Parse(src)
	require.NoError(t, err)
	req := &Request{Body: []byte(body)}
	denied, err := prog.Run(lib, req)
	require.NoError(t, err)
	return string(req.Body), denied
}

// matches reports whether the condition in src holds for body.
func matches(t *testing.T, cond, body string) bool {
	t.Helper()
	out, _ := run(t, "set hit to true when "+cond, body, nil)
	return gjson.Get(out, "hit").Bool()
}

func TestSPL_Path_NegativeIndex(t *testing.T) {
	body := `{"messages":[{"role":"system"},{"role":"user","content":"hi"},{"role":"assistant"}],"tools":[{"function":{"name":"f1"}}],"s":"str"}`

	tests := []struct {
		path string
		want string
		ok   bool
	}{
		{"messages[-1].role", "messages.2.role", true},
		{"messages[-3].role", "messages.0.role", true},
		{"messages[-4].role", "", false},
		{"messages[0].role", "messages.0.role", true},
		{"tools[-1].function.name", "tools.0.function.name", true},
		{"s[-1]", "", false},
		{"missing[-1]", "", false},
	}
	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			prog, err := Parse("remove " + tt.path)
			require.NoError(t, err)
			got, ok := prog.clauses[0].path.gjsonPath([]byte(body))
			assert.Equal(t, tt.ok, ok)
			assert.Equal(t, tt.want, got)
		})
	}

	// Root-level array with a negative index.
	prog, err := Parse("remove a[-1]")
	require.NoError(t, err)
	got, ok := prog.clauses[0].path.gjsonPath([]byte(`{"a":[1,2,3]}`))
	assert.True(t, ok)
	assert.Equal(t, "a.2", got)

	assert.True(t, matches(t, `messages[-1].role = "assistant"`, body))
	assert.True(t, matches(t, `messages[-1].content is missing`, body))
	assert.True(t, matches(t, `messages[-9].role is missing`, body))
	assert.False(t, matches(t, `messages[-9].role is present`, body))
}

func TestSPL_Eval_Compare(t *testing.T) {
	body := `{"s":"abc","n":2,"b":true,"z":null,"o":{"k":1},"a":[1],"big":12345678901234567890}`
	tests := []struct {
		cond string
		want bool
	}{
		{`s = "abc"`, true},
		{`s == "abc"`, true},
		{`s = "abd"`, false},
		{`s != "abd"`, true},
		{`s = 2`, false},
		{`s != 2`, true},
		{`n = 2`, true},
		{`n = 2.0`, true},
		{`n = "2"`, false},
		{`b = true`, true},
		{`b = false`, false},
		{`b != false`, true},
		{`z = null`, true},
		{`z != null`, false},
		{`missing = null`, false},
		{`missing != null`, true},
		{`missing != "x"`, true},
		{`missing = "x"`, false},
		{`o = "x"`, false},
		{`o != "x"`, true},
		{`a = 1`, false},
		{`n < 3`, true},
		{`n <= 2`, true},
		{`n > 2`, false},
		{`n >= 2`, true},
		{`n < "3"`, false},
		{`s < "abd"`, true},
		{`s > "abb"`, true},
		{`s >= "abc"`, true},
		{`s <= "abb"`, false},
		{`b < true`, false},
		{`z < 1`, false},
		{`missing < 1`, false},
		{`missing > 1`, false},
		{`s matches /^a.c$/`, true},
		{`s matches /^b/`, false},
		{`n matches /2/`, false},
		{`missing matches /.*/`, false},
		{`s is present`, true},
		{`z is present`, true},
		{`z is missing`, false},
		{`missing is missing`, true},
		{`o.k is present`, true},
		{`s in ["x", "abc"]`, true},
		{`s in ["x"]`, false},
		{`n in [1, 2]`, true},
		{`z in [null]`, true},
		{`missing in [null]`, false},
		{`s in []`, false},
		{`s = "abc" and n = 2`, true},
		{`s = "abc" and n = 3`, false},
		{`s = "x" or n = 2`, true},
		{`not s = "x"`, true},
		{`not (s = "abc" or n = 3)`, false},
		{`s = "x" or n = 2 and b = false`, false},
		{`(s = "x" or n = 2) and b = true`, true},
		{`big = 12345678901234567890`, true},
	}
	for _, tt := range tests {
		t.Run(tt.cond, func(t *testing.T) {
			assert.Equal(t, tt.want, matches(t, tt.cond, body))
		})
	}
}

func TestSPL_Eval_Actions(t *testing.T) {
	t.Run("default only fills missing", func(t *testing.T) {
		out, _ := run(t, "default temperature to 0.2\ndefault top_p to 0.9\ndefault stop to \"x\"", `{"temperature":0.7,"stop":null}`, nil)
		assert.Equal(t, 0.7, gjson.Get(out, "temperature").Float())
		assert.Equal(t, 0.9, gjson.Get(out, "top_p").Float())
		assert.Equal(t, gjson.Null, gjson.Get(out, "stop").Type, "null counts as present")
	})

	t.Run("set overwrites and creates nested", func(t *testing.T) {
		out, _ := run(t, `set temperature to 0.1
set chat_template_kwargs.enable_thinking to false
set meta to {"a": [1, 2], "b": "s"}
set stop to ["</s>", "\n"]
set n to null
set big to 12345678901234567890`, `{"temperature":0.7}`, nil)
		assert.Equal(t, `{"temperature":0.1,"chat_template_kwargs":{"enable_thinking":false},"meta":{"a":[1,2],"b":"s"},"stop":["</s>","\n"],"n":null,"big":12345678901234567890}`, out)
	})

	t.Run("remove present and missing", func(t *testing.T) {
		out, _ := run(t, "remove a\nremove missing\nremove o.k\nremove arr[-1]\nremove arr[-9]", `{"a":1,"o":{"k":1,"j":2},"arr":[1,2,3]}`, nil)
		assert.Equal(t, `{"o":{"j":2},"arr":[1,2]}`, out)
	})

	t.Run("set through negative index", func(t *testing.T) {
		out, _ := run(t, `set messages[-1].content to "replaced"`, `{"messages":[{"content":"a"},{"content":"b"}]}`, nil)
		assert.Equal(t, "replaced", gjson.Get(out, "messages.1.content").String())
		assert.Equal(t, "a", gjson.Get(out, "messages.0.content").String())
	})

	t.Run("unresolvable negative index is a no-op", func(t *testing.T) {
		out, _ := run(t, `set messages[-1].content to "x"`, `{"messages":[]}`, nil)
		assert.Equal(t, `{"messages":[]}`, out)
	})

	t.Run("body prefix escapes namespace words", func(t *testing.T) {
		out, _ := run(t, `set body.context to [1]
remove body.auth
set body.request.x to 1`, `{"auth":"k"}`, nil)
		assert.Equal(t, `{"context":[1],"request":{"x":1}}`, out)
	})

	t.Run("clauses run in order and conditions gate them", func(t *testing.T) {
		out, _ := run(t, `set a to 1
set a to 2 when a = 1
set a to 3 when a = 1
remove b when a = 2`, `{"b":true}`, nil)
		assert.Equal(t, `{"a":2}`, out)
	})
}

func TestSPL_Eval_Context(t *testing.T) {
	prog, err := Parse(`set context.s to "str"
set context.n to 1.5
set context.b to true
set context.o to {"k": "v"}
set context.l to [1, "a"]
default context.s to "ignored"
default context.d to "filled"
set context.gone to "x"
set context.gone to null
set context.removed to "x"
remove context.removed
set hit to true when context.s = "str" and context.n = "1.5" and context.missing is missing`)
	require.NoError(t, err)

	req := &Request{Body: []byte(`{}`)}
	denied, err := prog.Run(nil, req)
	require.NoError(t, err)
	assert.Nil(t, denied)
	assert.Equal(t, map[string]string{
		"s": "str",
		"n": "1.5",
		"b": "true",
		"o": `{"k":"v"}`,
		"l": `[1,"a"]`,
		"d": "filled",
	}, req.Context)
	assert.True(t, gjson.GetBytes(req.Body, "hit").Bool())

	// An existing map is written in place.
	existing := map[string]string{"pre": "set"}
	req = &Request{Body: []byte(`{}`), Context: existing}
	_, err = prog.Run(nil, req)
	require.NoError(t, err)
	assert.Equal(t, "set", existing["pre"])
	assert.Equal(t, "str", existing["s"])
}

func TestSPL_Eval_RequestNamespace(t *testing.T) {
	prog, err := Parse(`set key to true when auth.key is present
set key_val to true when auth.key = "secret"
set model to true when request.model = "alias"
set path to true when request.path matches /^\/v1\/chat/
set method to true when request.method = "POST"
set hdr to true when request.header.x-session-id = "abc"
set hdr_ci to true when request.header.X-Session-Id = "abc"
set hdr_missing to true when request.header.nope is missing`)
	require.NoError(t, err)

	req := &Request{
		Body:   []byte(`{}`),
		APIKey: "secret",
		Model:  "alias",
		Path:   "/v1/chat/completions",
		Method: "POST",
		Header: http.Header{"X-Session-Id": []string{"abc"}},
	}
	_, err = prog.Run(nil, req)
	require.NoError(t, err)
	assert.Equal(t, `{"key":true,"key_val":true,"model":true,"path":true,"method":true,"hdr":true,"hdr_ci":true,"hdr_missing":true}`, string(req.Body))

	// Everything absent.
	prog, err = Parse(`set a to true when auth.key is missing
set b to true when request.model is missing
set c to true when request.header.x is missing
set d to true when request.path is missing`)
	require.NoError(t, err)
	req = &Request{Body: []byte(`{}`)}
	_, err = prog.Run(nil, req)
	require.NoError(t, err)
	assert.Equal(t, `{"a":true,"b":true,"c":true,"d":true}`, string(req.Body))
}

func TestSPL_Eval_Deny(t *testing.T) {
	t.Run("default status", func(t *testing.T) {
		out, denied := run(t, `deny "no"
set after to true`, `{}`, nil)
		require.NotNil(t, denied)
		assert.Equal(t, 403, denied.Status)
		assert.Equal(t, "no", denied.Message)
		assert.Equal(t, `{}`, out, "nothing after deny runs")
	})

	t.Run("custom status", func(t *testing.T) {
		_, denied := run(t, `deny 400 "messages is required" when messages is missing`, `{}`, nil)
		require.NotNil(t, denied)
		assert.Equal(t, 400, denied.Status)
	})

	t.Run("condition false", func(t *testing.T) {
		out, denied := run(t, `deny "no" when a = 1
set after to true`, `{"a":2}`, nil)
		assert.Nil(t, denied)
		assert.True(t, gjson.Get(out, "after").Bool())
	})

	t.Run("deny inside apply stops caller", func(t *testing.T) {
		lib, err := NewLibrary(map[string]string{
			"authed": `deny 401 "login required" when auth.key is missing`,
		}, nil)
		require.NoError(t, err)
		out, denied := run(t, "apply authed\nset after to true", `{}`, lib)
		require.NotNil(t, denied)
		assert.Equal(t, 401, denied.Status)
		assert.Equal(t, `{}`, out)
	})
}

func TestSPL_Eval_ApplyOrderAndDepth(t *testing.T) {
	lib, err := NewLibrary(map[string]string{
		"inner": "set inner to true\nset shared to \"inner\"",
		"outer": "apply inner\nset outer to true",
	}, nil)
	require.NoError(t, err)

	out, denied := run(t, `set shared to "before"
apply outer
set after to true`, `{}`, lib)
	assert.Nil(t, denied)
	assert.Equal(t, `{"shared":"inner","inner":true,"outer":true,"after":true}`, out)

	// Applied policies see the caller's context map.
	lib, err = NewLibrary(map[string]string{"tag": `set context.tag to "x"`}, nil)
	require.NoError(t, err)
	prog, err := Parse("apply tag")
	require.NoError(t, err)
	req := &Request{Body: []byte(`{}`)}
	_, err = prog.Run(lib, req)
	require.NoError(t, err)
	assert.Equal(t, "x", req.Context["tag"])

	// A hand-built cyclic library trips the depth guard instead of looping.
	cyclic := &Library{policies: map[string]*Program{}}
	a, err := Parse("apply b")
	require.NoError(t, err)
	b, err := Parse("apply a")
	require.NoError(t, err)
	cyclic.policies["a"] = a
	cyclic.policies["b"] = b
	_, err = a.Run(cyclic, &Request{Body: []byte(`{}`)})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "policy nesting deeper than 32")

	// Unknown policy at run time is an error, not a panic.
	_, err = a.Run(&Library{policies: map[string]*Program{}}, &Request{Body: []byte(`{}`)})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unknown policy")
	_, err = a.Run(nil, &Request{Body: []byte(`{}`)})
	require.Error(t, err)
}

func TestSPL_Eval_InvalidBody(t *testing.T) {
	// gjson tolerates junk: reads are absent, writes still produce JSON.
	prog, err := Parse(`deny "bad" when messages is missing`)
	require.NoError(t, err)
	denied, err := prog.Run(nil, &Request{Body: []byte(`not json`)})
	require.NoError(t, err)
	require.NotNil(t, denied)
}

func TestSPL_Run_Concurrent(t *testing.T) {
	lib, err := NewLibrary(map[string]string{"p": `default temperature to 0.2`}, nil)
	require.NoError(t, err)
	prog, err := Parse(`apply p
set context.w to "coding" when model matches /coder/
deny "x" when messages is missing`)
	require.NoError(t, err)

	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			req := &Request{Body: []byte(`{"model":"qwen-coder","messages":[]}`)}
			denied, err := prog.Run(lib, req)
			assert.NoError(t, err)
			assert.Nil(t, denied)
			assert.Equal(t, 0.2, gjson.GetBytes(req.Body, "temperature").Float())
			assert.Equal(t, "coding", req.Context["w"])
		}()
	}
	wg.Wait()
}
