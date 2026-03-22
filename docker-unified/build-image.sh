#!/bin/bash
#
# Build script for unified CUDA container with version pinning
#
# Usage:
#   ./build-image.sh                               # Build with auto-detected versions
#   ./build-image.sh --no-cache                    # Build without cache
#   LLAMA_REF=b1234 ./build-image.sh               # Pin llama.cpp to a commit hash
#   LLAMA_REF=v1.2.3 ./build-image.sh              # Pin llama.cpp to a tag
#   LLAMA_REF=my-branch ./build-image.sh           # Pin llama.cpp to a branch
#   WHISPER_REF=v1.0.0 ./build-image.sh            # Pin whisper.cpp to a tag
#   SD_REF=master ./build-image.sh                 # Pin stable-diffusion.cpp to a branch
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
            echo "  LLAMA_REF            Pin llama.cpp to a commit, tag, or branch"
            echo "  WHISPER_REF          Pin whisper.cpp to a commit, tag, or branch"
            echo "  SD_REF               Pin stable-diffusion.cpp to a commit, tag, or branch"
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

# Resolve a git ref (commit hash, tag, or branch) to a commit hash.
# If the ref looks like a full SHA (40 hex chars), use it directly.
# Otherwise, query the remote for matching tags and branches.
resolve_ref() {
    local repo_url="$1"
    local ref="$2"

    # Full 40-char SHA — use as-is
    if [[ "${ref}" =~ ^[0-9a-f]{40}$ ]]; then
        echo "${ref}"
        return
    fi

    # Try tags first (exact match), then branches
    local hash
    hash=$(git ls-remote "${repo_url}" "refs/tags/${ref}" 2>/dev/null | head -1 | cut -f1)
    if [[ -n "${hash}" ]]; then
        echo "${hash}"
        return
    fi

    hash=$(git ls-remote "${repo_url}" "refs/heads/${ref}" 2>/dev/null | head -1 | cut -f1)
    if [[ -n "${hash}" ]]; then
        echo "${hash}"
        return
    fi

    # Short hash: search all remote refs for a SHA matching this prefix
    if [[ "${ref}" =~ ^[0-9a-f]+$ ]]; then
        hash=$(git ls-remote "${repo_url}" 2>/dev/null | grep "^${ref}" | head -1 | cut -f1)
        if [[ -n "${hash}" ]]; then
            echo "${hash}"
            return
        fi
    fi

    # Try GitHub API to resolve short commit hash to full SHA (requires 7+ hex chars)
    if [[ "${ref}" =~ ^[0-9a-f]{7,}$ ]] && \
       [[ "${repo_url}" =~ github\.com[:/]([^/]+)/([^/.]+)(\.git)?$ ]]; then
        local owner="${BASH_REMATCH[1]}"
        local repo="${BASH_REMATCH[2]}"
        hash=$(curl -sf "https://api.github.com/repos/${owner}/${repo}/commits/${ref}" \
            | grep '"sha"' | head -1 | grep -o '[0-9a-f]\{40\}')
        if [[ -n "${hash}" ]]; then
            echo "${hash}"
            return
        fi
    fi

    # Could not resolve — fail loudly rather than passing an unresolvable ref to Docker
    echo "ERROR: Could not resolve ref '${ref}' for ${repo_url}" >&2
    if [[ "${ref}" =~ ^[0-9a-f]+$ && ${#ref} -lt 7 ]]; then
        echo "  Short hashes must be at least 7 characters (got ${#ref})." >&2
    else
        echo "  Tried: tag, branch, git ls-remote prefix match, GitHub API" >&2
    fi
    echo "  Use a full 40-char SHA, a tag name, a branch name, or a 7+ char short hash." >&2
    return 1
}

get_default_branch() {
    local repo_url="$1"
    local hash
    hash=$(git ls-remote --heads "${repo_url}" master 2>/dev/null | head -1 | cut -f1)
    if [[ -n "${hash}" ]]; then
        echo "master"
    else
        echo "main"
    fi
}

echo "=========================================="
echo "llama-swap Unified CUDA Build"
echo "=========================================="
echo ""

# Resolve llama.cpp ref
if [[ -n "${LLAMA_REF:-}" ]]; then
    LLAMA_HASH=$(resolve_ref "${LLAMA_REPO}" "${LLAMA_REF}") || exit 1
    echo "llama.cpp: ${LLAMA_REF} -> ${LLAMA_HASH}"
else
    LLAMA_BRANCH=$(get_default_branch "${LLAMA_REPO}")
    LLAMA_HASH=$(resolve_ref "${LLAMA_REPO}" "${LLAMA_BRANCH}")
    if [[ -z "${LLAMA_HASH}" ]]; then
        echo "ERROR: Could not determine latest commit for llama.cpp" >&2
        exit 1
    fi
    echo "llama.cpp: latest (${LLAMA_BRANCH}): ${LLAMA_HASH}"
fi

# Resolve whisper.cpp ref
if [[ -n "${WHISPER_REF:-}" ]]; then
    WHISPER_HASH=$(resolve_ref "${WHISPER_REPO}" "${WHISPER_REF}") || exit 1
    echo "whisper.cpp: ${WHISPER_REF} -> ${WHISPER_HASH}"
else
    WHISPER_BRANCH=$(get_default_branch "${WHISPER_REPO}")
    WHISPER_HASH=$(resolve_ref "${WHISPER_REPO}" "${WHISPER_BRANCH}")
    if [[ -z "${WHISPER_HASH}" ]]; then
        echo "ERROR: Could not determine latest commit for whisper.cpp" >&2
        exit 1
    fi
    echo "whisper.cpp: latest (${WHISPER_BRANCH}): ${WHISPER_HASH}"
fi

# Resolve stable-diffusion.cpp ref
if [[ -n "${SD_REF:-}" ]]; then
    SD_HASH=$(resolve_ref "${SD_REPO}" "${SD_REF}") || exit 1
    echo "stable-diffusion.cpp: ${SD_REF} -> ${SD_HASH}"
else
    SD_BRANCH=$(get_default_branch "${SD_REPO}")
    SD_HASH=$(resolve_ref "${SD_REPO}" "${SD_BRANCH}")
    if [[ -z "${SD_HASH}" ]]; then
        echo "ERROR: Could not determine latest commit for stable-diffusion.cpp" >&2
        exit 1
    fi
    echo "stable-diffusion.cpp: latest (${SD_BRANCH}): ${SD_HASH}"
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

DOCKER_BUILDKIT=1 docker buildx build --load "${BUILD_ARGS[@]}" "${SCRIPT_DIR}"

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
