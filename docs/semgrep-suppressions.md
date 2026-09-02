# semgrep suppressions (false positives)

`aidc-scan` runs `semgrep scan --config auto` and treats WARNING/ERROR findings
as gate failures. Every finding in first-party code has been reviewed; the one
real issue (a GitHub Actions shell-injection in `release.yml`) was **fixed** by
passing the untrusted input through `env`. The rest are false positives or
by-design and are suppressed inline with a documented
`// nosemgrep: <rule-id>` (or `# nosemgrep:` in YAML) marker.

Vendored, third-party minified UI libraries under
`internal/server/ui_dist/vendor/` are excluded from directory scans via
`.semgrepignore`. Because `aidc-scan` passes changed files to semgrep
explicitly (which bypasses `.semgrepignore`), the individual findings inside
those bundles (Chart.js, highlight.js, KaTeX — prototype-pollution,
non-literal-regexp, incomplete-sanitization) also carry a per-line
`// nosemgrep:` guard. These are pre-built third-party libraries, not
first-party code, and are validated to still parse (`node --check`).

To list the live markers:

```sh
grep -rn "nosemgrep" internal/ cmd/ .github/
```

## Rule categories suppressed

- **`no-fprintf-to-responsewriter`, `no-direct-write-to-responsewriter`,
  `no-printf-in-responsewriter`** — the XSS-audit rules assume an HTML response.
  llama-swap is a JSON / plain-text / Prometheus / SSE API and proxy; it never
  emits HTML, so writing bytes to the `http.ResponseWriter` is normal and safe.
  Sites: `internal/perf/prometheus.go`, `internal/swaputil/http.go`,
  `internal/server/{api,apigroup,log}.go`, `cmd/{fake-model,wol-proxy}`, tests.
- **`dangerous-exec-command`** — launching operator-configured model commands is
  the product's core function (also `#nosec G204`). Sites:
  `internal/process/process_command.go`, `cmd/vllm-wrapper`.
- **`open-redirect`** — relative, same-origin redirects with fixed prefixes
  (also `#nosec G710`). Site: `internal/server/api.go`.
- **`math-random-used`** — non-cryptographic load-balancing selection (also
  `#nosec G404`). Site: `internal/router/loading.go`.
- **`racy-append-to-slice`** — the flagged append runs on the main goroutine
  before any concurrent goroutine starts; the concurrent appends are
  mutex-guarded. Site: `internal/server/server.go`.
- **`iterate-over-empty-map`, `string-to-int-signedness-cast`** — Windows GPU
  telemetry syscall code. Site: `internal/perf/pdh_windows.go`.
- **`run-shell-injection`** (`.github/workflows/unified-docker*.yml`) — inherited
  upstream CI whose `${{ }}` values are constrained build-matrix inputs, not free
  user input, and are already routed through `env` where user-facing. Suppressed
  pending an upstream hardening pass.
