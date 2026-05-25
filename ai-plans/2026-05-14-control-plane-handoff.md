# 2026-05-14 control-plane handoff

## Current state

The local fork already has most of the first llama-swap/Skein control-plane work on branch `feat/model-state-and-lifecycle-api`.

Implemented before this handoff:

- `GET /api/tags`
- `POST /api/show`
- `DELETE /api/delete`
- `GET /api/version`
- `GET /api/resources`
- `GET /api/config/info`
- `POST /api/config/models`
- `DELETE /api/config/models/:id`
- `POST /api/models/pull`
- `DELETE /api/models/:model`
- Models UI storage bar, file-exists badge, pull modal, and delete button

Implemented in this handoff:

- `PATCH /api/config/models/:id`
- Structured patch fields:
  - `ctx_size` or `ctx-size`
  - `n_gpu_layers` or `n-gpu-layers`
  - `ttl`
  - `flags` map for additional llama-server flags
- Partial updates preserve unrelated model config fields and unrelated command flags.
- Successful patches trigger config reload.

Example:

```bash
curl -X PATCH http://localhost:8080/api/config/models/qwen-coder \
  -H 'content-type: application/json' \
  -d '{"ctx_size":32768,"n_gpu_layers":99,"ttl":-1,"flags":{"threads":8}}'
```

## Files touched

- `proxy/proxymanager_api.go`
- `proxy/proxymanager_config.go`
- `proxy/proxymanager_config_test.go`

## Verification run

- `go test -v -run 'TestPatchCommandFlags|TestProxyManager_ApiConfigPatchModel' ./proxy`
- `make test-dev`

Note: `make test-dev` passed the Go test step. Its `staticcheck` step is currently masked with `|| true` and reports a local toolchain mismatch: module requires Go 1.26.1, while Staticcheck was built with Go 1.25.4.

`gofmt -l .` still reports pre-existing formatting drift in:

- `event/default_test.go`
- `event/event_test.go`

## Suggested next work

1. Add tests for the existing Ollama compatibility endpoints:
   - `/api/tags`
   - `/api/show`
   - `/api/delete`
2. Add response-shape docs for the control-plane endpoints.
3. Tighten `/api/resources` macOS behavior. It currently synthesizes Apple Silicon GPU from unified memory when perf has no GPU stats; that is useful, but utilization and temperature remain unknown.
4. Add `GET /api/hf/search?q=...` for GGUF discovery.
5. Consider a pull queue after direct pull is stable.

## Fleet UI direction

The existing Svelte UI should not be rewritten in Skein. Instead, make it a shared control surface:

- llama-swap keeps the current single-node UI and APIs.
- Skein exposes fleet APIs for backends, resources, model placement, routing, and proxy actions.
- The Svelte app adds a fleet route/mode that talks to Skein when configured or discovered.
- Existing model/resource components should get a small data-source boundary so the same components can render either one node or Skein-normalized fleet data.
- Node-specific detail actions should deep-link back to each backend's local llama-swap UI.

Browser code should not try to do mDNS directly. Discovery should run in Go and be exposed to the UI through HTTP.

## mDNS discovery direction

llama-swap does not currently appear to advertise itself via mDNS/Bonjour.

Proposed shape:

- llama-swap advertises `_llama-swap._tcp.local`.
- TXT metadata includes:
  - stable node ID
  - API version
  - UI path, usually `/ui/#/models`
  - feature flags such as `resources,tags,config_patch,pull`
  - `auth_required=true|false`
- Skein discovers `_llama-swap._tcp.local`, probes `/api/version` and `/api/resources`, then shows candidates.
- Skein should not automatically persist discovered nodes without operator acceptance.
- Skein can advertise `_skein._tcp.local` so the UI can find fleet mode through its backend.
