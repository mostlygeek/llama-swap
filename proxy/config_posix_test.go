//go:build !windows

package proxy

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestConfig_SanitizeCommand(t *testing.T) {
	// Test a command with spaces and newlines
	args, err := SanitizeCommand(`python model1.py \
		-a "double quotes" \
		--arg2 'single quotes'
		-s
		--arg3 123 \
		--arg4 '"string in string"'
		-c "'single quoted'"
		`)
	assert.NoError(t, err)
	assert.Equal(t, []string{
		"python", "model1.py",
		"-a", "double quotes",
		"--arg2", "single quotes",
		"-s",
		"--arg3", "123",
		"--arg4", `"string in string"`,
		"-c", `'single quoted'`,
	}, args)

	// Test an empty command
	args, err = SanitizeCommand("")
	assert.Error(t, err)
	assert.Nil(t, args)
}
