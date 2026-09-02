#!/usr/bin/env bash
# Generate a code-level SBOM in BOTH CycloneDX and SPDX JSON from the repo
# source tree and dependency manifests, using a single syft catalog so the two
# formats stay consistent.
#
# Env:
#   AIDC_SBOM_SRC   source to scan            (default '.')
#   AIDC_SBOM_DIR   output directory          (default './sbom')
#
# Outputs: $AIDC_SBOM_DIR/code.cdx.json, $AIDC_SBOM_DIR/code.spdx.json
# Exit: 0 ok, 2 tool-missing/usage.
set -euo pipefail

here="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=scripts/ci/aidc-lib-common.sh
. "$here/aidc-lib-common.sh"

sbom::require_tool syft "syft is baked into every aidc image; on other CI runners install it: https://github.com/anchore/syft"

src="${AIDC_SBOM_SRC:-.}"
out_dir="$(sbom::sbom_dir)"
cdx="$out_dir/code.cdx.json"
spdx="$out_dir/code.spdx.json"

sbom::log "cataloging source '$src' -> $cdx , $spdx"
# One scan, two output formats — keeps the component sets identical.
syft scan "dir:$src" \
  -o "cyclonedx-json=$cdx" \
  -o "spdx-json=$spdx"

sbom::log "code SBOM written (CycloneDX + SPDX)"
