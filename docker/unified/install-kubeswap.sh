#!/bin/bash
# Install kubeswap - build from llama-swap source
# Usage: ./install-kubeswap.sh [version]
#   version: full commit hash, release version (e.g. "170"/"v170") or "latest" (default)
#
# kubeswap is not part of the llama-swap release archives so it is compiled
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
        # No release points at this commit (e.g. a branch head that has
        # never shipped): build the commit itself. Falling back to latest
        # would silently change the revision, and the latest release may
        # not contain cmd/kubeswap at all.
        echo "No release tag found for commit ${VERSION:0:7}; building the commit directly"
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

# A raw commit hash checks out as-is; a release version needs its tag prefix.
if echo "${VERSION}" | grep -qE '^[0-9a-f]{40}$'; then
    REF="${VERSION}"
else
    REF="v${VERSION}"
fi

echo "=== Cloning ${REPO} @ ${REF} ==="
git clone --filter=blob:none --no-checkout "https://github.com/${REPO}.git" "${SRC}"
git -C "${SRC}" checkout --detach "${REF}"

if [ ! -d "${SRC}/cmd/kubeswap" ]; then
    echo "FATAL: ${REF} has no cmd/kubeswap; pin LS_VERSION to a release that includes it" >&2
    exit 1
fi

echo "=== Building kubeswap ==="
cd "${SRC}"
CGO_ENABLED=0 go build -trimpath \
    -ldflags "-X main.version=${VERSION} -X main.buildTime=$(date -u +%Y-%m-%dT%H:%M:%SZ)" \
    -o /install/bin/kubeswap ./cmd/kubeswap

# Validate
if [ ! -x "/install/bin/kubeswap" ]; then
    echo "FATAL: kubeswap binary not found or not executable" >&2
    ls -la /install/bin/ >&2
    exit 1
fi

echo "=== kubeswap (${REF}) installed ==="
ls -la /install/bin/kubeswap
