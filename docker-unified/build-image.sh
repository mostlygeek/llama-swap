#!/bin/bash
#
# Build script for unified CUDA container with commit hash pinning
#
# Usage:
#   ./build-image.sh                               # Build with auto-detected versions
#   ./build-image.sh --no-cache                    # Build without cache
#   LLAMA_COMMIT_HASH=abc123 ./build-image.sh      # Override llama.cpp commit
#   WHISPER_COMMIT_HASH=def456 ./build-image.sh    # Override whisper.cpp commit
#   SD_COMMIT_HASH=ghi789 ./build-image.sh         # Override stable-diffusion.cpp commit
#   LS_VERSION=170 ./build-image.sh                # Override llama-swap version
#

set -euo pipefail

NO_CACHE=false

for arg in "$@"; do
    case $arg in
        --no-cache)
            NO_CACHE=true
            ;;
        --help|-h)
            echo "Usage: ./build-image.sh [--no-cache]"
            echo ""
            echo "Environment variables:"
            echo "  DOCKER_IMAGE_TAG     Set custom image tag (default: llama-swap:unified)"
            echo "  LLAMA_COMMIT_HASH    Override llama.cpp commit hash"
            echo "  WHISPER_COMMIT_HASH  Override whisper.cpp commit hash"
            echo "  SD_COMMIT_HASH       Override stable-diffusion.cpp commit hash"
            echo "  LS_VERSION           Override llama-swap version (e.g., '170' or 'latest')"
            exit 0
            ;;
    esac
done

DOCKER_IMAGE_TAG="${DOCKER_IMAGE_TAG:-llama-swap:unified}"

# Git repository URLs
LLAMA_REPO="https://github.com/ggml-org/llama.cpp.git"
WHISPER_REPO="https://github.com/ggml-org/whisper.cpp.git"
SD_REPO="https://github.com/leejet/stable-diffusion.cpp.git"

get_latest_commit() {
    local repo_url="$1"
    local branch="${2:-master}"
    git ls-remote --heads "${repo_url}" "${branch}" 2>/dev/null | head -1 | cut -f1
}

get_default_branch() {
    local repo_url="$1"
    if git ls-remote --heads "${repo_url}" master &>/dev/null; then
        echo "master"
    elif git ls-remote --heads "${repo_url}" main &>/dev/null; then
        echo "main"
    else
        echo "master"
    fi
}

echo "=========================================="
echo "llama-swap Unified CUDA Build"
echo "=========================================="
echo ""

# Resolve llama.cpp commit
if [[ -n "${LLAMA_COMMIT_HASH:-}" ]]; then
    LLAMA_HASH="${LLAMA_COMMIT_HASH}"
    echo "llama.cpp: Using provided commit: ${LLAMA_HASH}"
else
    LLAMA_BRANCH=$(get_default_branch "${LLAMA_REPO}")
    LLAMA_HASH=$(get_latest_commit "${LLAMA_REPO}" "${LLAMA_BRANCH}")
    if [[ -z "${LLAMA_HASH}" ]]; then
        echo "ERROR: Could not determine latest commit for llama.cpp" >&2
        exit 1
    fi
    echo "llama.cpp: Auto-detected latest commit (${LLAMA_BRANCH}): ${LLAMA_HASH}"
fi

# Resolve whisper.cpp commit
if [[ -n "${WHISPER_COMMIT_HASH:-}" ]]; then
    WHISPER_HASH="${WHISPER_COMMIT_HASH}"
    echo "whisper.cpp: Using provided commit: ${WHISPER_HASH}"
else
    WHISPER_BRANCH=$(get_default_branch "${WHISPER_REPO}")
    WHISPER_HASH=$(get_latest_commit "${WHISPER_REPO}" "${WHISPER_BRANCH}")
    if [[ -z "${WHISPER_HASH}" ]]; then
        echo "ERROR: Could not determine latest commit for whisper.cpp" >&2
        exit 1
    fi
    echo "whisper.cpp: Auto-detected latest commit (${WHISPER_BRANCH}): ${WHISPER_HASH}"
fi

# Resolve stable-diffusion.cpp commit
if [[ -n "${SD_COMMIT_HASH:-}" ]]; then
    SD_HASH="${SD_COMMIT_HASH}"
    echo "stable-diffusion.cpp: Using provided commit: ${SD_HASH}"
else
    SD_BRANCH=$(get_default_branch "${SD_REPO}")
    SD_HASH=$(get_latest_commit "${SD_REPO}" "${SD_BRANCH}")
    if [[ -z "${SD_HASH}" ]]; then
        echo "ERROR: Could not determine latest commit for stable-diffusion.cpp" >&2
        exit 1
    fi
    echo "stable-diffusion.cpp: Auto-detected latest commit (${SD_BRANCH}): ${SD_HASH}"
fi

# Resolve llama-swap version
LS_VER="${LS_VERSION:-latest}"
echo "llama-swap: ${LS_VER}"

echo ""
echo "=========================================="
echo "Starting Docker build..."
echo "=========================================="
echo ""

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

BUILD_ARGS=(
    --build-arg "LLAMA_COMMIT_HASH=${LLAMA_HASH}"
    --build-arg "WHISPER_COMMIT_HASH=${WHISPER_HASH}"
    --build-arg "SD_COMMIT_HASH=${SD_HASH}"
    --build-arg "LS_VERSION=${LS_VER}"
    -t "${DOCKER_IMAGE_TAG}"
    -f "${SCRIPT_DIR}/Dockerfile"
)

if [[ "$NO_CACHE" == true ]]; then
    BUILD_ARGS+=(--no-cache)
    echo "Note: Building without cache"
fi

# Use docker buildx with max-parallelism=1 to prevent OOM during CUDA compilation
BUILDER_NAME="llama-swap-unified-builder"

if ! docker buildx inspect "$BUILDER_NAME" >/dev/null 2>&1; then
    echo "Creating custom buildx builder with max-parallelism=1..."

    cat > "${SCRIPT_DIR}/buildkitd.toml" << 'BUILDKIT_EOF'
[worker.oci]
  max-parallelism = 1
BUILDKIT_EOF

    docker buildx create --name "$BUILDER_NAME" \
        --driver docker-container \
        --buildkitd-config "${SCRIPT_DIR}/buildkitd.toml" \
        --use
else
    docker buildx use "$BUILDER_NAME"
fi

echo "Building with sequential stages (one at a time), each using all CPU cores..."
echo "Using builder: $BUILDER_NAME"

docker buildx build --builder "$BUILDER_NAME" --load "${BUILD_ARGS[@]}" "${SCRIPT_DIR}"

echo ""
echo "=========================================="
echo "Verifying build artifacts..."
echo "=========================================="
echo ""

MISSING_BINARIES=()
for binary in llama-server llama-cli whisper-server whisper-cli sd-server sd-cli llama-swap; do
    if ! docker run --rm "${DOCKER_IMAGE_TAG}" which "${binary}" >/dev/null 2>&1; then
        MISSING_BINARIES+=("${binary}")
    fi
done

if [[ ${#MISSING_BINARIES[@]} -gt 0 ]]; then
    echo "ERROR: Build succeeded but the following binaries are missing:"
    for binary in "${MISSING_BINARIES[@]}"; do
        echo "  - ${binary}"
    done
    echo ""
    echo "Try running with --no-cache flag:"
    echo "  ./build-image.sh --no-cache"
    exit 1
fi

echo "All expected binaries verified: llama-server, llama-cli, whisper-server, whisper-cli, sd-server, sd-cli, llama-swap"

echo ""
echo "=========================================="
echo "Build complete!"
echo "=========================================="
echo ""
echo "Image tag: ${DOCKER_IMAGE_TAG}"
echo ""
echo "Built with:"
echo "  llama.cpp:           ${LLAMA_HASH}"
echo "  whisper.cpp:         ${WHISPER_HASH}"
echo "  stable-diffusion.cpp: ${SD_HASH}"
echo "  llama-swap:          $(docker run --rm "${DOCKER_IMAGE_TAG}" cat /versions.txt | grep llama-swap | cut -d' ' -f2-)"
echo ""
echo "Run with:"
echo "  docker run -it --rm --gpus all ${DOCKER_IMAGE_TAG}"
