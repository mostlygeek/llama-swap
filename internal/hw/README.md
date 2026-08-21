# Hardware detection

The `hw` package creates a versioned `HardwareSnapshot` for the machine that
runs llama-swap. The snapshot describes hardware visible to the llama-swap
process. It does not try to describe hardware hidden by a container, virtual
machine, device filter, or operating-system permission.

The public entry point is:

```go
snapshot, err := hw.Detect(ctx, version)
```

llama-swap calls `Detect` once during startup. The result is kept unchanged for
the life of the process, including configuration reloads.

## Design goals

- Produce the version 1 `HardwareSnapshot` wire format.
- Use the same object for detection, the API response, and later reporting.
- Work without elevated privileges.
- Prefer operating-system APIs and tools that are normally available with a
  GPU driver.
- Keep platform-specific code behind Go build tags.
- Return partial information when a field cannot be detected safely.
- Never invent a value for an unknown fact.

This package detects static facts such as model names, memory capacity, and
driver versions. Live utilization, temperature, and memory usage belong in
`internal/perf`.

## Detection flow

`Detect` builds a snapshot in this order:

1. Set schema, capture, architecture, and operating-system defaults.
2. Read host, CPU, and system-memory information with `gopsutil`.
3. Adjust system memory for platform rules. Linux prefers non-device physical
   pages from `/proc/zoneinfo` over usable `MemTotal`, then applies any cgroup
   limit.
4. Run the platform-specific environment and accelerator probes.
5. Merge accelerator records that describe the same visible device.
6. Sort accelerators and assign dense, zero-based report indexes.
7. Validate the completed snapshot before returning it.

The supplied context controls external commands and normal `gopsutil` calls.
A canceled context stops detection and returns the context error.

System memory and schema validation are required for a usable snapshot. Most
CPU, operating-system, environment, and accelerator fields are best effort. A
failed optional probe leaves its fields `null`, uses an allowed unknown value,
or produces an empty accelerator list.

## Snapshot concepts

### Inference-host scope

`capture.scope` is always `inference_host`. The snapshot must describe the
machine running llama-swap, not a browser, API client, or monitoring server.

### Missing, unknown, and other

Optional facts use pointers so unavailable values encode as JSON `null`. Do
not use placeholder strings such as `"unknown"` or sentinel numbers such as
`-1`.

Some enum-like fields have both `unknown` and `other` values:

- `unknown` means detection could not determine the value.
- `other` means detection found a value that is not in the schema enum. The
  matching `raw_*` field must contain the original value.

Vendor and model strings are open-ended. Do not add a vendor or model
allowlist.

### Units

- Memory capacities are integer bytes.
- Power limits are finite watts.
- CPU counts are positive integers when present.
- Timestamps are UTC.

### Accelerator memory

Accelerator memory uses one of four kinds:

- `dedicated`: memory owned by the accelerator.
- `unified`: one coherent pool shared by CPU and accelerator, such as Apple
  unified memory.
- `shared_system`: system memory that the accelerator may borrow.
- `unknown`: the memory relationship could not be determined.

Do not add unified or shared-system capacity to total system memory. Those
values describe the same physical memory from another point of view.

### Accelerator identity and index

Platform probes return an internal `detectedAccelerator`. Its `identity` may be
a PCI address, UUID, LUID, or another stable value available during one
detection run. The identity is used only to merge results from multiple probes.
It is never included in `HardwareSnapshot` JSON.

An accelerator's public `index` is assigned after merging and sorting. It is a
report-local index, not a PCI index or durable hardware identifier.

When records merge, an existing non-null value wins. A later record fills only
missing fields. Put the most authoritative probe first when adding a new probe.

## Platform probes

| Platform | Environment and memory | Accelerators |
| --- | --- | --- |
| Linux | `gopsutil`, `/proc/zoneinfo`, WSL markers, virtualization metadata, `systemd-detect-virt`, container markers, cgroup memory limits | `nvidia-smi`, `rocm-smi`, AMD KFD topology, and DRM/sysfs |
| macOS | `gopsutil` and `sysctl` virtualization state | `system_profiler`, including Apple unified memory and Metal |
| Windows | `gopsutil` and WMI system metadata | `nvidia-smi`, DXGI, and WMI driver metadata or fallback enumeration |
| Other Go platforms | Common host, CPU, and memory data | No platform accelerator probe |

NVIDIA parsing is shared because `nvidia-smi` has the same static query format
on supported Linux and Windows systems.

## File layout

- `types.go`: versioned snapshot types and JSON field names.
- `validate.go`: schema and cross-field checks.
- `detect.go`: common detection, normalization, merging, and final indexing.
- `nvidia.go`: shared one-shot NVIDIA detection and parsing.
- `amd_linux.go`: Linux ROCm and KFD topology detection for AMD GPUs.
- `detect_linux.go`: Linux environment, memory, and generic DRM/sysfs probes.
- `detect_darwin.go`: macOS environment and `system_profiler` probes.
- `detect_windows.go`: Windows environment and WMI enrichment.
- `dxgi_windows.go`: native Windows DXGI adapter enumeration.
- `detect_other.go`: safe fallback for other operating systems.
- `hardware_*_test.go`: common and platform-specific tests.

## Adding or changing detection

When extending this package:

1. Keep wire-format changes in `types.go` and update `SchemaVersion` only when
   the external schema requires a new version.
2. Add matching validation before producing a new value.
3. Use a platform build tag for operating-system APIs and commands.
4. Pass the detection context to every external command.
5. Treat command absence, permissions, and unsupported driver fields as normal
   detection outcomes.
6. Normalize byte units and names at the probe boundary.
7. Give each accelerator the best private identity available so duplicate
   records can merge.
8. Do not expose private identities or tool-specific fields in the snapshot.
9. Add parser tests that use captured text or JSON instead of requiring local
   hardware.
10. Cross-compile platform-specific tests or the full binary after changing
    build-tagged code.

Use `HardwareSnapshot.Validate` as the final contract check. A detector should
not return a snapshot that fails validation.
