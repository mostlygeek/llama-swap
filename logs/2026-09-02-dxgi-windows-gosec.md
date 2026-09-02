# 2026-09-02 — Suppress DXGI COM interop gosec findings (Windows)

## Symptom / goal

CI `gosec` reported 12 `GOOS=windows` findings in
`internal/hw/dxgi_windows.go`:

- 5× **G115** (CWE-190, integer overflow) at lines 60, 73, 105, 124 (124 twice)
- 7× **G103** (CWE-242, use of `unsafe.Pointer`) at lines 56, 57, 69, 71, 83,
  84, 119

Local `make gosec` reported `Issues: 0` for all three GOOS — because the local
`gosec` is the `dev` build, which predates the G115 rule. CI pins
`gosec@v2.26.1` (`.github/workflows/gosec.yml:42`), which flags them.

## Diagnosis

Both classes are false positives inherent to DXGI COM interop:

- **G115** — a DXGI `HRESULT` is a 32-bit status code returned from a syscall as
  `uintptr`. `hresultFailed` truncates to `uint32` then `int32` and tests the
  sign bit — the documented Win32 `SUCCEEDED`/`FAILED` semantics. The
  `luid:%08x:%08x` identity reinterprets the fixed 32-bit `AdapterLUIDHighPart`
  ABI field for display. Neither can overflow on the 64-bit targets shipped.
- **G103** — `unsafe.Pointer` is mandatory to pass the COM factory/adapter
  vtable pointers and the `DXGI_ADAPTER_DESC` struct across
  `procCreateDXGIFactory.Call` / `syscall.SyscallN`. Identical by-design verdict
  to the existing `internal/perf/{pdh,d3dkmt}_windows.go` sites.

## Change

Added inline `#nosec` markers at the exact flagged lines, mirroring the
established Windows-syscall suppression style (`pdh_windows.go`):

- G103 ×7 — `// #nosec G103 -- unsafe.Pointer is required to marshal the DXGI COM syscall ABI`
- G115 ×4 — HRESULT/LUID reason strings (the `hresultFailed` line's single
  marker covers both flagged conversions on it, so 4 markers suppress 5
  findings).

Updated the audit ledger `docs/gosec-suppressions.md`:

- Summary totals: G115 25→29, G103 20→27, total 78→89.
- G115 and G103 section prose now name `internal/hw/dxgi_windows.go` and explain
  the HRESULT/LUID and COM-vtable rationale.

No code was restructured to dodge the scanner (per `AGENTS.md` policy).

## Commands

- `go install github.com/securego/gosec/v2/cmd/gosec@v2.26.1` — reproduce CI
- `GOOS=windows $(go env GOPATH)/bin/gosec ./internal/hw/` → `Issues: 0`
- `for os in linux darwin windows; do GOOS=$os gosec ./...; done` → all
  `Issues: 0`, no Golang build errors
- `GOOS=windows go build ./internal/hw/` → ok
- `gofmt -w internal/hw/dxgi_windows.go` → clean
- `go test ./internal/audit/` → `TestNosecLedgerInSync` passes
- `aidc-scan` → semgrep/gitleaks/gosec clean

## Verification

With the CI-pinned `gosec@v2.26.1`, all three GOOS targets report `Issues: 0`
and no build errors. The `TestNosecLedgerInSync` guard confirms the 11 new
markers match the ledger counts.

## Notes

`make gosec` locally runs the `dev` gosec, which under-reports relative to CI —
that is why these findings were invisible until CI ran. Verification here
installed `v2.26.1` explicitly. Follow-up worth considering: pin the same gosec
version in the `Makefile`/tooling so local and CI agree and future G115-class
findings surface before push.
