package spl

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSPL_Lexer_Tokens(t *testing.T) {
	tokens, err := lex(`set a-b.c[0] to "x\"y" # comment
  when d == 1 and e != -2.5e3 or f matches /a\/b\d/ and g in [true, null]
deny 400 "no"
default h to {"k": [1, {"n": "}"}]}`)
	require.NoError(t, err)

	kinds := make([]tokenKind, 0, len(tokens))
	texts := make([]string, 0, len(tokens))
	for _, tok := range tokens {
		kinds = append(kinds, tok.kind)
		texts = append(texts, tok.text)
	}
	assert.Equal(t, []tokenKind{
		tokIdent, tokIdent, tokDot, tokIdent, tokLBracket, tokNumber, tokRBracket, tokIdent, tokString,
		tokIdent, tokIdent, tokEq, tokNumber, tokIdent, tokIdent, tokNeq, tokNumber, tokIdent,
		tokIdent, tokIdent, tokRegex, tokIdent, tokIdent, tokIdent, tokLBracket, tokIdent, tokComma, tokIdent, tokRBracket,
		tokNewline,
		tokIdent, tokNumber, tokString,
		tokNewline,
		tokIdent, tokIdent, tokIdent, tokObject,
		tokEOF,
	}, kinds)
	assert.Equal(t, `x"y`, texts[8])
	assert.Equal(t, "-2.5e3", texts[16])
	assert.Equal(t, `a/b\d`, texts[20])
	assert.Equal(t, `{"k":[1,{"n":"}"}]}`, texts[37])
	// The continuation "when" on the next line swallowed the newline before it.
	assert.Equal(t, 2, tokens[9].line)
	assert.Equal(t, 3, tokens[9].col)
}

func TestSPL_Lexer_NewlineRules(t *testing.T) {
	tokens, err := lex("\n\n# leading comment\nremove a\n\n\nremove b\n  and\n  or\nremove c (\n)\n[\n1\n]\r\n")
	require.NoError(t, err)
	var newlines int
	for _, tok := range tokens {
		if tok.kind == tokNewline {
			newlines++
		}
	}
	// remove a | remove b and or | remove c ( ) | [ 1 ]
	assert.Equal(t, 3, newlines)
	assert.Equal(t, tokEOF, tokens[len(tokens)-1].kind)
}

func TestSPL_Lexer_Errors(t *testing.T) {
	tests := []struct {
		name string
		src  string
		want string
	}{
		{"unterminated string", "deny \"oops\n", "line 1:6: unterminated string"},
		{"unterminated regex", "remove a when b matches /abc\n", "line 1:25: unterminated regex"},
		{"unterminated object", "set a to {\"k\": 1", "line 1:10: unterminated object literal"},
		{"invalid object", "set a to {k: 1}", "line 1:10: invalid JSON object literal"},
		{"bad char", "set a to $", "line 1:10: unexpected character '$'"},
		{"lone bang", "remove a when b ! 1", `line 1:17: unexpected character "!"`},
		{"bad number", "set a to 1.2.3", `line 1:10: invalid number "1.2.3"`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := lex(tt.src)
			require.Error(t, err)
			assert.Equal(t, tt.want, err.Error())
		})
	}
}

func TestSPL_Parse_Actions(t *testing.T) {
	prog, err := Parse(`
default temperature to 0.2
set chat_template_kwargs to {"reasoning_effort": "high"}
set stop to ["a", ["b", 1], {"c": null}]
remove reasoning_effort
apply coding-defaults
apply coding-defaults
deny "nope"
deny 429 "slow down"
set set.in to "keywords as keys"
set context.workload to "coding"
`)
	require.NoError(t, err)
	require.Equal(t, 10, prog.Len())
	assert.Equal(t, []string{"coding-defaults"}, prog.Applies())

	c := prog.clauses
	assert.Equal(t, actDefault, c[0].action)
	assert.Equal(t, "0.2", c[0].value.raw)
	assert.Equal(t, actSet, c[1].action)
	assert.Equal(t, `{"reasoning_effort":"high"}`, c[1].value.raw)
	assert.Equal(t, `["a",["b",1],{"c":null}]`, c[2].value.raw)
	assert.Equal(t, kindArray, c[2].value.kind)
	assert.Equal(t, actRemove, c[3].action)
	assert.Equal(t, actApply, c[4].action)
	assert.Equal(t, "coding-defaults", c[4].policy)
	assert.Equal(t, actDeny, c[6].action)
	assert.Equal(t, 403, c[6].status)
	assert.Equal(t, "nope", c[6].message)
	assert.Equal(t, 429, c[7].status)
	assert.Equal(t, "slow down", c[7].message)
	assert.Equal(t, nsBody, c[8].path.ns)
	assert.Equal(t, "set.in", c[8].path.text)
	assert.Equal(t, nsContext, c[9].path.ns)
	assert.Equal(t, "workload", c[9].path.key())
}

func TestSPL_Parse_Conditions(t *testing.T) {
	prog, err := Parse(`
remove a when b = "x"
remove a when b == 1
remove a when b != true
remove a when b < 1 or b <= 2 or b > 3 or b >= 4
remove a when b matches /^co+der$/
remove a when b is present and c is missing
remove a when b in ["x", 2, null]
remove a when a = 1 or b = 2 and c = 3
remove a when not a = 1 and b = 2
remove a when (a = 1 or b = 2) and not (c = 3)
remove a
  when b = 1
  and c = 2
  or d = 3
`)
	require.NoError(t, err)
	require.Equal(t, 11, prog.Len())

	assert.Equal(t, tokEq, prog.clauses[0].cond.(condCmp).op)
	assert.Equal(t, tokEq, prog.clauses[1].cond.(condCmp).op)
	assert.Equal(t, tokNeq, prog.clauses[2].cond.(condCmp).op)
	assert.IsType(t, condOr{}, prog.clauses[3].cond)
	assert.IsType(t, condMatches{}, prog.clauses[4].cond)
	assert.IsType(t, condAnd{}, prog.clauses[5].cond)
	assert.Len(t, prog.clauses[6].cond.(condIn).values, 3)

	// a = 1 or (b = 2 and c = 3)
	or7 := prog.clauses[7].cond.(condOr)
	assert.IsType(t, condCmp{}, or7.left)
	assert.IsType(t, condAnd{}, or7.right)

	// (not a = 1) and b = 2
	and8 := prog.clauses[8].cond.(condAnd)
	assert.IsType(t, condNot{}, and8.left)

	and9 := prog.clauses[9].cond.(condAnd)
	assert.IsType(t, condOr{}, and9.left)
	assert.IsType(t, condNot{}, and9.right)

	// (b = 1 and c = 2) or d = 3, spread over continuation lines
	or10 := prog.clauses[10].cond.(condOr)
	assert.IsType(t, condAnd{}, or10.left)
}

func TestSPL_Parse_Paths(t *testing.T) {
	tests := []struct {
		src     string
		ns      namespace
		segs    []segment
		wantErr string
	}{
		{src: "remove a", ns: nsBody, segs: []segment{{key: "a"}}},
		{src: "remove metadata.client.name", ns: nsBody, segs: []segment{{key: "metadata"}, {key: "client"}, {key: "name"}}},
		{src: "remove messages[0].role", ns: nsBody, segs: []segment{{key: "messages"}, {index: 0, isIndex: true}, {key: "role"}}},
		{src: "remove messages[-1]", ns: nsBody, segs: []segment{{key: "messages"}, {index: -1, isIndex: true}}},
		{src: "remove body.context", ns: nsBody, segs: []segment{{key: "context"}}},
		{src: "remove context", ns: nsBody, segs: []segment{{key: "context"}}},
		{src: "remove context.x", ns: nsContext, segs: []segment{{key: "x"}}},
		{src: "remove a when auth.key is present", ns: nsBody},
		{src: "remove a when request.header.x-session-id is present", ns: nsBody},
		{src: "remove a when request.model = \"m\"", ns: nsBody},
		{src: "remove context.a.b", wantErr: "line 1:8: context paths must be context.<key>"},
		{src: "remove context[0]", wantErr: "line 1:8: context paths must be context.<key>"},
		{src: "set auth.key to \"x\"", wantErr: "line 1:5: auth.* is read-only"},
		{src: "remove request.path", wantErr: "line 1:8: request.* is read-only"},
		{src: "remove a when auth.user is present", wantErr: `line 1:15: unknown auth field "auth.user", only auth.key is available`},
		{src: "remove a when request.body is present", wantErr: `line 1:15: unknown request field "request.body"`},
		{src: "remove a[1.5]", wantErr: "line 1:10: array index must be an integer"},
		{src: "remove a.", wantErr: "line 1:10: unexpected end of program, expected a field name after '.'"},
	}
	for _, tt := range tests {
		t.Run(tt.src, func(t *testing.T) {
			prog, err := Parse(tt.src)
			if tt.wantErr != "" {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.wantErr)
				return
			}
			require.NoError(t, err)
			pth := prog.clauses[0].path
			assert.Equal(t, tt.ns, pth.ns)
			if tt.segs != nil {
				assert.Equal(t, tt.segs, pth.segs)
			}
		})
	}
}

func TestSPL_Parse_Errors(t *testing.T) {
	tests := []struct {
		name string
		src  string
		want string
	}{
		{"missing to", "set a 1", `line 1:7: unexpected "1", expected 'to'`},
		{"missing value", "set a to", "line 1:9: unexpected end of program, expected a value"},
		{"unknown action", "sett a to 1", `line 1:1: unexpected "sett", expected an action (default, set, remove, apply, deny)`},
		{"number action", "1", `line 1:1: unexpected "1", expected an action`},
		{"deny low status", `deny 200 "x"`, "line 1:6: deny status must be an integer between 400 and 599"},
		{"deny high status", `deny 600 "x"`, "line 1:6: deny status must be an integer between 400 and 599"},
		{"deny float status", `deny 403.5 "x"`, "line 1:6: deny status must be an integer between 400 and 599"},
		{"deny missing message", "deny", "line 1:5: unexpected end of program, expected a quoted message"},
		{"object in comparison", `remove a when b = {"x": 1}`, "line 1:19: object and array literals are only allowed in set and default values"},
		{"list in comparison", `remove a when b = [1]`, "line 1:19: object and array literals are only allowed in set and default values"},
		{"invalid regex", "remove a when b matches /(/", "line 1:25: invalid regex:"},
		{"bad predicate", "remove a when b", "line 1:16: unexpected end of program, expected a comparison operator, 'is', 'matches' or 'in'"},
		{"bad is", "remove a when b is absent", `line 1:20: unexpected "absent", expected 'present' or 'missing'`},
		{"trailing garbage", "remove a b", `line 1:10: unexpected "b", expected end of line`},
		{"trailing garbage after when", "remove a when b = 1 c", `line 1:21: unexpected "c", expected end of line`},
		{"unclosed paren", "remove a when (b = 1", "line 1:21: unexpected end of program, expected ')'"},
		{"apply number", "apply 1", `line 1:7: unexpected "1", expected a policy name`},
		{"second line", "remove a\nset b 1", `line 2:7: unexpected "1", expected 'to'`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := Parse(tt.src)
			require.Error(t, err)
			var perr *Error
			require.True(t, errors.As(err, &perr), "error should be *spl.Error")
			assert.Contains(t, err.Error(), tt.want)
		})
	}
}

func TestSPL_Parse_Empty(t *testing.T) {
	for _, src := range []string{"", "\n\n", "# just a comment\n", "  \n # c \n\n"} {
		prog, err := Parse(src)
		require.NoError(t, err)
		assert.Equal(t, 0, prog.Len())
		assert.Empty(t, prog.Applies())
	}
}
