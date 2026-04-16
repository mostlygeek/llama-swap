# Improve Testability (#655)

## Current Pain Points

1. **Tests bypass config loading** - ~80% of tests build `config.Config` structs directly, skipping YAML parsing, env var substitution, macro expansion, and `${PORT}` assignment. Config bugs in those paths go untested.

2. **simple-responder is everywhere** - Every proxy/routing test launches a real subprocess, waits for health checks (~healthCheckTimeout: 15), and manages process lifecycle just to test HTTP routing. Most of that overhead is wasted.

3. **Port counter is fragile** - A global `nextTestPort` counter starting at 12000 with a mutex. Parallel tests or leftover processes can collide.

## Stages

### Stage 1: YAML-based test config helper

**Goal:** Tests go through the real `LoadConfigFromReader` path instead of hand-building structs.

**Effort:** Low | **Impact:** Config bugs caught earlier | **Risk:** None

Create a test helper in `proxy/helpers_test.go`:

```go
// testConfigFromYAML substitutes simple-responder paths and loads through
// the real config pipeline (env vars, macros, port assignment, etc.)
func testConfigFromYAML(t *testing.T, yamlTmpl string) config.Config {
    t.Helper()
    yamlStr := strings.ReplaceAll(yamlTmpl, "{{RESPONDER}}", filepath.ToSlash(simpleResponderPath))
    cfg, err := config.LoadConfigFromReader(strings.NewReader(yamlStr))
    require.NoError(t, err)
    return cfg
}
```

Tests would then look like:

```go
func TestProxyManager_SwapProcessCorrectly(t *testing.T) {
    config := testConfigFromYAML(t, `
healthCheckTimeout: 15
logLevel: error
models:
  model1:
    cmd: {{RESPONDER}} --port ${PORT} -silent -respond model1
  model2:
    cmd: {{RESPONDER}} --port ${PORT} -silent -respond model2
`)
    proxy := New(config)
    // ... same assertions
}
```

**Why this stage first:** Zero production code changes. Pure test-side refactoring. Can be done incrementally - migrate tests one at a time. Each migrated test now validates the full config pipeline.

**Scope:** ~20-30 tests in `proxymanager_test.go`, `processgroup_test.go`, `peerproxy_test.go`.

### Stage 2: In-process test server (eliminate simple-responder for routing tests)

**Goal:** Replace simple-responder subprocess launches with `httptest.Server` for tests that don't specifically test process lifecycle.

**Effort:** Medium | **Impact:** 10-100x faster routing tests | **Risk:** Low (additive, no existing code broken)

Two parts:

**2a. Extract simple-responder's handler into a test helper:**

```go
// newTestUpstreamServer creates an in-process HTTP server that mimics
// llama.cpp's API (same endpoints as simple-responder).
func newTestUpstreamServer(respond string) *httptest.Server {
    mux := http.NewServeMux()
    mux.HandleFunc("/v1/chat/completions", func(w http.ResponseWriter, r *http.Request) { ... })
    mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) { ... })
    // ... other endpoints
    return httptest.NewServer(mux)
}
```

**2b. Add "external" process mode to `Process`:**

Allow `cmd` to be empty/omitted when `proxy` is set. This means "connect to an already-running server" (also useful for real configs pointing at external services). In `Process.Start()`:

```go
func (p *Process) Start() error {
    if p.config.Cmd == "" {
        // No subprocess to manage - just verify the proxy is reachable
        p.setState(StateReady)
        return nil
    }
    // existing subprocess logic...
}
```

Tests for routing/auth/CORS/streaming then become:

```go
func TestProxyManager_AuthRequired(t *testing.T) {
    upstream := newTestUpstreamServer("model1")
    defer upstream.Close()

    config := testConfigFromYAML(t, `
healthCheckTimeout: 15
logLevel: error
requiredAPIKeys: [test-key]
models:
  model1:
    cmd: ""   # no subprocess
    proxy: `+upstream.URL+`
`)
    // proxy requests go directly to httptest.Server, no subprocess needed
}
```

**Why this matters:** The `httptest.Server` is ready instantly. No process spawn, no health check timeout, no port allocation. Routing tests go from ~100ms each (process startup + health check) to ~1ms.

**Scope:** Most tests in `proxymanager_test.go` (auth, CORS, model listing, streaming, peer proxy), `peerproxy_test.go`, `metrics_monitor_test.go`.

### Stage 3: Migrate tests incrementally

**Goal:** Convert existing tests to use the Stage 1 + Stage 2 helpers.

**Effort:** Medium | **Impact:** Cleaner, more reliable tests | **Risk:** None

Priority order:
1. `proxymanager_test.go` routing tests (highest count, most repetition)
2. `peerproxy_test.go` (straightforward, all HTTP routing)
3. `metrics_monitor_test.go` (capture logic doesn't need real processes)
4. `processgroup_test.go` swap tests (keep simple-responder for actual swap lifecycle tests)

Tests that **must keep simple-responder:**
- Process lifecycle: start/stop, SIGKILL, SIGTERM, TTL expiry, health check failures, failed start counting
- ProcessGroup swap concurrency (the port-collision test in `TestProcessGroup_ProxyRequestSwapIsTrueParallel`)

**Scope:** ~60-70% of tests can drop simple-responder.

### Stage 4 (optional): Process interface for ProcessGroup

**Goal:** Enable pure unit tests of ProcessGroup's swap/exclusive/concurrency logic without any HTTP server at all.

**Effort:** High | **Impact:** Pure unit tests possible | **Risk:** Medium (refactor core code)

```go
type ProcessController interface {
    Start() error
    Stop(StopStrategy)
    ProxyRequest(http.ResponseWriter, *http.Request) error
    CurrentState() ProcessState
    ID() string
    SetState(ProcessState)  // for test setup
}
```

This requires:
- Extracting the interface
- A `MockProcess` implementation
- Refactoring `ProcessGroup` to use the interface instead of `*Process`

**Recommendation:** Only do this if ProcessGroup grows significantly more complex. Stages 1-3 give 80% of the benefit for 20% of the effort.

## Effort/Impact Summary

| Stage | Effort | Impact | Risk |
|-------|--------|--------|------|
| 1. YAML config helper | Low | Config bugs caught earlier | None |
| 2. In-process test server | Medium | 10-100x faster routing tests | Low |
| 3. Migrate tests | Medium | Cleaner, more reliable tests | None |
| 4. Process interface | High | Pure unit tests possible | Medium |

**Recommended approach:** Do stages 1-3 in order. Each stage is independently valuable and can ship on its own. Stage 4 is deferred unless there's a specific need.
