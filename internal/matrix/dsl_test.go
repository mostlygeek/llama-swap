package matrix

import (
	"fmt"
	"strings"
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
				{typ: tokIdent, val: "abc"},
				{typ: tokEOF},
			},
		},
		{
			name:  "identifier with hyphens and dots",
			input: "model-name.v2",
			expect: []token{
				{typ: tokIdent, val: "model-name.v2"},
				{typ: tokEOF},
			},
		},
		{
			name:  "operators and reference",
			input: "(a | +base) & b",
			expect: []token{
				{typ: tokLParen, val: "("},
				{typ: tokIdent, val: "a"},
				{typ: tokOr, val: "|"},
				{typ: tokRef, val: "base"},
				{typ: tokRParen, val: ")"},
				{typ: tokAnd, val: "&"},
				{typ: tokIdent, val: "b"},
				{typ: tokEOF},
			},
		},
		{name: "empty ref", input: "+", errMsg: "expected set name after '+'"},
		{name: "invalid character", input: "a @ b", errMsg: "unexpected character"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tokens, err := tokenize(tt.input)
			if tt.errMsg != "" {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.errMsg)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.expect, tokens)
		})
	}
}

func TestProgram_Expressions(t *testing.T) {
	tests := []struct {
		name     string
		dsl      string
		relevant []string
		expect   [][]string
	}{
		{
			name:     "single model",
			dsl:      "a",
			relevant: []string{"a"},
			expect:   [][]string{{"a"}},
		},
		{
			name:     "and",
			dsl:      "a & b",
			relevant: []string{"a", "b"},
			expect:   [][]string{{"a", "b"}},
		},
		{
			name:     "or",
			dsl:      "a | b | c",
			relevant: []string{"a", "b", "c"},
			expect:   [][]string{{"a"}, {"b"}, {"c"}},
		},
		{
			name:     "cartesian product",
			dsl:      "(a | b) & (c | d)",
			relevant: []string{"a", "b", "c", "d"},
			expect: [][]string{
				{"a", "c"},
				{"a", "d"},
				{"b", "c"},
				{"b", "d"},
			},
		},
		{
			name:     "precedence",
			dsl:      "a | b & c",
			relevant: []string{"a", "b", "c"},
			expect:   [][]string{{"a"}, {"b", "c"}},
		},
		{
			name:     "deduplicates models",
			dsl:      "a & a",
			relevant: []string{"a"},
			expect:   [][]string{{"a"}},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			program := compileIdentity(t, []Definition{{Name: "test", DSL: tt.dsl}}, tt.relevant)
			assert.Equal(t, tt.expect, projectedSets(program, "test", tt.relevant))
		})
	}
}

func TestProgram_References(t *testing.T) {
	models := []string{"g", "q", "m", "v", "e"}
	program := compileIdentity(t, []Definition{
		{Name: "llms", DSL: "g | q | m"},
		{Name: "with_tts", DSL: "+llms & v"},
		{Name: "mega", DSL: "+with_tts & e"},
	}, models)

	assert.Equal(t, [][]string{
		{"e", "g", "v"},
		{"e", "q", "v"},
		{"e", "m", "v"},
	}, projectedSets(program, "mega", models))
}

func TestProgram_OverlappingReferences(t *testing.T) {
	models := []string{"target", "a", "b"}
	program := compileIdentity(t, []Definition{
		{Name: "base", DSL: "a & b"},
		{Name: "extended", DSL: "target & +base & b"},
	}, models)

	result := program.Solve("target", []string{"a", "b"}, nil)
	assert.Empty(t, result.Evict)
	assert.Equal(t, []string{"a", "b", "target"}, result.TargetSet)
}

func TestProgram_CompileErrors(t *testing.T) {
	models := map[string]bool{"a": true}
	resolve := func(name string) (string, bool) {
		return name, models[name]
	}

	tests := []struct {
		name   string
		defs   []Definition
		errMsg string
	}{
		{name: "empty expression", defs: []Definition{{Name: "s", DSL: ""}}, errMsg: "empty DSL expression"},
		{name: "unmatched open paren", defs: []Definition{{Name: "s", DSL: "(a"}}, errMsg: "missing closing parenthesis"},
		{name: "unmatched close paren", defs: []Definition{{Name: "s", DSL: "a)"}}, errMsg: "unexpected token"},
		{name: "empty parens", defs: []Definition{{Name: "s", DSL: "()"}}, errMsg: "unexpected token"},
		{name: "unknown model", defs: []Definition{{Name: "s", DSL: "missing"}}, errMsg: "unknown var or model"},
		{name: "unknown reference", defs: []Definition{{Name: "s", DSL: "+missing"}}, errMsg: "references undefined set"},
		{
			name: "reference cycle",
			defs: []Definition{
				{Name: "a", DSL: "+b"},
				{Name: "b", DSL: "+a"},
			},
			errMsg: "circular reference",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := Compile(tt.defs, resolve)
			require.Error(t, err)
			assert.Contains(t, err.Error(), tt.errMsg)
		})
	}
}

func TestProgram_NoExpansionLimit(t *testing.T) {
	const (
		dimensions = 6
		choices    = 10
	)

	definition, models := productDefinition(dimensions, choices)
	program := compileIdentity(t, []Definition{definition}, models)

	running := make([]string, 0, dimensions+1)
	for dimension := range dimensions {
		running = append(running, fmt.Sprintf("model-%d-9", dimension))
	}
	running = append(running, "outside")

	result := program.Solve("target", running, nil)
	assert.Equal(t, []string{"outside"}, result.Evict)
	assert.Equal(t, "large", result.SetName)
	assert.Len(t, result.TargetSet, dimensions+1)
}

func TestProgram_MoreThan64RelevantModels(t *testing.T) {
	const modelCount = 70
	models := []string{"target"}
	for i := range modelCount {
		models = append(models, fmt.Sprintf("model-%d", i))
	}
	program := compileIdentity(t, []Definition{{
		Name: "wide",
		DSL:  strings.Join(models, " & "),
	}}, models)

	running := append(append([]string(nil), models[1:]...), "outside")
	result := program.Solve("target", running, nil)
	assert.Equal(t, []string{"outside"}, result.Evict)
	assert.Len(t, result.TargetSet, modelCount+1)
}

func TestProgram_DominatedProjection(t *testing.T) {
	program := compileIdentity(t, []Definition{{
		Name: "preferred",
		DSL:  "target & (a | (a & b))",
	}}, []string{"target", "a", "b"})

	result := program.Solve("target", []string{"a", "b"}, nil)
	assert.Empty(t, result.Evict)
	assert.Equal(t, []string{"a", "b", "target"}, result.TargetSet)
}

func TestProgram_TargetAbsent(t *testing.T) {
	program := compileIdentity(t, []Definition{{
		Name: "known",
		DSL:  "a & b",
	}}, []string{"a", "b"})

	result := program.Solve("missing", []string{"a", "b"}, nil)
	assert.Equal(t, []string{"a", "b"}, result.Evict)
	assert.Equal(t, []string{"missing"}, result.TargetSet)
	assert.Empty(t, result.SetName)
}

func compileIdentity(t *testing.T, definitions []Definition, models []string) *Program {
	t.Helper()
	known := make(map[string]bool, len(models))
	for _, model := range models {
		known[model] = true
	}
	program, err := Compile(definitions, func(name string) (string, bool) {
		return name, known[name]
	})
	require.NoError(t, err)
	return program
}

func projectedSets(program *Program, setName string, relevant []string) [][]string {
	evaluator := newEvaluator(relevant)
	for _, set := range program.sets {
		if set.name != setName {
			continue
		}
		states := evaluator.evaluate(set.root)
		result := make([][]string, 0, len(states))
		for _, state := range states {
			result = append(result, flattenWitness(state.witness))
		}
		return result
	}
	return nil
}

func productDefinition(dimensions, choices int) (Definition, []string) {
	groups := make([]string, dimensions)
	models := []string{"target"}
	for dimension := range dimensions {
		alternatives := make([]string, choices)
		for choice := range choices {
			name := fmt.Sprintf("model-%d-%d", dimension, choice)
			alternatives[choice] = name
			models = append(models, name)
		}
		groups[dimension] = "(" + strings.Join(alternatives, " | ") + ")"
	}
	return Definition{
		Name: "large",
		DSL:  "target & " + strings.Join(groups, " & "),
	}, models
}
