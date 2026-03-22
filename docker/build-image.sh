#!/bin/bash
#
# Build script for llama-swap-docker with commit hash pinning
#
# Usage:
#   ./build-image.sh --cuda                    # Build CUDA image
#   ./build-image.sh --vulkan                  # Build Vulkan image
#   ./build-image.sh --cuda --no-cache         # Build CUDA image without cache
#   LLAMA_COMMIT_HASH=abc123 ./build-image.sh --cuda      # Override llama.cpp commit
#   LLAMA_COMMIT_HASH=b8429 ./build-image.sh --vulkan    # Override llama.cpp release tag (vulkan uses prebuilt binaries)
#   WHISPER_COMMIT_HASH=def456 ./build-image.sh --vulkan  # Override whisper.cpp commit
#   SD_COMMIT_HASH=ghi789 ./build-image.sh --cuda        # Override stable-diffusion.cpp commit
#
# Features:
#   - Auto-detects latest commit hashes from git repos
#   - Builds llama-swap from local source code
#   - Allows environment variable overrides for reproducible builds
#   - Cache-friendly: changing commit hash busts cache appropriately
#   - Supports both CUDA and Vulkan backends (requires explicit flag)
#

set -euo pipefail

# Parse command line arguments
BACKEND=""
NO_CACHE=false

if [[ $# -eq 0 ]]; then
    echo "Error: No backend specified. Please use --cuda or --vulkan."
    echo ""
    echo "Usage: ./build-image.sh --cuda|--vulkan [--no-cache]"
    echo ""
    echo "Options:"
    echo "  --cuda      Build CUDA image (NVIDIA GPUs)"
    echo "  --vulkan    Build Vulkan image (AMD GPUs and compatible hardware)"
    echo "  --no-cache  Force rebuild without using Docker cache"
    echo "  --help, -h  Show this help message"
    echo ""
    echo "Environment variables:"
    echo "  DOCKER_IMAGE_TAG     Set custom image tag (default: llama-swap:cuda or llama-swap:vulkan)"
    echo "  LLAMA_COMMIT_HASH    Override llama.cpp commit hash"
    echo "  WHISPER_COMMIT_HASH  Override whisper.cpp commit hash"
    echo "  SD_COMMIT_HASH       Override stable-diffusion.cpp commit hash"
    exit 1
fi

for arg in "$@"; do
    case $arg in
        --cuda)
            BACKEND="cuda"
            ;;
        --vulkan)
            BACKEND="vulkan"
            ;;
        --no-cache)
            NO_CACHE=true
            ;;
        --help|-h)
            echo "Usage: ./build-image.sh --cuda|--vulkan [--no-cache]"
            echo ""
            echo "Options:"
            echo "  --cuda      Build CUDA image (NVIDIA GPUs)"
            echo "  --vulkan    Build Vulkan image (AMD GPUs and compatible hardware)"
            echo "  --no-cache  Force rebuild without using Docker cache"
            echo "  --help, -h  Show this help message"
            echo ""
            echo "Environment variables:"
            echo "  DOCKER_IMAGE_TAG     Set custom image tag (default: llama-swap:cuda or llama-swap:vulkan)"
            echo "  LLAMA_COMMIT_HASH    Override llama.cpp commit hash"
            echo "  WHISPER_COMMIT_HASH  Override whisper.cpp commit hash"
            echo "  SD_COMMIT_HASH       Override stable-diffusion.cpp commit hash"
            exit 0
            ;;
    esac
done

# Validate backend selection
if [[ -z "$BACKEND" ]]; then
    echo "Error: No backend specified. Please use --cuda or --vulkan."
    exit 1
fi

# Configuration
if [[ -n "${DOCKER_IMAGE_TAG:-}" ]]; then
    # User provided a custom tag, use it as-is
    :
elif [[ "$BACKEND" == "vulkan" ]]; then
    DOCKER_IMAGE_TAG="llama-swap:vulkan"
else
    DOCKER_IMAGE_TAG="llama-swap:cuda"
fi
DOCKER_BUILDKIT="${DOCKER_BUILDKIT:-1}"

# Single unified Dockerfile, backend selected via build arg
DOCKERFILE="Dockerfile"
if [[ "$BACKEND" == "vulkan" ]]; then
    echo "Building for: Vulkan (AMD GPUs and compatible hardware)"
else
    echo "Building for: CUDA (NVIDIA GPUs)"
fi

# Git repository URLs
LLAMA_REPO="https://github.com/ggml-org/llama.cpp.git"
WHISPER_REPO="https://github.com/ggml-org/whisper.cpp.git"
SD_REPO="https://github.com/leejet/stable-diffusion.cpp.git"

# Function to get the latest commit hash from a git repo's default branch
get_latest_commit() {
    local repo_url="$1"
    local branch="${2:-master}"

    # Try to get the latest commit hash for the specified branch
    git ls-remote --heads "${repo_url}" "${branch}" 2>/dev/null | head -1 | cut -f1
}

# Function to get the default branch name (master or main)
get_default_branch() {
    local repo_url="$1"

    # Check for master first
    if git ls-remote --heads "${repo_url}" master &>/dev/null; then
        echo "master"
    elif git ls-remote --heads "${repo_url}" main &>/dev/null; then
        echo "main"
    else
        echo "master"  # fallback
    fi
}

# Function to get the latest release tag from a GitHub repo
get_latest_release_tag() {
    local owner_repo="$1"
    curl -fsSL "https://api.github.com/repos/${owner_repo}/releases/latest" \
        | grep '"tag_name"' | head -1 | cut -d'"' -f4
}

echo "=========================================="
echo "llama-swap-docker Build Script"
echo "=========================================="
echo ""

# Determine commit hashes / release tags - use env vars or auto-detect
# For vulkan builds, llama and sd use GitHub release tags (prebuilt binaries).
# For cuda builds (or whisper on any backend), use git commit hashes.
if [[ -n "${LLAMA_COMMIT_HASH:-}" ]]; then
    LLAMA_HASH="${LLAMA_COMMIT_HASH}"
    echo "llama.cpp: Using provided version: ${LLAMA_HASH}"
elif [[ "$BACKEND" == "vulkan" ]]; then
    LLAMA_HASH=$(get_latest_release_tag "ggml-org/llama.cpp")
    if [[ -z "${LLAMA_HASH}" ]]; then
        echo "ERROR: Could not determine latest release tag for llama.cpp" >&2
        exit 1
    fi
    echo "llama.cpp: Auto-detected latest release tag: ${LLAMA_HASH}"
else
    LLAMA_BRANCH=$(get_default_branch "${LLAMA_REPO}")
    LLAMA_HASH=$(get_latest_commit "${LLAMA_REPO}" "${LLAMA_BRANCH}")
    if [[ -z "${LLAMA_HASH}" ]]; then
        echo "ERROR: Could not determine latest commit for llama.cpp" >&2
        exit 1
    fi
    echo "llama.cpp: Auto-detected latest commit (${LLAMA_BRANCH}): ${LLAMA_HASH}"
fi

if [[ -n "${WHISPER_COMMIT_HASH:-}" ]]; then
    WHISPER_HASH="${WHISPER_COMMIT_HASH}"
    echo "whisper.cpp: Using provided commit hash: ${WHISPER_HASH}"
else
    WHISPER_BRANCH=$(get_default_branch "${WHISPER_REPO}")
    WHISPER_HASH=$(get_latest_commit "${WHISPER_REPO}" "${WHISPER_BRANCH}")
    if [[ -z "${WHISPER_HASH}" ]]; then
        echo "ERROR: Could not determine latest commit for whisper.cpp" >&2
        exit 1
    fi
    echo "whisper.cpp: Auto-detected latest commit (${WHISPER_BRANCH}): ${WHISPER_HASH}"
fi

if [[ -n "${SD_COMMIT_HASH:-}" ]]; then
    SD_HASH="${SD_COMMIT_HASH}"
    echo "stable-diffusion.cpp: Using provided version: ${SD_HASH}"
elif [[ "$BACKEND" == "vulkan" ]]; then
    SD_HASH=$(get_latest_release_tag "leejet/stable-diffusion.cpp")
    if [[ -z "${SD_HASH}" ]]; then
        echo "ERROR: Could not determine latest release tag for stable-diffusion.cpp" >&2
        exit 1
    fi
    echo "stable-diffusion.cpp: Auto-detected latest release tag: ${SD_HASH}"
else
    SD_BRANCH=$(get_default_branch "${SD_REPO}")
    SD_HASH=$(get_latest_commit "${SD_REPO}" "${SD_BRANCH}")
    if [[ -z "${SD_HASH}" ]]; then
        echo "ERROR: Could not determine latest commit for stable-diffusion.cpp" >&2
        exit 1
    fi
    echo "stable-diffusion.cpp: Auto-detected latest commit (${SD_BRANCH}): ${SD_HASH}"
fi

echo ""
echo "=========================================="
echo "Starting Docker build..."
echo "=========================================="
echo ""

# Build the Docker image with commit hashes as build args
# Build context is the repository root (..) so the Dockerfile can access Go source
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "${SCRIPT_DIR}/.." && pwd)"
BUILD_ARGS=(
    --build-arg "BACKEND=${BACKEND}"
    --build-arg "LLAMA_COMMIT_HASH=${LLAMA_HASH}"
    --build-arg "WHISPER_COMMIT_HASH=${WHISPER_HASH}"
    --build-arg "SD_COMMIT_HASH=${SD_HASH}"
    -t "${DOCKER_IMAGE_TAG}"
    -f "${SCRIPT_DIR}/${DOCKERFILE}"
)

if [[ "$NO_CACHE" == true ]]; then
    BUILD_ARGS+=(--no-cache)
    echo "Note: Building without cache"
fi

# Use docker buildx with a custom builder for parallelism control
# The legacy DOCKER_BUILDKIT=1 docker build doesn't respect BUILDKIT_MAX_PARALLELISM env var
# We need to use a custom builder with a buildkitd.toml config file
BUILDER_NAME="llama-swap-builder"

# Check if our custom builder exists with the right config, create/update if needed
if ! docker buildx inspect "$BUILDER_NAME" >/dev/null 2>&1; then
    echo "Creating custom buildx builder with max-parallelism=1..."
    
    # Create buildkitd.toml config file
    cat > buildkitd.toml << 'BUILDKIT_EOF'
[worker.oci]
  max-parallelism = 1
BUILDKIT_EOF
    
    # Create the builder with the config
    docker buildx create --name "$BUILDER_NAME" \
        --driver docker-container \
        --buildkitd-config buildkitd.toml \
        --use
else
    # Switch to our builder
    docker buildx use "$BUILDER_NAME"
fi

echo "Building with sequential stages (one at a time), each using all CPU cores..."
echo "Using builder: $BUILDER_NAME"

# Use docker buildx build with --load to load the image into Docker
# The --builder flag ensures we use our custom builder with max-parallelism=1
# Build context is the repository root so we can access Go source files
docker buildx build --builder "$BUILDER_NAME" --load "${BUILD_ARGS[@]}" "${REPO_ROOT}"

echo ""
echo "=========================================="
echo "Verifying build artifacts..."
echo "=========================================="
echo ""

# Verify all expected binaries exist in the image
MISSING_BINARIES=()

for binary in llama-server llama-cli whisper-server whisper-cli sd-server sd-cli llama-swap; do
    if ! docker run --rm "${DOCKER_IMAGE_TAG}" which "${binary}" >/dev/null 2>&1; then
        MISSING_BINARIES+=("${binary}")
    fi
done

if [[ ${#MISSING_BINARIES[@]} -gt 0 ]]; then
    echo "ERROR: Build succeeded but the following binaries are missing from the image:"
    for binary in "${MISSING_BINARIES[@]}"; do
        echo "  - ${binary}"
    done
    echo ""
    echo "This usually indicates a build stage failure. Try running with --no-cache flag:"
    echo "  ./build-image.sh --vulkan --no-cache"
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
if [[ "$BACKEND" == "vulkan" ]]; then
    echo "Run with:"
    echo "  docker run -it --rm --device /dev/dri:/dev/dri ${DOCKER_IMAGE_TAG}"
    echo ""
    echo "Note: For AMD GPUs, you may also need to mount render devices:"
    echo "  docker run -it --rm --device /dev/dri:/dev/dri --group-add video ${DOCKER_IMAGE_TAG}"
else
    echo "Run with:"
    echo "  docker run -it --rm --gpus all ${DOCKER_IMAGE_TAG}"
fi
