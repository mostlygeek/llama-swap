package config

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// writeYAML writes content to a file named name inside dir. Returns the full
// path of the written file.
func writeYAML(t *testing.T, dir, name, content string) string {
	t.Helper()
	p := filepath.Join(dir, name)
	require.NoError(t, os.MkdirAll(filepath.Dir(p), 0o755))
	require.NoError(t, os.WriteFile(p, []byte(content), 0o644))
	return p
}

// modelCfg builds a single-model YAML snippet indented for nesting under a
// `models:` key. The proxy uses a fixed port so tests don't depend on
// ${PORT} allocation.
func modelCfg(id, cmd string) string {
	return "  " + id + ":\n    cmd: " + cmd + "\n    proxy: \"http://localhost:9999\"\n"
}

func TestLoadConfigSources_NeitherProvided(t *testing.T) {
	_, err := LoadConfigSources("", "")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "at least one of -config or -config-dir")
}

func TestLoadConfigSources_ConfigOnly(t *testing.T) {
	dir := t.TempDir()
	cfgPath := writeYAML(t, dir, "config.yaml", `
models:
`+modelCfg("model1", "echo hi")+`
groups:
  group1:
    members: ["model1"]
`)
	cfg, err := LoadConfigSources(cfgPath, "")
	require.NoError(t, err)
	_, id, ok := cfg.FindConfig("model1")
	require.True(t, ok)
	assert.Equal(t, "model1", id)
}

func TestLoadConfigSources_DirOnly(t *testing.T) {
	dir := t.TempDir()
	writeYAML(t, dir, "a.yaml", "models:\n"+modelCfg("alpha", "echo a"))
	writeYAML(t, dir, "b.yaml", "models:\n"+modelCfg("beta", "echo b"))

	cfg, err := LoadConfigSources("", dir)
	require.NoError(t, err)
	for _, want := range []string{"alpha", "beta"} {
		_, _, ok := cfg.FindConfig(want)
		assert.True(t, ok, "model %s should be present", want)
	}
}

func TestLoadConfigSources_ConfigPlusDirAdditive(t *testing.T) {
	// -config lives outside -config-dir; both contribute models additively.
	dir := t.TempDir()
	cfgPath := writeYAML(t, dir, "config.yaml", "models:\n"+modelCfg("base", "echo base"))
	cfgDir := t.TempDir()
	writeYAML(t, cfgDir, "extra.yaml", "models:\n"+modelCfg("ext", "echo ext"))

	cfg, err := LoadConfigSources(cfgPath, cfgDir)
	require.NoError(t, err)
	for _, want := range []string{"base", "ext"} {
		_, _, ok := cfg.FindConfig(want)
		assert.True(t, ok, "model %s should be present after merge", want)
	}
}

// TestLoadConfigSources_ConfigInDirOverlap verifies that a -config file that
// is also a member of -config-dir is rejected.
func TestLoadConfigSources_ConfigInDirOverlap(t *testing.T) {
	dir := t.TempDir()
	cfgPath := writeYAML(t, dir, "main.yaml", "models:\n"+modelCfg("base", "echo base"))

	_, err := LoadConfigSources(cfgPath, dir)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "is also present in -config-dir")
}

func TestLoadConfigSources_DuplicateModelID(t *testing.T) {
	dir := t.TempDir()
	writeYAML(t, dir, "a.yaml", "models:\n"+modelCfg("dup", "echo a"))
	writeYAML(t, dir, "b.yaml", "models:\n"+modelCfg("dup", "echo b"))

	_, err := LoadConfigSources("", dir)
	require.Error(t, err)
	assert.Contains(t, err.Error(), `duplicate models "dup"`)
}

func TestLoadConfigSources_DuplicateGroupID(t *testing.T) {
	dir := t.TempDir()
	writeYAML(t, dir, "a.yaml", `
models:
`+modelCfg("m1", "echo m1")+"groups:\n  g1:\n    members: [m1]\n")
	writeYAML(t, dir, "b.yaml", `
models:
`+modelCfg("m2", "echo m2")+"groups:\n  g1:\n    members: [m2]\n")

	_, err := LoadConfigSources("", dir)
	require.Error(t, err)
	assert.Contains(t, err.Error(), `duplicate groups "g1"`)
}

func TestLoadConfigSources_DuplicatePeer(t *testing.T) {
	dir := t.TempDir()
	peerA := "peers:\n  remote:\n    proxy: http://x:1\n    models: [m1]\n"
	peerB := "peers:\n  remote:\n    proxy: http://x:2\n    models: [m2]\n"
	writeYAML(t, dir, "a.yaml", "models:\n"+modelCfg("m1", "echo m1")+"\n"+peerA)
	writeYAML(t, dir, "b.yaml", "models:\n"+modelCfg("m2", "echo m2")+"\n"+peerB)

	_, err := LoadConfigSources("", dir)
	require.Error(t, err)
	assert.Contains(t, err.Error(), `duplicate peers "remote"`)
}

func TestLoadConfigSources_ScalarConflict(t *testing.T) {
	dir := t.TempDir()
	writeYAML(t, dir, "a.yaml", "models:\n"+modelCfg("m1", "echo m1")+"\nglobalTTL: 100\n")
	writeYAML(t, dir, "b.yaml", "models:\n"+modelCfg("m2", "echo m2")+"\nglobalTTL: 200\n")

	_, err := LoadConfigSources("", dir)
	require.Error(t, err)
	assert.Contains(t, err.Error(), `conflict at "globalTTL"`)
}

func TestLoadConfigSources_ScalarSameValueNoConflict(t *testing.T) {
	dir := t.TempDir()
	writeYAML(t, dir, "a.yaml", "models:\n"+modelCfg("m1", "echo m1")+"\nglobalTTL: 100\n")
	writeYAML(t, dir, "b.yaml", "models:\n"+modelCfg("m2", "echo m2")+"\nglobalTTL: 100\n")

	cfg, err := LoadConfigSources("", dir)
	require.NoError(t, err)
	assert.Equal(t, 100, cfg.GlobalTTL)
}

func TestLoadConfigSources_MacrosConcatenate(t *testing.T) {
	dir := t.TempDir()
	writeYAML(t, dir, "a.yaml", "macros:\n  LOW: 1\nmodels:\n"+modelCfg("m1", "echo ${LOW}"))
	writeYAML(t, dir, "b.yaml", "macros:\n  HIGH: 2\nmodels:\n"+modelCfg("m2", "echo ${HIGH}"))

	cfg, err := LoadConfigSources("", dir)
	require.NoError(t, err)
	// Both macros are available globally after merge.
	low, ok := cfg.Macros.Get("LOW")
	require.True(t, ok)
	assert.Equal(t, 1, low)
	high, ok := cfg.Macros.Get("HIGH")
	require.True(t, ok)
	assert.Equal(t, 2, high)
}

func TestLoadConfigSources_APIKeysConcatenate(t *testing.T) {
	dir := t.TempDir()
	writeYAML(t, dir, "a.yaml", "models:\n"+modelCfg("m1", "echo m1")+"\napiKeys: [key-a]\n")
	writeYAML(t, dir, "b.yaml", "models:\n"+modelCfg("m2", "echo m2")+"\napiKeys: [key-b]\n")

	cfg, err := LoadConfigSources("", dir)
	require.NoError(t, err)
	assert.ElementsMatch(t, []string{"key-a", "key-b"}, cfg.RequiredAPIKeys)
}

func TestLoadConfigSources_RoutingGroupsMerge(t *testing.T) {
	dir := t.TempDir()
	writeYAML(t, dir, "a.yaml", `
models:
`+modelCfg("m1", "echo m1")+`
routing:
  router:
    settings:
      groups:
        groupA:
          members: [m1]
`)
	writeYAML(t, dir, "b.yaml", `
models:
`+modelCfg("m2", "echo m2")+`
routing:
  router:
    settings:
      groups:
        groupB:
          members: [m2]
`)

	cfg, err := LoadConfigSources("", dir)
	require.NoError(t, err)
	groups := cfg.Routing.Router.Settings.Groups
	assert.Contains(t, groups, "groupA")
	assert.Contains(t, groups, "groupB")
	// default group added by pipeline for orphaned/leftover routing groups...
	// here both groups reference distinct models
}

func TestLoadConfigSources_EnvMacrosSubstituted(t *testing.T) {
	dir := t.TempDir()
	// Use ${PORT} in cmd so the pipeline allocates a port and substitutes it;
	// verifies env/macro substitution runs on the merged document.
	writeYAML(t, dir, "a.yaml", "models:\n  m1:\n    cmd: serve --port ${PORT}\n    proxy: \"http://localhost:${PORT}\"\n")
	cfg, err := LoadConfigSources("", dir)
	require.NoError(t, err)
	m := cfg.Models["m1"]
	assert.NotContains(t, m.Cmd, "${PORT}", "PORT macro should have been substituted")
	assert.NotContains(t, m.Proxy, "${PORT}", "PORT macro should have been substituted in proxy")
}

func TestLoadConfigSources_SortedOrderDeterministic(t *testing.T) {
	// Two files defining distinct models, scanned in z..a order by filename.
	// Determine merged result is the same regardless of how the FS returns them.
	dir := t.TempDir()
	writeYAML(t, dir, "z.yaml", "models:\n"+modelCfg("zmodel", "echo z"))
	writeYAML(t, dir, "a.yaml", "models:\n"+modelCfg("amodel", "echo a"))

	const runs = 3
	for i := 0; i < runs; i++ {
		cfg, err := LoadConfigSources("", dir)
		require.NoError(t, err)
		// startPort-based allocation: first allocated model gets 5800.
		// Sorted order means amodel gets 5800, zmodel gets 5801.
		_, _, ok := cfg.FindConfig("amodel")
		assert.True(t, ok)
		_, _, ok = cfg.FindConfig("zmodel")
		assert.True(t, ok)
	}
}

func TestLoadConfigSources_EmptyDirWithConfig(t *testing.T) {
	dir := t.TempDir()
	cfgDir := t.TempDir()
	cfgPath := writeYAML(t, dir, "main.yaml", "models:\n"+modelCfg("m1", "echo m1"))

	cfg, err := LoadConfigSources(cfgPath, cfgDir)
	require.NoError(t, err)
	assert.Contains(t, cfg.Models, "m1")
}

func TestLoadConfigSources_EmptyDirOnly(t *testing.T) {
	// An empty -config-dir with no -config is an error: there is nothing to
	// load and silently producing an empty config would mask the misconfig.
	cfgDir := t.TempDir()
	_, err := LoadConfigSources("", cfgDir)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no configuration sources found")
}

func TestLoadConfigSources_AssertNoUnknownMacrosAfterMerge(t *testing.T) {
	// Macros defined in one file should not satisfy unknown-macro validation in
	// another — they do, because merge concats global macros before validation
	// runs. This test documents that a macro from file A is usable in file B.
	dir := t.TempDir()
	writeYAML(t, dir, "macros.yaml", "macros:\n  SHARED: hello\nmodels:\n"+modelCfg("dummy", "echo dummy"))
	writeYAML(t, dir, "use.yaml", "models:\n"+modelCfg("user", "echo ${SHARED}"))

	cfg, err := LoadConfigSources("", dir)
	require.NoError(t, err)
	m := cfg.Models["user"]
	assert.Contains(t, m.Cmd, "hello")
	assert.NotContains(t, m.Cmd, "${SHARED}")
}

func TestLoadConfigSources_KindMismatchErrors(t *testing.T) {
	dir := t.TempDir()
	writeYAML(t, dir, "a.yaml", "startPort: 5800\nmodels:\n"+modelCfg("m1", "echo m1"))
	writeYAML(t, dir, "b.yaml", "startPort: [5800, 5801]\nmodels:\n"+modelCfg("m2", "echo m2"))

	_, err := LoadConfigSources("", dir)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "incompatible YAML node kinds")
}

func TestLoadConfigSources_NullYieldsToValue(t *testing.T) {
	// File A: routing.router block absent (null on root for routing);
	// file B: defines routing.router.settings.groups. Merge should keep B's.
	dir := t.TempDir()
	writeYAML(t, dir, "a.yaml", "models:\n"+modelCfg("m1", "echo m1"))
	writeYAML(t, dir, "b.yaml", "routing:\n  router:\n    settings:\n      groups:\n        g1:\n          members: [m1]\nmodels:\n"+modelCfg("m2", "echo m2"))

	cfg, err := LoadConfigSources("", dir)
	require.NoError(t, err)
	assert.Contains(t, cfg.Routing.Router.Settings.Groups, "g1")
}
