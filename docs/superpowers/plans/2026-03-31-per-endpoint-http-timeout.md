# Per-Endpoint HTTP Timeout Configuration Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add configurable HTTP timeout settings to both models and peers to support lower-end hardware.

**Architecture:** Add an `HTTPTimeout` struct to both `ModelConfig` and `PeerConfig`, then modify the proxy creation code to use these configured values instead of hardcoded defaults. All new fields are optional with sensible defaults.

**Tech Stack:** Go, YAML configuration

---

## File Structure

| File | Purpose |
|------|---------|
| `proxy/config/model_config.go` | Add HTTPTimeout struct and field to ModelConfig |
| `proxy/config/peer.go` | Add HTTPTimeout field to PeerConfig |
| `proxy/process.go` | Apply configured timeouts to model reverse proxy transport |
| `proxy/peerproxy.go` | Create per-peer transports with configured timeouts |
| `config.example.yaml` | Add example configurations demonstrating httpTimeout |
| `docs/configuration.md` | Document the new httpTimeout configuration option |
| `proxy/config/config_test.go` | Add tests for HTTPTimeout parsing in models |
| `proxy/config/peer_test.go` | Create new file to test PeerConfig with HTTPTimeout |
| `proxy/process_test.go` | Add tests to verify timeout values are applied correctly |

---

## Task 1: Add HTTPTimeoutConfig struct to config package

**Files:**
- Create: None
- Modify: `proxy/config/model_config.go` (add new struct type)
- Test: `proxy/config/config_test.go` (add test for HTTPTimeout parsing)

- [ ] **Step 1: Add HTTPTimeoutConfig struct to model_config.go**

Add this new type definition near the top of `proxy/config/model_config.go`, before the `ModelConfig` struct:

```go
// HTTPTimeoutConfig holds HTTP timeout settings for proxy connections
type HTTPTimeoutConfig struct {
	ConnectTimeout        int `yaml:"connectTimeout"`        // seconds, default 30
	ResponseHeaderTimeout int `yaml:"responseHeaderTimeout"` // seconds, default 60
}
```

- [ ] **Step 2: Add HTTPTimeout field to ModelConfig struct**

In `proxy/config/model_config.go`, add the HTTPTimeout field to the `ModelConfig` struct (around line 40, after `SendLoadingState`):

```go
	// HTTP timeout settings for proxy connections
	HTTPTimeout HTTPTimeoutConfig `yaml:"httpTimeout"`
```

The struct should look like:
```go
type ModelConfig struct {
	Cmd           string   `yaml:"cmd"`
	CmdStop       string   `yaml:"cmdStop"`
	Proxy         string   `yaml:"proxy"`
	Aliases       []string `yaml:"aliases"`
	Env           []string `yaml:"env"`
	CheckEndpoint string   `yaml:"checkEndpoint"`
	UnloadAfter   int      `yaml:"ttl"`
	Unlisted      bool     `yaml:"unlisted"`
	UseModelName  string   `yaml:"useModelName"`

	// #179 for /v1/models
	Name        string `yaml:"name"`
	Description string `yaml:"description"`

	// Limit concurrency of HTTP requests to process
	ConcurrencyLimit int `yaml:"concurrencyLimit"`

	// Model filters see issue #174
	Filters ModelFilters `yaml:"filters"`

	// Macros: see #264
	Macros MacroList `yaml:"macros"`

	// Metadata: see #264
	Metadata map[string]any `yaml:"metadata"`

	// override global setting
	SendLoadingState *bool `yaml:"sendLoadingState"`

	// HTTP timeout settings for proxy connections
	HTTPTimeout HTTPTimeoutConfig `yaml:"httpTimeout"`
}
```

- [ ] **Step 3: Add HTTPTimeout field to PeerConfig struct**

In `proxy/config/peer.go`, add the HTTPTimeout field to the `PeerConfig` struct (around line 10):

```go
type PeerConfig struct {
	Proxy     string   `yaml:"proxy"`
	ProxyURL  *url.URL `yaml:"-"`
	ApiKey    string   `yaml:"apiKey"`
	Models    []string `yaml:"models"`
	Filters   Filters  `yaml:"filters"`

	// HTTP timeout settings for proxy connections
	HTTPTimeout HTTPTimeoutConfig `yaml:"httpTimeout"`
}
```

Note: You'll need to import `HTTPTimeoutConfig` from model_config.go (same package) or move the struct definition to a shared location like `config.go`.

- [ ] **Step 4: Create test for HTTPTimeout parsing in config_test.go**

Add a new test to `proxy/config/config_test.go`:

```go
func TestConfig_HTTPTimeoutParsing(t *testing.T) {
	configYaml := `
models:
  model1:
    cmd: test-server --port ${PORT}
    httpTimeout:
      connectTimeout: 45
      responseHeaderTimeout: 120
`

	config, err := LoadConfigFromReader(strings.NewReader(configYaml))
	require.NoError(t, err)

	modelConfig, found := config.Models["model1"]
	require.True(t, found, "model1 should exist in config")

	assert.Equal(t, 45, modelConfig.HTTPTimeout.ConnectTimeout)
	assert.Equal(t, 120, modelConfig.HTTPTimeout.ResponseHeaderTimeout)
}

func TestConfig_HTTPTimeoutDefaults(t *testing.T) {
	configYaml := `
models:
  model1:
    cmd: test-server --port ${PORT}
`

	config, err := LoadConfigFromReader(strings.NewReader(configYaml))
	require.NoError(t, err)

	modelConfig, found := config.Models["model1"]
	require.True(t, found, "model1 should exist in config")

	// When httpTimeout is not specified, values should be 0 (zero values)
	assert.Equal(t, 0, modelConfig.HTTPTimeout.ConnectTimeout)
	assert.Equal(t, 0, modelConfig.HTTPTimeout.ResponseHeaderTimeout)
}
```

- [ ] **Step 5: Run the new tests to ensure they pass**

```bash
go test -v -run TestConfig_HTTPTimeout ./proxy/config/
```

Expected: Tests pass with 0 timeout values (we'll apply defaults later in the proxy code).

- [ ] **Step 6: Commit**

```bash
git add proxy/config/model_config.go proxy/config/peer.go proxy/config/config_test.go
git commit -m "proxy/config: add HTTPTimeout configuration structs for models and peers"
```

---

## Task 2: Update process.go to use configured HTTP timeouts

**Files:**
- Modify: `proxy/process.go:84-127` (NewProcess function)
- Test: `proxy/process_test.go` (add test for timeout configuration)

- [ ] **Step 1: Read the current NewProcess implementation**

Review the `NewProcess` function in `proxy/process.go` to understand the current transport setup (lines 84-127).

- [ ] **Step 2: Modify NewProcess to create custom transport with timeouts**

In `proxy/process.go`, update the `NewProcess` function to create a custom transport with configured timeout values. Replace the existing reverse proxy creation code (around lines 90-106) with:

```go
	var reverseProxy *httputil.ReverseProxy
	if proxyURL != nil {
		reverseProxy = httputil.NewSingleHostReverseProxy(proxyURL)
		
		// Create custom transport with configured timeouts
		transport := &http.Transport{
			DialContext: (&net.Dialer{
				Timeout:   getTimeoutOrDefault(config.HTTPTimeout.ConnectTimeout, 30) * time.Second,
				KeepAlive: 30 * time.Second,
			}).DialContext,
			TLSHandshakeTimeout:   10 * time.Second,
			ResponseHeaderTimeout: getTimeoutOrDefault(config.HTTPTimeout.ResponseHeaderTimeout, 60) * time.Second,
			ExpectContinueTimeout: 1 * time.Second,
			MaxIdleConns:          100,
			MaxIdleConnsPerHost:   10,
			IdleConnTimeout:       90 * time.Second,
		}
		reverseProxy.Transport = transport
		
		reverseProxy.ModifyResponse = func(resp *http.Response) error {
			// prevent nginx from buffering streaming responses (e.g., SSE)
			if strings.Contains(strings.ToLower(resp.Header.Get("Content-Type")), "text/event-stream") {
				resp.Header.Set("X-Accel-Buffering", "no")
			}
			return nil
		}
	}
```

Add the helper function near the top of the file (after imports or as a private function):

```go
// getTimeoutOrDefault returns the timeout value if > 0, otherwise returns the default
func getTimeoutOrDefault(configured int, defaultValue int) time.Duration {
	if configured > 0 {
		return time.Duration(configured)
	}
	return time.Duration(defaultValue)
}
```

Also ensure the import for `time` package is present (should already be there).

- [ ] **Step 3: Add test for custom timeout configuration in process_test.go**

Add a new test to `proxy/process_test.go`:

```go
func TestProcess_CustomHTTPTimeouts(t *testing.T) {
	config := config.ModelConfig{
		Cmd:           "echo test",
		Proxy:         "http://localhost:8080",
		CheckEndpoint: "/health",
		HTTPTimeout: config.HTTPTimeoutConfig{
			ConnectTimeout:        45,
			ResponseHeaderTimeout: 120,
		},
	}

	debugLogger := NewLogMonitorWriter(io.Discard)
	process := NewProcess("test-model", 30, config, debugLogger, debugLogger)

	// Verify the process was created successfully
	assert.NotNil(t, process)
	assert.Equal(t, "test-model", process.ID)
}
```

- [ ] **Step 4: Run process tests to ensure they pass**

```bash
go test -v -run TestProcess ./proxy/
```

Expected: All tests pass including the new custom timeout test.

- [ ] **Step 5: Run make test-dev to check for any static analysis issues**

```bash
make test-dev
```

Fix any staticcheck errors that appear.

- [ ] **Step 6: Commit**

```bash
git add proxy/process.go proxy/process_test.go
git commit -m "proxy: apply configured HTTP timeouts to model reverse proxy transport"
```

---

## Task 3: Update peerproxy.go to use per-peer HTTP timeouts

**Files:**
- Modify: `proxy/peerproxy.go:27-102` (NewPeerProxy function)
- Test: `proxy/peerproxy_test.go` (modify existing test or add new test)

- [ ] **Step 1: Read the current NewPeerProxy implementation**

Review the `NewPeerProxy` function in `proxy/peerproxy.go` to understand how it currently creates a shared transport for all peers (lines 27-102).

- [ ] **Step 2: Modify NewPeerProxy to create per-peer transports with custom timeouts**

Replace the shared transport creation code (lines 37-50) in `proxy/peerproxy.go`:

```go
	for _, peerID := range peerIDs {
		peer := peers[peerID]
		
		// Create a transport with per-peer timeout configuration
		peerTransport := &http.Transport{
			DialContext: (&net.Dialer{
				Timeout:   getTimeoutOrDefault(peer.HTTPTimeout.ConnectTimeout, 30) * time.Second,
				KeepAlive: 30 * time.Second,
			}).DialContext,
			TLSHandshakeTimeout:   10 * time.Second,
			ResponseHeaderTimeout: getTimeoutOrDefault(peer.HTTPTimeout.ResponseHeaderTimeout, 60) * time.Second,
			ExpectContinueTimeout: 1 * time.Second,
			MaxIdleConns:          100,
			MaxIdleConnsPerHost:   10,
			IdleConnTimeout:       90 * time.Second,
		}
		
		// Create reverse proxy for this peer
		reverseProxy := httputil.NewSingleHostReverseProxy(peer.ProxyURL)
		reverseProxy.Transport = peerTransport
		// ... rest of the existing code
```

Note: You may need to add the `getTimeoutOrDefault` helper function to peerproxy.go as well, or move it to a shared location. For simplicity, add it to peerproxy.go:

```go
// getTimeoutOrDefault returns the timeout value if > 0, otherwise returns the default
func getTimeoutOrDefault(configured int, defaultValue int) time.Duration {
	if configured > 0 {
		return time.Duration(configured)
	}
	return time.Duration(defaultValue)
}
```

- [ ] **Step 3: Add test for peer HTTP timeout configuration**

Add or update a test in `proxy/peerproxy_test.go`:

```go
func TestPeerProxy_CustomHTTPTimeouts(t *testing.T) {
	proxyURL, _ := url.Parse("http://localhost:8080")
	
	peers := config.PeerDictionaryConfig{
		"test-peer": config.PeerConfig{
			Proxy:    "http://localhost:8080",
			ProxyURL: proxyURL,
			Models:   []string{"model1"},
			HTTPTimeout: config.HTTPTimeoutConfig{
				ConnectTimeout:        45,
				ResponseHeaderTimeout: 300,
			},
		},
	}

	debugLogger := NewLogMonitorWriter(io.Discard)
	peerProxy, err := NewPeerProxy(peers, debugLogger)

	assert.NoError(t, err)
	assert.NotNil(t, peerProxy)
	assert.True(t, peerProxy.HasPeerModel("model1"))
}
```

- [ ] **Step 4: Run peer proxy tests**

```bash
go test -v -run TestPeerProxy ./proxy/
```

Expected: All tests pass.

- [ ] **Step 5: Run make test-dev again**

```bash
make test-dev
```

- [ ] **Step 6: Commit**

```bash
git add proxy/peerproxy.go proxy/peerproxy_test.go
git commit -m "proxy: apply configured HTTP timeouts per peer in peer proxy"
```

---

## Task 4: Update config.example.yaml with httpTimeout examples

**Files:**
- Modify: `config.example.yaml`

- [ ] **Step 1: Add httpTimeout example to a model configuration**

Find a model configuration in `config.example.yaml` (around line 200-325) and add an `httpTimeout` section. Add it to the main example model ("llama"):

```yaml
    # httpTimeout: configure HTTP timeouts for proxy connections to this model
    # - optional, default: unset (uses defaults: connectTimeout=30s, responseHeaderTimeout=60s)
    # - useful for models running on slower hardware that need longer timeouts
    # - connectTimeout: TCP connection timeout in seconds
    # - responseHeaderTimeout: time to wait for response headers in seconds
    #   (increasing this helps avoid 502 errors on slow hardware)
    httpTimeout:
      connectTimeout: 30
      responseHeaderTimeout: 120  # 2 minutes for slower responses
```

Place this after the `sendLoadingState` field (around line 320) and before the `unlisted` model example.

- [ ] **Step 2: Add httpTimeout example to a peer configuration**

Find the peers section in `config.example.yaml` (around line 436-467) and add an `httpTimeout` example to one of the peers:

```yaml
  low-end-peer:
    proxy: http://192.168.1.100:8080
    models:
      - slow-image-model
    # httpTimeout: configure HTTP timeouts for connections to this peer
    # - optional, default: unset (uses defaults)
    # - useful when the peer runs on lower-end hardware
    httpTimeout:
      connectTimeout: 30
      responseHeaderTimeout: 600  # 10 minutes for very slow image generation
```

- [ ] **Step 3: Validate the YAML syntax**

Use a YAML validator or simply check that the file parses:

```bash
go run llama-swap.go -config config.example.yaml -dry-run 2>&1 | head -20
```

If there's no dry-run flag, try:
```bash
head -50 config.example.yaml | yq eval '.' -
```

Or just visually check the indentation matches surrounding code.

- [ ] **Step 4: Commit**

```bash
git add config.example.yaml
git commit -m "config: add httpTimeout examples to config.example.yaml"
```

---

## Task 5: Update docs/configuration.md with httpTimeout documentation

**Files:**
- Modify: `docs/configuration.md`

- [ ] **Step 1: Add httpTimeout documentation for models**

Find the model configuration documentation section (around line 200-325) and add a new section for `httpTimeout`. Add it after `concurrencyLimit` (around line 320):

```markdown
    # httpTimeout: configure HTTP timeouts for connections to this model
    # - optional, default: unset (uses internal defaults)
    # - when omitted, connectTimeout defaults to 30 seconds
    # - when omitted, responseHeaderTimeout defaults to 60 seconds
    # - useful for models on slower hardware that need longer timeouts
    # - increase responseHeaderTimeout to avoid "timeout awaiting response headers" errors
    httpTimeout:
      # connectTimeout: TCP connection timeout in seconds
      # - optional, default: 30
      connectTimeout: 30

      # responseHeaderTimeout: time to wait for response headers in seconds
      # - optional, default: 60
      # - for slow image generation or large models, consider increasing to 300+ seconds
      responseHeaderTimeout: 120
```

- [ ] **Step 2: Add httpTimeout documentation for peers**

Find the peers documentation section (around line 436-467) and add the httpTimeout documentation:

```markdown
    # httpTimeout: configure HTTP timeouts for connections to this peer
    # - optional, default: unset (uses internal defaults)
    # - useful when the peer runs on slower hardware
    httpTimeout:
      connectTimeout: 30
      responseHeaderTimeout: 600  # 10 minutes for very slow hardware
```

- [ ] **Step 3: Review the documentation for consistency**

Check that the documentation style matches existing entries (comment format, indentation, etc.).

- [ ] **Step 4: Commit**

```bash
git add docs/configuration.md
git commit -m "docs: document httpTimeout configuration for models and peers"
```

---

## Task 6: Run full test suite

**Files:**
- None to modify, just run tests

- [ ] **Step 1: Run the full test suite**

```bash
make test-all
```

This runs all tests including long-running concurrency tests.

- [ ] **Step 2: Fix any failing tests**

If tests fail:
1. Read the error message carefully
2. Check if it's related to your changes
3. Fix the issue and re-run
4. Common issues: missing imports, type mismatches, test data issues

- [ ] **Step 3: Final commit if any fixes were needed**

```bash
git commit -m "fix: address test failures from httpTimeout implementation"  # if needed
```

---

## Verification Checklist

Before considering implementation complete, verify:

- [ ] All tests pass (`make test-all`)
- [ ] No static analysis errors (`make test-dev`)
- [ ] Config examples parse correctly
- [ ] Documentation is clear and complete
- [ ] Both models and peers support httpTimeout
- [ ] Default values match current hardcoded values when not specified
- [ ] Timeout values are properly applied to HTTP transports

---

## Post-Implementation Notes

This implementation:
1. Adds optional `httpTimeout` configuration to both models and peers
2. Maintains full backward compatibility (omitting = use defaults)
3. Uses the same default values as the previous hardcoded values
4. Allows users to increase `responseHeaderTimeout` to avoid 502 errors on slow hardware
5. Follows existing patterns in the codebase (similar to TTL configuration)
