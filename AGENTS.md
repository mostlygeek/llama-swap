## Project Description:

llama-swap is a light weight, transparent proxy server that provides automatic model swapping to llama.cpp's server.

## Tech stack

- golang
- typescript, vite and svelt5 for UI (located in ui/)

## Workflow Tasks

- when summarizing changes only include details that require further action
- just say "Done." when there is no further action
- use the github CLI `gh` to create pull requests and work with github
- Rules for creating pull requests:
  - keep them short and focused on changes.
  - never include a test plan
  - write the summary using the same style rules as commit message

## Testing

- Follow test naming conventions like `TestServer_<test name>`, `TestProcessCommand_<test name>`, etc.
- Use `go test -v -run <name pattern for new tests>` to run any new tests you've written.
- Run `gofmt -w <file>` before committing to fix any formatting
- Build go binaries into the ./build/ subdirectory
- Use `make test-dev` after running new tests for a quick over all test run. This runs `go test` and `staticcheck`. Fix any static checking errors. Use this only when changes are made to any code under the `internal/` directory
- Use `make test-all` before completing work. This includes long running concurrency tests.
- The web UI under `internal/server/ui_dist/` is hand-authored vanilla ES-module JavaScript committed to the repo with no build step; edit the served files directly. Go's `make test` covers serving/embedding (`internal/server/ui_test.go`).

## Security scanning

- Run `make gosec` after code changes; it scans `GOOS=linux`, `darwin`, and `windows` and must report zero findings.
- Fix genuine findings. For a false positive, suppress at the exact line with `// #nosec G<rule> -- <reason>` — never restructure code to dodge the scanner and never blanket-disable a rule.
- Every suppression is documented in [docs/gosec-suppressions.md](docs/gosec-suppressions.md); update that ledger whenever you add or remove a `#nosec` marker.

### Commit message example format:

```
proxy: add new feature

Add new feature that implements functionality X and Y.

- key change 1
- key change 2
- key change 3

fixes #123
```

## Code Reviews

- use three levels High, Medium, Low severity
- label each discovered issue with a label like H1, M2, L3 respectively
- High severity are must fix issues (security, race conditions, critical bugs)
- Medium severity are recommended improvements (coding style, missing functionality, inconsistencies)
- Low severity are nice to have changes and nits
- Include a suggestion with each discovered item
- Limit your code review to three items with the highest priority first
- Double check your discovered items and recommended remediations

<!-- aidc:core-logics:start -->
# Shared Agent Guidance (aidc)

Read `/opt/CORE_LOGICS/patternlist.md` before starting work.

Use `/opt/CORE_LOGICS` for reusable guidance that should survive beyond this repo. Add broadly useful patterns there on the current project branch rather than storing them only in repo-local scratch files.

Keep project edits inside `/workspace` unless the task explicitly targets `/opt/CORE_LOGICS`.

## Security guardrails (non-negotiable)

Before declaring any code task complete, run **`aidc-scan`** (on PATH inside
the container) and fix every finding above LOW. It scopes itself to your
changed files and picks the right scanners automatically — semgrep + secrets
always; shellcheck/bandit/gosec/cargo-audit/bundle-audit/npm-audit when the
matching files changed; dependency vetting (`vet`) and the SBOM/license gate
only when manifests or the LICENSE changed. `aidc-scan --all` scans the whole
repo; individual scanners remain available directly (see `docs/security.md`
in the aidc repo for the full matrix).

Non-negotiable regardless of change size: never dismiss findings as "out of
scope" or "pre-existing" without explicit user confirmation — fix or flag,
never silently skip. When findings exist, the work is not done: fix them,
re-run `aidc-scan`, and only then report the task as complete. If anything
looks like a live credential, also run `trufflehog filesystem --no-update .`.

(For Claude Code a Stop hook enforces this mechanically; other agents follow
this text — same rule either way.)

## Testing & coverage

Every code change ships with tests. Aim for full coverage of the lines you add or change — cover the happy path, edge cases, and error handling, not just the obvious case. If a line is genuinely untestable, say why in the change.

Run the project's coverage tool and confirm the changed code is exercised before declaring work complete:

- **Go**: `go test -cover ./...`
- **Python**: `pytest --cov` (`coverage run -m pytest` + `coverage report`)
- **Rust**: `cargo llvm-cov` (or `cargo tarpaulin`)
- **Node**: `npm test -- --coverage` (`jest --coverage` / `vitest run --coverage`)
- **Ruby**: SimpleCov via `bundle exec rspec`

Coverage tools that aren't pre-installed can be added per project in `.devcontainer/project-setup.sh`.

## Documentation & changelog

Scale the paperwork to the change:

- **Trivial changes** (typo, comment, formatting — no logic, no dependencies,
  no security surface): no changelog or session-log entry required.
- **Everything else** updates **both** changelog files:
  - `CHANGELOG.md` — one high-level bullet under the right heading, in
    [Keep a Changelog](https://keepachangelog.com/en/1.1.0/) format.
  - `DETAILED_CHANGELOG.md` — a dated long-form entry (what / why / how /
    commands / verification / notes), newest first.

When you do document, document properly: record the exact commands run, the
reasoning behind decisions, errors hit and how they were resolved — enough
for someone to audit, reproduce, or roll back the change without re-deriving
it.

## Session log convention

Working sessions that change behavior or configuration write a log to
`logs/YYYY-MM-DD-<slug>.md` (session date + short kebab-case slug). Read-only
or purely conversational sessions don't need one. Existing entries are the
template — match their structure: symptom → diagnosis → change (with diff) →
commands → verification → notes. See `logs/README.md`.
<!-- aidc:core-logics:end -->
