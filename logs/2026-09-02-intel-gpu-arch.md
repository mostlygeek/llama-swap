# 2026-09-02 — Intel GPU architecture/model from PCI device ID

## Symptom / goal

On an Intel Arc B-series host the Hardware tab showed, for both the discrete
card (driver `xe`) and the integrated GPU (driver `i915`):

- `Architecture: Not detected`
- `Model` falling back to the generic `Gpu N`

Goal: populate a human-readable architecture codename (and a model where
unambiguous) for Intel GPUs on Linux.

## Diagnosis

`detectDRMSysfs` (`internal/hw/detect_linux.go`) builds Intel accelerators from
DRM sysfs but never set the `Architecture` field — that field is only populated
for AMD (ROCm/KFD gfx strings) and NVIDIA. Intel also leaves
`.../device/product_name` empty, so `Model` stayed empty too.

Intel encodes the GPU generation in the PCI device ID exposed at
`.../device/device` (e.g. `0xe20b`). Those IDs are stable, published values
(kernel `include/drm/intel/pciids.h` `INTEL_*_IDS` macros; Mesa chipset tables),
so architecture is derivable from the ID alone with no extra tooling.

Note the two "Not detected" values that are **correct** and were left alone: the
`i915` integrated GPU legitimately has no power cap and uses shared system
memory. The `xe`-driver discrete VRAM ("Shared System") is a separate bug
tracked for later (`device/tile0/vram0/total_bytes` vs the amdgpu-only
`mem_info_vram_total`).

## Change

New `internal/hw/intel.go` (platform-neutral — unit-testable without Linux,
reusable by the Windows dxgi path later):

```go
func intelGPU(deviceID uint16) (intelGPUInfo, bool)
```

- `intelArchByID map[uint16]string` grouped by platform with source-cited
  comments: DG1, Alchemist/DG2 + ATS-M, Battlemage/BMG G21, and integrated
  Tiger/Rocket/Alder/Meteor/Arrow/Lunar Lake.
- `intelModelByID map[uint16]string` only where a die → one product name:
  `0xE20B`→Arc B580, `0xE20C`→Arc B570, `0xE211`→Arc Pro B50, `0xE212`→Arc
  Pro B60. Alchemist dies + integrated parts (one die, many SKUs) get
  architecture only.
- Unknown IDs return `ok=false` → field stays "Not detected", never guessed.

`internal/hw/detect_linux.go` — in `detectDRMSysfs`, when the vendor is Intel:

```go
if id, err := strconv.ParseUint(strings.TrimPrefix(strings.ToLower(
        readTrimmed(filepath.Join(devicePath, "device"))), "0x"), 16, 16); err == nil {
    if info, ok := intelGPU(uint16(id)); ok {
        architecture = nonEmptyStringPtr(info.Architecture)
        if model == "" {
            model = info.Model
        }
    }
}
```

`ParseUint(_, 16, 16)` bounds the value, so the `uint16` conversion cannot
overflow (no gosec G115). No type/schema/UI change: `Architecture`/`Model` are
already `*string` on `Accelerator` and `hardware.js` renders both.

Tests: `internal/hw/intel_test.go` — discrete hit+model, Battlemage/Alchemist
arch-only, one ID per integrated platform, and unknown/boundary IDs that must
miss.

## Commands

- `gofmt -w internal/hw/intel.go internal/hw/intel_test.go internal/hw/detect_linux.go`
- `go test -v -run TestIntelGPU ./internal/hw/` → 5 passed
- `make test-dev` → all packages ok, staticcheck clean
- `make gosec` → `Issues: 0` for linux, darwin, windows
- `aidc-scan` → semgrep/gitleaks/gosec clean

## Verification

Unit tests pass and exercise the new lookup + boundaries. Real-hardware
confirmation on the reporter's B-series box is pending — the `xe` card should
read `Architecture: Battlemage` (and a model when the device ID is one of the
mapped SKUs).

## Notes

- Refresh the table as Intel ships new device IDs; per-group source citations
  make it a copy-from-header task.
- Follow-up: `xe`-driver VRAM detection (`device/tile0/vram0/total_bytes`) so
  discrete Intel memory stops reporting "Shared System".

## Follow-up (same session) — Apple GPU driver "Not detected"

### Symptom

Apple M4 Max showed `Driver: Not detected` (Architecture "Apple M4", Model
"Apple M4 Max", 128 GiB Unified were all correct; Power Limit not exposed by
Apple Silicon).

### Diagnosis

`parseSystemProfiler` only checked `spdisplays_metal` / `spdisplays_metal_support`
for the Metal driver. The machine's `system_profiler -json SPDisplaysDataType`
(pasted by the user) reports it under the renamed key
`spdisplays_mtlgpufamilysupport` = `spdisplays_metal4`.

### Change

- New neutral `internal/hw/apple.go`: `metalFamilyKeys` (all known spellings,
  newest first) + `metalVersion()` regex extractor.
- `detect_darwin.go` resolves via `firstMapString(display, metalFamilyKeys...)`
  and sets `Driver{Name:"Metal", Version:metalVersion(...)}` → renders "Metal 4".

### Verification

`TestMetalVersion` (neutral, runs on Linux) + `TestHardware_DarwinMetalDriverFamily`
(darwin, compile+vet-checked here via `GOOS=darwin go vet`, runs on macOS CI).
`make gosec` 0 across all OS; `aidc-scan` clean.
