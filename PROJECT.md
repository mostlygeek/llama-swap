# Mantle — llama.cpp Management Proxy

## Project Goals

Mantle is a daemon/proxy for llama.cpp built on top of [llama-swap](https://github.com/mostlygeek/llama-swap). It extends the existing proxy with three management layers:

### 1. Model Management
- Browse HuggingFace for GGUF models
- Download with progress monitoring and resume support
- Track download tasks (progress, cancel)
- List and delete downloaded models

### 2. Configuration Management
- View the current YAML config in-browser
- Edit and save with YAML validation
- Hot-reload on save (no restart required)

### 3. Backend Build Management
- Trigger Docker builds of any llama.cpp fork from the UI
- Monitor build progress in real-time via SSE
- Cancel running builds
- List and delete compiled backends

### Future Goals (not yet implemented)
- Load balancing with peer co-workers (sticky sessions, failover)
- Multi-engine version management
- Daemon install (systemd/launchd)
- TTL auto-unload with smart scheduling

---

## Architecture

```
llama-swap core (Go proxy)  ←  Mantle extensions
     │                              │
     ├── Model lifecycle             ├── HF browse/download (mantle/)
     ├── Config loading              ├── Config editor + hot-reload
     ├── SSE event streaming         ├── Docker build orchestration
     ├── Performance monitoring      ├── Task tracking (progress/cancel)
     └── Svelte 5 UI                 └── New UI routes
```

### Tech Stack
- **Backend:** Go (extends llama-swap's existing packages)
- **Frontend:** Svelte 5 + TypeScript + Vite (same as existing UI)
- **Events:** Typed in-process event bus → SSE to browser
- **Builds:** Docker (reuses existing `build-llamacpp.sh`)

---

## Package Layout

```
llama-swap.go                 — entry point (modified: sets runtime paths)
internal/
  config/
    config.go                 — added ConfigPath, ModelsDir, BackendsDir, BuildScript fields
  shared/
    events.go                 — added BackendBuildProgressEvent, ModelDownloadProgressEvent
  server/
    server.go                 — integrated mantle.Handler (init + routes)
  mantle/                     — NEW: all Mantle management logic
    mantle.go                 — TaskManager, Task, HF search API
    download.go               — async GGUF download with resume + progress
    build.go                  — async Docker build via build-llamacpp.sh
    config.go                 — local models/backends listing + deletion
    api.go                    — HTTP handlers + SSE streaming (15 endpoints)
ui-svelte/
  src/
    lib/
      types.ts                — added MantleTask, HFModel, LocalModel, BackendEntry, etc.
      mantleApi.ts            — NEW: typed API client for all /api/mantle/ endpoints
    routes/
      ModelManager.svelte     — NEW: HF browser + download with progress bar + cancel
      ConfigEditor.svelte     — NEW: YAML editor with save + hot-reload
      BackendManager.svelte   — NEW: build trigger + progress + backend list
    App.svelte                — added /models/hub, /config, /backends routes
    components/
      Header.svelte           — added Hub, Backends, Config nav links
```

---

## API Reference (`/api/mantle/`)

### HF Model Browsing

| Method | Endpoint | Description |
|--------|----------|-------------|
| GET | `/api/mantle/models/search?q=<query>&limit=<n>` | Search HF for GGUF models |
| GET | `/api/mantle/models/files?model=<id>` | List GGUF files in a HF repo |

### Download Management

| Method | Endpoint | Description |
|--------|----------|-------------|
| POST | `/api/mantle/models/download` | Start download `{modelID, filename}` → task |
| DELETE | `/api/mantle/models/download/{id}` | Cancel download |
| GET | `/api/mantle/models/download/{id}/stream` | SSE progress stream |

### Local Models

| Method | Endpoint | Description |
|--------|----------|-------------|
| GET | `/api/mantle/models/local` | List downloaded GGUF files |
| DELETE | `/api/mantle/models/local/{name}` | Delete a model file |

### Config

| Method | Endpoint | Description |
|--------|----------|-------------|
| GET | `/api/mantle/config` | Get current config YAML |
| PUT | `/api/mantle/config` | Update config (validates YAML, writes, hot-reloads) |

### Backend Builds

| Method | Endpoint | Description |
|--------|----------|-------------|
| POST | `/api/mantle/backends/build` | Start build `{repo, branch}` → task |
| DELETE | `/api/mantle/backends/build/{id}` | Cancel build |
| GET | `/api/mantle/backends/build/{id}/stream` | SSE progress stream |
| GET | `/api/mantle/backends` | List compiled backends |
| DELETE | `/api/mantle/backends/{name}` | Delete a backend |

### Task Status

| Method | Endpoint | Description |
|--------|----------|-------------|
| GET | `/api/mantle/tasks` | List all tasks |
| GET | `/api/mantle/tasks/{id}` | Get single task status |

---

## How It Works

### Task System
Long-running operations (downloads, builds) create a `Task` object stored in `TaskManager`. Each task has:
- A cancel `context.Context` (for cleanup)
- A `cancelCh` (closed on cancel, polled by goroutines)
- Progress state updated atomically with mutex

Progress events are emitted through the existing typed event bus (`internal/event`) using new event types:
- `BackendBuildProgressEvent` (ID 0x08)
- `ModelDownloadProgressEvent` (ID 0x09)

### SSE Streaming
Each task has a dedicated SSE endpoint (`/api/mantle/models/download/{id}/stream` or `/api/mantle/backends/build/{id}/stream`) that:
1. Sends the initial task state
2. Subscribes to the typed event bus for that task type
3. Forwards events as `data:` lines
4. Watches `task.Done()` for the final state
5. Cleans up subscriptions when the client disconnects

### Config Hot-Reload
`PUT /api/mantle/config`:
1. Reads the raw YAML body
2. Parses via `config.LoadConfigFromReader` (validates all macros, aliases, ports)
3. Preserves runtime paths (ConfigPath, ModelsDir, BackendsDir, BuildScript)
4. Writes to disk
5. Emits `ConfigFileChangedEvent` (start/end) — triggers UI refresh via existing SSE stream

### HuggingFace Integration
- Searches via `huggingface.co/api/models` endpoint (sorted by downloads)
- Filters results for `.gguf` file presence
- Lists individual GGUF files via the model detail API
- Downloads use `Range` header for resume support

### Backend Builds
- Runs the existing `build-llamacpp.sh` inside Docker
- Output goes to `backends/build-{taskID}/` subdirectories
- Progress parsed from Docker step markers `[1/12]` and CMake `[42%]`
- Cancellation kills the Docker process and removes the output directory

---

## New UI Routes

| Route | Component | Purpose |
|-------|-----------|---------|
| `/models/hub` | `ModelManager.svelte` | HF search, file pick, download with progress |
| `/backends` | `BackendManager.svelte` | Build trigger, live progress, backend list |
| `/config` | `ConfigEditor.svelte` | YAML editor with Ctrl+S save + reload |

Existing routes (`/models`, `/logs`, `/activity`, `/performance`, playground) are unchanged.

---

## Things to Know

### Compilation
- `internal/mantle/` only depends on `internal/config`, `internal/event`, `internal/shared` — no new external Go deps
- The `go.mod` module path is `github.com/mostlygeek/llama-swap` (not changed, we're in the same module)

### Runtime Paths
Paths are set in `llama-swap.go` right after `config.LoadConfig()`:
- **ConfigPath** — the config file path from `-config` flag
- **ModelsDir** — defaults to `{configDir}/models/`
- **BackendsDir** — defaults to `{configDir}/backends/`
- **BuildScript** — defaults to `{configDir}/../llama-swap-additions/backends/build-llamacpp.sh`

These are re-applied during hot-reload so they survive config file changes.

### Event IDs
New event IDs are contiguous with existing ones:
- 0x08 = BackendBuildProgressEvent
- 0x09 = ModelDownloadProgressEvent

### Task States
Tasks transition: `running` → `completed` | `failed` | `cancelled`
Cancelled tasks clean up partial files (`.part` or build output directory).

---

## Current Status

- [x] Backend: Task manager with cancel support
- [x] Backend: HF model search + file listing
- [x] Backend: Async download with resume + SSE progress
- [x] Backend: Docker build orchestration + SSE progress
- [x] Backend: Config GET/PUT with validation + hot-reload
- [x] Backend: Local models/backends listing + deletion
- [x] Frontend: HF browser + download UI
- [x] Frontend: Build trigger + progress UI
- [x] Frontend: Config editor with save
- [x] Frontend: Nav links wired
- [ ] Go code: Needs build verification (no Go compiler in sandbox)
- [ ] Frontend: Needs `npm install && npm run build` to verify Svelte compilation

## What a Newcomer Should Do Next

1. **Fix Go compilation** — `go build ./...` from repo root and fix any type/import errors
2. **Fix the SSE stream issue** — `streamProgress()` in `api.go` subscribes to the typed event bus but the events are emitted by `Task.UpdateProgress()` which runs in the download/build goroutines. Need to verify the event bus wiring works end-to-end.
3. **Verify UI builds** — `cd ui-svelte && npm install && npm run build`
4. **Add missing features** — config YAML syntax highlighting, HF search pagination, multi-file download queue
5. **Write tests** — `internal/mantle/` has zero test coverage
