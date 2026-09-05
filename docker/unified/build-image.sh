#!/bin/bash
#
# Build script for the unified container.
#
# Usage:
#   ./build-image.sh --cuda                              # Build CUDA 12 image
#   ./build-image.sh --cuda13                            # Build CUDA 13 image
#   ./build-image.sh --vulkan                            # Build Vulkan image
#   ./build-image.sh --cuda13 --platform=linux/arm64     # Build for another arch
#   ./build-image.sh --cuda --no-cache                   # Build without cache
#   LLAMA_REF=b1234 ./build-image.sh --vulkan            # Pin llama.cpp to a commit hash
#   LLAMA_REF=v1.2.3 ./build-image.sh --cuda             # Pin llama.cpp to a tag
#   WHISPER_REF=v1.0.0 ./build-image.sh --vulkan         # Pin whisper.cpp to a tag
#   SD_REF=master ./build-image.sh --cuda                # Pin stable-diffusion.cpp to a branch
#   AUDIO_REF=main ./build-image.sh --cuda               # Pin audio.cpp to a branch
#   LS_VERSION=170 ./build-image.sh --cuda               # Override llama-swap version
#   IK_LLAMA_REF=main ./build-image.sh --cuda            # Pin ik_llama.cpp to main branch (CUDA only)
#
# The build is one Dockerfile per piece:
#
#   base-<backend>.Dockerfile  the builder base, published as its own image
#   <project>.Dockerfile       one upstream project -> a scratch image of /install
#   runtime.Dockerfile         copies those /install trees into the final image
#
# so a project's inputs are exactly two files plus the base tag. That is what
# the artifacts tag is keyed on, and why editing the runtime rebuilds nothing.
#
# CUDA variants. --cuda and --cuda13 are the same CUDA build against different
# toolkits: CUDA 12 compiled for Pascal through Ada, and CUDA 13 compiled for
# Ampere through Blackwell. CUDA 13 dropped Maxwell, Pascal and Volta from nvcc
# entirely, so one image cannot cover both ranges. Only CUDA_VERSION and
# CMAKE_CUDA_ARCHITECTURES differ between them, and the builder base's tag is
# already keyed on both, so the two variants get their own base and artifacts
# images without any Dockerfile or install script knowing about the split.
#
# Split modes (CI). Compiling every project in one job put five concurrent CUDA
# builds on four cores, which no longer fits in a GitHub Actions job:
#
#   ./build-image.sh --cuda --resolve         # print resolved hashes as key=value
#   ./build-image.sh --cuda --stage=base      # build + push the builder base
#   ./build-image.sh --cuda --stage=whisper   # build + push one project's artifacts
#   ./build-image.sh --cuda --assemble        # assemble one platform's image
#   ./build-image.sh --cuda --manifest        # join the platforms into one tag
#
# Platforms. CUDA 13 publishes linux/amd64 and linux/arm64 -- NVIDIA's GB10
# (DGX Spark) pairs a Blackwell GPU with a Grace CPU, so an aarch64 image is the
# only way to reach it. Every stage builds natively on a runner of the matching
# architecture; emulating a multi-hour CUDA compile through QEMU is not
# practical. --assemble therefore produces one platform's image under an
# arch-qualified tag, and --manifest joins those into the tag users pull.
#

set -euo pipefail

# BACKEND picks the build itself (which base Dockerfile, which cmake flags).
# VARIANT names the flavour that gets published, and is what every image tag is
# built from: cuda and cuda13 are both BACKEND=cuda.
BACKEND=""
VARIANT=""
NO_CACHE=false
MODE="unified"
STAGE_TARGET=""
PUSH=false
WHISPER_FFMPEG="${WHISPER_FFMPEG:-yes}"

# Platform to build for. Empty means the host's own architecture, which is what
# a local build wants; CI sets it explicitly and schedules the job on a runner
# of that architecture.
PLATFORM="${PLATFORM:-}"

# Platforms --manifest joins, comma separated. Defaults to just this one, so a
# single-platform variant takes the same code path as a multi-platform one.
MANIFEST_PLATFORMS="${MANIFEST_PLATFORMS:-}"

# Date suffix for the published tags. CI resolves it once, in the setup job, so
# every platform's image and the manifest that joins them agree even when a
# build runs across UTC midnight.
DATE_TAG="${DATE_TAG:-}"

# Registry holding the base and artifacts images used by --stage/--assemble.
#
# Deliberately a different package from the published llama-swap images. These
# are build inputs, not releases: a new tag is minted per project per upstream
# commit, so sharing the package would bury :unified-cuda under thousands of
# :art-* tags. It also keeps them out of reach of the delete-untagged cleanup
# in containers.yml, which is scoped to `package: llama-swap`.
ARTIFACT_REPO="${ARTIFACT_REPO:-ghcr.io/mostlygeek/llama-swap-build}"

# Upstream projects compiled into the image. ik-llama is CUDA only.
ALL_PROJECTS=(whisper sd audio llama ik-llama)

for arg in "$@"; do
    case $arg in
        --cuda)    BACKEND="cuda";   VARIANT="cuda" ;;
        --cuda13)  BACKEND="cuda";   VARIANT="cuda13" ;;
        --vulkan)  BACKEND="vulkan"; VARIANT="vulkan" ;;
        --no-cache) NO_CACHE=true ;;
        --push)     PUSH=true ;;
        --resolve)  MODE="resolve" ;;
        --assemble) MODE="assemble" ;;
        --manifest) MODE="manifest" ;;
        --platform=*)  PLATFORM="${arg#*=}" ;;
        --platforms=*) MANIFEST_PLATFORMS="${arg#*=}" ;;
        --stage=*)
            MODE="stage"
            STAGE_TARGET="${arg#*=}"
            ;;
        --help|-h)
            echo "Usage: ./build-image.sh --cuda|--cuda13|--vulkan [--no-cache]"
            echo ""
            echo "Options:"
            echo "  --cuda      Build CUDA 12 image, Pascal through Ada (sm 60-89)"
            echo "  --cuda13    Build CUDA 13 image, Ampere through Blackwell (sm 80-120)"
            echo "  --vulkan    Build Vulkan image (AMD GPUs and compatible hardware)"
            echo "  --no-cache  Force rebuild without using Docker cache"
            echo "  --platform=PLATFORM  linux/amd64 or linux/arm64 (default: this host)"
            echo "  --help, -h  Show this help message"
            echo ""
            echo "Split build (CI only):"
            echo "  --resolve        Print resolved commit hashes as key=value and exit"
            echo "  --stage=base     Build and push the builder base image"
            echo "  --stage=PROJECT  Build and push one project's artifacts image"
            echo "                   (${ALL_PROJECTS[*]})"
            echo "  --assemble       Assemble one platform's image from published artifacts"
            echo "  --manifest       Join the per-platform images into one multi-arch tag"
            echo "  --platforms=LIST Platforms --manifest joins (default: just --platform)"
            echo "  --push           Push what --assemble built"
            echo ""
            echo "Environment variables:"
            echo "  DOCKER_IMAGE_TAG     Set custom image tag (default: llama-swap:unified-<variant>,"
            echo "                       e.g. llama-swap:unified-cuda13)"
            echo "  LLAMA_REF            Pin llama.cpp to a commit, tag, or branch"
            echo "  WHISPER_REF          Pin whisper.cpp to a commit, tag, or branch"
            echo "  SD_REF               Pin stable-diffusion.cpp to a commit, tag, or branch"
            echo "  AUDIO_REF            Pin audio.cpp to a commit, tag, or branch"
            echo "  IK_LLAMA_REF         Pin ik_llama.cpp to a commit, tag, or branch (CUDA only)"
            echo "  LS_VERSION           Override llama-swap version (e.g., '170' or 'latest')"
            echo "  WHISPER_FFMPEG       Enable whisper.cpp FFmpeg support (default: yes)"
            echo "  CMAKE_CUDA_ARCHITECTURES  CUDA compute capabilities to compile natively"
            echo "                       (default: 60;61;75;86;89 for --cuda,"
            echo "                       80;86;89;90;100;120 for --cuda13 on amd64,"
            echo "                       90;100;120;121 for --cuda13 on arm64)"
            echo "  CUDA_VERSION         CUDA toolkit/runtime version as an nvidia/cuda image tag"
            echo "                       (default: 12.9.1 for --cuda, 13.3.1 for --cuda13)"
            echo "  DATE_TAG             Date suffix for published tags (default: today, UTC)"
            echo "  ARTIFACT_REPO        Registry for base and artifacts images"
            echo "                       (default: ghcr.io/mostlygeek/llama-swap-build)"
            exit 0
            ;;
    esac
done

if [[ -z "$BACKEND" ]]; then
    echo "Error: No backend specified. Please use --cuda, --cuda13 or --vulkan."
    echo ""
    echo "Usage: ./build-image.sh --cuda|--cuda13|--vulkan [--no-cache]"
    exit 1
fi

# The architecture being built for. Every stage compiles natively on a machine
# of this architecture -- CI picks the runner, a local build uses the host.
if [[ -n "${PLATFORM}" ]]; then
    case "${PLATFORM}" in
        linux/amd64) ARCH="amd64" ;;
        linux/arm64) ARCH="arm64" ;;
        *) echo "ERROR: unsupported platform '${PLATFORM}' (use linux/amd64 or linux/arm64)" >&2; exit 1 ;;
    esac
else
    case "$(uname -m)" in
        x86_64)        ARCH="amd64" ;;
        aarch64|arm64) ARCH="arm64" ;;
        *) echo "ERROR: unsupported host architecture '$(uname -m)'" >&2; exit 1 ;;
    esac
fi

# Architecture qualifier for the base and artifacts tags below. amd64 keeps the
# unsuffixed names it has always had, so introducing a second architecture
# invalidates nothing: every artifacts image published to date stays addressable
# and no amd64 project recompiles. Anything else is suffixed.
if [[ "${ARCH}" == "amd64" ]]; then
    ARCH_TAG=""
else
    ARCH_TAG="-${ARCH}"
fi

# CUDA toolkit/runtime version, matching nvidia/cuda image tags, and the compute
# capabilities compiled as SASS. Together they are what separates the two CUDA
# variants: the builder base and the runtime image are both built from
# CUDA_VERSION, so compiled binaries and their runtime libraries always come
# from the same CUDA, and the base tag is keyed on both.
#
#   cuda    CUDA 12: Pascal (60/61) through Ada (89). 12.x still ships nvcc
#           support for Maxwell, Pascal and Volta.
#   cuda13  CUDA 13: Ampere (80/86) through Blackwell, by way of Ada (89) and
#           Hopper (90). CUDA 13 removed everything below Turing from nvcc, and
#           llama-swap still ships images for those cards, hence two CUDA
#           variants rather than a version bump.
#
# The architecture lists differ by platform because the hardware does. On amd64
# the range is Ampere through Blackwell on PCIe cards. On arm64 the only NVIDIA
# parts that exist are Grace pairings, so Pascal through Ada are pointless and
# 121 -- GB10, the Blackwell GPU in DGX Spark, and the whole reason for an arm64
# image -- is essential. 90 is GH200, 100 is GB200, 120 covers a discrete
# GeForce or RTX PRO card in an aarch64 host.
#
# Two things about Blackwell shape these lists. It spans two CUDA major versions
# -- datacenter parts are compute 10.x (100, 103), GeForce, RTX PRO and GB10 are
# 12.x (120, 121) -- and PTX only JITs forward within a major version, so the two
# branches cannot stand in for each other and both have to be listed. And ggml
# rewrites any plain 12X into the architecture-specific 12Xa, because Blackwell's
# FP4 tensor core instructions exist only under that target. Writing 120 is
# therefore what gets RTX 50xx and RTX PRO Blackwell native FP4 code, and writing
# 120a here would only risk ik_llama.cpp, the one project with no such rewrite.
# The 12Xa targets are real-only though: nothing in the 12.x family is reachable
# by JIT, so a 12.x part runs only if it is named here.
#
# Jetson's 87 and Thor's 110 are left out: those boards need an l4t base image,
# not nvidia/cuda. Only the CUDA base reads these as build args; the projects
# inherit the architectures through the base image's ENV.
case "${VARIANT}:${ARCH}" in
    cuda13:arm64)
        CUDA_VERSION="${CUDA_VERSION:-13.3.1}"
        CMAKE_CUDA_ARCHITECTURES="${CMAKE_CUDA_ARCHITECTURES:-90;100;120;121}"
        ;;
    cuda13:*)
        CUDA_VERSION="${CUDA_VERSION:-13.3.1}"
        CMAKE_CUDA_ARCHITECTURES="${CMAKE_CUDA_ARCHITECTURES:-80;86;89;90;100;120}"
        ;;
    *)
        CUDA_VERSION="${CUDA_VERSION:-12.9.1}"
        CMAKE_CUDA_ARCHITECTURES="${CMAKE_CUDA_ARCHITECTURES:-60;61;75;86;89}"
        ;;
esac

# Resolved once here rather than per stage: a build that crosses UTC midnight
# would otherwise tag its platforms and its manifest with different dates.
DATE_TAG="${DATE_TAG:-$(date -u +%Y-%m-%d)}"

# The value becomes part of nvidia/cuda image tags, so a bad version should
# fail here with a clear message rather than a docker "image not found".
if [[ "$BACKEND" == "cuda" && ! "$CUDA_VERSION" =~ ^[0-9]+\.[0-9]+(\.[0-9]+)?$ ]]; then
    echo "ERROR: CUDA_VERSION '${CUDA_VERSION}' is not a valid nvidia/cuda version (e.g., 12.9.1)" >&2
    exit 1
fi

# --resolve emits machine-readable output only, so the progress chatter that
# every other mode prints has to stay off it.
log() {
    if [[ "$MODE" != "resolve" ]]; then
        echo "$@"
    fi
}

DOCKER_IMAGE_TAG="${DOCKER_IMAGE_TAG:-llama-swap:unified-${VARIANT}}"

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

# The builder base is addressed by its own Dockerfile's content, so the
# backends move independently and a base edit propagates into every project
# tag below without anything having to inspect the base.
base_tag() {
    local h
    # The CUDA base bakes CMAKE_CUDA_ARCHITECTURES into its ENV and the
    # CUDA_VERSION into its FROM line, so an override of either must change
    # the tag even though the Dockerfile is unchanged. That is also what keeps
    # the cuda and cuda13 variants apart: they share base-cuda.Dockerfile and
    # differ only in these two values.
    h="$( {
        sha256sum "${SCRIPT_DIR}/base-${BACKEND}.Dockerfile"
        if [[ "${BACKEND}" == "cuda" ]]; then
            echo "${CMAKE_CUDA_ARCHITECTURES}"
            echo "${CUDA_VERSION}"
        fi
    } | sha256sum | cut -c1-12)"
    echo "${ARTIFACT_REPO}:base-${VARIANT}${ARCH_TAG}-${h}"
}

# A project's artifacts are determined by the upstream commit plus its recipe:
# its own Dockerfile, its install script, the base it compiles from, and the
# build args it reads. All of them are files or tags -- nothing is inferred.
artifact_tag() {
    local project="$1" commit recipe
    commit="$(project_hash "${project}")" || return 1
    recipe="$( {
        cat "${SCRIPT_DIR}/${project}.Dockerfile"
        cat "${SCRIPT_DIR}/install-${project}.sh"
        echo "base=$(base_tag)"
        echo "WHISPER_FFMPEG=${WHISPER_FFMPEG}"
    } | sha256sum | cut -c1-8 )"
    echo "${ARTIFACT_REPO}:art-${project}-${VARIANT}${ARCH_TAG}-${commit:0:12}-${recipe}"
}

image_exists() {
    docker buildx imagetools inspect "$1" >/dev/null 2>&1
}

log "=========================================="
case "$MODE" in
    stage)    log "llama-swap Build (${STAGE_TARGET}, ${VARIANT}, ${ARCH})" ;;
    assemble) log "llama-swap Unified Assemble (${VARIANT}, ${ARCH})" ;;
    manifest) log "llama-swap Manifest (${VARIANT})" ;;
    *)        log "llama-swap Unified Build (${VARIANT}, ${ARCH})" ;;
esac
log "=========================================="
log ""

if [[ "$MODE" == "stage" && "$STAGE_TARGET" != "base" ]]; then
    if ! printf '%s\n' "${ALL_PROJECTS[@]}" | grep -qx -- "${STAGE_TARGET}"; then
        echo "ERROR: unknown stage target '${STAGE_TARGET}'" >&2
        echo "  Known targets: base ${ALL_PROJECTS[*]}" >&2
        exit 1
    fi
    if ! backend_projects | grep -qx -- "${STAGE_TARGET}"; then
        echo "ERROR: project '${STAGE_TARGET}' is not built for the ${VARIANT} variant" >&2
        exit 1
    fi
fi

# ── --manifest: join the per-platform images into one tag ─────────────
#
# Runs after every platform's assemble job has pushed its arch-qualified image.
# imagetools reads those from the registry and writes a manifest list, so this
# needs no builder and no image pull.

if [[ "$MODE" == "manifest" ]]; then
    MANIFEST_ARCHES=()
    for platform in ${MANIFEST_PLATFORMS//,/ }; do
        case "${platform}" in
            linux/amd64) MANIFEST_ARCHES+=("amd64") ;;
            linux/arm64) MANIFEST_ARCHES+=("arm64") ;;
            *) echo "ERROR: unsupported platform '${platform}' in --platforms" >&2; exit 1 ;;
        esac
    done
    if [[ ${#MANIFEST_ARCHES[@]} -eq 0 ]]; then
        MANIFEST_ARCHES=("${ARCH}")
    fi

    # Every source has to be present. Publishing a manifest that silently lost a
    # platform would leave users of that architecture pulling a stale image with
    # nothing to indicate the build had failed.
    MISSING=()
    for arch in "${MANIFEST_ARCHES[@]}"; do
        for suffix in "" "-rootless"; do
            tag="${DOCKER_IMAGE_TAG}-${arch}${suffix}"
            if image_exists "${tag}"; then
                echo "  ${tag}"
            else
                echo "  ${tag}  [MISSING]"
                MISSING+=("${tag}")
            fi
        done
    done
    if [[ ${#MISSING[@]} -gt 0 ]]; then
        echo "" >&2
        echo "ERROR: cannot publish the manifest, these images were never pushed:" >&2
        printf '  - %s\n' "${MISSING[@]}" >&2
        echo "" >&2
        echo "The assemble job for that platform must have failed." >&2
        exit 1
    fi

    for suffix in "" "-rootless"; do
        srcs=()
        for arch in "${MANIFEST_ARCHES[@]}"; do
            srcs+=("${DOCKER_IMAGE_TAG}-${arch}${suffix}")
        done
        echo ""
        echo "Publishing ${DOCKER_IMAGE_TAG}${suffix} (${MANIFEST_ARCHES[*]})"
        docker buildx imagetools create \
            -t "${DOCKER_IMAGE_TAG}${suffix}" \
            -t "${DOCKER_IMAGE_TAG}${suffix}-${DATE_TAG}" \
            "${srcs[@]}"
    done

    echo ""
    echo "Published:"
    echo "  ${DOCKER_IMAGE_TAG}"
    echo "  ${DOCKER_IMAGE_TAG}-${DATE_TAG}"
    echo "  ${DOCKER_IMAGE_TAG}-rootless"
    echo "  ${DOCKER_IMAGE_TAG}-rootless-${DATE_TAG}"
    exit 0
fi

# ── Resolve refs ──────────────────────────────────────────────────────
#
# A full 40-char SHA short-circuits resolve_ref without a network call, so CI
# resolves every ref once up front and passes the hashes to the per-project and
# assemble jobs. That keeps a moving branch from resolving differently in each
# job and assembling an image out of mismatched artifacts.

if [[ -n "${LLAMA_REF:-}" ]]; then
    LLAMA_HASH=$(resolve_ref "${LLAMA_REPO}" "${LLAMA_REF}") || exit 1
    log "llama.cpp: ${LLAMA_REF} -> ${LLAMA_HASH}"
else
    LLAMA_HASH=$(get_latest_hash "${LLAMA_REPO}")
    [[ -n "${LLAMA_HASH}" ]] || { echo "ERROR: Could not determine latest commit for llama.cpp" >&2; exit 1; }
    log "llama.cpp: latest HEAD: ${LLAMA_HASH}"
fi

if [[ -n "${WHISPER_REF:-}" ]]; then
    WHISPER_HASH=$(resolve_ref "${WHISPER_REPO}" "${WHISPER_REF}") || exit 1
    log "whisper.cpp: ${WHISPER_REF} -> ${WHISPER_HASH}"
else
    WHISPER_HASH=$(get_latest_hash "${WHISPER_REPO}")
    [[ -n "${WHISPER_HASH}" ]] || { echo "ERROR: Could not determine latest commit for whisper.cpp" >&2; exit 1; }
    log "whisper.cpp: latest HEAD: ${WHISPER_HASH}"
fi

if [[ -n "${SD_REF:-}" ]]; then
    SD_HASH=$(resolve_ref "${SD_REPO}" "${SD_REF}") || exit 1
    log "stable-diffusion.cpp: ${SD_REF} -> ${SD_HASH}"
else
    SD_HASH=$(get_latest_hash "${SD_REPO}")
    [[ -n "${SD_HASH}" ]] || { echo "ERROR: Could not determine latest commit for stable-diffusion.cpp" >&2; exit 1; }
    log "stable-diffusion.cpp: latest HEAD: ${SD_HASH}"
fi

if [[ -n "${AUDIO_REF:-}" ]]; then
    AUDIO_HASH=$(resolve_ref "${AUDIO_REPO}" "${AUDIO_REF}") || exit 1
    log "audio.cpp: ${AUDIO_REF} -> ${AUDIO_HASH}"
else
    AUDIO_HASH=$(get_latest_hash "${AUDIO_REPO}")
    [[ -n "${AUDIO_HASH}" ]] || { echo "ERROR: Could not determine latest commit for audio.cpp" >&2; exit 1; }
    log "audio.cpp: latest HEAD: ${AUDIO_HASH}"
fi

# CUDA only, but --resolve always reports it so one setup job feeds both backends
if [[ "$BACKEND" == "cuda" || "$MODE" == "resolve" ]]; then
    if [[ -n "${IK_LLAMA_REF:-}" ]]; then
        IK_LLAMA_HASH=$(resolve_ref "${IK_LLAMA_REPO}" "${IK_LLAMA_REF}") || exit 1
        log "ik_llama.cpp: ${IK_LLAMA_REF} -> ${IK_LLAMA_HASH}"
    else
        IK_LLAMA_HASH=$(get_latest_hash "${IK_LLAMA_REPO}")
        [[ -n "${IK_LLAMA_HASH}" ]] || { echo "ERROR: Could not determine latest commit for ik_llama.cpp" >&2; exit 1; }
        log "ik_llama.cpp: latest HEAD: ${IK_LLAMA_HASH}"
    fi
else
    IK_LLAMA_HASH="n/a"
    log "ik_llama.cpp: skipped (vulkan build)"
fi

if [[ -n "${LS_VERSION:-}" ]]; then
    LS_HASH=$(resolve_ref "${LLAMA_SWAP_REPO}" "${LS_VERSION}") || exit 1
    log "llama-swap: ${LS_VERSION} -> ${LS_HASH}"
else
    LS_HASH=$(get_latest_hash "${LLAMA_SWAP_REPO}")
    [[ -n "${LS_HASH}" ]] || { echo "ERROR: Could not determine latest commit for llama-swap" >&2; exit 1; }
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

CACHE_ARGS=()
if [[ "$NO_CACHE" == true ]]; then
    CACHE_ARGS+=(--no-cache)
    echo "Note: Building without cache"
fi

BASE_TAG="$(base_tag)"

PLATFORM_ARGS=()
if [[ -n "${PLATFORM}" ]]; then
    PLATFORM_ARGS+=(--platform="${PLATFORM}")
fi

# What the assembled image is tagged. --assemble builds one platform's half of
# a manifest list, so it is published arch-qualified and --manifest joins the
# halves under DOCKER_IMAGE_TAG itself. A local build has only its own platform
# and wants the plain name.
if [[ "${MODE}" == "assemble" ]]; then
    RUNTIME_TAG="${DOCKER_IMAGE_TAG}-${ARCH}"
else
    RUNTIME_TAG="${DOCKER_IMAGE_TAG}"
fi
ROOTLESS_TAG="${RUNTIME_TAG}-rootless"

# ── Builders ──────────────────────────────────────────────────────────

build_base() {
    local output=("$@")
    local args=()
    if [[ "${BACKEND}" == "cuda" ]]; then
        args+=(--build-arg "CMAKE_CUDA_ARCHITECTURES=${CMAKE_CUDA_ARCHITECTURES}")
        args+=(--build-arg "CUDA_VERSION=${CUDA_VERSION}")
    fi
    echo ""
    echo "=========================================="
    echo "Building builder base (${VARIANT})..."
    echo "=========================================="
    echo ""
    DOCKER_BUILDKIT=1 docker buildx build "${output[@]}" \
        -f "${SCRIPT_DIR}/base-${BACKEND}.Dockerfile" \
        -t "${BASE_TAG}" \
        "${args[@]}" \
        "${PLATFORM_ARGS[@]}" \
        "${CACHE_ARGS[@]}" \
        "${SCRIPT_DIR}"
}

build_project() {
    local project="$1"; shift
    local output=("$@")
    local tag
    tag="$(artifact_tag "${project}")"
    echo ""
    echo "=========================================="
    echo "Building ${project} artifacts (${VARIANT})..."
    echo "=========================================="
    echo ""
    DOCKER_BUILDKIT=1 docker buildx build "${output[@]}" \
        -f "${SCRIPT_DIR}/${project}.Dockerfile" \
        -t "${tag}" \
        --build-arg "BUILDER_BASE=${BASE_TAG}" \
        --build-arg "BACKEND=${BACKEND}" \
        --build-arg "WHISPER_FFMPEG=${WHISPER_FFMPEG}" \
        --build-arg "WHISPER_COMMIT_HASH=${WHISPER_HASH}" \
        --build-arg "SD_COMMIT_HASH=${SD_HASH}" \
        --build-arg "AUDIO_COMMIT_HASH=${AUDIO_HASH}" \
        --build-arg "LLAMA_COMMIT_HASH=${LLAMA_HASH}" \
        --build-arg "IK_LLAMA_COMMIT_HASH=${IK_LLAMA_HASH}" \
        "${PLATFORM_ARGS[@]}" \
        "${CACHE_ARGS[@]}" \
        "${SCRIPT_DIR}"
}

build_runtime() {
    local args=(
        --build-arg "BACKEND=${BACKEND}"
        --build-arg "BUILDER_BASE=${BASE_TAG}"
        --build-arg "LS_VERSION=${LS_HASH}"
        --build-arg "LLAMA_COMMIT_HASH=${LLAMA_HASH}"
        --build-arg "WHISPER_COMMIT_HASH=${WHISPER_HASH}"
        --build-arg "SD_COMMIT_HASH=${SD_HASH}"
        --build-arg "AUDIO_COMMIT_HASH=${AUDIO_HASH}"
        --build-arg "IK_LLAMA_COMMIT_HASH=${IK_LLAMA_HASH}"
        --build-arg "CUDA_VERSION=${CUDA_VERSION}"
        --build-arg "CMAKE_CUDA_ARCHITECTURES=${CMAKE_CUDA_ARCHITECTURES}"
        --build-arg "VARIANT=${VARIANT}"
    )
    local project
    while read -r project; do
        args+=(--build-arg "$(project_image_arg "${project}")=$(artifact_tag "${project}")")
    done < <(backend_projects)

    echo ""
    echo "=========================================="
    echo "Building runtime image (${VARIANT})..."
    echo "=========================================="
    echo ""
    DOCKER_BUILDKIT=1 docker buildx build --load \
        -f "${SCRIPT_DIR}/runtime.Dockerfile" \
        -t "${RUNTIME_TAG}" \
        "${args[@]}" "${PLATFORM_ARGS[@]}" "${CACHE_ARGS[@]}" \
        "${SCRIPT_DIR}"
}

# The rootless image is the finished runtime image plus a non-root user.
#
# It builds with the `default` builder -- the docker daemon -- on purpose. Its
# FROM names the image the step above just loaded, and only the daemon has that
# image: a docker-container builder resolves FROM through the registry, where it
# found the *previously published* runtime image and silently layered the user
# onto a build one night old.
build_rootless() {
    echo ""
    echo "=========================================="
    echo "Building rootless image (${VARIANT}, ${ARCH})..."
    echo "=========================================="
    echo ""
    DOCKER_BUILDKIT=1 docker buildx build --builder default --load \
        -f "${SCRIPT_DIR}/rootless.Dockerfile" \
        -t "${ROOTLESS_TAG}" \
        --build-arg "BASE_IMAGE=${RUNTIME_TAG}" \
        "${PLATFORM_ARGS[@]}" \
        "${SCRIPT_DIR}"
}

# ── --stage: build and publish one image ──────────────────────────────

if [[ "$MODE" == "stage" ]]; then
    if [[ "$STAGE_TARGET" == "base" ]]; then
        TARGET_TAG="${BASE_TAG}"
    else
        TARGET_TAG="$(artifact_tag "${STAGE_TARGET}")"
    fi

    echo ""
    echo "Image: ${TARGET_TAG}"

    if [[ "$NO_CACHE" != true ]] && image_exists "${TARGET_TAG}"; then
        echo "Already published for this commit and recipe, nothing to build."
        exit 0
    fi

    if [[ "$STAGE_TARGET" == "base" ]]; then
        build_base --push
    else
        if ! image_exists "${BASE_TAG}"; then
            echo "" >&2
            echo "ERROR: builder base ${BASE_TAG} has not been published." >&2
            echo "  Run: build-image.sh --${VARIANT} --stage=base" >&2
            exit 1
        fi
        build_project "${STAGE_TARGET}" --push
    fi

    echo ""
    echo "Published: ${TARGET_TAG}"
    exit 0
fi

# ── --assemble: runtime image from published artifacts ────────────────

if [[ "$MODE" == "assemble" ]]; then
    MISSING=()
    echo ""
    echo "Inputs:"
    for tag in "${BASE_TAG}" $(while read -r p; do artifact_tag "$p"; done < <(backend_projects)); do
        if image_exists "${tag}"; then
            echo "  ${tag}"
        else
            echo "  ${tag}  [MISSING]"
            MISSING+=("${tag}")
        fi
    done

    if [[ ${#MISSING[@]} -gt 0 ]]; then
        echo "" >&2
        echo "ERROR: cannot assemble, these images were never published:" >&2
        printf '  - %s\n' "${MISSING[@]}" >&2
        echo "" >&2
        echo "Run the matching --stage=base / --stage=PROJECT build first." >&2
        exit 1
    fi
else
    # Unified: compile everything locally, then assemble from the local images.
    build_base --load
    while read -r project; do
        build_project "${project}" --load
    done < <(backend_projects)
fi

build_runtime

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
    if ! docker run --rm --entrypoint which "${RUNTIME_TAG}" "${binary}" >/dev/null 2>&1; then
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
    echo "  ./build-image.sh --${VARIANT} --no-cache"
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
if ! docker run --rm --entrypoint grep "${RUNTIME_TAG}" \
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

if ! docker run "${SMOKE_ARGS[@]}" --entrypoint audiocpp_server "${RUNTIME_TAG}" --help >/dev/null; then
    echo "ERROR: audiocpp_server --help failed; the binary or its runtime"
    echo "       libraries are broken in the image."
    exit 1
fi

echo "audio.cpp verified: deployment build (compiled model spec catalog), binary runs"

# The entrypoint turns LLAMA_SWAP_* variables into flags. Point -config at a path
# that cannot exist and look for it in llama-swap's own output: nothing but
# run.sh can have put it on the command line. run.sh echoes the command line
# too, so its line is filtered out first -- otherwise this would pass on our own
# echo without the binary ever having received the flag.
#
# The container exits non-zero here by design, so the output is captured rather
# than piped: under `set -o pipefail` a pipeline would report the expected
# failure as a failed check.
ENTRYPOINT_PROBE="/nonexistent/llama-swap-env-probe.yaml"
PROBE_OUT="$(docker run --rm -e "LLAMA_SWAP_CONFIG=${ENTRYPOINT_PROBE}" "${RUNTIME_TAG}" 2>&1 || true)"
if ! grep -v '^run\.sh:' <<<"${PROBE_OUT}" | grep -qF -- "${ENTRYPOINT_PROBE}"; then
    echo "ERROR: LLAMA_SWAP_CONFIG did not reach llama-swap as -config;"
    echo "       run.sh is not translating environment variables into flags."
    echo "Container output:"
    echo "${PROBE_OUT}"
    exit 1
fi

# The starter config the image ships has to load, or a container started with
# nothing mounted exits on its first run. -validate only loads the config -- no
# listener, no hardware detection -- so this is cheap and catches the config
# drifting into something unloadable. This calls llama-swap directly with
# arguments (bypassing run.sh's env-var mapping, which does not cover
# -validate -- see run.sh for why) at the same path run.sh defaults -config to.
VALIDATE_OUT="$(docker run --rm "${RUNTIME_TAG}" -config /etc/llama-swap/config/config.yaml -validate 2>&1 || true)"
if ! grep -q "config is valid" <<<"${VALIDATE_OUT}"; then
    echo "ERROR: the config shipped at /etc/llama-swap/config/config.yaml does not load,"
    echo "       so this image exits immediately when run with nothing mounted."
    echo "Container output:"
    echo "${VALIDATE_OUT}"
    exit 1
fi

# A value that is not a boolean has to stop the container rather than be read as
# "off", which would leave the server running with a setting the operator
# believes they turned on.
if docker run --rm -e LLAMA_SWAP_WATCH_CONFIG=notabool "${RUNTIME_TAG}" >/dev/null 2>&1; then
    echo "ERROR: LLAMA_SWAP_WATCH_CONFIG=notabool was accepted; run.sh should"
    echo "       refuse a non-boolean instead of treating it as false."
    exit 1
fi

echo "entrypoint verified: LLAMA_SWAP_* variables become flags"

build_rootless

echo "Rootless image built: ${ROOTLESS_TAG}"

# --push publishes exactly what was just built and verified. The date-suffixed
# tags are minted by --manifest instead, so a user-facing tag never names a
# single platform's image.
if [[ "$PUSH" == true ]]; then
    echo ""
    echo "=========================================="
    echo "Pushing images..."
    echo "=========================================="
    echo ""
    docker push "${RUNTIME_TAG}"
    docker push "${ROOTLESS_TAG}"
fi

echo ""
echo "=========================================="
echo "Build complete!"
echo "=========================================="
echo ""
echo "Image tags:"
echo "  ${RUNTIME_TAG}"
echo "  ${ROOTLESS_TAG}"
echo ""
echo "Built with:"
echo "  platform:             linux/${ARCH}"
echo "  llama.cpp:            ${LLAMA_HASH}"
echo "  whisper.cpp:          ${WHISPER_HASH}"
echo "  stable-diffusion.cpp: ${SD_HASH}"
echo "  audio.cpp:            ${AUDIO_HASH}"
if [[ "$BACKEND" == "cuda" ]]; then
    echo "  ik_llama.cpp:         ${IK_LLAMA_HASH}"
    echo "  CUDA version:         ${CUDA_VERSION}"
    echo "  CUDA architectures:   ${CMAKE_CUDA_ARCHITECTURES}"
fi
echo "  llama-swap:           $(docker run --rm --entrypoint cat "${RUNTIME_TAG}" /versions.txt | grep llama-swap | cut -d' ' -f2-)"
echo ""
if [[ "$BACKEND" == "vulkan" ]]; then
    echo "Run with:"
    echo "  docker run -it --rm --device /dev/dri:/dev/dri ${RUNTIME_TAG}"
    echo ""
    echo "Note: For AMD GPUs, you may also need:"
    echo "  docker run -it --rm --device /dev/dri:/dev/dri --group-add video ${RUNTIME_TAG}"
else
    echo "Run with:"
    echo "  docker run -it --rm --gpus all ${RUNTIME_TAG}"
fi
