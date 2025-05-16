//go:build windows

package proxy

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestConfig_SanitizeCommand(t *testing.T) {
	// does not support single quoted strings like in config_posix_test.go
	args, err := SanitizeCommand(`python model1.py \

	-a "double quotes" \
	-s
	--arg3 123 \

	   # comment 2
	--arg4 '"string in string"'



	# this will get stripped out as well as the white space above
	-c "'single quoted'"
	`)
	assert.NoError(t, err)
	assert.Equal(t, []string{
		"python", "model1.py",
		"-a", "double quotes",
		"-s",
		"--arg3", "123",
		"--arg4", "'string in string'", // this is a little weird but the lexer says so...?
		"-c", `'single quoted'`,
	}, args)

	// Test an empty command
	args, err = SanitizeCommand("")
	assert.Error(t, err)
	assert.Nil(t, args)
}

func TestConfig_WindowsCmdStopIsSet(t *testing.T) {
	content := `
	models:
	  model1:
		cmd: path/to/cmd --arg1 one
	`
	// Load the config and verify
	config, err := LoadConfigFromReader(strings.NewReader(content))
	assert.NoError(t, err)
	assert.Equal(t, "taskkill /f /t /pid ${PID}", config.Models["model1"].CmdStop)
}
