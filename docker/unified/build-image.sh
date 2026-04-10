#!/bin/bash
#
# Build script for unified container with version pinning
#
# Usage:
#   ./build-image.sh --cuda                              # Build CUDA image
#   ./build-image.sh --vulkan                            # Build Vulkan image
#   ./build-image.sh --cuda --no-cache                   # Build without cache
#   LLAMA_REF=b1234 ./build-image.sh --vulkan            # Pin llama.cpp to a commit hash
#   LLAMA_REF=v1.2.3 ./build-image.sh --cuda             # Pin llama.cpp to a tag
#   WHISPER_REF=v1.0.0 ./build-image.sh --vulkan         # Pin whisper.cpp to a tag
#   SD_REF=master ./build-image.sh --cuda                # Pin stable-diffusion.cpp to a branch
#   LS_VERSION=170 ./build-image.sh --cuda               # Override llama-swap version
#   IK_LLAMA_REF=main ./build-image.sh --cuda            # Pin ik_llama.cpp to main branch (CUDA only)
#

set -euo pipefail

BACKEND=""
NO_CACHE=false

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
            echo "  DOCKER_IMAGE_TAG     Set custom image tag (default: llama-swap:unified-cuda or llama-swap:unified-vulkan)"
            echo "  LLAMA_REF            Pin llama.cpp to a commit, tag, or branch"
            echo "  WHISPER_REF          Pin whisper.cpp to a commit, tag, or branch"
            echo "  SD_REF               Pin stable-diffusion.cpp to a commit, tag, or branch"
            echo "  IK_LLAMA_REF         Pin ik_llama.cpp to a commit, tag, or branch (CUDA only)"
            echo "  LS_VERSION           Override llama-swap version (e.g., '170' or 'latest')"
            exit 0
            ;;
    esac
done

if [[ -z "$BACKEND" ]]; then
    echo "Error: No backend specified. Please use --cuda or --vulkan."
    echo ""
    echo "Usage: ./build-image.sh --cuda|--vulkan [--no-cache]"
    exit 1
fi

DOCKER_IMAGE_TAG="${DOCKER_IMAGE_TAG:-llama-swap:unified-${BACKEND}}"

# Git repository URLs
LLAMA_REPO="https://github.com/ggml-org/llama.cpp.git"
WHISPER_REPO="https://github.com/ggml-org/whisper.cpp.git"
SD_REPO="https://github.com/leejet/stable-diffusion.cpp.git"
LLAMA_SWAP_REPO="https://github.com/mostlygeek/llama-swap.git"
IK_LLAMA_REPO="https://github.com/ikawrakow/ik_llama.cpp.git"

# Resolve a git ref (commit hash, tag, or branch) to a full commit hash.
# Requires only: git, network access to the remote.
resolve_ref() {
    local repo_url="$1"
    local ref="$2"

    # Full 40-char SHA — use as-is
    if [[ "${ref}" =~ ^[0-9a-f]{40}$ ]]; then
        echo "${ref}"
        return
    fi

    # Try tag then branch (exact match)
    local hash
    hash=$(git ls-remote "${repo_url}" "refs/tags/${ref}" "refs/heads/${ref}" 2>/dev/null | head -1 | cut -f1)
    if [[ -n "${hash}" ]]; then
        echo "${hash}"
        return
    fi

    # Short hash (7+ chars): scan all refs for a SHA with this prefix
    if [[ "${ref}" =~ ^[0-9a-f]{7,}$ ]]; then
        hash=$(git ls-remote "${repo_url}" 2>/dev/null | grep "^${ref}" | head -1 | cut -f1)
        if [[ -n "${hash}" ]]; then
            echo "${hash}"
            return
        fi
    fi

    echo "ERROR: Could not resolve ref '${ref}' for ${repo_url}" >&2
    if [[ "${ref}" =~ ^[0-9a-f]+$ && ${#ref} -lt 7 ]]; then
        echo "  Short hashes must be at least 7 characters (got ${#ref})." >&2
    else
        echo "  Tried: tag, branch, git ls-remote prefix match" >&2
    fi
    echo "  Use a full 40-char SHA, a tag name, a branch name, or a 7+ char short hash." >&2
    return 1
}

# Resolve HEAD of a repo without needing to know the default branch name.
get_latest_hash() {
    git ls-remote "${1}" HEAD 2>/dev/null | head -1 | cut -f1
}

echo "=========================================="
echo "llama-swap Unified Build (${BACKEND})"
echo "=========================================="
echo ""

# Resolve llama.cpp ref
if [[ -n "${LLAMA_REF:-}" ]]; then
    LLAMA_HASH=$(resolve_ref "${LLAMA_REPO}" "${LLAMA_REF}") || exit 1
    echo "llama.cpp: ${LLAMA_REF} -> ${LLAMA_HASH}"
else
    LLAMA_HASH=$(get_latest_hash "${LLAMA_REPO}")
    if [[ -z "${LLAMA_HASH}" ]]; then
        echo "ERROR: Could not determine latest commit for llama.cpp" >&2
        exit 1
    fi
    echo "llama.cpp: latest HEAD: ${LLAMA_HASH}"
fi

# Resolve whisper.cpp ref
if [[ -n "${WHISPER_REF:-}" ]]; then
    WHISPER_HASH=$(resolve_ref "${WHISPER_REPO}" "${WHISPER_REF}") || exit 1
    echo "whisper.cpp: ${WHISPER_REF} -> ${WHISPER_HASH}"
else
    WHISPER_HASH=$(get_latest_hash "${WHISPER_REPO}")
    if [[ -z "${WHISPER_HASH}" ]]; then
        echo "ERROR: Could not determine latest commit for whisper.cpp" >&2
        exit 1
    fi
    echo "whisper.cpp: latest HEAD: ${WHISPER_HASH}"
fi

# Resolve stable-diffusion.cpp ref
if [[ -n "${SD_REF:-}" ]]; then
    SD_HASH=$(resolve_ref "${SD_REPO}" "${SD_REF}") || exit 1
    echo "stable-diffusion.cpp: ${SD_REF} -> ${SD_HASH}"
else
    SD_HASH=$(get_latest_hash "${SD_REPO}")
    if [[ -z "${SD_HASH}" ]]; then
        echo "ERROR: Could not determine latest commit for stable-diffusion.cpp" >&2
        exit 1
    fi
    echo "stable-diffusion.cpp: latest HEAD: ${SD_HASH}"
fi

# Resolve ik_llama.cpp ref (CUDA only)
if [[ "$BACKEND" == "cuda" ]]; then
    if [[ -n "${IK_LLAMA_REF:-}" ]]; then
        IK_LLAMA_HASH=$(resolve_ref "${IK_LLAMA_REPO}" "${IK_LLAMA_REF}") || exit 1
        echo "ik_llama.cpp: ${IK_LLAMA_REF} -> ${IK_LLAMA_HASH}"
    else
        IK_LLAMA_HASH=$(get_latest_hash "${IK_LLAMA_REPO}")
        if [[ -z "${IK_LLAMA_HASH}" ]]; then
            echo "ERROR: Could not determine latest commit for ik_llama.cpp" >&2
            exit 1
        fi
        echo "ik_llama.cpp: latest HEAD: ${IK_LLAMA_HASH}"
    fi
else
    IK_LLAMA_HASH="n/a"
    echo "ik_llama.cpp: skipped (vulkan build)"
fi

# Resolve llama-swap ref
if [[ -n "${LS_VERSION:-}" ]]; then
    LS_HASH=$(resolve_ref "${LLAMA_SWAP_REPO}" "${LS_VERSION}") || exit 1
    echo "llama-swap: ${LS_VERSION} -> ${LS_HASH}"
else
    LS_HASH=$(get_latest_hash "${LLAMA_SWAP_REPO}")
    if [[ -z "${LS_HASH}" ]]; then
        echo "ERROR: Could not determine latest commit for llama-swap" >&2
        exit 1
    fi
    echo "llama-swap: latest HEAD: ${LS_HASH}"
fi

echo ""
echo "=========================================="
echo "Starting Docker build..."
echo "=========================================="
echo ""

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

BUILD_ARGS=(
    --build-arg "BACKEND=${BACKEND}"
    --build-arg "LLAMA_COMMIT_HASH=${LLAMA_HASH}"
    --build-arg "WHISPER_COMMIT_HASH=${WHISPER_HASH}"
    --build-arg "SD_COMMIT_HASH=${SD_HASH}"
    --build-arg "IK_LLAMA_COMMIT_HASH=${IK_LLAMA_HASH}"
    --build-arg "LS_VERSION=${LS_HASH}"
    --build-arg "RUN_UID=${RUN_UID:-0}"
    -t "${DOCKER_IMAGE_TAG}"
    -f "${SCRIPT_DIR}/Dockerfile"
)

if [[ "$NO_CACHE" == true ]]; then
    BUILD_ARGS+=(--no-cache)
    echo "Note: Building without cache"
elif [[ "${GITHUB_ACTIONS:-}" == "true" && "${ACT:-}" != "true" ]]; then
    CACHE_REF="ghcr.io/mostlygeek/llama-swap:unified-${BACKEND}-cache"
    BUILD_ARGS+=(
        --cache-from "type=registry,ref=${CACHE_REF}"
        --cache-to "type=registry,ref=${CACHE_REF},mode=max"
    )
    echo "Note: Using registry cache (${CACHE_REF})"
fi

DOCKER_BUILDKIT=1 docker buildx build --load "${BUILD_ARGS[@]}" "${SCRIPT_DIR}"

echo ""
echo "=========================================="
echo "Verifying build artifacts..."
echo "=========================================="
echo ""

EXPECTED_BINARIES=(llama-server llama-cli whisper-server whisper-cli sd-server sd-cli llama-swap)
if [[ "$BACKEND" == "cuda" ]]; then
    EXPECTED_BINARIES+=(ik-llama-server)
fi

MISSING_BINARIES=()
for binary in "${EXPECTED_BINARIES[@]}"; do
    if ! docker run --rm --entrypoint which "${DOCKER_IMAGE_TAG}" "${binary}" >/dev/null 2>&1; then
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
    echo "  ./build-image.sh --${BACKEND} --no-cache"
    exit 1
fi

VERIFIED_LIST="llama-server, llama-cli, whisper-server, whisper-cli, sd-server, sd-cli, llama-swap"
if [[ "$BACKEND" == "cuda" ]]; then
    VERIFIED_LIST="${VERIFIED_LIST}, ik-llama-server"
fi
echo "All expected binaries verified: ${VERIFIED_LIST}"

echo ""
echo "=========================================="
echo "Build complete!"
echo "=========================================="
echo ""
echo "Image tag: ${DOCKER_IMAGE_TAG}"
echo ""
echo "Built with:"
echo "  llama.cpp:            ${LLAMA_HASH}"
echo "  whisper.cpp:          ${WHISPER_HASH}"
echo "  stable-diffusion.cpp: ${SD_HASH}"
if [[ "$BACKEND" == "cuda" ]]; then
    echo "  ik_llama.cpp:         ${IK_LLAMA_HASH}"
fi
echo "  llama-swap:           $(docker run --rm --entrypoint cat "${DOCKER_IMAGE_TAG}" /versions.txt | grep llama-swap | cut -d' ' -f2-)"
echo ""
if [[ "$BACKEND" == "vulkan" ]]; then
    echo "Run with:"
    echo "  docker run -it --rm --device /dev/dri:/dev/dri ${DOCKER_IMAGE_TAG}"
    echo ""
    echo "Note: For AMD GPUs, you may also need:"
    echo "  docker run -it --rm --device /dev/dri:/dev/dri --group-add video ${DOCKER_IMAGE_TAG}"
else
    echo "Run with:"
    echo "  docker run -it --rm --gpus all ${DOCKER_IMAGE_TAG}"
fi
