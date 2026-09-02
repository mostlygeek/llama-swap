#!/usr/bin/env bash
# aidc scripts/ci shared helpers.
#
# Sourced by the sbom-*/license-* scripts. Deliberately standalone: it never
# sources lib/aidc.sh, so the whole scripts/ci/ tree runs on any bare CI runner
# (GitHub Actions, Jenkins, GitLab, ...) with only bash + jq + syft/vet on PATH.
#
# Bash 3.2-safe (macOS system bash): no associative arrays, no `mapfile`, and
# every array expansion is guarded for `set -u`.
#
# This file is not executed directly; callers `source` it. shellcheck: the
# functions here are consumed by the sourcing scripts.
# shellcheck shell=bash

# --- output -----------------------------------------------------------------

sbom::log()  { printf 'aidc-sbom: %s\n' "$*"; }
sbom::warn() { printf 'aidc-sbom: WARN: %s\n' "$*" >&2; }
sbom::err()  { printf 'aidc-sbom: ERROR: %s\n' "$*" >&2; }

# Exit codes: 0 ok, 1 policy violation (fail mode), 2 tool-missing/usage.
sbom::die() { sbom::err "$*"; exit 2; }

# --- tooling ----------------------------------------------------------------

# require_tool <name> [hint] — exit 2 with a clear message if missing.
sbom::require_tool() {
  local name="$1"
  local hint="${2:-}"
  if ! command -v "$name" >/dev/null 2>&1; then
    if [[ -n "$hint" ]]; then
      sbom::die "'$name' not found on PATH. $hint"
    fi
    sbom::die "'$name' not found on PATH."
  fi
}

# --- paths ------------------------------------------------------------------

# Resolve and create the SBOM output directory. Honors $AIDC_SBOM_DIR
# (default ./sbom). Echoes the resolved directory.
sbom::sbom_dir() {
  local dir="${AIDC_SBOM_DIR:-./sbom}"
  mkdir -p "$dir"
  printf '%s\n' "$dir"
}

# --- project-license resolution ---------------------------------------------

# Normalize a raw license string toward an SPDX-ish identifier. Best-effort:
# trims whitespace and maps a few common aliases. Unknown values pass through.
sbom::_normalize_license() {
  local raw="$1"
  # trim
  raw="${raw#"${raw%%[![:space:]]*}"}"
  raw="${raw%"${raw##*[![:space:]]}"}"
  case "$raw" in
    "Apache License 2.0"|"Apache-2"|"Apache 2.0") printf 'Apache-2.0\n' ;;
    "The MIT License"|"MIT License")              printf 'MIT\n' ;;
    "BSD"|"BSD License")                          printf 'BSD-3-Clause\n' ;;
    "GPLv3"|"GPL-3"|"GPL3")                        printf 'GPL-3.0-only\n' ;;
    "GPLv2"|"GPL-2"|"GPL2")                        printf 'GPL-2.0-only\n' ;;
    *)                                            printf '%s\n' "$raw" ;;
  esac
}

# Guess an SPDX id from the text of a LICENSE file. Conservative: only returns
# a value on a confident match, otherwise prints nothing.
sbom::_license_from_text() {
  local file="$1"
  [[ -f "$file" ]] || return 0
  # Read the first ~40 lines; that covers the identifying header of every
  # common license without slurping huge files.
  local head_text
  head_text="$(head -n 40 "$file" 2>/dev/null || true)"
  case "$head_text" in
    *"GNU AFFERO GENERAL PUBLIC LICENSE"*)    printf 'AGPL-3.0-only\n'; return 0 ;;
    *"GNU LESSER GENERAL PUBLIC LICENSE"*)    printf 'LGPL-3.0-only\n'; return 0 ;;
  esac
  case "$head_text" in
    *"GNU GENERAL PUBLIC LICENSE"*)
      case "$head_text" in
        *"Version 3"*) printf 'GPL-3.0-only\n'; return 0 ;;
        *"Version 2"*) printf 'GPL-2.0-only\n'; return 0 ;;
      esac
      ;;
  esac
  case "$head_text" in
    *"Apache License"*"Version 2.0"*) printf 'Apache-2.0\n'; return 0 ;;
    *"Mozilla Public License Version 2.0"*) printf 'MPL-2.0\n'; return 0 ;;
    *"Permission is hereby granted, free of charge"*) printf 'MIT\n'; return 0 ;;
    *"Redistribution and use in source and binary forms"*)
      case "$head_text" in
        *"Neither the name"*) printf 'BSD-3-Clause\n'; return 0 ;;
        *) printf 'BSD-2-Clause\n'; return 0 ;;
      esac
      ;;
    *"ISC License"*) printf 'ISC\n'; return 0 ;;
  esac
  return 0
}

# Read a manifest's declared license, if present. Uses jq for JSON manifests
# and grep for TOML. Prints the raw value (may be empty).
sbom::_license_from_manifest() {
  local dir="$1"
  local val=""
  if [[ -f "$dir/package.json" ]] && command -v jq >/dev/null 2>&1; then
    # .license may be a string or {type: "..."}.
    val="$(jq -r '(.license // empty) | if type=="object" then (.type // "") else . end' "$dir/package.json" 2>/dev/null || true)"
    [[ -n "$val" ]] && { printf '%s\n' "$val"; return 0; }
  fi
  if [[ -f "$dir/Cargo.toml" ]]; then
    val="$(grep -E '^[[:space:]]*license[[:space:]]*=' "$dir/Cargo.toml" 2>/dev/null | head -1 | sed -E 's/^[^=]*=[[:space:]]*"?([^"]*)"?.*/\1/' || true)"
    [[ -n "$val" ]] && { printf '%s\n' "$val"; return 0; }
  fi
  if [[ -f "$dir/pyproject.toml" ]]; then
    val="$(grep -E '^[[:space:]]*license[[:space:]]*=' "$dir/pyproject.toml" 2>/dev/null | head -1 | sed -E 's/.*"([^"]*)".*/\1/' || true)"
    [[ -n "$val" ]] && { printf '%s\n' "$val"; return 0; }
  fi
  if [[ -f "$dir/composer.json" ]] && command -v jq >/dev/null 2>&1; then
    val="$(jq -r 'if (.license|type)=="array" then .license[0] else (.license // empty) end' "$dir/composer.json" 2>/dev/null || true)"
    [[ -n "$val" ]] && { printf '%s\n' "$val"; return 0; }
  fi
  return 0
}

# Resolve the project's own declared license as an SPDX-ish id.
# Order: $AIDC_PROJECT_LICENSE override -> manifest license field -> LICENSE
# file text heuristics. Prints the id, or empty if it cannot be determined.
# Arg 1: project root (default '.').
sbom::resolve_project_license() {
  local root="${1:-.}"
  if [[ -n "${AIDC_PROJECT_LICENSE:-}" ]]; then
    sbom::_normalize_license "$AIDC_PROJECT_LICENSE"
    return 0
  fi
  local val
  val="$(sbom::_license_from_manifest "$root")"
  if [[ -n "$val" ]]; then
    sbom::_normalize_license "$val"
    return 0
  fi
  local lf
  for lf in "$root/LICENSE" "$root/LICENSE.txt" "$root/LICENSE.md" "$root/COPYING"; do
    if [[ -f "$lf" ]]; then
      val="$(sbom::_license_from_text "$lf")"
      if [[ -n "$val" ]]; then
        printf '%s\n' "$val"
        return 0
      fi
    fi
  done
  return 0
}
