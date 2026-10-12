package matrix

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func compileOrphansTestProgram(t *testing.T, definitions []Definition, models []string) *Program {
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

func TestProgram_OrphansSynthesis(t *testing.T) {
	program := compileOrphansTestProgram(t, []Definition{{Name: "main", DSL: "a & +auto:orphans"}}, []string{"c", "a", "b"})
	models, mode := program.SynthesizedOrphans()
	require.Equal(t, "synthesized", mode)
	require.Equal(t, []string{"b", "c"}, models)

	decision := program.Solve("b", []string{"a"}, nil)
	require.Equal(t, "main", decision.SetName)
	require.Equal(t, []string{"a", "b"}, decision.TargetSet)
}

func TestProgram_OrphansEmptyUnderAnd(t *testing.T) {
	program := compileOrphansTestProgram(t, []Definition{{Name: "main", DSL: "a & +auto:orphans"}}, []string{"a"})
	decision := program.Solve("a", nil, nil)
	require.Equal(t, "main", decision.SetName)
	require.Equal(t, []string{"a"}, decision.TargetSet)
}

func TestProgram_OrphansEmptyOnly(t *testing.T) {
	program := compileOrphansTestProgram(t, []Definition{
		{Name: "always", DSL: "a"},
		{Name: "scratch", DSL: "+auto:orphans"},
	}, []string{"a"})
	require.NotPanics(t, func() { program.Solve("a", nil, nil) })
	require.Equal(t, "always", program.Solve("a", nil, nil).SetName)
}

func TestProgram_OrphansSharedRef(t *testing.T) {
	program := compileOrphansTestProgram(t, []Definition{
		{Name: "one", DSL: "a & +auto:orphans"},
		{Name: "two", DSL: "b & +auto:orphans"},
	}, []string{"a", "b", "c"})
	models, _ := program.SynthesizedOrphans()
	require.Equal(t, []string{"c"}, models)
	require.Equal(t, []string{"a", "c"}, program.Solve("a", []string{"c"}, nil).TargetSet)
	require.Equal(t, []string{"b", "c"}, program.Solve("b", []string{"c"}, nil).TargetSet)
}

func TestProgram_OrphansTransitiveDrop(t *testing.T) {
	program := compileOrphansTestProgram(t, []Definition{
		{Name: "scratch", DSL: "+auto:orphans"},
		{Name: "outer", DSL: "+scratch & x"},
	}, []string{"x"})
	require.Equal(t, "outer", program.Solve("x", nil, nil).SetName)
	require.Equal(t, []string{"x"}, program.Solve("x", nil, nil).TargetSet)
}

func TestProgram_OrphansTransitiveChain(t *testing.T) {
	program := compileOrphansTestProgram(t, []Definition{
		{Name: "a1", DSL: "+auto:orphans"},
		{Name: "a2", DSL: "+a1"},
		{Name: "a3", DSL: "+a2 & x"},
	}, []string{"x"})
	require.Equal(t, "a3", program.Solve("x", nil, nil).SetName)
	require.Equal(t, []string{"x"}, program.Solve("x", nil, nil).TargetSet)
}

func TestProgram_OrphansTransitiveNonEmpty(t *testing.T) {
	program := compileOrphansTestProgram(t, []Definition{
		{Name: "scratch", DSL: "+auto:orphans"},
		{Name: "outer", DSL: "+scratch & x"},
	}, []string{"x", "orphan"})
	decision := program.Solve("orphan", nil, nil)
	require.Contains(t, []string{"outer", "scratch"}, decision.SetName)
	require.Contains(t, decision.TargetSet, "orphan")
}

func TestProgram_OrphansNested(t *testing.T) {
	program := compileOrphansTestProgram(t, []Definition{{Name: "nested", DSL: "(x | +auto:orphans) & y"}}, []string{"x", "y", "z"})
	require.Equal(t, []string{"y", "z"}, program.Solve("z", nil, nil).TargetSet)

	empty := compileOrphansTestProgram(t, []Definition{{Name: "nested", DSL: "(x & +auto:orphans) | y"}}, []string{"x", "y"})
	require.Equal(t, []string{"x"}, empty.Solve("x", nil, nil).TargetSet)
	require.Equal(t, []string{"y"}, empty.Solve("y", nil, nil).TargetSet)
}

func TestProgram_UndefinedBareLeaf(t *testing.T) {
	program := compileOrphansTestProgram(t, []Definition{{Name: "bare", DSL: "undefined"}}, []string{"undefined"})
	_, mode := program.SynthesizedOrphans()
	require.Equal(t, "", mode)
	require.Equal(t, "bare", program.Solve("undefined", nil, nil).SetName)

	_, err := Compile([]Definition{{Name: "bare", DSL: "undefined"}}, func(string) (string, bool) {
		return "", false
	}, []string{"a"})
	require.EqualError(t, err, `set "bare": unknown var or model "undefined"`)
}

func TestProgram_OrphansReferenceValidation(t *testing.T) {
	for _, name := range []string{"undefined", "auto:missing", "tag:vision"} {
		t.Run(name, func(t *testing.T) {
			_, err := Compile([]Definition{{Name: "main", DSL: "+" + name}}, func(id string) (string, bool) {
				return id, true
			}, []string{"a"})
			require.EqualError(t, err, `set "main" references undefined set "`+name+`"`)
		})
	}
	// The previous special name remains usable as an ordinary user set.
	program := compileOrphansTestProgram(t, []Definition{
		{Name: "undefined", DSL: "a"}, {Name: "main", DSL: "+undefined"},
	}, []string{"a", "b"})
	models, mode := program.SynthesizedOrphans()
	require.Empty(t, mode)
	require.Empty(t, models)
	require.Equal(t, "undefined", program.Solve("a", nil, nil).SetName)
}

func TestProgram_OrphansNeverSelectable(t *testing.T) {
	program := compileOrphansTestProgram(t, []Definition{{Name: "main", DSL: "a & +auto:orphans"}}, []string{"a", "b"})
	for _, target := range []string{"a", "b"} {
		require.NotEqual(t, "auto:orphans", program.Solve(target, nil, nil).SetName)
	}
}

func TestProgram_OrphansZeroUse(t *testing.T) {
	program := compileOrphansTestProgram(t, []Definition{{Name: "main", DSL: "a"}}, []string{"a", "b"})
	models, mode := program.SynthesizedOrphans()
	require.Equal(t, "", mode)
	require.Empty(t, models)
}

func TestProgram_OrphansDeterministic(t *testing.T) {
	definitions := []Definition{{Name: "main", DSL: "a & +auto:orphans"}}
	first := compileOrphansTestProgram(t, definitions, []string{"a", "c", "b"})
	second := compileOrphansTestProgram(t, definitions, []string{"b", "a", "c"})
	firstModels, firstMode := first.SynthesizedOrphans()
	secondModels, secondMode := second.SynthesizedOrphans()
	require.Equal(t, firstModels, secondModels)
	require.Equal(t, firstMode, secondMode)
	for _, target := range []string{"a", "b", "c"} {
		require.Equal(t, first.Solve(target, nil, nil), second.Solve(target, nil, nil))
	}
}

func TestProgram_OrphansShadowed(t *testing.T) {
	program := compileOrphansTestProgram(t, []Definition{{Name: "auto:orphans", DSL: "a"}, {Name: "scratch", DSL: "+auto:orphans"}}, []string{"a", "b"})
	_, mode := program.SynthesizedOrphans()
	require.Equal(t, "user-defined", mode)
	require.Equal(t, "auto:orphans", program.Solve("a", nil, nil).SetName)
}

func TestProgram_OrphansUnreferencedShadowSet(t *testing.T) {
	program := compileOrphansTestProgram(t, []Definition{{Name: "auto:orphans", DSL: "a"}}, []string{"a"})
	require.Equal(t, "auto:orphans", program.Solve("a", nil, nil).SetName)
}
