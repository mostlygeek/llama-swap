package config

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDSL_Tokenize(t *testing.T) {
	tests := []struct {
		name   string
		input  string
		expect []token
		errMsg string
	}{
		{
			name:  "single identifier",
			input: "abc",
			expect: []token{
				{tokIdent, "abc"},
				{tokEOF, ""},
			},
		},
		{
			name:  "identifier with hyphens and dots",
			input: "model-name.v2",
			expect: []token{
				{tokIdent, "model-name.v2"},
				{tokEOF, ""},
			},
		},
		{
			name:  "and expression",
			input: "a & b",
			expect: []token{
				{tokIdent, "a"},
				{tokAnd, "&"},
				{tokIdent, "b"},
				{tokEOF, ""},
			},
		},
		{
			name:  "or expression",
			input: "a | b",
			expect: []token{
				{tokIdent, "a"},
				{tokOr, "|"},
				{tokIdent, "b"},
				{tokEOF, ""},
			},
		},
		{
			name:  "parentheses",
			input: "(a | b) & c",
			expect: []token{
				{tokLParen, "("},
				{tokIdent, "a"},
				{tokOr, "|"},
				{tokIdent, "b"},
				{tokRParen, ")"},
				{tokAnd, "&"},
				{tokIdent, "c"},
				{tokEOF, ""},
			},
		},
		{
			name:  "ref token",
			input: "+llms & v",
			expect: []token{
				{tokRef, "llms"},
				{tokAnd, "&"},
				{tokIdent, "v"},
				{tokEOF, ""},
			},
		},
		{
			name:  "no whitespace",
			input: "(a|b)&c",
			expect: []token{
				{tokLParen, "("},
				{tokIdent, "a"},
				{tokOr, "|"},
				{tokIdent, "b"},
				{tokRParen, ")"},
				{tokAnd, "&"},
				{tokIdent, "c"},
				{tokEOF, ""},
			},
		},
		{
			name:   "empty ref",
			input:  "+",
			errMsg: "expected set name after '+'",
		},
		{
			name:   "invalid character",
			input:  "a @ b",
			errMsg: "unexpected character",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tokens, err := tokenize(tt.input)
			if tt.errMsg != "" {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.errMsg)
			} else {
				require.NoError(t, err)
				assert.Equal(t, tt.expect, tokens)
			}
		})
	}
}

func TestDSL_ParseAndExpand(t *testing.T) {
	tests := []struct {
		name   string
		dsl    string
		refs   map[string][][]string
		expect [][]string
		errMsg string
	}{
		{
			name:   "single model",
			dsl:    "L",
			expect: [][]string{{"L"}},
		},
		{
			name:   "two models with AND",
			dsl:    "a & b",
			expect: [][]string{{"a", "b"}},
		},
		{
			name:   "two models with OR",
			dsl:    "a | b",
			expect: [][]string{{"a"}, {"b"}},
		},
		{
			name:   "three models with OR",
			dsl:    "a | b | c",
			expect: [][]string{{"a"}, {"b"}, {"c"}},
		},
		{
			name: "cartesian product (a|b) & (c|d)",
			dsl:  "(a | b) & (c | d)",
			expect: [][]string{
				{"a", "c"},
				{"a", "d"},
				{"b", "c"},
				{"b", "d"},
			},
		},
		{
			name: "three-way AND",
			dsl:  "a & b & c",
			expect: [][]string{
				{"a", "b", "c"},
			},
		},
		{
			name: "(g | q | m) & v",
			dsl:  "(g | q | m) & v",
			expect: [][]string{
				{"g", "v"},
				{"q", "v"},
				{"m", "v"},
			},
		},
		{
			name: "(g | q) & v & e",
			dsl:  "(g | q) & v & e",
			expect: [][]string{
				{"e", "g", "v"},
				{"e", "q", "v"},
			},
		},
		{
			name: "precedence: a | b & c means a | (b & c)",
			dsl:  "a | b & c",
			expect: [][]string{
				{"a"},
				{"b", "c"},
			},
		},
		{
			name: "+ref inlining",
			dsl:  "+llms & v",
			refs: map[string][][]string{
				"llms": {{"g"}, {"q"}, {"m"}},
			},
			expect: [][]string{
				{"g", "v"},
				{"q", "v"},
				{"m", "v"},
			},
		},
		{
			name: "+ref chained",
			dsl:  "+with_tts & e",
			refs: map[string][][]string{
				"with_tts": {{"g", "v"}, {"q", "v"}, {"m", "v"}},
			},
			expect: [][]string{
				{"e", "g", "v"},
				{"e", "q", "v"},
				{"e", "m", "v"},
			},
		},
		{
			name: "dedup within combination",
			dsl:  "a & a",
			expect: [][]string{
				{"a"},
			},
		},
		{
			name:   "empty expression",
			dsl:    "",
			errMsg: "empty DSL expression",
		},
		{
			name:   "unmatched open paren",
			dsl:    "(a | b",
			errMsg: "missing closing parenthesis",
		},
		{
			name:   "unmatched close paren",
			dsl:    "a | b)",
			errMsg: "unexpected token",
		},
		{
			name:   "unknown ref",
			dsl:    "+unknown",
			errMsg: "unknown set reference +unknown",
		},
		{
			name:   "empty parens",
			dsl:    "()",
			errMsg: "unexpected token",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			refs := tt.refs
			if refs == nil {
				refs = map[string][][]string{}
			}
			result, err := ParseAndExpandDSL(tt.dsl, refs)
			if tt.errMsg != "" {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.errMsg)
			} else {
				require.NoError(t, err)
				assert.Equal(t, tt.expect, result)
			}
		})
	}
}

func TestDSL_ExpansionCap(t *testing.T) {
	// Build an expression that would exceed 1000 combinations:
	// (a1|a2|...|a32) & (b1|b2|...|b32) = 1024 combos
	var aItems, bItems []string
	for i := 0; i < 32; i++ {
		aItems = append(aItems, fmt.Sprintf("a%d", i))
		bItems = append(bItems, fmt.Sprintf("b%d", i))
	}
	dsl := fmt.Sprintf("(%s) & (%s)",
		join(aItems, " | "),
		join(bItems, " | "),
	)
	_, err := ParseAndExpandDSL(dsl, map[string][][]string{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "exceeded")
}

func TestDSL_ExtractRefs(t *testing.T) {
	refs, err := extractRefs("+llms & v & +other")
	require.NoError(t, err)
	assert.Equal(t, []string{"llms", "other"}, refs)

	refs, err = extractRefs("a & b")
	require.NoError(t, err)
	assert.Empty(t, refs)
}

func join(items []string, sep string) string {
	result := ""
	for i, item := range items {
		if i > 0 {
			result += sep
		}
		result += item
	}
	return result
}
