#!/usr/bin/env bash
# Generate a build-time SBOM in BOTH CycloneDX and SPDX JSON from a built
# container image. No-op (with a clear notice, exit 0) when AIDC_IMAGE_REF is
# unset — i.e. the project ships no Docker setup, so there is nothing to scan.
#
# Env:
#   AIDC_IMAGE_REF  image ref to scan (e.g. myapp:latest). Empty => skip.
#   AIDC_SBOM_DIR   output directory  (default './sbom')
#
# Outputs: $AIDC_SBOM_DIR/image.cdx.json, $AIDC_SBOM_DIR/image.spdx.json
# Exit: 0 ok/skipped, 2 tool-missing/usage.
set -euo pipefail

here="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=scripts/ci/aidc-lib-common.sh
. "$here/aidc-lib-common.sh"

image_ref="${AIDC_IMAGE_REF:-}"
if [[ -z "$image_ref" ]]; then
  sbom::log "AIDC_IMAGE_REF not set; skipping build-time SBOM (no image to scan)"
  exit 0
fi

sbom::require_tool syft "syft is baked into every aidc image; on other CI runners install it: https://github.com/anchore/syft"

out_dir="$(sbom::sbom_dir)"
cdx="$out_dir/image.cdx.json"
spdx="$out_dir/image.spdx.json"

sbom::log "cataloging image '$image_ref' -> $cdx , $spdx"
syft scan "$image_ref" \
  -o "cyclonedx-json=$cdx" \
  -o "spdx-json=$spdx"

sbom::log "build-time SBOM written (CycloneDX + SPDX)"
