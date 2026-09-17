#!/usr/bin/env bash
# Add a CHANGELOG.md entry by asking an AI coding harness to follow the
# repository's release-note rules.

set -euo pipefail

usage() {
  cat <<'EOF'
Usage: scripts/add-changelog.sh <version> [codex|claude|opencode]

Adds the supplied release version to CHANGELOG.md. The default harness is
codex.
EOF
}

validate_version() {
  local version="$1"

  if [[ ! "$version" =~ ^v[0-9]+$ ]]; then
    printf 'Version must be in the form v<number>, for example v256.\n' >&2
    exit 1
  fi
}

changelog_prompt() {
  local version="$1"

  printf '%s\n' "Use docs/changelog-rules.md to add the ${version} release entry to CHANGELOG.md. The release changes are committed: inspect them with git log from the last release tag to HEAD (or main as the rules require). Do not use Git reflogs or .git/logs paths. Complete the edit before replying; do not only describe the planned work. Make only the required changelog update."
}

run_codex() {
  local prompt="$1"

  codex exec \
    --approve-for-me \
    --model gpt-5.6-terra \
    -c 'model_reasoning_effort="medium"' \
    "$prompt"
}

run_claude() {
  local prompt="$1"

  claude --print --model claude-sonnet-5 "$prompt"
}

run_opencode() {
  local prompt="$1"

  opencode run "$prompt"
}

main() {
  if [[ $# -lt 1 || $# -gt 2 ]]; then
    usage >&2
    exit 1
  fi

  local version="$1"
  local harness="${2:-codex}"
  local repo_root
  local prompt

  validate_version "$version"
  repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
  cd "$repo_root"
  prompt="$(changelog_prompt "$version")"

  case "$harness" in
    codex)
      run_codex "$prompt"
      ;;
    claude)
      run_claude "$prompt"
      ;;
    opencode)
      run_opencode "$prompt"
      ;;
    *)
      printf 'Unknown harness: %s\n' "$harness" >&2
      usage >&2
      exit 1
      ;;
  esac
}

main "$@"
