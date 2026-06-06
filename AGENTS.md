## Project

llama-swap is a lightweight, transparent proxy server that provides automatic model swapping for llama.cpp's server.

## Tech Stack

- Go (proxy server, CLI)
- TypeScript, Vite, Svelte 5, Tailwind (UI in `ui-svelte/`)

## Architecture

```
llama-swap.go          # entry point, flag parsing, server startup
proxy/                 # core proxy logic: process management, routing, metrics, UI embed
internal/              # supporting packages: config, server, process, cache, router, events
cmd/                   # auxiliary tools: simple-responder (testing), wol-proxy, monitor-test
ui-svelte/             # Svelte 5 frontend, builds into internal/server/ui_dist/
config.example.yaml    # annotated example config
config-schema.json     # JSON schema for config validation
```

## Prerequisites

`go`, `npm`, `staticcheck`

## Running Locally

```bash
go run . -config config.example.yaml
```

## Testing

- Test naming: `TestProxyManager_<name>`, `TestProcessGroup_<name>`, etc.
- Run new tests: `go test -v -run <pattern> ./proxy/... ./internal/...`
- Run `gofmt -w <file>` before committing
- `make test-dev` — quick check after code changes in `proxy/` or `internal/` (runs `go test` + `staticcheck`)
- `make test-all` — full suite including race detection; run before completing work
- `make test-ui` — after UI changes in `ui-svelte/`
- Build binaries into `./build/`

## Workflow

- When summarizing changes only include details that require further action
- Just say "Done." when there is no further action
- Use the GitHub CLI `gh` to create pull requests and work with GitHub

See [docs/commit-format.md](docs/commit-format.md) and [docs/code-review.md](docs/code-review.md).

## Contributing to This File

Keep AGENTS.md lean — it is loaded into every agent context window. When adding new guidelines, create a file in `docs/` and link it from here rather than inlining. Update AGENTS.md only for high-frequency instructions that every session needs.
