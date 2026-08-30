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
#   AUDIO_REF=main ./build-image.sh --cuda               # Pin audio.cpp to a branch
#   LS_VERSION=170 ./build-image.sh --cuda               # Override llama-swap version
#   IK_LLAMA_REF=main ./build-image.sh --cuda            # Pin ik_llama.cpp to main branch (CUDA only)
#
# Split build (CI only). Compiling every upstream project in one job puts five
# concurrent CUDA builds on four cores, which no longer fits in a GitHub Actions
# job. These modes let each project compile on its own runner and publish an
# artifacts-only image, which a final assemble step copies into the unified
# image. The resulting image is identical either way -- see README.md.
#
#   ./build-image.sh --cuda --resolve         # print resolved hashes as key=value
#   ./build-image.sh --cuda --stage=whisper   # build + push one project's artifacts
#   ./build-image.sh --cuda --assemble        # assemble unified image from artifacts
#

set -euo pipefail

BACKEND=""
NO_CACHE=false
MODE="unified"
STAGE_PROJECT=""
WHISPER_FFMPEG="${WHISPER_FFMPEG:-yes}"

# Registry holding the per-project artifacts images used by --stage/--assemble.
ARTIFACT_REPO="${ARTIFACT_REPO:-ghcr.io/mostlygeek/llama-swap}"

# Upstream projects compiled into the image. ik-llama is CUDA only.
ALL_PROJECTS=(whisper sd audio llama ik-llama)

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
        --resolve)
            MODE="resolve"
            ;;
        --assemble)
            MODE="assemble"
            ;;
        --stage=*)
            MODE="stage"
            STAGE_PROJECT="${arg#*=}"
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
            echo "Split build (CI only):"
            echo "  --resolve         Print resolved commit hashes as key=value and exit"
            echo "  --stage=PROJECT   Build and push one project's artifacts image"
            echo "                    (${ALL_PROJECTS[*]})"
            echo "  --assemble        Assemble the unified image from published artifacts"
            echo ""
            echo "Environment variables:"
            echo "  DOCKER_IMAGE_TAG     Set custom image tag (default: llama-swap:unified-cuda or llama-swap:unified-vulkan)"
            echo "  LLAMA_REF            Pin llama.cpp to a commit, tag, or branch"
            echo "  WHISPER_REF          Pin whisper.cpp to a commit, tag, or branch"
            echo "  SD_REF               Pin stable-diffusion.cpp to a commit, tag, or branch"
            echo "  AUDIO_REF            Pin audio.cpp to a commit, tag, or branch"
            echo "  IK_LLAMA_REF         Pin ik_llama.cpp to a commit, tag, or branch (CUDA only)"
            echo "  LS_VERSION           Override llama-swap version (e.g., '170' or 'latest')"
            echo "  WHISPER_FFMPEG       Enable whisper.cpp FFmpeg support (default: yes)"
            echo "  ARTIFACT_REPO        Registry for --stage/--assemble artifacts images"
            echo "                       (default: ghcr.io/mostlygeek/llama-swap)"
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

# --resolve emits machine-readable output only, so the progress chatter that
# every other mode prints has to stay off it.
log() {
    if [[ "$MODE" != "resolve" ]]; then
        echo "$@"
    fi
}

DOCKER_IMAGE_TAG="${DOCKER_IMAGE_TAG:-llama-swap:unified-${BACKEND}}"

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

# Git repository URLs
LLAMA_REPO="https://github.com/ggml-org/llama.cpp.git"
WHISPER_REPO="https://github.com/ggml-org/whisper.cpp.git"
SD_REPO="https://github.com/leejet/stable-diffusion.cpp.git"
AUDIO_REPO="https://github.com/0xShug0/audio.cpp.git"
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

# Projects built for this backend. ik_llama.cpp has no vulkan support.
backend_projects() {
    local project
    for project in "${ALL_PROJECTS[@]}"; do
        if [[ "${project}" == "ik-llama" && "${BACKEND}" != "cuda" ]]; then
            continue
        fi
        echo "${project}"
    done
}

project_hash() {
    case "$1" in
        whisper)  echo "${WHISPER_HASH}" ;;
        sd)       echo "${SD_HASH}" ;;
        audio)    echo "${AUDIO_HASH}" ;;
        llama)    echo "${LLAMA_HASH}" ;;
        ik-llama) echo "${IK_LLAMA_HASH}" ;;
        *) echo "ERROR: unknown project '$1'" >&2; return 1 ;;
    esac
}

project_image_arg() {
    case "$1" in
        whisper)  echo "WHISPER_IMAGE" ;;
        sd)       echo "SD_IMAGE" ;;
        audio)    echo "AUDIO_IMAGE" ;;
        llama)    echo "LLAMA_IMAGE" ;;
        ik-llama) echo "IK_LLAMA_IMAGE" ;;
        *) echo "ERROR: unknown project '$1'" >&2; return 1 ;;
    esac
}

# Everything that goes into a project's artifacts, beyond the upstream commit:
# the Dockerfile (stage definition, base images, CMAKE_CUDA_ARCHITECTURES), the
# install script, and the build args the script reads. The registry layer cache
# this replaces keyed on all of it, so the tag has to as well -- otherwise
# editing an install script or the architecture list would leave every project
# whose upstream had not moved pinned to a stale artifacts image.
recipe_hash() {
    local project="$1"
    {
        cat "${SCRIPT_DIR}/Dockerfile"
        cat "${SCRIPT_DIR}/install-${project}.sh"
        echo "WHISPER_FFMPEG=${WHISPER_FFMPEG}"
    } | sha256sum | cut -c1-8
}

# Artifacts images are addressed by the upstream commit plus that recipe, so a
# rerun with both unchanged finds its own output already published and reuses
# it instead of recompiling, and any change to either forces a rebuild.
artifact_tag() {
    local project="$1" hash
    hash="$(project_hash "${project}")" || return 1
    echo "${ARTIFACT_REPO}:art-${project}-${BACKEND}-${hash:0:12}-$(recipe_hash "${project}")"
}

artifact_exists() {
    docker buildx imagetools inspect "$1" >/dev/null 2>&1
}

log "=========================================="
if [[ "$MODE" == "stage" ]]; then
    log "llama-swap Artifacts Build (${STAGE_PROJECT}, ${BACKEND})"
elif [[ "$MODE" == "assemble" ]]; then
    log "llama-swap Unified Assemble (${BACKEND})"
else
    log "llama-swap Unified Build (${BACKEND})"
fi
log "=========================================="
log ""

if [[ "$MODE" == "stage" ]]; then
    if ! printf '%s\n' "${ALL_PROJECTS[@]}" | grep -qx -- "${STAGE_PROJECT}"; then
        echo "ERROR: unknown project '${STAGE_PROJECT}'" >&2
        echo "  Known projects: ${ALL_PROJECTS[*]}" >&2
        exit 1
    fi
    if ! backend_projects | grep -qx -- "${STAGE_PROJECT}"; then
        echo "ERROR: project '${STAGE_PROJECT}' is not built for the ${BACKEND} backend" >&2
        exit 1
    fi
fi

# ── Resolve refs ──────────────────────────────────────────────────────
#
# A full 40-char SHA short-circuits resolve_ref without a network call, so CI
# resolves every ref once up front and passes the hashes to the per-project and
# assemble jobs. That keeps a moving branch from resolving differently in each
# job and assembling an image out of mismatched artifacts.

# Resolve llama.cpp ref
if [[ -n "${LLAMA_REF:-}" ]]; then
    LLAMA_HASH=$(resolve_ref "${LLAMA_REPO}" "${LLAMA_REF}") || exit 1
    log "llama.cpp: ${LLAMA_REF} -> ${LLAMA_HASH}"
else
    LLAMA_HASH=$(get_latest_hash "${LLAMA_REPO}")
    if [[ -z "${LLAMA_HASH}" ]]; then
        echo "ERROR: Could not determine latest commit for llama.cpp" >&2
        exit 1
    fi
    log "llama.cpp: latest HEAD: ${LLAMA_HASH}"
fi

# Resolve whisper.cpp ref
if [[ -n "${WHISPER_REF:-}" ]]; then
    WHISPER_HASH=$(resolve_ref "${WHISPER_REPO}" "${WHISPER_REF}") || exit 1
    log "whisper.cpp: ${WHISPER_REF} -> ${WHISPER_HASH}"
else
    WHISPER_HASH=$(get_latest_hash "${WHISPER_REPO}")
    if [[ -z "${WHISPER_HASH}" ]]; then
        echo "ERROR: Could not determine latest commit for whisper.cpp" >&2
        exit 1
    fi
    log "whisper.cpp: latest HEAD: ${WHISPER_HASH}"
fi

# Resolve stable-diffusion.cpp ref
if [[ -n "${SD_REF:-}" ]]; then
    SD_HASH=$(resolve_ref "${SD_REPO}" "${SD_REF}") || exit 1
    log "stable-diffusion.cpp: ${SD_REF} -> ${SD_HASH}"
else
    SD_HASH=$(get_latest_hash "${SD_REPO}")
    if [[ -z "${SD_HASH}" ]]; then
        echo "ERROR: Could not determine latest commit for stable-diffusion.cpp" >&2
        exit 1
    fi
    log "stable-diffusion.cpp: latest HEAD: ${SD_HASH}"
fi

# Resolve audio.cpp ref
if [[ -n "${AUDIO_REF:-}" ]]; then
    AUDIO_HASH=$(resolve_ref "${AUDIO_REPO}" "${AUDIO_REF}") || exit 1
    log "audio.cpp: ${AUDIO_REF} -> ${AUDIO_HASH}"
else
    AUDIO_HASH=$(get_latest_hash "${AUDIO_REPO}")
    if [[ -z "${AUDIO_HASH}" ]]; then
        echo "ERROR: Could not determine latest commit for audio.cpp" >&2
        exit 1
    fi
    log "audio.cpp: latest HEAD: ${AUDIO_HASH}"
fi

# Resolve ik_llama.cpp ref (CUDA only, but --resolve always reports it so one
# setup job can feed both backends)
if [[ "$BACKEND" == "cuda" || "$MODE" == "resolve" ]]; then
    if [[ -n "${IK_LLAMA_REF:-}" ]]; then
        IK_LLAMA_HASH=$(resolve_ref "${IK_LLAMA_REPO}" "${IK_LLAMA_REF}") || exit 1
        log "ik_llama.cpp: ${IK_LLAMA_REF} -> ${IK_LLAMA_HASH}"
    else
        IK_LLAMA_HASH=$(get_latest_hash "${IK_LLAMA_REPO}")
        if [[ -z "${IK_LLAMA_HASH}" ]]; then
            echo "ERROR: Could not determine latest commit for ik_llama.cpp" >&2
            exit 1
        fi
        log "ik_llama.cpp: latest HEAD: ${IK_LLAMA_HASH}"
    fi
else
    IK_LLAMA_HASH="n/a"
    log "ik_llama.cpp: skipped (vulkan build)"
fi

# Resolve llama-swap ref
if [[ -n "${LS_VERSION:-}" ]]; then
    LS_HASH=$(resolve_ref "${LLAMA_SWAP_REPO}" "${LS_VERSION}") || exit 1
    log "llama-swap: ${LS_VERSION} -> ${LS_HASH}"
else
    LS_HASH=$(get_latest_hash "${LLAMA_SWAP_REPO}")
    if [[ -z "${LS_HASH}" ]]; then
        echo "ERROR: Could not determine latest commit for llama-swap" >&2
        exit 1
    fi
    log "llama-swap: latest HEAD: ${LS_HASH}"
fi

if [[ "$MODE" == "resolve" ]]; then
    echo "llama_hash=${LLAMA_HASH}"
    echo "whisper_hash=${WHISPER_HASH}"
    echo "sd_hash=${SD_HASH}"
    echo "audio_hash=${AUDIO_HASH}"
    echo "ik_llama_hash=${IK_LLAMA_HASH}"
    echo "ls_hash=${LS_HASH}"
    exit 0
fi

BUILD_ARGS=(
    --build-arg "BACKEND=${BACKEND}"
    --build-arg "LLAMA_COMMIT_HASH=${LLAMA_HASH}"
    --build-arg "WHISPER_COMMIT_HASH=${WHISPER_HASH}"
    --build-arg "SD_COMMIT_HASH=${SD_HASH}"
    --build-arg "AUDIO_COMMIT_HASH=${AUDIO_HASH}"
    --build-arg "IK_LLAMA_COMMIT_HASH=${IK_LLAMA_HASH}"
    --build-arg "LS_VERSION=${LS_HASH}"
    --build-arg "WHISPER_FFMPEG=${WHISPER_FFMPEG}"
    -f "${SCRIPT_DIR}/Dockerfile"
)

if [[ "$NO_CACHE" == true ]]; then
    BUILD_ARGS+=(--no-cache)
    echo "Note: Building without cache"
fi

# ── --stage: build and publish one project's artifacts ────────────────

if [[ "$MODE" == "stage" ]]; then
    ARTIFACT_TAG="$(artifact_tag "${STAGE_PROJECT}")"

    echo ""
    echo "Artifacts image: ${ARTIFACT_TAG}"

    if [[ "$NO_CACHE" != true ]] && artifact_exists "${ARTIFACT_TAG}"; then
        echo "Already published for this commit and recipe, nothing to build."
        exit 0
    fi

    echo ""
    echo "=========================================="
    echo "Building ${STAGE_PROJECT} artifacts..."
    echo "=========================================="
    echo ""

    DOCKER_BUILDKIT=1 docker buildx build --push \
        --target "${STAGE_PROJECT}-artifacts" \
        -t "${ARTIFACT_TAG}" \
        "${BUILD_ARGS[@]}" "${SCRIPT_DIR}"

    echo ""
    echo "Published: ${ARTIFACT_TAG}"
    exit 0
fi

# ── --assemble: point each -src stage at a published artifacts image ──

if [[ "$MODE" == "assemble" ]]; then
    MISSING_ARTIFACTS=()
    echo ""
    echo "Artifacts images:"
    while read -r project; do
        tag="$(artifact_tag "${project}")"
        if artifact_exists "${tag}"; then
            echo "  ${tag}"
        else
            echo "  ${tag}  [MISSING]"
            MISSING_ARTIFACTS+=("${tag}")
        fi
        BUILD_ARGS+=(--build-arg "$(project_image_arg "${project}")=${tag}")
    done < <(backend_projects)

    if [[ ${#MISSING_ARTIFACTS[@]} -gt 0 ]]; then
        echo ""
        echo "ERROR: cannot assemble, these artifacts images were never published:" >&2
        for tag in "${MISSING_ARTIFACTS[@]}"; do
            echo "  - ${tag}" >&2
        done
        echo "" >&2
        echo "Run the matching --stage=PROJECT build first." >&2
        exit 1
    fi
fi

BUILD_ARGS+=(-t "${DOCKER_IMAGE_TAG}")

echo ""
echo "=========================================="
echo "Starting Docker build..."
echo "=========================================="
echo ""

DOCKER_BUILDKIT=1 docker buildx build --load "${BUILD_ARGS[@]}" "${SCRIPT_DIR}"

echo ""
echo "=========================================="
echo "Verifying build artifacts..."
echo "=========================================="
echo ""

EXPECTED_BINARIES=(llama-server llama-cli llama-bench whisper-server whisper-cli sd-server sd-cli audiocpp_server audiocpp_cli llama-swap vllm-wrapper)
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

VERIFIED_LIST="llama-server, llama-cli, llama-bench, whisper-server, whisper-cli, sd-server, sd-cli, audiocpp_server, audiocpp_cli, llama-swap, vllm-wrapper"
if [[ "$BACKEND" == "cuda" ]]; then
    VERIFIED_LIST="${VERIFIED_LIST}, ik-llama-server"
fi
echo "All expected binaries verified: ${VERIFIED_LIST}"

# audio.cpp must be a deployment build: the model_specs catalog is compiled into
# the binaries, since the image ships no model_specs/ directory for the runtime
# to discover. Without it every load of a package without an embedded spec fails
# with "model spec not found for family ...". The compiled catalog is raw JSON in
# .rodata, so grepping the binary for a known spec confirms it is there.
if ! docker run --rm --entrypoint grep "${DOCKER_IMAGE_TAG}" \
        -aq '"family": "pocket_tts"' /usr/local/bin/audiocpp_server; then
    echo "ERROR: audiocpp_server was not built with AUDIOCPP_DEPLOYMENT_BUILD=ON;"
    echo "       its compiled model spec catalog is missing."
    exit 1
fi

# Run the binary so a missing runtime library is caught here rather than on a
# user's first request. CUDA builds link libcuda.so.1, which the NVIDIA
# container runtime only injects with --gpus; point at the stub copied into the
# image so this works on a build machine with no GPU.
SMOKE_ARGS=(--rm)
if [[ "$BACKEND" == "cuda" ]]; then
    SMOKE_ARGS+=(-e "LD_LIBRARY_PATH=/usr/local/cuda/lib64/stubs:/usr/local/cuda/lib64")
fi

if ! docker run "${SMOKE_ARGS[@]}" --entrypoint audiocpp_server "${DOCKER_IMAGE_TAG}" --help >/dev/null; then
    echo "ERROR: audiocpp_server --help failed; the binary or its runtime"
    echo "       libraries are broken in the image."
    exit 1
fi

echo "audio.cpp verified: deployment build (compiled model spec catalog), binary runs"

echo ""
echo "=========================================="
echo "Building rootless image..."
echo "=========================================="
echo ""

ROOTLESS_TAG="${DOCKER_IMAGE_TAG}-rootless"
docker buildx build --load -t "${ROOTLESS_TAG}" - <<EOF
FROM ${DOCKER_IMAGE_TAG}
USER root
RUN groupadd --system --gid 10001 llama-swap && \\
    useradd --system --uid 10001 --gid 10001 \\
      --home /app --shell /sbin/nologin llama-swap && \\
    chown -R 10001:10001 /etc/llama-swap /models
USER 10001
EOF

echo "Rootless image built: ${ROOTLESS_TAG}"

echo ""
echo "=========================================="
echo "Build complete!"
echo "=========================================="
echo ""
echo "Image tags:"
echo "  ${DOCKER_IMAGE_TAG}"
echo "  ${ROOTLESS_TAG}"
echo ""
echo "Built with:"
echo "  llama.cpp:            ${LLAMA_HASH}"
echo "  whisper.cpp:          ${WHISPER_HASH}"
echo "  stable-diffusion.cpp: ${SD_HASH}"
echo "  audio.cpp:            ${AUDIO_HASH}"
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
