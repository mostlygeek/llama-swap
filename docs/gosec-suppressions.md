# gosec suppressions (false positives)

`make gosec` runs `gosec` against `GOOS=linux`, `darwin`, and `windows` and is
expected to report **zero findings** in CI. Every gosec finding in `internal/`
has been reviewed; the genuine ones (29 × `G104` unhandled errors) were fixed by
handling the error, and the rest are false positives or by-design and are
suppressed inline with a documented `// #nosec <rule> -- <reason>` marker.

This file is the human-readable audit ledger for those suppressions. Policy
(per `AGENTS.md`): suppress a false positive at the exact line with a one-line
justification — never restructure code to dodge the scanner, and never
blanket-disable a rule. When you add or remove a `#nosec`, update this file.

To list the live markers at any time:

```sh
grep -rn "#nosec" internal/
```

Total: **33** suppressions across **9** rules (G115 ×18, G204 ×6, G120 ×2, G710 ×2, G304 ×1, G703 ×1, G705 ×1, G118 ×1, G103 ×1).

---

## G115 — integer overflow (uint64 → int) · 18 sites · HIGH

**Verdict: false positive on every supported build target.**

Every flagged expression is `int(U / 1024*1024)` where `U` is a `uint64` byte
count (system RAM, swap, or VRAM from `gopsutil` / `mactop` / LACT). For **any**
`uint64` value — including the maximum, garbage, or adversarial input —
`floor(U / 2^20) ≤ floor((2^64 − 1) / 2^20) = 2^44 − 1 = 17,592,186,044,415`,
which is far below `MaxInt64 = 2^63 − 1`. The project builds only for 64-bit
targets (`GOARCH=amd64`, `arm64`), where `int` is 64-bit, so the conversion can
never overflow. The bound comes from the `/2^20` divisor, not from any
assumption about realistic memory sizes (safety margin `2^63 / 2^44 = 2^19`).

Caveat: this closed-form guarantee assumes a 64-bit `int`. The repo ships no
32-bit (`386`/`arm`) targets; if one were ever added, overflow would still
require > 2 PB of the measured metric, but the guarantee would degrade from
mathematical to physical.

Marker: `// #nosec G115 -- uint64 bytes /(1024*1024) <= 17592186044415 < MaxInt64 on 64-bit build targets`

Sites:
- `internal/perf/gpu_parse.go:158,159` — mactop memory (used/total)
- `internal/perf/monitor_unix.go:460,463` — LACT VRAM (used/total, nil-guarded)
- `internal/perf/monitor_unix.go:546,547,574,575,576` — swap + system RAM
- `internal/perf/monitor_darwin.go:89,185,186,199,200,201` — RAM + swap
- `internal/perf/monitor_windows.go:109,110,111` — system RAM

---

## G204 — subprocess with non-constant args · 6 sites · MEDIUM

**Verdict: by-design / false positive.** Launching operator-configured commands
and shelling out to monitoring tools is the application's core purpose. None of
the arguments come from untrusted/remote input, and no shell is invoked.

- `internal/process/process_command.go:417` — runs the model's `cmd`, parsed via
  `config.SanitizedCommand()` (operator config, CLI-flag trust).
- `internal/process/process_command.go:532` — runs the operator's `cmdStop`
  (`config.SanitizeCommand`); the only interpolation is an integer `${PID}`.
- `internal/process/runtime_windows.go:50` — `taskkill` (literal binary), literal
  flags, integer PID.
- `internal/perf/monitor_unix.go:146` — `nvidia-smi` (literal binary + flags +
  integer `--loop` arg).
- `internal/perf/monitor_windows.go:41` — `nvidia-smi`, same as above.
- `internal/perf/monitor_darwin.go:124` — `mactop` (literal binary + flags +
  integer `--interval` arg).

---

## G120 — bounded multipart form parsing · 2 sites · MEDIUM

**Verdict: bounded by design.** `r.ParseMultipartForm(32 << 20)` caps in-memory
buffering at 32 MiB (larger parts spill to temp files). The audio/image upload
endpoints legitimately need large bodies; 32 MiB is a deliberate limit. (If
stricter DoS protection is ever wanted, add an `http.MaxBytesReader` hard cap —
at the cost of capping legitimate large uploads.)

Marker: `// #nosec G120 -- multipart parsing bounded at 32 MiB; required for audio/image upload endpoints`

Sites: `internal/server/filters.go:99`, `internal/router/router.go:147`

---

## G710 — open redirect · 2 sites · MEDIUM

**Verdict: false positive.** The redirect target is always
`"/upstream/" + searchName + "/"`, where `searchName` is a *configured* model
name resolved from the request path. It is a relative, same-origin path under
`/upstream/` and cannot point off-site.

Marker: `// #nosec G710 -- same-origin /upstream/<configured-model>/ path; cannot redirect off-site`

Sites: `internal/server/api.go:220,222`

---

## G304 — file path from variable · 1 site · MEDIUM

**Verdict: by-design.** `os.Open(path)` opens the config file at the
operator-supplied `--config` path — CLI-flag trust, not untrusted input.

Site: `internal/config/config.go:184`

---

## G703 — file path from env var · 1 site · HIGH

**Verdict: by-design / false positive.** `os.Stat` of
`LACT_DAEMON_SOCKET_PATH`, an operator-set env var (same trust as a CLI flag)
naming the LACT daemon Unix socket to dial. Not untrusted input, and the path is
used for `net.DialTimeout`, not a file-content read.

Site: `internal/perf/monitor_unix.go:342`

---

## G705 — XSS via response write · 1 site · MEDIUM

**Verdict: false positive.** `w.Write(history)` streams logs on a response the
handler already sets to `Content-Type: text/plain` + `X-Content-Type-Options:
nosniff`, so a browser never renders the bytes as HTML.

Site: `internal/server/log.go:117`

---

## G118 — goroutine vs request-scoped context · 1 site · HIGH

**Verdict: false positive.** `go p.run()` starts the single-writer lifecycle
goroutine — one per model — which returns on `parentCtx.Done()` (router teardown
/ app shutdown). It is bounded and not request-scoped, so the leak/CWE-400
concern does not apply. The `Background`-rooted `startCtx`/`cmdCtx` it derives
are explicitly cancelled and also guarded by `parentCtx.Done()`.

Site: `internal/process/process_command.go:113`

---

## G103 — use of unsafe · 1 site · LOW (windows only)

**Verdict: required idiom.** `uintptr(unsafe.Pointer(&info))` / `unsafe.Sizeof`
is the mandated way to call the Win32 `SetInformationJobObject` syscall via
`golang.org/x/sys/windows`; the syscall cannot be made without it.

Site: `internal/process/treecleanup_windows.go:37`
