#!/bin/bash
# Install llama-swap - download latest release binary from GitHub
# Usage: ./install-llama-swap.sh [version]
#   version: release version number (e.g., "170") or "latest" (default)
set -e

VERSION="${1:-latest}"
REPO="mostlygeek/llama-swap"

mkdir -p /install/bin

# If a full commit hash is given, find the release tag that points to it
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

# Resolve "latest" to actual version number
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

# Download and extract
URL="https://github.com/${REPO}/releases/download/v${VERSION}/llama-swap_${VERSION}_linux_amd64.tar.gz"
echo "=== Downloading llama-swap v${VERSION} ==="
echo "URL: $URL"
curl -fSL -o /tmp/llama-swap.tar.gz "$URL"
tar -xzf /tmp/llama-swap.tar.gz -C /install/bin/
rm /tmp/llama-swap.tar.gz

# Validate
if [ ! -x "/install/bin/llama-swap" ]; then
    echo "FATAL: llama-swap binary not found or not executable" >&2
    ls -la /install/bin/ >&2
    exit 1
fi

echo "$VERSION" > /install/llama-swap-version

echo "=== llama-swap v${VERSION} installed ==="
ls -la /install/bin/llama-swap
