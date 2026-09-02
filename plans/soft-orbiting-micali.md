# Plan: Intel GPU architecture (and model) mapping from PCI device IDs

## Context

On an Intel Arc B70 host, the Hardware tab shows **Architecture: Not detected** for
both Intel GPUs, and **Model** falls back to a generic `Gpu N`. Root cause: the Linux
sysfs detector `detectDRMSysfs` (`internal/hw/detect_linux.go`) never populates the
`Architecture` field for Intel, and Intel's `.../device/product_name` sysfs attribute
is almost always empty, so `Model` is empty too. Only AMD (ROCm/KFD gfx strings) and
NVIDIA set architecture today.

Intel encodes the GPU generation in the PCI **device ID** (`.../device/device`, e.g.
`0xe20b` for Battlemage). These IDs are stable, published values in the kernel
(`include/drm/i915_pciids.h`, xe `xe_pciids.h`) and Mesa (`src/intel/dev/intel_device_info.c`,
`include/pci_ids/*`). We can map device ID → architecture codename (and, for discrete
cards, marketing model) from a curated table.

Decisions confirmed with the user:
- **Label format:** human-readable codename — `Battlemage`, `Alchemist`, `Meteor Lake`, etc.
- **Coverage:** Arc discrete (DG1, Alchemist/DG2, Battlemage/BMG) + recent integrated Xe
  (Meteor Lake, Lunar Lake, Arrow Lake, Tiger/Alder/Raptor Lake Xe/Iris Xe). Older Gen9-
  integrated parts are out of scope (stay "Not detected").
- **Fields:** populate both `Architecture` and `Model`. Discrete IDs map to a concrete
  model (e.g. `Arc B570`); integrated IDs where the marketing name is ambiguous set
  architecture only and leave `Model` nil.

## Approach

### 1. New lookup table + function — `internal/hw/intel.go` (no build constraint)

Platform-neutral so it is unit-testable without Linux and reusable by the Windows dxgi
path later.

```go
package hw

type intelGPUInfo struct {
    Architecture string // codename, e.g. "Battlemage" — always set for a match
    Model        string // marketing name, e.g. "Arc B570" — "" when ambiguous (most iGPUs)
}

// intelGPU returns architecture/model for a known Intel PCI device ID, ok=false otherwise.
func intelGPU(deviceID uint16) (intelGPUInfo, bool)
```

Implementation: a `map[uint16]intelGPUInfo` literal grouped by family with a comment per
group citing the source header. Representative IDs (full lists pulled from the kernel/Mesa
sources during implementation):
- **Battlemage (BMG):** `0xE202, 0xE20B, 0xE20C, 0xE20D, 0xE210, 0xE212, 0xE215, 0xE216` →
  arch `Battlemage`, models `Arc B580`/`Arc B570` where the ID is unambiguous.
- **Alchemist (DG2):** `0x5690–0x56BF`, `0x4F80–0x4F88` → arch `Alchemist`, models
  `Arc A770`/`A750`/`A580`/`A380`/`A310`/`Arc Pro A60`/`A40` where unambiguous.
- **DG1:** `0x4905–0x4909` → `DG1`.
- **Integrated Xe:** Meteor Lake (`0x7D40/0x7D45/0x7D55/0x7DD5`…), Lunar Lake, Arrow Lake,
  Tiger/Alder/Raptor Lake Xe/Iris Xe → arch codename only, `Model` = "".

Fallback: unknown ID → `ok=false` → no change (field stays "Not detected"), so we never
guess wrong.

### 2. Wire into the Linux sysfs detector — `internal/hw/detect_linux.go`

In `detectDRMSysfs`, after `vendor` is resolved and before building the accelerator:

- Parse the PCI device ID: `readTrimmed(filepath.Join(devicePath, "device"))` →
  strip `0x`, `strconv.ParseUint(s, 16, 16)`.
- When `vendor == "Intel"` and the parse succeeds, call `intelGPU(id)`. On a hit:
  - set `Architecture` = `nonEmptyStringPtr(info.Architecture)`.
  - set model: prefer existing sysfs `product_name` if non-empty, else `info.Model`
    (so `model := readTrimmed(...)` becomes `if model == "" { model = info.Model }`).

Leave the existing `mem_info_vram_total` / power-limit logic untouched (VRAM is the
separate `xe`-driver fix discussed earlier; not part of this plan).

### 3. Tests — `internal/hw/intel_test.go` (new, no build constraint)

Table-driven `TestIntelGPU_<...>` covering:
- Discrete hit with model (e.g. `0xe20b` → `Battlemage` + expected model).
- Alchemist range hit (e.g. `0x56a0`).
- Integrated hit, arch-only, empty model (e.g. a Meteor Lake ID).
- Unknown ID → `ok=false`.
- Boundary IDs at the edges of any range-based groups.

Follow the naming convention (`TestIntelGPU_...`), run with
`go test -v -run TestIntelGPU ./internal/hw/`.

Optional (nice-to-have, only if low-cost): parameterize `detectDRMSysfs` roots like
`detectAMDKFD` already does, to add one integration test writing a fake
`.../device/{vendor,device}` tree. If it requires reworking `hasAccessibleRenderNode`'s
`/dev/dri` lookup, skip it — the pure-function tests are the coverage that matters.

## Files
- `internal/hw/intel.go` — new: table + `intelGPU` lookup (neutral).
- `internal/hw/intel_test.go` — new: unit tests.
- `internal/hw/detect_linux.go` — wire lookup into `detectDRMSysfs` (~6 lines).

No UI change: `hardware.js` already renders `accelerator.architecture` and `model` via
`shown(...)`; populating the Go fields is sufficient. No type/schema change: `Architecture`
and `Model` are already `*string` on `Accelerator` (`internal/hw/types.go`) with no enum
validation in `validate.go`.

## Verification
1. `gofmt -w internal/hw/intel.go internal/hw/intel_test.go internal/hw/detect_linux.go`
2. `go test -v -run TestIntelGPU ./internal/hw/`
3. `make test-dev` (go test + staticcheck; `internal/` changed).
4. `make gosec` — must be zero findings across linux/darwin/windows.
5. `aidc-scan` on changed files; fix anything above LOW.
6. Real-hardware smoke: build into `./build/`, run on the B70 box, open the Hardware tab —
   the `xe` GPU should now read **Architecture: Battlemage** (and a model if its ID is
   unambiguous). The i915 iGPU should read its integrated codename. (VRAM/"Shared System"
   is unchanged — tracked as the separate `xe` VRAM fix.)
7. Update `CHANGELOG.md` + `DETAILED_CHANGELOG.md`; add `logs/YYYY-MM-DD-intel-gpu-arch.md`.

## Notes / follow-ups
- Table needs occasional refresh as Intel ships new device IDs; the per-group source
  citations in `intel.go` make that a copy-from-header task. Unknown IDs degrade gracefully.
- The Windows dxgi detector can reuse `intelGPU` later; out of scope here.
- Separate known bug (not in this plan): `xe`-driver VRAM should be read from
  `device/tile0/vram0/total_bytes` instead of the amdgpu-only `mem_info_vram_total`.
