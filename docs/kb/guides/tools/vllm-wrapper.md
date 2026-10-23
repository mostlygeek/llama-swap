---
title: Fast vLLM swapping with vllm-wrapper and sleep mode
summary: vllm-wrapper is used as cmd and cmdStop so llama-swap puts a running vLLM server to sleep instead of stopping it, and wakes it up on the next request.
category: guides
tags: [vllm-wrapper, vllm, sleep-mode, wake-up, vram, swap, tools]
config_keys: [models.*.cmd, models.*.cmdStop, models.*.proxy, models.*.ttl]
updated: 2026-10-23
---

# Fast vLLM swapping with vllm-wrapper and sleep mode

Starting vLLM from scratch is slow. vLLM's sleep mode frees the GPU memory while
keeping the process alive, so waking it is much faster than a cold start.
`vllm-wrapper` connects that to llama-swap:

- `vllm-wrapper serve` is the model's `cmd`. It wakes the vLLM server (or starts
  it if it is not running), waits until it is healthy, then proxies `${PORT}` to it.
- `vllm-wrapper sleep` is the model's `cmdStop`. It puts vLLM to sleep and stops
  the `serve` process. The vLLM server keeps running, asleep.

vLLM must be started with `--enable-sleep-mode`. vllm-wrapper runs on Linux and
macOS, and ships in the unified llama-swap image. To build it from the
repository root:

```bash
go build -o vllm-wrapper ./cmd/vllm-wrapper
```

## Config

Everything after `--` is the vLLM command, started directly (no shell):

```yaml
models:
  qwen-7b:
    cmd: |
      vllm-wrapper serve
      --vllm-url http://127.0.0.1:8000
      --listen :${PORT}
      --wait-timeout 600s
      --
      vllm serve /models/qwen-7b
      --host 127.0.0.1
      --port 8000
      --enable-sleep-mode
    cmdStop: vllm-wrapper sleep --vllm-url http://127.0.0.1:8000 --stop-pid ${PID}
    proxy: http://127.0.0.1:${PORT}
    ttl: 3600
```

`--vllm-url` must match the port you give vLLM. Give each vLLM model its own
port; two models sharing one would wake the same server.

## Flags

`serve`:

| flag | default | meaning |
| --- | --- | --- |
| `--vllm-url` | required | base URL of the vLLM server |
| `--listen` | required | address to proxy on, normally `:${PORT}` |
| `--wait-timeout` | `120s` | how long to wait for vLLM to become healthy after a cold start |
| `--health-path` | `/health` | vLLM health endpoint |
| `--sleep-level` | `1` | level used when `serve` receives SIGTERM |
| `--journal-unit` | none | systemd user unit whose logs are copied to llama-swap's log (see `guides/tools/vllm-wrapper-systemd`) |

`sleep`:

| flag | default | meaning |
| --- | --- | --- |
| `--vllm-url` | required | base URL of the vLLM server |
| `--sleep-level` | `1` | vLLM sleep level sent in the `/sleep` request |
| `--stop-pid` | none | PID of the `serve` process to stop after vLLM is asleep. Set it to `${PID}`; without it the unload ends in a process-group kill |

## What goes wrong

- **The first swap-in times out.** A cold start loads the model. Raise
  `--wait-timeout` and `healthCheckTimeout`.
- **Sleep mode errors.** vLLM answers `/sleep` and `/wake_up` only when started
  with `--enable-sleep-mode`; without it `cmdStop` fails.
- **VRAM is not freed.** Check vLLM's own logs for the `/sleep` request and its
  result. What a sleep level frees is defined by vLLM, not by vllm-wrapper.
- **vLLM is killed on unload and the next swap-in is a cold start.** llama-swap
  treats the model as stopped when `serve` exits. Without `--stop-pid ${PID}` in
  `cmdStop`, `serve` keeps running; after `unloadTimeout` (default 10s)
  llama-swap kills the whole process group, including a vLLM started directly
  after `--`. Always pass `--stop-pid ${PID}`. Starting vLLM with `systemd-run`
  also keeps it out of the process group.
