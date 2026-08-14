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
- To enable automatic start‑if‑not‑running, provide the daemon executable and arguments after `--`.

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

#### Daemon startup

For native vLLM installations or when `llama-swap` splits the command into separate arguments, use the `--` separator. Everything after `--` is treated as the daemon executable and its arguments, launched directly without `sh -c`:

```yaml
models:
  my-vllm-model:
    cmd: |
      vllm-wrapper serve
      --vllm-url http://127.0.0.1:8000
      --listen :${PORT}
      --wait-timeout 600s
      --
      ${vllm}
      serve
      ${model_path}
      --host 127.0.0.1
      --port 8000
      --max-model-len ${context_size}
      --enable-sleep-mode
```

Benefits of argv-based startup:
- Enables native vLLM without Docker.
- Supports vLLM executables defined through llama-swap macros.
- Allows readable multiline commands in YAML.
- Preserves individual vLLM options (can be commented out).
- Avoids shell parsing overhead.
- Correctly preserves arguments containing spaces.

### As a model's `cmdStop`

Configure your model's `cmdStop` to invoke `vllm-wrapper sleep`:

```yaml
models:
  my-vllm-model:
    cmdStop: |
      vllm-wrapper sleep
      --vllm-url http://127.0.0.1:8000
      --stop-pid ${PID}
    # Optional flags:
    #   --sleep-level: sleep level to use (default: 1)
    #   --stop-pid: PID of the serve proxy to terminate after a successful sleep request
```

When llama-swap stops the model, it will:
1. Send a sleep request to the vLLM daemon (POST to `/sleep` with JSON `{"level": 1}`).
2. If `--stop-pid` is provided, send SIGTERM to the specified `vllm-wrapper serve` process after the sleep request succeeds.
3. Exit with status 0, leaving the vLLM daemon running but asleep while allowing llama-swap to complete the unload operation.

## Example Configuration

Here is a complete example using vLLM with sleep mode, demonstrating cold start on first swap‑in and fast wake‑up on subsequent swaps:

```yaml
models:
  qwen-7b-chat:
    cmd: |
      vllm-wrapper serve
      --vllm-url http://127.0.0.1:8000
      --listen :${PORT}
      --
      ${vllm}
      serve
      ${model_path}
      --host 127.0.0.1
      --port 8000
      --enable-sleep-mode
    cmdStop: vllm-wrapper sleep --vllm-url http://127.0.0.1:8000 --stop-pid ${PID}
    # You may also want to set a TTL to automatically unload after a period of inactivity:
    ttl: 3600   # unload after 1 hour of inactivity
```

## Systemd setup for native vLLM startup

`llama-swap` runs as the system service user `llama`, while vLLM is started as a transient user service with `systemd-run --user`.

Enable the user systemd manager:

```bash
sudo loginctl enable-linger llama

uid=$(id -u llama)
sudo systemctl start "user@${uid}.service"
```

Expose the user D-Bus session to `llama.service`:

```bash
sudo mkdir -p /etc/systemd/system/llama.service.d

sudo tee /etc/systemd/system/llama.service.d/user-bus.conf >/dev/null <<EOF
[Unit]
Wants=user@${uid}.service
After=user@${uid}.service

[Service]
Environment=XDG_RUNTIME_DIR=/run/user/${uid}
Environment=DBUS_SESSION_BUS_ADDRESS=unix:path=/run/user/${uid}/bus
EOF

sudo systemctl daemon-reload
sudo systemctl restart llama.service
```

Use `systemd-run` in the `llama-swap` model command:

```yaml
cmd: |
  vllm-wrapper serve
  --vllm-url http://127.0.0.1:18000
  --listen :${PORT}
  --wait-timeout 5m
  --journal-unit: vllm-qwen.service
  # optional systemd user unit whose new journal entries are forwarded to the wrapper's stdout, making vLLM logs available through llama-swap's upstream log stream.
  --
  systemd-run
  --user
  --unit=vllm-qwen
  --collect
  --property=Restart=no
  --property=StandardOutput=journal
  --property=StandardError=journal
  --setenv=HF_HOME
  --setenv=VLLM_SERVER_DEV_MODE
  --
  ${vllm}
  serve
  ${model}
  --port 18000
  --enable-sleep-mode
```

Environment variables required by vLLM must be passed explicitly with `--setenv`.

Check the service status:

```bash
uid=$(id -u llama)

sudo -u llama env \
  XDG_RUNTIME_DIR="/run/user/${uid}" \
  DBUS_SESSION_BUS_ADDRESS="unix:path=/run/user/${uid}/bus" \
  systemctl --user status vllm-qwen.service
```

Follow the vLLM logs:

```bash
uid=$(id -u llama)

sudo -u llama env \
  XDG_RUNTIME_DIR="/run/user/${uid}" \
  DBUS_SESSION_BUS_ADDRESS="unix:path=/run/user/${uid}/bus" \
  journalctl --user \
    -u vllm-qwen.service \
    -f \
    -o cat
```

## How it works

### serve subcommand

1. **Health check**: Sends a GET request to `${vllm-url}${health-path}` (default `/health`). If the response is HTTP 200, the daemon is considered healthy and awake, and we proceed to step 4.
2. **Wake up**: If the health check fails (non‑200 or connection error), send a POST request to `${vllm-url}/wake_up`. If the wake‑up succeeds (HTTP 200 or 204), proceed to step 4.
3. **Start daemon**: If the wake‑up fails (indicating the daemon is not running), use the command specified after `--` (argv-based, launched via `exec.Command`). The wrapper starts the command as a child process, then waits for the daemon to become healthy by polling the health path.
4. **Reverse proxy**: Once the daemon is healthy, start an HTTP server listening on `${PORT}` (or the address provided to `--listen`) that proxies all requests to the vLLM upstream URL. The proxy preserves streaming responses by setting `X-Accel-Buffering: no`.

### sleep subcommand

1. Sends a POST request to `${vllm-url}/sleep` with a JSON body `{"level": <level>}` where `<level>` is the sleep level (default 1).
2. If `--stop-pid` is provided, sends SIGTERM to the specified `vllm-wrapper serve` process after the sleep request succeeds.
3. Exits with status 0, leaving the vLLM daemon running in sleep mode.

## Notes

- The wrapper uses standard library only (no external dependencies).
- It is designed to be simple and robust.
- For production use, ensure the vLLM daemon is properly managed (e.g., restarted if it crashes) outside of this wrapper.
- The wrapper does not handle TLS certificates; if your vLLM server uses HTTPS, provide the appropriate URL and ensure the system's root CAs are configured.
- On SIGTERM, the wrapper sends a sleep request to vLLM (using the configured sleep level) before shutting down, then exits cleanly without killing the vLLM daemon.

## Building

```bash
go build -o vllm-wrapper ./cmd/vllm-wrapper
```

## Running Tests

```bash
go test ./...
```
