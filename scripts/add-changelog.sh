#!/usr/bin/env bash
# Add a CHANGELOG.md entry by asking an AI coding harness to follow the
# repository's release-note rules.

set -euo pipefail

usage() {
  cat <<'EOF'
Usage: scripts/add-changelog.sh [harness] [version]
       scripts/add-changelog.sh [version]

Adds a release version to CHANGELOG.md. The harness is codex, claude or
opencode and defaults to claude. When version is omitted it is the highest
v<number> tag plus one.
EOF
}

validate_version() {
  local version="$1"

  if [[ ! "$version" =~ ^v[0-9]+$ ]]; then
    printf 'Version must be in the form v<number>, for example v256.\n' >&2
    exit 1
  fi
}

next_version() {
  local highest

  highest="$(git tag --sort=-v:refname | grep -E '^v[0-9]+$' | head -n 1 || true)"
  printf 'v%d\n' "$(( ${highest#v} + 1 ))"
}

check_changelog() {
  local version="$1"

  if ! git diff --quiet -- CHANGELOG.md; then
    printf 'CHANGELOG.md has unstaged changes. Commit, stage or stash them first.\n' >&2
    exit 1
  fi

  if grep -qE "^## ${version}( |$)" CHANGELOG.md; then
    printf 'CHANGELOG.md already has an entry for %s.\n' "$version" >&2
    exit 1
  fi
}

changelog_prompt() {
  local version="$1"

  printf '%s\n' "Use docs/changelog-rules.md to add the ${version} release entry to CHANGELOG.md. The release changes are committed: inspect them with git log from the last release tag to HEAD (or main as the rules require). Read each commit's full message and use gh pr view to read each pull request's description and author. Do not use Git reflogs or .git/logs paths. Complete the edit before replying; do not only describe the planned work. Make only the required changelog update."
}

run_codex() {
  local prompt="$1"

  codex exec \
    --approve-for-me \
    --model gpt-6-sol \
    -c 'model_reasoning_effort="medium"' \
    "$prompt"
}

run_claude() {
  local prompt="$1"

  claude --print \
    --model claude-opus-5-5 \
    --permission-mode acceptEdits \
    --allowedTools 'Read' 'Edit' 'Bash(git log:*)' 'Bash(git tag:*)' \
      'Bash(git show:*)' 'Bash(gh pr view:*)' \
    -- "$prompt" </dev/null
}

run_opencode() {
  local prompt="$1"

  opencode run "$prompt"
}

main() {
  if [[ $# -gt 2 ]]; then
    usage >&2
    exit 1
  fi

  local harness="claude"
  local version=""

  # The harness is optional: a leading v<number> is taken as the version.
  if [[ $# -gt 0 && "$1" =~ ^v[0-9]+$ ]]; then
    version="$1"
  else
    harness="${1:-claude}"
    version="${2:-}"
  fi
  local repo_root
  local prompt

  repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
  cd "$repo_root"
  if [[ -z "$version" ]]; then
    version="$(next_version)"
  fi
  validate_version "$version"
  check_changelog "$version"
  printf 'Adding changelog entry for %s using %s\n' "$version" "$harness"
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
