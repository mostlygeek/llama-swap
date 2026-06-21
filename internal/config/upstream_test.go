package config

import (
	"regexp"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const upstreamConfigHeader = `
models:
  model1:
    cmd: path/to/cmd --arg1 one
    proxy: "http://localhost:8080"
`

func TestConfig_UpstreamIgnorePaths_DefaultWhenAbsent(t *testing.T) {
	// When upstream is not specified at all, the default pattern is applied.
	content := upstreamConfigHeader
	cfg, err := LoadConfigFromReader(strings.NewReader(content))
	require.NoError(t, err)
	require.Len(t, cfg.Upstream.IgnorePaths, 1)

	def := cfg.Upstream.IgnorePaths[0]
	assert.IsType(t, &regexp.Regexp{}, def)
	assert.Equal(t, DefaultUpstreamIgnorePathsPattern, def.String())

	// The default matches common static-asset suffixes.
	assert.True(t, def.MatchString("/foo.js"))
	assert.True(t, def.MatchString("/bar/baz.json"))
	assert.True(t, def.MatchString("/static/img.png"))
	assert.True(t, def.MatchString("/notes.txt"))
	assert.True(t, def.MatchString("/favicon.ico"))
	// And does not match inference API paths.
	assert.False(t, def.MatchString("/v1/chat/completions"))
	assert.False(t, def.MatchString("/v1/models"))
	assert.False(t, def.MatchString("/health"))
}

func TestConfig_UpstreamIgnorePaths_DefaultWhenSectionEmpty(t *testing.T) {
	// When upstream is present but ignorePaths is omitted, the default is still
	// applied.
	content := `upstream: {}` + "\n" + upstreamConfigHeader
	cfg, err := LoadConfigFromReader(strings.NewReader(content))
	require.NoError(t, err)
	require.Len(t, cfg.Upstream.IgnorePaths, 1)
	assert.Equal(t, DefaultUpstreamIgnorePathsPattern, cfg.Upstream.IgnorePaths[0].String())
}

func TestConfig_UpstreamIgnorePaths_Compiles(t *testing.T) {
	content := `
upstream:
  ignorePaths:
    - ".*\\.(js|json|css|png|gif|jpg|jpeg|txt)$"
    - "^/static/.*"
` + upstreamConfigHeader

	cfg, err := LoadConfigFromReader(strings.NewReader(content))
	require.NoError(t, err)
	require.Len(t, cfg.Upstream.IgnorePaths, 2)

	// Verify the patterns are compiled into *regexp.Regexp and match as expected.
	assert.True(t, cfg.Upstream.IgnorePaths[0].MatchString("/foo.js"))
	assert.True(t, cfg.Upstream.IgnorePaths[0].MatchString("/bar/baz.json"))
	assert.False(t, cfg.Upstream.IgnorePaths[0].MatchString("/v1/chat/completions"))
	assert.True(t, cfg.Upstream.IgnorePaths[1].MatchString("/static/foo.png"))
	assert.False(t, cfg.Upstream.IgnorePaths[1].MatchString("/v1/chat/completions"))

	// Confirm the type is *regexp.Regexp to satisfy the API contract.
	for _, re := range cfg.Upstream.IgnorePaths {
		assert.IsType(t, &regexp.Regexp{}, re)
	}
}

func TestConfig_UpstreamIgnorePaths_InvalidRegexReturnsError(t *testing.T) {
	content := `
upstream:
  ignorePaths:
    - "[invalid("
` + upstreamConfigHeader

	_, err := LoadConfigFromReader(strings.NewReader(content))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "upstream.ignorePaths")
	assert.Contains(t, err.Error(), "invalid regular expression")
}
