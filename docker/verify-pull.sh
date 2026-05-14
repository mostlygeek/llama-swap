#!/bin/bash

# Walk the OCI index of every non-versioned tag for one backend and
# confirm each declared platform's child manifest is reachable. Uses
# `docker buildx imagetools inspect --raw` so each invocation finishes
# in seconds without pulling layers.
#
# Catches the failure mode where cleanup deletes a tagged manifest
# list's per-platform children, leaving a dangling index — `docker pull`
# then 404s on the referenced digest even though the tag still exists.
#
# Inputs (env):
#   REPO       — ghcr.io/<owner>/<repo>
#   TAG        — backend tag (cpu, intel, cuda, ...)
#   PLATFORMS  — comma-separated list of platforms the index must serve
#                (e.g. linux/amd64,linux/arm64)
#
# Exits non-zero on any missing platform or unreachable child manifest.

set -uo pipefail

: "${REPO:?REPO is required}"
: "${TAG:?TAG is required}"
: "${PLATFORMS:?PLATFORMS is required}"

IFS=',' read -ra want_archs <<< "${PLATFORMS}"
rc=0

for suffix in "" "-non-root"; do
    full="${REPO}:${TAG}${suffix}"
    echo "::group::${full}"

    if ! raw=$(docker buildx imagetools inspect --raw "${full}" 2>&1); then
        echo "FAIL: top-level manifest unreachable"
        echo "${raw}"
        rc=1
        echo "::endgroup::"
        continue
    fi

    if echo "${raw}" | jq -e '.manifests' >/dev/null 2>&1; then
        # OCI index / Docker manifest list — extract real platform children
        # (excludes `unknown/unknown` attestation manifests).
        present=$(echo "${raw}" | jq -r '
            .manifests[]
            | select(.platform.architecture != "unknown")
            | "\(.platform.os)/\(.platform.architecture)"
        ' | sort -u)
        digests=$(echo "${raw}" | jq -r '
            .manifests[]
            | select(.platform.architecture != "unknown")
            | .digest
        ')
    else
        # Single manifest. build-container.sh only emits amd64 here.
        present="linux/amd64"
        digests=""
    fi

    echo "Present: $(echo "${present}" | tr '\n' ' ')"

    for want in "${want_archs[@]}"; do
        if ! grep -qx -- "${want}" <<< "${present}"; then
            echo "FAIL: missing expected platform ${want}"
            rc=1
        fi
    done

    # Walk each child digest. If cleanup ever deletes a per-platform
    # child of a tagged index, this fetch 404s — the exact symptom
    # users hit with `docker pull :cpu`.
    base="${full%:*}"
    for d in ${digests}; do
        if ! docker buildx imagetools inspect --raw "${base}@${d}" >/dev/null 2>&1; then
            echo "FAIL: child manifest ${d} unreachable (dangling index)"
            rc=1
        fi
    done

    echo "::endgroup::"
done

exit $rc
