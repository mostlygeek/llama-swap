#!/bin/bash
# Install vllm-wrapper - build from llama-swap source
# Usage: ./install-vllm-wrapper.sh [version]
#   version: full commit hash, release version (e.g. "170"/"v170") or "latest" (default)
#
# vllm-wrapper is not part of the llama-swap release archives so it is compiled
# from the same source revision the llama-swap binary was released from.
set -e

# shellcheck source=lib-release.sh
source "$(dirname "$0")/lib-release.sh"

VERSION="${1:-latest}"
REPO="mostlygeek/llama-swap"
SRC=/src/llama-swap

mkdir -p /install/bin

# If a full commit hash is given, find the release tag that points to it. This
# mirrors install-llama-swap.sh so both binaries come from the same revision.
if echo "${VERSION}" | grep -qE '^[0-9a-f]{40}$'; then
    echo "=== Resolving commit ${VERSION:0:7} to release tag ==="
    TAG=$(git ls-remote --tags "https://github.com/${REPO}.git" 2>/dev/null \
        | grep "^${VERSION}" | sed 's|.*refs/tags/||' | grep -v '\^{}' | head -1)
    if [ -n "${TAG}" ]; then
        echo "Resolved to tag: ${TAG}"
        VERSION="${TAG#v}"
    else
        # No release points at this commit (e.g. a branch head that has
        # never shipped): build the commit itself. Falling back to latest
        # would silently change the revision and depends on the GitHub API,
        # which is rate limited for unauthenticated builders.
        echo "No release tag found for commit ${VERSION:0:7}; building the commit directly"
    fi
fi

# Strip leading 'v' prefix so both "198" and "v198" work
VERSION="${VERSION#v}"

# Resolve "latest" to the tag of the most recent release
if [ "$VERSION" = "latest" ]; then
    echo "=== Resolving latest llama-swap release ==="
    VERSION=$(resolve_latest_version "${REPO}")
    if [ -z "$VERSION" ]; then
        echo "FATAL: Could not determine latest release version" >&2
        exit 1
    fi
    echo "Latest version: ${VERSION}"
fi

# A raw commit hash checks out as-is; a release version needs its tag prefix.
if echo "${VERSION}" | grep -qE '^[0-9a-f]{40}$'; then
    REF="${VERSION}"
else
    REF="v${VERSION}"
fi

echo "=== Cloning ${REPO} @ ${REF} ==="
git clone --filter=blob:none --no-checkout "https://github.com/${REPO}.git" "${SRC}"
git -C "${SRC}" checkout --detach "${REF}"

if [ ! -d "${SRC}/cmd/vllm-wrapper" ]; then
    echo "FATAL: ${REF} has no cmd/vllm-wrapper (added in v243); pin LS_VERSION to v243 or newer" >&2
    exit 1
fi

echo "=== Building vllm-wrapper ==="
cd "${SRC}"
CGO_ENABLED=0 go build -trimpath -o /install/bin/vllm-wrapper ./cmd/vllm-wrapper

# Validate
if [ ! -x "/install/bin/vllm-wrapper" ]; then
    echo "FATAL: vllm-wrapper binary not found or not executable" >&2
    ls -la /install/bin/ >&2
    exit 1
fi

echo "=== vllm-wrapper (${REF}) installed ==="
ls -la /install/bin/vllm-wrapper
