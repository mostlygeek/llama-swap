---
title: Troubleshooting - a model won't load
summary: Work through the usual causes when a model fails to start, times out, or hangs on every request.
category: guides
tags: [troubleshooting, debugging, health-check, check-endpoint, logs, port]
config_keys: [healthCheckTimeout, models.*.checkEndpoint, models.*.proxy, models.*.cmd, logLevel]
updated: 2026-08-25
---

# Troubleshooting: a model won't load

Work through these in order. The first two account for most reports.

The first check is to copy the configured `cmd`, substitute a real port for
`${PORT}`, and run the command on its own in a shell.

## 1. Does the command work on its own?

Copy the `cmd` out of your config, substitute a real port for `${PORT}`, and
run it in a shell.

```console
$ llama-server --port 10001 --model /models/qwen.gguf
```

If it fails there, it is not a llama-swap problem — wrong path, missing GGUF,
insufficient VRAM, bad flag. Fix it there first.

## 2. Does `proxy` match the port in `cmd`?

This is the most common misconfiguration.

- Used `${PORT}` in `cmd` → leave `proxy` unset.
- Hardcoded a port in `cmd` → `proxy` **must** point at that port.
- Running a container → `proxy: "http://127.0.0.1:${PORT}"`, and check the
  right-hand side of `-p ${PORT}:NNNN` matches the port the server listens on
  *inside* the container.

Symptom of a mismatch: the process starts fine, but every request hangs or
returns a connection error.

## 3. Read the logs

```console
$ curl http://localhost:8080/logs
```

The web UI has a Logs page with proxy and upstream streams separated. The
**upstream** stream is the model's own stdout/stderr — that is where the real
error usually is.

Turn up detail with `logLevel: debug`.

## 4. Is the health check right?

llama-swap polls `checkEndpoint` (default `/health`) until it returns HTTP 200.
Servers that don't have `/health` never look ready, and every request waits
until `healthCheckTimeout` expires.

```yaml
models:
  vllm-model:
    checkEndpoint: /v1/models     # vLLM style
```

`checkEndpoint: "none"` skips the check entirely. That trades a timeout for
requests hitting a server that hasn't finished loading, so treat it as a last
resort.

## 5. Is it just slow?

```yaml
healthCheckTimeout: 500   # seconds; default 120, minimum 15
```

Large models, cold page cache and vLLM's compile step can all exceed the
default. If the log shows the server still starting up when llama-swap gives
up, raise this.

## 6. Is something else holding the port or the GPU?

A stale process from a previous run will do both.

```console
$ curl -X POST http://localhost:8080/api/models/unload
$ nvidia-smi                    # or rocm-smi
$ docker ps                     # stale containers?
```

Containers left behind by a missing or wrong `cmdStop` are a recurring cause;
use `cmdStop: docker stop ${MODEL_ID}` for Docker-managed models.

## 7. Validate the config file

```console
$ llama-swap --config config.yaml -validate
```

This checks the config and exits. Add the schema modeline at the top of your
file for editor validation as you type:

```yaml
# yaml-language-server: $schema=https://raw.githubusercontent.com/mostlygeek/llama-swap/refs/heads/main/config-schema.json
```

## Quick reference

| Symptom | Likely cause |
| --- | --- |
| Requests hang, process is running | `proxy` / `${PORT}` mismatch |
| "model not found" | ID in the request doesn't match the `models` key or an alias |
| Times out on every first request | `checkEndpoint` wrong, or `healthCheckTimeout` too low |
| Works once, fails after a swap | previous process not stopped — check `cmdStop` and `unloadTimeout` |
| Port already in use | stale process or container |
| Config won't load at all | unset `${env.VAR}`, or YAML syntax — run with `-validate` |

## Related

- `guides/model-runtime/writing-cmd`, `guides/model-runtime/ttl-and-unloading`
- `reference/config/healthCheckTimeout`, `reference/config/logLevel`
