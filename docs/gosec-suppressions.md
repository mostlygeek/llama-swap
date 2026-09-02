# gosec suppressions (false positives)

`make gosec` runs `gosec` against `GOOS=linux`, `darwin`, and `windows` and is
expected to report **zero findings** in CI. Every gosec finding in `internal/`
has been reviewed. The genuine ones were **fixed by handling the error / adding
a limit**, not suppressed:

- All `G104` unhandled-error findings were fixed by explicitly handling or
  discarding the error (`_ =` / `_, _ =`), across `internal/` and `cmd/`.
- All `G112` slowloris findings were fixed by setting `ReadHeaderTimeout` on
  every `http.Server` (`llama-swap.go`, `cmd/*`).

The remaining findings are false positives or by-design, suppressed inline with
a documented `// #nosec <rule> -- <reason>` marker. This file is the
human-readable audit ledger for those suppressions. Policy (per `AGENTS.md`):
suppress a false positive at the exact line with a one-line justification —
never restructure code to dodge the scanner, and never blanket-disable a rule.
When you add or remove a `#nosec`, update this file.

To list the live markers at any time:

```sh
grep -rn "#nosec" internal/
```

Total: **89** suppressions across **12** rules (G115 ×29, G103 ×27, G304 ×10, G204 ×7, G404 ×4, G202 ×3, G117 ×2, G120 ×2, G710 ×2, G118 ×1, G703 ×1, G705 ×1).

---

## G115 — integer overflow in numeric conversions · 29 sites · HIGH

**Verdict: false positive on the supported 64-bit build targets.**

Every flagged expression converts a bounded system/hardware counter — RAM/VRAM
byte counts (divided down by `1024*1024`), GPU utilization percentages, or
fixed-width syscall fields — read from gopsutil / mactop / LACT / the Windows
D3DKMT and PDH APIs. The values are physically bounded well below `MaxInt`/the
target width on the 64-bit targets the project ships (`amd64`, `arm64`).
The `internal/hw/dxgi_windows.go` sites reinterpret DXGI `HRESULT` status codes
and the fixed 32-bit LUID ABI field: an `HRESULT` is defined as a 32-bit value,
so truncating the syscall `uintptr` return to 32 bits and testing its sign bit
is the documented Win32 `SUCCEEDED`/`FAILED` semantics, not an overflow.
Files: `internal/perf/{monitor_unix,monitor_darwin,monitor_windows,gpu_parse,d3dkmt_windows,pdh_windows}.go`,
`internal/hw/{detect_linux,dxgi_windows}.go`.

## G103 — use of unsafe.Pointer · 27 sites · (info)

**Verdict: required, by design.** All sites are in the Windows GPU/adapter
telemetry syscall paths (`internal/perf/d3dkmt_windows.go`, `pdh_windows.go`,
`internal/hw/dxgi_windows.go`, `internal/process/treecleanup_windows.go`), where
`unsafe.Pointer` is mandatory to marshal fixed-layout structs and COM vtable
pointers across the `syscall`/`golang.org/x/sys/windows` boundary. There is no
safe alternative for these OS ABIs.

## G204 — subprocess launched with a variable · 7 sites · MEDIUM

**Verdict: by design — this is the product.** llama-swap's core function is to
launch operator-configured model server commands (`cmd`, `cmdStop`) and helper
tools (`nvidia-smi`, `rocm-smi`, `powermetrics`). The command source is the
operator's trusted config, not attacker input.
Files: `internal/process/{process_command,runtime_windows}.go`,
`internal/perf/{monitor_unix,monitor_darwin,monitor_windows}.go`,
`internal/hw/detect_linux.go`.

## G304 — file inclusion via variable · 10 sites · MEDIUM

**Verdict: false positive.** Every path is either the operator-supplied config
file (`internal/config/{config,merge,load_windows}.go`) or an internally
constructed sysfs/hwmon path enumerated by the kernel
(`internal/hw/{detect_linux,amd_linux}.go`, `internal/perf/monitor_sysfs.go` —
the GPU telemetry provider reads `/sys/class/drm/*/` + `/proc/<pid>/fdinfo/`
paths it derives from `ReadDir` listings, never from request input). None
derive from request input.

## G404 — weak random number generator · 4 sites · HIGH

**Verdict: false positive.** `internal/router/loading.go` uses `math/rand` only
to spread load and jitter across ready processes. This is not a security or
cryptographic context; a CSPRNG would add cost without benefit.

## G202 — SQL string concatenation · 3 sites · MEDIUM

**Verdict: false positive.** `internal/store/store.go` builds dynamic `WHERE`
clauses whose user values are always bound via `?` placeholders, and the only
concatenated identifiers (sort column, direction) come from an internal
whitelist. No user input reaches the SQL text.

## G117 — marshaling of a struct with a sensitive field · 2 sites · MEDIUM

**Verdict: false positive.** `internal/config/redact.go` marshals the config as
the first step of `RedactedYAML`/`ConfigTopLevelKeys`, which then scrub the
credential-named keys (`apiKeys`) before returning. The marshaled intermediate
never leaves the function un-redacted.

## G120 — unbounded form parsing · 2 sites · MEDIUM

**Verdict: false positive.** `internal/swaputil/http.go` parses multipart forms
with an explicit `MaxMultiPartSize` (32 MB) in-memory bound. A hard total-size
cap would break the large audio/image uploads this proxy must forward.

## G710 — open redirect · 2 sites · MEDIUM

**Verdict: false positive.** `internal/server/api.go` issues relative,
same-origin redirects with fixed `/comfyui/` and `/upstream/` prefixes; only the
query string is carried over and the host is never user-controlled.

## G118 — goroutine uses context.Background · 1 site · MEDIUM

**Verdict: by design.** `internal/process/process_command.go` starts the process
lifecycle goroutine, which intentionally outlives any single request context.

## G703 — path traversal via taint · 1 site · HIGH

**Verdict: false positive.** `internal/perf/monitor_unix.go` reads the LACT
daemon socket path from the operator-configured `LACT_DAEMON_SOCKET_PATH`
environment variable.

## G705 — XSS via taint · 1 site · MEDIUM

**Verdict: false positive.** `internal/server/log.go` streams log history as
`text/plain` with `X-Content-Type-Options: nosniff`, so the browser never
interprets it as HTML.
