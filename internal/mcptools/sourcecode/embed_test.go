//go:build embed_src

package sourcecode

import (
	"io/fs"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSourceCode_EmbeddedSource(t *testing.T) {
	require.NotNil(t, Embedded)
	for _, want := range []string{"llama-swap.go", "internal/mcptools/sourcecode/index.go", "ui/src/App.svelte"} {
		_, err := fs.Stat(Embedded, want)
		assert.NoError(t, err, want)
	}
	err := fs.WalkDir(Embedded, ".", func(p string, d fs.DirEntry, err error) error {
		require.NoError(t, err)
		if !d.IsDir() {
			assert.False(t, strings.HasSuffix(p, "_test.go") || strings.HasSuffix(p, ".test.ts"), "%s is a test", p)
		}
		return nil
	})
	require.NoError(t, err)
}
