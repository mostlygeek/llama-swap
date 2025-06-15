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

func TestConfig_DefaultValuesWindows(t *testing.T) {
	content := `
models:
  model1:
    cmd: path/to/cmd --port ${PORT}
`

	config, err := LoadConfigFromReader(strings.NewReader(content))
	assert.NoError(t, err)
	assert.Equal(t, 120, config.HealthCheckTimeout)
	assert.Equal(t, 5800, config.StartPort)
	assert.Equal(t, "info", config.LogLevel)

	// Test default group exists
	defaultGroup, exists := config.Groups["(default)"]
	assert.True(t, exists, "default group should exist")
	if assert.NotNil(t, defaultGroup, "default group should not be nil") {
		assert.Equal(t, true, defaultGroup.Swap)
		assert.Equal(t, true, defaultGroup.Exclusive)
		assert.Equal(t, false, defaultGroup.Persistent)
		assert.Equal(t, []string{"model1"}, defaultGroup.Members)
	}

	model1, exists := config.Models["model1"]
	assert.True(t, exists, "model1 should exist")
	if assert.NotNil(t, model1, "model1 should not be nil") {
		assert.Equal(t, "path/to/cmd --port 5800", model1.Cmd) // has the port replaced
		assert.Equal(t, "", model1.CmdStop)
		assert.Equal(t, "http://localhost:5800", model1.Proxy)
		assert.Equal(t, "/health", model1.CheckEndpoint)
		assert.Equal(t, []string{}, model1.Aliases)
		assert.Equal(t, []string{}, model1.Env)
		assert.Equal(t, 0, model1.UnloadAfter)
		assert.Equal(t, false, model1.Unlisted)
		assert.Equal(t, "", model1.UseModelName)
		assert.Equal(t, 0, model1.ConcurrencyLimit)
	}
}
