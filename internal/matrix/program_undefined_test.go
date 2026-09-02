package matrix

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func compileUndefinedTestProgram(t *testing.T, definitions []Definition, models []string) *Program {
	t.Helper()
	program, err := Compile(definitions, func(name string) (string, bool) {
		for _, model := range models {
			if name == model {
				return model, true
			}
		}
		return "", false
	}, models)
	require.NoError(t, err)
	return program
}

func TestProgram_UndefinedSynthesis(t *testing.T) {
	program := compileUndefinedTestProgram(t, []Definition{{Name: "main", DSL: "a & +undefined"}}, []string{"c", "a", "b"})
	models, mode := program.SynthesizedUndefined()
	require.Equal(t, "synthesized", mode)
	require.Equal(t, []string{"b", "c"}, models)

	decision := program.Solve("b", []string{"a"}, nil)
	require.Equal(t, "main", decision.SetName)
	require.Equal(t, []string{"a", "b"}, decision.TargetSet)
}

func TestProgram_UndefinedEmptyUnderAnd(t *testing.T) {
	program := compileUndefinedTestProgram(t, []Definition{{Name: "main", DSL: "a & +undefined"}}, []string{"a"})
	decision := program.Solve("a", nil, nil)
	require.Equal(t, "main", decision.SetName)
	require.Equal(t, []string{"a"}, decision.TargetSet)
}

func TestProgram_UndefinedEmptyOnly(t *testing.T) {
	program := compileUndefinedTestProgram(t, []Definition{
		{Name: "always", DSL: "a"},
		{Name: "scratch", DSL: "+undefined"},
	}, []string{"a"})
	require.NotPanics(t, func() { program.Solve("a", nil, nil) })
	require.Equal(t, "always", program.Solve("a", nil, nil).SetName)
}

func TestProgram_UndefinedSharedRef(t *testing.T) {
	program := compileUndefinedTestProgram(t, []Definition{
		{Name: "one", DSL: "a & +undefined"},
		{Name: "two", DSL: "b & +undefined"},
	}, []string{"a", "b", "c"})
	models, _ := program.SynthesizedUndefined()
	require.Equal(t, []string{"c"}, models)
	require.Equal(t, []string{"a", "c"}, program.Solve("a", []string{"c"}, nil).TargetSet)
	require.Equal(t, []string{"b", "c"}, program.Solve("b", []string{"c"}, nil).TargetSet)
}

func TestProgram_UndefinedTransitiveDrop(t *testing.T) {
	program := compileUndefinedTestProgram(t, []Definition{
		{Name: "scratch", DSL: "+undefined"},
		{Name: "outer", DSL: "+scratch & x"},
	}, []string{"x"})
	require.Equal(t, "outer", program.Solve("x", nil, nil).SetName)
	require.Equal(t, []string{"x"}, program.Solve("x", nil, nil).TargetSet)
}

func TestProgram_UndefinedTransitiveChain(t *testing.T) {
	program := compileUndefinedTestProgram(t, []Definition{
		{Name: "a1", DSL: "+undefined"},
		{Name: "a2", DSL: "+a1"},
		{Name: "a3", DSL: "+a2 & x"},
	}, []string{"x"})
	require.Equal(t, "a3", program.Solve("x", nil, nil).SetName)
	require.Equal(t, []string{"x"}, program.Solve("x", nil, nil).TargetSet)
}

func TestProgram_UndefinedTransitiveNonEmpty(t *testing.T) {
	program := compileUndefinedTestProgram(t, []Definition{
		{Name: "scratch", DSL: "+undefined"},
		{Name: "outer", DSL: "+scratch & x"},
	}, []string{"x", "orphan"})
	decision := program.Solve("orphan", nil, nil)
	require.Contains(t, []string{"outer", "scratch"}, decision.SetName)
	require.Contains(t, decision.TargetSet, "orphan")
}

func TestProgram_UndefinedNested(t *testing.T) {
	program := compileUndefinedTestProgram(t, []Definition{{Name: "nested", DSL: "(x | +undefined) & y"}}, []string{"x", "y", "z"})
	require.Equal(t, []string{"y", "z"}, program.Solve("z", nil, nil).TargetSet)

	empty := compileUndefinedTestProgram(t, []Definition{{Name: "nested", DSL: "(x & +undefined) | y"}}, []string{"x", "y"})
	require.Equal(t, []string{"x"}, empty.Solve("x", nil, nil).TargetSet)
	require.Equal(t, []string{"y"}, empty.Solve("y", nil, nil).TargetSet)
}

func TestProgram_UndefinedBareLeaf(t *testing.T) {
	program := compileUndefinedTestProgram(t, []Definition{{Name: "bare", DSL: "undefined"}}, []string{"undefined"})
	_, mode := program.SynthesizedUndefined()
	require.Equal(t, "", mode)
	require.Equal(t, "bare", program.Solve("undefined", nil, nil).SetName)

	_, err := Compile([]Definition{{Name: "bare", DSL: "undefined"}}, func(string) (string, bool) {
		return "", false
	}, []string{"a"})
	require.EqualError(t, err, `set "bare": unknown var or model "undefined"`)
}

func TestProgram_UndefinedNeverSelectable(t *testing.T) {
	program := compileUndefinedTestProgram(t, []Definition{{Name: "main", DSL: "a & +undefined"}}, []string{"a", "b"})
	for _, target := range []string{"a", "b"} {
		require.NotEqual(t, "undefined", program.Solve(target, nil, nil).SetName)
	}
}

func TestProgram_UndefinedZeroUse(t *testing.T) {
	program := compileUndefinedTestProgram(t, []Definition{{Name: "main", DSL: "a"}}, []string{"a", "b"})
	models, mode := program.SynthesizedUndefined()
	require.Equal(t, "", mode)
	require.Empty(t, models)
}

func TestProgram_UndefinedDeterministic(t *testing.T) {
	definitions := []Definition{{Name: "main", DSL: "a & +undefined"}}
	first := compileUndefinedTestProgram(t, definitions, []string{"a", "c", "b"})
	second := compileUndefinedTestProgram(t, definitions, []string{"b", "a", "c"})
	firstModels, firstMode := first.SynthesizedUndefined()
	secondModels, secondMode := second.SynthesizedUndefined()
	require.Equal(t, firstModels, secondModels)
	require.Equal(t, firstMode, secondMode)
	for _, target := range []string{"a", "b", "c"} {
		require.Equal(t, first.Solve(target, nil, nil), second.Solve(target, nil, nil))
	}
}

func TestProgram_UndefinedShadowed(t *testing.T) {
	program := compileUndefinedTestProgram(t, []Definition{{Name: "undefined", DSL: "a"}, {Name: "scratch", DSL: "+undefined"}}, []string{"a", "b"})
	_, mode := program.SynthesizedUndefined()
	require.Equal(t, "user-defined", mode)
	require.Equal(t, "undefined", program.Solve("a", nil, nil).SetName)
}

func TestProgram_UndefinedUnreferencedShadowSet(t *testing.T) {
	program := compileUndefinedTestProgram(t, []Definition{{Name: "undefined", DSL: "a"}}, []string{"a"})
	require.Equal(t, "undefined", program.Solve("a", nil, nil).SetName)
}
