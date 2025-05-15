//go:build windows

package proxy

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestConfig_SanitizeCommand(t *testing.T) {
	// does not support single quoted strings like in config_posix_test.go
	args, err := SanitizeCommand(`python model1.py \
    -a "double quotes" \
	-s
	--arg3 123 \
	--arg4 '"string in string"'
	-c "'single quoted'"
	`)
	assert.NoError(t, err)
	assert.Equal(t, []string{
		"python", "model1.py",
		"-a", "double quotes",
		"-s",
		"--arg3", "123",
		"--arg4", `"'string in string'"`, // this is a little weird but the lexer says so...?
		"-c", `'single quoted'`,
	}, args)

	// Test an empty command
	args, err = SanitizeCommand("")
	assert.Error(t, err)
	assert.Nil(t, args)

}
