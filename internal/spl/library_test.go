package spl

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSPL_Library_Valid(t *testing.T) {
	lib, err := NewLibrary(map[string]string{
		"b":     "apply a",
		"a":     "set x to 1",
		"empty": "",
	}, []string{"model"})
	require.NoError(t, err)
	assert.Equal(t, []string{"a", "b", "empty"}, lib.Names())
	prog, ok := lib.Lookup("a")
	require.True(t, ok)
	assert.Equal(t, 1, prog.Len())
	_, ok = lib.Lookup("nope")
	assert.False(t, ok)

	var nilLib *Library
	_, ok = nilLib.Lookup("a")
	assert.False(t, ok)
	assert.Nil(t, nilLib.Names())
}

func TestSPL_Library_ParseError(t *testing.T) {
	_, err := NewLibrary(map[string]string{"ok": "remove a", "bad": "set a"}, nil)
	require.Error(t, err)
	assert.Equal(t, "policies.bad: line 1:6: unexpected end of program, expected 'to'", err.Error())
}

func TestSPL_Library_UnknownApply(t *testing.T) {
	_, err := NewLibrary(map[string]string{"a": "remove x\napply zzz"}, nil)
	require.Error(t, err)
	assert.Equal(t, `policies.a: line 2:7: apply references unknown policy "zzz"`, err.Error())
}

func TestSPL_Library_Cycle(t *testing.T) {
	_, err := NewLibrary(map[string]string{"a": "apply a"}, nil)
	require.Error(t, err)
	assert.Equal(t, "policies: cycle detected: a -> a", err.Error())

	_, err = NewLibrary(map[string]string{"a": "apply b", "b": "apply a"}, nil)
	require.Error(t, err)
	assert.Equal(t, "policies: cycle detected: a -> b -> a", err.Error())

	_, err = NewLibrary(map[string]string{
		"a": "apply b",
		"b": "apply c",
		"c": "apply a",
		"d": "apply a",
	}, nil)
	require.Error(t, err)
	assert.Equal(t, "policies: cycle detected: a -> b -> c -> a", err.Error())

	// A diamond is not a cycle.
	_, err = NewLibrary(map[string]string{
		"a": "apply b\napply c",
		"b": "apply d",
		"c": "apply d",
		"d": "remove x",
	}, nil)
	require.NoError(t, err)
}

func TestSPL_Library_ProtectedPath(t *testing.T) {
	protected := []string{"model"}
	tests := []struct {
		src  string
		want string
	}{
		{`set model to "x"`, `line 1:5: cannot set protected parameter "model"`},
		{`default model to "x"`, `line 1:9: cannot set protected parameter "model"`},
		{`remove model`, `line 1:8: cannot remove protected parameter "model"`},
		{`set body.model to "x"`, `line 1:5: cannot set protected parameter "model"`},
		{`set model.x to "x"`, `line 1:5: cannot set protected parameter "model"`},
	}
	for _, tt := range tests {
		t.Run(tt.src, func(t *testing.T) {
			_, err := NewLibrary(map[string]string{"p": tt.src}, protected)
			require.Error(t, err)
			assert.Equal(t, "policies.p: "+tt.want, err.Error())

			prog, err := Parse(tt.src)
			require.NoError(t, err)
			err = prog.Validate(nil, protected)
			require.Error(t, err)
			assert.Equal(t, tt.want, err.Error())
		})
	}

	// Reading model and writing context.model are fine.
	prog, err := Parse(`set context.model to "x" when model = "y"
set request_model to "z" when request.model = "y"`)
	require.NoError(t, err)
	assert.NoError(t, prog.Validate(nil, protected))

	// Without a protected list anything goes.
	prog, err = Parse(`set model to "x"`)
	require.NoError(t, err)
	assert.NoError(t, prog.Validate(nil, nil))
}

func TestSPL_Program_Validate_NilLibrary(t *testing.T) {
	prog, err := Parse("apply anything")
	require.NoError(t, err)
	err = prog.Validate(nil, nil)
	require.Error(t, err)
	assert.Equal(t, `line 1:7: apply references unknown policy "anything"`, err.Error())

	lib, err := NewLibrary(map[string]string{"anything": ""}, nil)
	require.NoError(t, err)
	assert.NoError(t, prog.Validate(lib, nil))
}
