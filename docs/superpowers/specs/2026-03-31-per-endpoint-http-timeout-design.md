# Design: Per-Endpoint HTTP Timeout Configuration

## Summary

Expose HTTP timeout settings as configurable parameters for both models and peers to support lower-end hardware that requires longer timeouts than the current hardcoded defaults.

## Background

Users are experiencing `502 - peer proxy error: net/http: timeout awaiting response headers` errors when using slower hardware. The current implementation has hardcoded timeouts:
- `peerproxy.go`: ResponseHeaderTimeout = 60 seconds
- `process.go`: Various hardcoded timeouts for status updates and graceful shutdown

## Goals

1. Allow per-model HTTP timeout configuration
2. Allow per-peer HTTP timeout configuration
3. Maintain backward compatibility (omit = use defaults)
4. Add examples to `config.example.yaml`
5. Update configuration documentation

## Non-Goals

- Global HTTP timeout defaults (per-endpoint only)
- Breaking changes to existing configurations
- Modifying the existing `healthCheckTimeout` (already configurable globally)

## Configuration Design

### New `HTTPTimeout` struct (applies to both models and peers)

```yaml
httpTimeout:
  connectTimeout: 30           # seconds, default: 30
  responseHeaderTimeout: 60      # seconds, default: 60
```

### Model Configuration Example

```yaml
models:
  "slow-image-model":
    cmd: llama-server --port ${PORT} -m /path/to/model.gguf
    httpTimeout:
      connectTimeout: 30
      responseHeaderTimeout: 300   # 5 minutes for slow image generation
```

### Peer Configuration Example

```yaml
peers:
  "low-end-peer":
    proxy: http://192.168.1.100:8080
    models: [image-model]
    httpTimeout:
      connectTimeout: 30
      responseHeaderTimeout: 600   # 10 minutes for very slow hardware
```

## Implementation Plan

### 1. Add HTTPTimeout struct to `proxy/config/model_config.go`

Create a new struct for timeout configuration:

```go
type HTTPTimeoutConfig struct {
    ConnectTimeout        int `yaml:"connectTimeout"`        // seconds, default 30
    ResponseHeaderTimeout int `yaml:"responseHeaderTimeout"` // seconds, default 60
}
```

Add to `ModelConfig`:
```go
type ModelConfig struct {
    // ... existing fields ...
    HTTPTimeout HTTPTimeoutConfig `yaml:"httpTimeout"`
}
```

### 2. Add HTTPTimeout to `proxy/config/peer.go`

Add to `PeerConfig`:
```go
type PeerConfig struct {
    // ... existing fields ...
    HTTPTimeout HTTPTimeoutConfig `yaml:"httpTimeout"`
}
```

### 3. Update `proxy/process.go`

Modify `NewProcess()` to:
1. Accept the configured HTTPTimeout values
2. Create a custom transport with those timeouts when building the reverse proxy
3. Apply the timeout to the reverseProxy.Transport

### 4. Update `proxy/peerproxy.go`

Modify `NewPeerProxy()` to:
1. Create per-peer transports instead of a shared transport
2. Use configured timeout values from each peer's HTTPTimeout config
3. Fall back to sensible defaults when not configured

### 5. Update `config.example.yaml`

Add example HTTP timeout configurations to demonstrate the feature.

### 6. Update `docs/configuration.md`

Document the new `httpTimeout` field for both models and peers sections.

## Defaults and Validation

- `ConnectTimeout`: default 30 seconds (same as current hardcoded value in peerproxy.go)
- `ResponseHeaderTimeout`: default 60 seconds (same as current hardcoded value in peerproxy.go)
- All values must be positive integers (>= 1)
- Zero or omitted values use the defaults

## Testing

1. Unit tests for config parsing with httpTimeout values
2. Integration tests verifying timeout values are applied to transports
3. Test default values when httpTimeout is omitted
4. Test custom values override defaults correctly

## Files to Modify

1. `proxy/config/model_config.go` - Add HTTPTimeout struct and field to ModelConfig
2. `proxy/config/peer.go` - Add HTTPTimeout field to PeerConfig
3. `proxy/process.go` - Use configured timeouts in reverse proxy transport
4. `proxy/peerproxy.go` - Create per-peer transports with configured timeouts
5. `config.example.yaml` - Add example configurations
6. `docs/configuration.md` - Document the new feature

## Backward Compatibility

This change is fully backward compatible:
- Omitting `httpTimeout` uses the same defaults as current hardcoded values
- No changes to existing configuration file structure
- No changes to the API
- All new fields are optional
