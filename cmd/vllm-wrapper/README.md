# vllm-wrapper

`vllm-wrapper` is a standalone helper program designed to be used as a model's `cmd` and `cmdStop` in llama-swap configurations for vLLM servers that have been started with `--enable-sleep-mode`.

It provides two subcommands:

- `serve`: Used as a model's `cmd`. Manages the vLLM daemon lifecycle: if the daemon is not running, starts it using the provided start command; if running but asleep, wakes it up; waits for it to be healthy, then runs a reverse proxy from a local port to the vLLM upstream.
- `sleep`: Used as a model's `cmdStop`. Sends a sleep request to the vLLM daemon to free VRAM while keeping the process alive.

## Why use this?

When using vLLM with llama-swap, you can leverage vLLM's sleep mode to drastically reduce swap-in times. Instead of stopping and starting the vLLM process (which incurs a cold start), you can put the vLLM daemon to sleep when not in use (via `cmdStop`) and wake it up when needed (via `cmd`). This keeps the vLLM process running, preserving the GPU context and allowing for near-instant wake-ups.

## Prerequisites

- vLLM server must be started with `--enable-sleep-mode`.
- The vLLM server must be reachable at the URL provided to the wrapper.
- To enable automatic start‑if‑not‑running, provide a `--start-cmd` flag with a command that launches the vLLM server (e.g., a `docker run` command that includes `--enable-sleep-mode`). The wrapper will start the daemon if it is not reachable, then wait for it to become healthy.

## Installation

Build the binary from source:

```bash
go build -o vllm-wrapper ./cmd/vllm-wrapper
```

Or install via `go install`:

```bash
go install ./cmd/vllm-wrapper
```

## Usage in llama-swap

### As a model's `cmd`

Configure your model in `config.yaml` with a `cmd` that invokes `vllm-wrapper serve`:

```yaml
models:
  my-vllm-model:
    cmd: vllm-wrapper serve --vllm-url http://127.0.0.1:8000 --listen :${PORT} --start-cmd "docker run --rm -p 8000:8000 ... --enable-sleep-mode"
    # Optional flags:
    #   --sleep-level: sleep level to use when sleeping (default: 1)
    #   --health-path: health check path (default: /health)
    #   --wait-timeout: timeout waiting for daemon to become healthy (default: 120s)
```

When llama-swap starts the model, it will:
1. Check if the vLLM daemon is healthy by querying `${vllm-url}${health-path}` (default `/health`).
2. If healthy and awake, proceed to step 4.
3. If not healthy, attempt to wake the daemon by calling `${vllm-url}/wake_up`.
4. If wake‑up fails (e.g., connection refused), execute the `--start-cmd` to start the daemon.
5. Wait for the daemon to become healthy (polling the health path).
6. Start a reverse proxy from the port assigned by llama-swap (via `${PORT}`) to `${vllm-url}`.
7. Stay in the foreground as a proxy, allowing llama-swap to consider the model as running.

### As a model's `cmdStop`

Configure your model's `cmdStop` to invoke `vllm-wrapper sleep`:

```yaml
models:
  my-vllm-model:
    cmdStop: vllm-wrapper sleep --vllm-url http://127.0.0.1:8000
    # Optional flag:
    #   --sleep-level: sleep level to use (default: 1)
```

When llama-swap stops the model, it will:
1. Send a sleep request to the vLLM daemon (POST to `/sleep` with JSON `{"level": 1}`).
2. Exit with status 0, leaving the vLLM daemon running but asleep.

## Example Configuration

Here is a complete example using vLLM with sleep mode, demonstrating cold start on first swap‑in and fast wake‑up on subsequent swaps:

```yaml
models:
  qwen-7b-chat:
    cmd: vllm-wrapper serve --vllm-url http://127.0.0.1:8000 --listen :${PORT} --start-cmd "docker run --rm -p 8000:8000 ... --enable-sleep-mode"
    cmdStop: vllm-wrapper sleep --vllm-url http://127.0.0.1:8000
    # You may also want to set a TTL to automatically unload after a period of inactivity:
    ttl: 3600   # unload after 1 hour of inactivity
```

## How it works

### serve subcommand

1. **Health check**: Sends a GET request to `${vllm-url}${health-path}` (default `/health`). If the response is HTTP 200, the daemon is considered healthy and awake, and we proceed to step 4.
2. **Wake up**: If the health check fails (non‑200 or connection error), send a POST request to `${vllm-url}/wake_up`. If the wake‑up succeeds (HTTP 200 or 204), proceed to step 4.
3. **Start daemon**: If the wake‑up fails (indicating the daemon is not running), execute the command specified by `--start-cmd` (run via `sh -c`). The wrapper starts the command as a child process, then waits for the daemon to become healthy by polling the health path.
4. **Reverse proxy**: Once the daemon is healthy, start an HTTP server listening on `${PORT}` (or the address provided to `--listen`) that proxies all requests to the vLLM upstream URL. The proxy preserves streaming responses by setting `X-Accel-Buffering: no`.

### sleep subcommand

1. Sends a POST request to `${vllm-url}/sleep` with a JSON body `{"level": <level>}` where `<level>` is the sleep level (default 1).
2. Upon receiving a successful response (HTTP 200), exits with status 0.

## Notes

- The wrapper uses standard library only (no external dependencies).
- It is designed to be simple and robust.
- For production use, ensure the vLLM daemon is properly managed (e.g., restarted if it crashes) outside of this wrapper.
- The wrapper does not handle TLS certificates; if your vLLM server uses HTTPS, provide the appropriate URL and ensure the system's root CAs are configured.
- On SIGTERM/SIGINT, the wrapper exits cleanly and does **not** kill the vLLM daemon, allowing it to be slept later.

## Building

```bash
go build -o vllm-wrapper ./cmd/vllm-wrapper
```

## Running Tests

```bash
go test ./...
```
