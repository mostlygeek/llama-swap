# vllm-wrapper

`vllm-wrapper` is a standalone helper program designed to be used as a model's `cmd` and `cmdStop` in llama-swap configurations for vLLM servers that have been started with `--enable-sleep-mode`.

It provides two subcommands:

- `serve`: Used as a model's `cmd`. Ensures the vLLM daemon is awake, waits for it to be healthy, then runs a reverse proxy from a local port to the vLLM upstream.
- `sleep`: Used as a model's `cmdStop`. Sends a sleep request to the vLLM daemon to free VRAM while keeping the process alive.

## Why use this?

When using vLLM with llama-swap, you can leverage vLLM's sleep mode to drastically reduce swap-in times. Instead of stopping and starting the vLLM process (which incurs a cold start), you can put the vLLM daemon to sleep when not in use (via `cmdStop`) and wake it up when needed (via `cmd`). This keeps the vLLM process running, preserving the GPU context and allowing for near-instant wake-ups.

## Prerequisites

- vLLM server must be started with `--enable-sleep-mode`.
- The vLLM server must be reachable at the URL provided to the wrapper.
- The wrapper does **not** start or stop the vLLM process itself; it assumes the vLLM daemon is already running (e.g., as a system service, Docker container, or managed by llama-swap via another mechanism). It only manages the sleep state.

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
    cmd: vllm-wrapper serve --vllm-url http://127.0.0.1:8000 --listen :${PORT}
    # Optional flags:
    #   --sleep-level: sleep level to use when sleeping (default: 1)
    #   --health-check-timeout: timeout for health checks (default: 120s)
```

When llama-swap starts the model, it will:
1. Ensure the vLLM daemon at `http://127.0.0.1:8000` is awake (by calling `/wake_up`).
2. Wait for the daemon to be healthy (by polling `/v1/models`).
3. Start a reverse proxy from the port assigned by llama-swap (via `${PORT}`) to `http://127.0.0.1:8000`.
4. Stay in the foreground as a proxy, allowing llama-swap to consider the model as running.

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

Here is a complete example using vLLM with sleep mode:

```yaml
models:
  qwen-7b-chat:
    cmd: vllm-wrapper serve --vllm-url http://127.0.0.1:8000 --listen :${PORT}
    cmdStop: vllm-wrapper sleep --vllm-url http://127.0.0.1:8000
    # You may also want to set a TTL to automatically unload after a period of inactivity:
    ttl: 3600   # unload after 1 hour of inactivity
```

## How it works

### serve subcommand

1. **Wake up**: Sends a POST request to `${vllm-url}/wake_up`. If the vLLM daemon is not reachable (connection refused), the wrapper exits with an error.
2. **Health check**: Polls `${vllm-url}/v1/models` until it returns HTTP 200 or the timeout is reached.
3. **Reverse proxy**: Starts an HTTP server listening on `${PORT}` (or the address provided to `--listen`) that proxies all requests to the vLLM upstream URL. The proxy preserves streaming responses by setting `X-Accel-Buffering: no`.

### sleep subcommand

1. Sends a POST request to `${vllm-url}/sleep` with a JSON body `{"level": <level>}` where `<level>` is the sleep level (default 1).
2. Upon receiving a successful response (HTTP 200), exits with status 0.

## Notes

- The wrapper uses standard library only (no external dependencies).
- It is designed to be simple and robust.
- For production use, ensure the vLLM daemon is properly managed (e.g., restarted if it crashes) outside of this wrapper.
- The wrapper does not handle TLS certificates; if your vLLM server uses HTTPS, provide the appropriate URL and ensure the system's root CAs are configured.

## Building

```bash
go build -o vllm-wrapper ./cmd/vllm-wrapper
```

## Running Tests

```bash
go test ./...
```
