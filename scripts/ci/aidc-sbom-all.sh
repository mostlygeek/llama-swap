#!/usr/bin/env bash
# One entry point any CI (GitHub Actions, Jenkins, GitLab, ...) can call:
#   1. code-level SBOM (CycloneDX + SPDX)
#   2. build-time SBOM from AIDC_IMAGE_REF, if set (CycloneDX + SPDX)
#   3. code-vs-build diff, if a build-time SBOM was produced
#   4. license-conflict check
#
# All configuration is via env vars (see the individual scripts). This script
# only orchestrates and aggregates the exit code, so CI configs stay thin.
#
# Env (in addition to those the sub-scripts read):
#   AIDC_IMAGE_REF     image to scan for the build-time SBOM (empty => skip)
#   AIDC_LICENSE_MODE  'warn' (default) | 'fail'
#
# Exit: 0 ok, 1 license conflict in fail mode, 2 tool-missing/usage.
set -euo pipefail

here="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=scripts/ci/aidc-lib-common.sh
. "$here/aidc-lib-common.sh"

sbom::log "=== code-level SBOM ==="
"$here/aidc-sbom-code.sh"

sbom::log "=== build-time SBOM ==="
"$here/aidc-sbom-image.sh"

sbom::log "=== code-vs-build diff ==="
"$here/aidc-sbom-diff.sh"

sbom::log "=== license check (mode: ${AIDC_LICENSE_MODE:-warn}) ==="
# Reuse the code SPDX SBOM just generated instead of re-cataloging.
out_dir="$(sbom::sbom_dir)"
AIDC_LICENSE_SBOM="${AIDC_LICENSE_SBOM:-$out_dir/code.spdx.json}" "$here/aidc-license-check.sh"

sbom::log "SBOM pipeline complete; artifacts in $out_dir"
