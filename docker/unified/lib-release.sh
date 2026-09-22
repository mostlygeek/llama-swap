#!/bin/bash
# Shared helpers for the llama-swap installer scripts.
#
# Sourced, not executed: `source /build/lib-release.sh`.

# resolve_latest_version <owner/repo>
#
# Prints the newest release version with the leading "v" stripped (e.g. "257").
#
# The releases API is the source of truth, but api.github.com rate limits
# unauthenticated callers by IP and Docker builds on shared CI runners hit that
# limit regularly (HTTP 403). A build stage has no credentials to authenticate
# with, so when the API refuses, fall back to the highest v<N> tag on the git
# remote: that is a different endpoint, it is not rate limited the same way,
# and releases here are always tagged v<N> with N increasing, so the highest
# tag is the latest release.
resolve_latest_version() {
    local repo="$1"
    local version=""

    version=$(curl -fsSL "https://api.github.com/repos/${repo}/releases/latest" 2>/dev/null \
        | grep '"tag_name"' | head -1 | cut -d'"' -f4 | sed 's/^v//') || true

    if [ -z "${version}" ]; then
        echo "GitHub releases API unavailable, falling back to git tags" >&2
        version=$(git ls-remote --tags --refs "https://github.com/${repo}.git" 2>/dev/null \
            | sed 's|.*refs/tags/||' | grep -E '^v[0-9]+$' | sort -V | tail -1 \
            | sed 's/^v//') || true
    fi

    echo "${version}"
}
