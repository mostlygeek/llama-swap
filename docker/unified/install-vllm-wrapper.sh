#!/bin/bash
# Install vllm-wrapper - build from llama-swap source
# Usage: ./install-vllm-wrapper.sh [version]
#   version: full commit hash, release version (e.g. "170"/"v170") or "latest" (default)
#
# vllm-wrapper is not part of the llama-swap release archives so it is compiled
# from the same source revision the llama-swap binary was released from.
set -e

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
        echo "No release tag found for commit ${VERSION:0:7}, using latest"
        VERSION="latest"
    fi
fi

# Strip leading 'v' prefix so both "198" and "v198" work
VERSION="${VERSION#v}"

# Resolve "latest" to the tag of the most recent release
if [ "$VERSION" = "latest" ]; then
    echo "=== Resolving latest llama-swap release ==="
    VERSION=$(curl -fsSL "https://api.github.com/repos/${REPO}/releases/latest" \
        | grep '"tag_name"' | head -1 | cut -d'"' -f4 | sed 's/^v//')
    if [ -z "$VERSION" ]; then
        echo "FATAL: Could not determine latest release version" >&2
        exit 1
    fi
    echo "Latest version: ${VERSION}"
fi

REF="v${VERSION}"

echo "=== Cloning ${REPO} @ ${REF} ==="
git clone --filter=blob:none --no-checkout "https://github.com/${REPO}.git" "${SRC}"
git -C "${SRC}" checkout --detach "${REF}"

if [ ! -d "${SRC}/cmd/vllm-wrapper" ]; then
    echo "FATAL: ${REF} has no cmd/vllm-wrapper (added in v243), pin LS_VERSION to v243 or newer" >&2
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
