#!/usr/bin/env bash
# License-conflict gate. Resolves the project's own declared license, builds a
# license inventory of third-party dependencies from a syft SPDX SBOM, and
# flags any dependency whose license conflicts with the project license per the
# compatibility matrix. Designed to run as early as possible: in the dev loop
# (`aidc licenses`), a pre-commit hook, and CI.
#
# The syft-SPDX + matrix check is the always-on, offline, deterministic gate.
# Optionally (AIDC_LICENSE_USE_VET=1) it also runs `vet` with a generated CEL
# license filter for OSV/Insights-backed enrichment (needs network).
#
# Env:
#   AIDC_LICENSE_MODE    'warn' (exit 0, report only) | 'fail' (exit 1 on conflict). Default 'warn'.
#   AIDC_SBOM_SRC        source to scan for the license inventory (default '.')
#   AIDC_SBOM_DIR        output/reuse dir (default './sbom')
#   AIDC_LICENSE_MATRIX  path to the compatibility matrix TSV
#                        (default: license-matrix.tsv next to this script)
#   AIDC_LICENSE_SBOM    reuse an existing SPDX-json instead of running syft
#   AIDC_PROJECT_LICENSE override the detected project license (SPDX id)
#   AIDC_LICENSE_USE_VET 1 => also run vet license enrichment (default 0)
#
# Outputs: $AIDC_SBOM_DIR/license-report.json
# Exit: 0 ok / warn, 1 conflict in fail mode, 2 tool-missing/usage.
set -euo pipefail

here="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=scripts/ci/aidc-lib-common.sh
. "$here/aidc-lib-common.sh"

sbom::require_tool jq

mode="${AIDC_LICENSE_MODE:-warn}"
case "$mode" in
  warn|fail) ;;
  *) sbom::die "AIDC_LICENSE_MODE must be 'warn' or 'fail' (got '$mode')" ;;
esac

src="${AIDC_SBOM_SRC:-.}"
out_dir="$(sbom::sbom_dir)"
matrix="${AIDC_LICENSE_MATRIX:-$here/license-matrix.tsv}"
report="$out_dir/license-report.json"

[[ -f "$matrix" ]] || sbom::die "license matrix not found: $matrix"

project_license="$(sbom::resolve_project_license "$src")"
if [[ -z "$project_license" ]]; then
  sbom::warn "could not determine the project's own license; only wildcard '*' matrix rules will apply"
fi

# --- obtain the dependency license inventory (SPDX json) --------------------
spdx=""
if [[ -n "${AIDC_LICENSE_SBOM:-}" ]]; then
  spdx="$AIDC_LICENSE_SBOM"
  [[ -f "$spdx" ]] || sbom::die "AIDC_LICENSE_SBOM points at a missing file: $spdx"
elif [[ -f "$out_dir/code.spdx.json" ]]; then
  spdx="$out_dir/code.spdx.json"
else
  sbom::require_tool syft "syft is baked into every aidc image; on other CI runners install it: https://github.com/anchore/syft"
  spdx="$(mktemp -t aidc-lic-spdx.XXXXXX)"
  trap 'rm -f "$spdx"' EXIT
  sbom::log "no SPDX SBOM found; cataloging '$src' for the license inventory"
  syft scan "dir:$src" -o "spdx-json=$spdx" >/dev/null
fi

# --- build the set of conflicting dep licenses for this project license -----
# Bash 3.2-safe: a newline-delimited string, membership tested with grep -Fxq.
conflict_set=""
while IFS=$'\t' read -r proj dep _rest; do
  # Skip comments and blank lines.
  case "$proj" in ''|'#'*) continue ;; esac
  [[ -n "$dep" ]] || continue
  if [[ "$proj" == "*" || "$proj" == "$project_license" ]]; then
    conflict_set="$conflict_set$dep"$'\n'
  fi
done < "$matrix"

is_conflicting() {
  local lic="$1"
  [[ -n "$lic" ]] || return 1
  printf '%s' "$conflict_set" | grep -Fxq -- "$lic"
}

# --- extract (name, version, license) rows from the SPDX SBOM ---------------
# Prefer licenseConcluded, fall back to licenseDeclared. Emit TSV; split simple
# SPDX expressions ("A OR B", "A AND B", parentheses) into atomic ids upstream
# is awkward in jq, so we split per-row in bash below.
dep_rows="$(jq -r '
  (.packages // [])[]
  | . as $p
  | (($p.licenseConcluded // "NOASSERTION") as $lc
     | (if $lc == "NOASSERTION" then ($p.licenseDeclared // "NOASSERTION") else $lc end)) as $lic
  | [ ($p.name // "?"), ($p.versionInfo // ""), $lic ] | @tsv
' "$spdx" 2>/dev/null || true)"

# --- evaluate conflicts -----------------------------------------------------
conflicts_json="[]"
conflict_count=0
if [[ -n "$dep_rows" ]]; then
  while IFS=$'\t' read -r name version lic; do
    [[ -n "$name" ]] || continue
    # Split a license expression into atomic ids: strip parentheses, split on
    # AND/OR/WITH, trim, then test each.
    normalized="$(printf '%s' "$lic" | tr '()' '  ' | sed -E 's/[[:space:]]+(OR|AND|WITH)[[:space:]]+/\n/g')"
    while IFS= read -r atom; do
      atom="${atom#"${atom%%[![:space:]]*}"}"
      atom="${atom%"${atom##*[![:space:]]}"}"
      [[ -n "$atom" ]] || continue
      if is_conflicting "$atom"; then
        entry="$(jq -n \
          --arg component "$name" \
          --arg version "$version" \
          --arg license "$atom" \
          '{component:$component, version:$version, license:$license, source:"matrix"}')"
        conflicts_json="$(printf '%s' "$conflicts_json" | jq --argjson e "$entry" '. + [$e]')"
        conflict_count=$((conflict_count + 1))
      fi
    done <<EOF
$normalized
EOF
  done <<EOF
$dep_rows
EOF
fi

# --- optional vet enrichment (opt-in; needs network) ------------------------
vet_ran=false
vet_flagged=false
if [[ "${AIDC_LICENSE_USE_VET:-0}" == "1" ]]; then
  if command -v vet >/dev/null 2>&1; then
    vet_ran=true
    # Build a CEL filter over the same conflict set: match any package carrying
    # a conflicting license. vet's license field is the SPDX id list `licenses`.
    cel_terms=""
    while IFS= read -r dep; do
      [[ -n "$dep" ]] || continue
      cel_terms="$cel_terms || licenses.exists(l, l == \"$dep\")"
    done <<EOF
$(printf '%s' "$conflict_set" | sort -u)
EOF
    cel_terms="${cel_terms# || }"
    if [[ -n "$cel_terms" ]]; then
      sbom::log "running vet license enrichment (network required)"
      if vet scan -D "$src" --filter "$cel_terms" --filter-fail >/dev/null 2>&1; then
        vet_flagged=false
      else
        # Non-zero => vet's --filter-fail matched a conflicting license (or vet
        # hit an infra error; enable only where network is available). Advisory.
        vet_flagged=true
      fi
    fi
  else
    sbom::warn "AIDC_LICENSE_USE_VET=1 but vet is not on PATH; skipping vet enrichment"
  fi
fi

# --- write the report -------------------------------------------------------
jq -n \
  --arg project_license "${project_license:-}" \
  --arg mode "$mode" \
  --arg spdx "$spdx" \
  --argjson conflicts "$conflicts_json" \
  --arg vet_ran "$vet_ran" \
  --arg vet_flagged "$vet_flagged" \
  '{
     project_license: $project_license,
     mode: $mode,
     sbom_source: $spdx,
     conflicts: $conflicts,
     conflict_count: ($conflicts | length),
     vet: {ran: ($vet_ran == "true"), flagged: ($vet_flagged == "true")}
   }' > "$report"

# --- report + exit ----------------------------------------------------------
total_flagged=$conflict_count
if [[ "$vet_flagged" == "true" ]]; then
  total_flagged=$((total_flagged + 1))
fi

if [[ "$total_flagged" -eq 0 ]]; then
  sbom::log "no license conflicts for project license '${project_license:-unknown}' (report: $report)"
  exit 0
fi

sbom::warn "found $conflict_count dependency license conflict(s) for project license '${project_license:-unknown}':"
jq -r '.conflicts[] | "  ! " + .component + " @ " + .version + " : " + .license' "$report" >&2 || true
[[ "$vet_flagged" == "true" ]] && sbom::warn "  ! vet license policy also flagged a conflict"
sbom::warn "full report: $report"

if [[ "$mode" == "fail" ]]; then
  sbom::err "license conflicts present and AIDC_LICENSE_MODE=fail"
  exit 1
fi
sbom::warn "AIDC_LICENSE_MODE=warn; not failing. Set AIDC_LICENSE_MODE=fail to gate."
exit 0
