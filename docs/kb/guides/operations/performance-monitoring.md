---
title: Performance monitoring and GPU probes
summary: How the Performance page, /metrics and GPU probes work, when GPUs are read, and how to pick probes for your hardware.
category: guides
tags: [performance, gpu, vram, metrics, prometheus, nvidia-smi, rocm-smi, lact, sysfs, power]
config_keys: [performance.disabled, performance.system.poll, performance.gpu.poll, performance.gpu.probes, performance.gpu.skipSuspended, performance.gpu.exclude, performance.gpu.settings]
updated: 2026-10-24
---

# Performance monitoring and GPU probes

llama-swap collects system stats (CPU, RAM, swap, load, network) and GPU stats
for the Performance page, Prometheus at `/metrics`, and the `sys__performance`
MCP tool. It is experimental and on by default.

## When readings are taken

Each section has `poll`:

- `always`: read every `every` (5s minimum).
- `active`: read only while a model is loaded **and** someone is watching: the
  Performance page is open, Prometheus scrapes `/metrics`, or the MCP tool asks.

System stats default to `always`. GPUs default to `active`, so monitoring never
keeps an idle GPU awake. With no model loaded, `/metrics` leaves out the GPU
gauges and the page shows "Paused". Set `gpu.poll: always` for continuous
GPU graphs.

On Linux, `skipSuspended` (default on) checks the PCI power state first and
does not query a runtime-suspended GPU, so a laptop dGPU can stay asleep.

## Probes

GPU stats come from probes. Every probe returns the same fields; a value a
probe cannot read is left out, never shown as 0.

| probe | OS | reads |
| --- | --- | --- |
| `nvidia-smi` | Linux, Windows | NVIDIA, including GB10/Jetson unified memory |
| `rocm-smi` | Linux | AMD, VRAM and GTT |
| `lact` | Linux | the LACT daemon socket, any vendor |
| `xpu-smi` | Linux | Intel discrete GPUs |
| `sysfs` | Linux | kernel hwmon, amdgpu counters and DRM fdinfo; no tools needed |
| `d3dkmt` | Windows | any WDDM GPU |
| `mactop`, `ioreg` | macOS | Apple Silicon |

`probes: [auto]` (the default) picks probes from the detected hardware, for
example only `nvidia-smi` on an NVIDIA box, or `sysfs` on an AMD APU. Mixed
hardware runs every probe. A list runs only those probes, in that order.
When two probes report the same GPU (matched by PCI bus ID, then UUID), the
first one wins.

```yaml
performance:
  gpu:
    poll: always              # keep graphing while idle
    probes: [sysfs, rocm-smi] # an AMD APU: sysfs sees GTT, rocm-smi only the carveout
    exclude: ["Intel UHD"]    # hide an unused iGPU (PCI bus ID, UUID or name substring)
    settings:
      rocm-smi:
        path: /opt/rocm/bin/rocm-smi
      lact:
        socket: /run/lactd.sock
```

`performance.disabled: true` turns everything off. `gpu.disabled: true` keeps
system stats but stops GPU reads.

## What goes wrong

- **GPU section is empty.** The probe's tool is not installed or not in the
  container. Run `go run ./cmd/monitor-test` on the host to see which probes
  work, then install the tool or set `settings.<probe>.path`.
- **Idle power is higher with llama-swap running.** Check that `gpu.poll` is
  `active`, or set `gpu.disabled: true`.
- **APU memory looks tiny.** `rocm-smi` and `lact` only see the VRAM carveout;
  put `sysfs` first. The chart shows GTT as dashed "shared" lines.
- **A GPU shows up twice.** One probe could not report a PCI address. Remove
  one probe from the list or hide one entry with `exclude`.
- **Metrics labels changed.** GPU gauges use `{gpu="<pci bus id>",name="..."}`
  and `llamaswap_gpu_memory_util_percent` no longer exists.
