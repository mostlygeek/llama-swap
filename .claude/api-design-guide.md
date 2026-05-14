# llama-swap API Design Guide for AI Agents

This document captures the lessons from CodeRabbit review of PR #755 and the
patterns the llama-swap codebase expects. Read this before adding or modifying
any HTTP endpoints.

---

## Route patterns

### Use wildcards for model IDs

Model IDs can contain slashes (e.g. `author/model-name`). Routes that accept a
model ID **must** use `*model`, not `:model`:

```go
// CORRECT
apiGroup.GET("/models/*model", pm.apiGetModelHandler)
apiGroup.DELETE("/models/*model", pm.apiDeleteModel)

// WRONG — breaks on slash-containing IDs
apiGroup.GET("/models/:model", pm.apiGetModelHandler)
```

Handlers must trim the leading `/` that gin prepends to wildcard captures:

```go
requestedModel := strings.TrimPrefix(c.Param("model"), "/")
```

The `load` and `unload` routes already use `*model` and are the reference:
```go
apiGroup.POST("/models/load/*model", pm.apiLoadSingleModelHandler)
apiGroup.POST("/models/unload/*model", pm.apiUnloadSingleModelHandler)
```

---

## Process group access

### Always use swapProcessGroup for model operations

`findGroupByModelName` only locates the process group — it skips exclusive-group
eviction. Any handler that triggers model loading **must** use `swapProcessGroup`:

```go
// CORRECT — honours exclusive-group eviction
processGroup, err := pm.swapProcessGroup(realModelName)
if err != nil {
    pm.sendErrorResponse(c, http.StatusInternalServerError, err.Error())
    return
}

// WRONG — bypasses swap semantics
processGroup := pm.findGroupByModelName(realModelName)
```

`pm.matrix.ProxyRequest` is correct for matrix mode and does not need this change.

---

## Model state reporting

### Alias resolution in modelProcessState

`modelProcessState` resolves aliases to canonical IDs before doing any process
lookup. If you need to report state for a model ID that may be an alias, call
`modelProcessState` directly — do not pre-resolve before passing it in.

### State values

The full set of valid state strings: `"ready"`, `"starting"`, `"stopping"`,
`"stopped"`, `"shutdown"`, `"unknown"`.

- `"stopped"` means the process exists but is stopped, or has no process object.
- `"unknown"` is reserved for future states that are not yet handled.
- Never return `"stopped"` from the switch `default:` — use `"unknown"` so new
  states surface clearly rather than silently masking bugs.

---

## Streaming responses (ndjson)

### Commit to streaming only after validating upstream

Do **not** set `Content-Type: application/x-ndjson` or write any bytes until
you have confirmed the upstream HTTP response is `200 OK`. Once the first byte
is written, the status code is locked at 200 and errors can no longer carry a
correct HTTP status.

```go
// CORRECT — check upstream status first, then start stream
resp, err := http.DefaultClient.Do(hreq)
// ... error check ...
if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
    c.JSON(resp.StatusCode, gin.H{"error": msg})   // proper status, no stream started
    return
}
if resp.StatusCode != http.StatusOK {
    c.JSON(http.StatusBadGateway, gin.H{"error": msg})
    return
}
// Only now start streaming
if stream {
    c.Header("Content-Type", "application/x-ndjson")
    ...
}
```

### Write/flush errors in stream handlers

Check errors from `c.Writer.Write` and `c.Writer.Flush` — a broken client
connection should terminate the goroutine, not spin silently.

---

## File operations

### Check f.Close() before rename

`f.Close()` can surface deferred write errors (buffer flush, fsync). Ignoring
it and calling `os.Rename` immediately can promote a truncated file:

```go
// CORRECT
if err := f.Close(); err != nil {
    os.Remove(tmp)
    // ... error response ...
    return
}
if err := os.Rename(tmp, dest); err != nil { ... }
```

### Path traversal: use filepath.Rel

`strings.HasPrefix(filepath.Clean(dest), filepath.Clean(dir))` is unsafe —
`/tmp/foo` would match `/tmp/foobar`. Use `filepath.Rel`:

```go
rel, err := filepath.Rel(filepath.Clean(dir), filepath.Clean(dest))
if err != nil || strings.HasPrefix(rel, "..") {
    // path traversal detected
}
```

---

## Security

### URL allowlisting (SSRF)

Any handler that makes an outbound HTTP request based on user-supplied URLs must
validate the target host. For model downloads:

- Short form `owner/repo/file.gguf` is always safe (URL is constructed internally).
- Full URL form: require `https://` and `huggingface.co` / `*.huggingface.co`.
- Loopback addresses (`127.0.0.1`, `localhost`, `::1`) are allowed for
  development and testing.
- All other remote hosts are rejected with `400 Bad Request`.

```go
host := strings.ToLower(u.Hostname())
isLoopback := host == "localhost" || host == "127.0.0.1" || host == "::1"
if !isLoopback {
    if u.Scheme != "https" { return error }
    if !isHuggingFaceHost(host) { return error }
}
```

### Bearer token scoping

Never forward authentication tokens to unvalidated hosts. Only attach the
`Authorization: Bearer` header when the download URL is a HuggingFace domain:

```go
if isHuggingFaceHost(u.Hostname()) {
    hreq.Header.Set("Authorization", "Bearer "+token)
}
```

---

## Platform portability

### Isolate syscall imports with build tags

`syscall.Statfs` is Unix-only and breaks Windows builds. Split any
platform-specific code into tagged files:

```
proxy/proxymanager_storage_unix.go    //go:build !windows
proxy/proxymanager_storage_windows.go //go:build windows
```

The shared file (`proxymanager_models.go`) must not import `syscall` directly.

---

## Code organisation

### Don't duplicate shared utilities

`config.SanitizeCommand` is the authoritative shell tokeniser for command
strings. Do not copy-paste a local version. All callers in `proxy/` should
import and call `config.SanitizeCommand(cmd)`.

### Deterministic map iteration

`pm.config.Models` is a `map[string]config.ModelConfig`. Never rely on
iteration order for user-visible results. Sort the keys first:

```go
ids := make([]string, 0, len(pm.config.Models))
for id := range pm.config.Models { ids = append(ids, id) }
sort.Strings(ids)
for _, id := range ids { ... }
```

---

## Test conventions

### Naming

All test functions in `proxy/` follow the pattern `TestProxyManager_<subject>`:

```go
// CORRECT
func TestProxyManager_apiPullModel(t *testing.T) { ... }
func TestProxyManager_resolveHFSource(t *testing.T) { ... }

// WRONG
func TestAPIPullModel(t *testing.T) { ... }
func TestResolveHFSource(t *testing.T) { ... }
```

### Test coverage checklist for new endpoints

- [ ] Happy path (200)
- [ ] Model not found (404)
- [ ] Slash-containing and alias model IDs
- [ ] Error body content, not just status code
- [ ] Streaming path (ndjson) if the endpoint supports it
- [ ] Security rejection cases (SSRF, path traversal) for any endpoint that
      accepts user-supplied URLs or file paths
