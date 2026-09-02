#!/usr/bin/env bash
# Diff the code-level SBOM against the build-time SBOM and highlight what
# changed: components added in the image, removed from the image, or present in
# both at a different version. Compares CycloneDX component sets by name (and
# group) so a version bump is reported as a change rather than add+remove.
#
# Env:
#   AIDC_SBOM_DIR   directory holding code.cdx.json / image.cdx.json (default './sbom')
#
# Outputs: $AIDC_SBOM_DIR/diff.json + a human-readable summary on stdout.
# Exit: 0 ok/skipped, 2 missing input/usage.
set -euo pipefail

here="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=scripts/ci/aidc-lib-common.sh
. "$here/aidc-lib-common.sh"

sbom::require_tool jq

out_dir="$(sbom::sbom_dir)"
code_cdx="$out_dir/code.cdx.json"
image_cdx="$out_dir/image.cdx.json"
diff_json="$out_dir/diff.json"

[[ -f "$code_cdx" ]] || sbom::die "missing $code_cdx (run aidc-sbom-code.sh first)"

if [[ ! -f "$image_cdx" ]]; then
  sbom::log "no build-time SBOM ($image_cdx absent); skipping diff"
  exit 0
fi

# Key each component by "group/name" (version-independent) -> version so a
# version change is detected rather than looking like an unrelated add+remove.
jq -n \
  --slurpfile code "$code_cdx" \
  --slurpfile image "$image_cdx" '
    def keyed:
      reduce ((.components // [])[]) as $c
        ({};
         ((if ($c.group // "") == "" then "" else ($c.group + "/") end) + ($c.name // "?")) as $k
         | .[$k] = ($c.version // ""));
    ($code[0] | keyed) as $a
    | ($image[0] | keyed) as $b
    | {
        added:   [ $b | to_entries[] | select($a[.key] == null)                              | {name: .key, version: .value} ],
        removed: [ $a | to_entries[] | select($b[.key] == null)                              | {name: .key, version: .value} ],
        changed: [ $a | to_entries[] | select($b[.key] != null and $b[.key] != .value)       | {name: .key, code_version: .value, image_version: $b[.key]} ]
      }
    | .summary = {added: (.added|length), removed: (.removed|length), changed: (.changed|length)}
  ' >"$diff_json"

added=$(jq '.summary.added' "$diff_json")
removed=$(jq '.summary.removed' "$diff_json")
changed=$(jq '.summary.changed' "$diff_json")

sbom::log "code-vs-build SBOM diff: +${added} added, -${removed} removed, ~${changed} changed"
jq -r '
  (.added[]   | "  + " + .name + " @ " + .version),
  (.removed[] | "  - " + .name + " @ " + .version),
  (.changed[] | "  ~ " + .name + " : " + .code_version + " -> " + .image_version)
' "$diff_json" || true

sbom::log "diff written to $diff_json"
