#!/bin/bash

set -euo pipefail

cd $(dirname "$0")

# use this to test locally, example:
# GITHUB_TOKEN=$(gh auth token) LOG_DEBUG=1 DEBUG_ABORT_BUILD=1 ./docker/build-container.sh rocm
# you need read:package scope on the token. Generate a personal access token with
# the scopes: gist, read:org, repo, write:packages
# then: gh auth login (and copy/paste the new token)

LOG_DEBUG=${LOG_DEBUG:-0}
DEBUG_ABORT_BUILD=${DEBUG_ABORT_BUILD:-}

log_debug() {
    if [ "$LOG_DEBUG" = "1" ]; then
        echo "[DEBUG] $*"
    fi
}

log_info() {
    echo "[INFO] $*"
}

ARCH=$1
PUSH_IMAGES=${2:-false}

# When set, each pushed tag's manifest/index digest is written to
# $DIGEST_DIR/<tag>.digest. verify-pull.sh reads these to assert the
# registry still serves the digest this run pushed (catches cleanup
# replacing or pruning a tag's index after the build). No-op unless we
# actually push — non-push builds wouldn't be on the registry to verify.
DIGEST_DIR=${DIGEST_DIR:-}

ALLOWED_ARCHS=("intel" "vulkan" "musa" "cuda" "cuda13" "cpu" "rocm")
if [[ ! " ${ALLOWED_ARCHS[@]} " =~ " ${ARCH} " ]]; then
  log_info "Error: ARCH must be one of the following: ${ALLOWED_ARCHS[@]}"
  exit 1
fi

if [[ -z "${GITHUB_TOKEN:-}" ]]; then
  log_info "Error: GITHUB_TOKEN is not set or is empty."
  exit 1
fi

# llama.cpp + stable-diffusion.cpp upstream base images. Override via
# BASE_LLAMACPP_IMAGE / BASE_SDCPP_IMAGE to test against forks.
BASE_IMAGE=${BASE_LLAMACPP_IMAGE:-ghcr.io/ggml-org/llama.cpp}
SD_IMAGE=${BASE_SDCPP_IMAGE:-ghcr.io/leejet/stable-diffusion.cpp}

# Destination namespace — defaults to the current GitHub repo so fork
# CI publishes to its own ghcr.io.
LS_REPO=${GITHUB_REPOSITORY:-mostlygeek/llama-swap}

# Where to pull the llama-swap release tarball from. Decoupled from
# LS_REPO so forks (which usually have no releases) still build against
# the upstream binary unless they override this.
LS_BINARY_REPO=${LS_BINARY_REPO:-mostlygeek/llama-swap}

# Authenticated request — unauth'd github.com API is 60/hr per IP and
# GHA runners share IPs, so the call regularly returns rate-limit JSON
# and `.tag_name` then resolves to "null", producing a bogus `vnull`.
LS_VER=$(curl -s -H "Authorization: Bearer $GITHUB_TOKEN" \
    "https://api.github.com/repos/${LS_BINARY_REPO}/releases/latest" \
    | jq -r .tag_name | sed 's/v//')

if [[ -z "$LS_VER" || "$LS_VER" == "null" ]]; then
    log_info "Error: could not resolve latest llama-swap release tag from ${LS_BINARY_REPO}"
    exit 1
fi

# Walks llama.cpp package versions paginated to find the newest tag
# matching a prefix (e.g. "server" for cpu, "server-cuda" for cuda).
fetch_llama_tag() {
    local tag_prefix=$1
    local page=1
    local per_page=100

    while true; do
        log_debug "Fetching page $page for tag prefix: $tag_prefix"

        local response=$(curl -s -H "Authorization: Bearer $GITHUB_TOKEN" \
            "https://api.github.com/users/ggml-org/packages/container/llama.cpp/versions?per_page=${per_page}&page=${page}")

        if echo "$response" | jq -e '.message' > /dev/null 2>&1; then
            local error_msg=$(echo "$response" | jq -r '.message')
            log_info "GitHub API error: $error_msg"
            return 1
        fi

        if [ "$(echo "$response" | jq 'length')" -eq 0 ]; then
            log_debug "No more pages (empty response)"
            return 1
        fi

        local found_tag=$(echo "$response" | jq -r \
            ".[] | select(.metadata.container.tags[]? | startswith(\"$tag_prefix\")) | .metadata.container.tags[] | select(startswith(\"$tag_prefix\"))" \
            | sort -r | head -n1)

        if [ -n "$found_tag" ]; then
            log_debug "Found tag: $found_tag on page $page"
            echo "$found_tag" | awk -F '-' '{print $NF}'
            return 0
        fi

        page=$((page + 1))
        if [ $page -gt 50 ]; then
            log_info "Reached pagination safety limit (50 pages)"
            return 1
        fi
    done
}

if [ "$ARCH" == "cpu" ]; then
    LCPP_TAG=$(fetch_llama_tag "server")
    BASE_TAG=server-${LCPP_TAG}
else
    LCPP_TAG=$(fetch_llama_tag "server-${ARCH}")
    BASE_TAG=server-${ARCH}-${LCPP_TAG}
fi

SD_TAG=master-${ARCH}

if [[ -z "$LCPP_TAG" ]]; then
    log_info "Abort: Could not find llama-server container for arch: $ARCH"
    exit 1
fi
log_info "LCPP_TAG: $LCPP_TAG"

if [[ ! -z "$DEBUG_ABORT_BUILD" ]]; then
    log_info "Abort: DEBUG_ABORT_BUILD set"
    exit 0
fi

# Platforms to build for. Driven by the workflow's per-backend matrix
# entry (e.g. `linux/amd64,linux/arm64` for cpu). Default keeps a bare
# `./build-container.sh <arch>` invocation working locally.
PLATFORMS=${PLATFORMS:-linux/amd64}
MULTI_ARCH=0
case ",${PLATFORMS}," in
    *,*,*) MULTI_ARCH=1 ;;
esac

# buildx output mode:
#   --push    publish a tagged manifest list directly.
#   --load    smoke, single arch — into local dockerd so sd-server can FROM.
#   (none)    smoke, multi-arch — cacheonly; buildx can't --load an index.
if [ "$PUSH_IMAGES" == "true" ]; then
    BUILDX_OUTPUT="--push"
elif [ "$MULTI_ARCH" -eq 1 ]; then
    BUILDX_OUTPUT=""
else
    BUILDX_OUTPUT="--load"
fi

# GHA layer cache, scoped per backend+arch+container-type to keep
# amd64/arm64 caches disjoint. Skipped outside GHA — `type=gha` needs
# ACTIONS_RUNTIME_TOKEN.
buildx_cache_flags() {
    local scope=$1
    if [ "${GITHUB_ACTIONS:-}" = "true" ]; then
        echo "--cache-from type=gha,scope=${scope} --cache-to type=gha,mode=max,scope=${scope}"
    fi
}

PLATFORM_SLUG=$(echo "$PLATFORMS" | tr ',/' '--')

# Wraps `docker buildx build` and, when DIGEST_DIR is set on a push
# build, extracts the pushed manifest digest from the buildx metadata
# file into $DIGEST_DIR/<digest_name>.digest. For backends that re-tag
# in a second buildx pass (sd-server layer for musa/vulkan), passing
# the same digest_name overwrites the base build's digest — which is
# correct, since the second push is what the tag resolves to.
buildx_run() {
    local digest_name="$1"; shift
    if [ -n "$DIGEST_DIR" ] && [ "$PUSH_IMAGES" = "true" ]; then
        mkdir -p "$DIGEST_DIR"
        local meta
        meta=$(mktemp)
        docker buildx build --metadata-file "$meta" "$@"
        local digest
        digest=$(jq -r '."containerimage.digest" // empty' "$meta")
        rm -f "$meta"
        if [ -z "$digest" ]; then
            log_info "Error: no containerimage.digest in buildx metadata for ${digest_name}"
            return 1
        fi
        echo "$digest" > "$DIGEST_DIR/${digest_name}.digest"
        log_info "Recorded digest ${digest} -> ${DIGEST_DIR}/${digest_name}.digest"
    else
        docker buildx build "$@"
    fi
}

for CONTAINER_TYPE in non-root root; do
    CONTAINER_TAG="ghcr.io/${LS_REPO}:v${LS_VER}-${ARCH}-${LCPP_TAG}"
    CONTAINER_LATEST="ghcr.io/${LS_REPO}:${ARCH}"
    USER_UID=0
    USER_GID=0
    USER_HOME=/root

    if [ "$CONTAINER_TYPE" == "non-root" ]; then
        CONTAINER_TAG="${CONTAINER_TAG}-non-root"
        CONTAINER_LATEST="${CONTAINER_LATEST}-non-root"
        USER_UID=10001
        USER_GID=10001
        USER_HOME=/app
    fi

    CACHE_FLAGS=$(buildx_cache_flags "${ARCH}-${PLATFORM_SLUG}-${CONTAINER_TYPE}")

    # Digest filename mirrors verify-pull.sh's tag-suffix loop:
    # `<arch>` for root, `<arch>-non-root` for non-root.
    DIGEST_NAME="${ARCH}"
    [ "$CONTAINER_TYPE" = "non-root" ] && DIGEST_NAME="${ARCH}-non-root"

    log_info "Building $CONTAINER_TYPE $CONTAINER_TAG $LS_VER on $PLATFORMS"
    buildx_run "$DIGEST_NAME" $BUILDX_OUTPUT $CACHE_FLAGS --provenance=false \
        --platform "$PLATFORMS" \
        -f llama-swap.Containerfile \
        --build-arg BASE_IMAGE=${BASE_IMAGE} --build-arg BASE_TAG=${BASE_TAG} \
        --build-arg LS_VER=${LS_VER} --build-arg LS_REPO=${LS_BINARY_REPO} \
        --build-arg UID=${USER_UID} --build-arg GID=${USER_GID} --build-arg USER_HOME=${USER_HOME} \
        -t ${CONTAINER_TAG} -t ${CONTAINER_LATEST} .

    # sd-server layer for backends that bundle stable-diffusion.cpp.
    # Only run on the push path: buildx then pulls FROM the just-pushed
    # base via the registry. The docker-container buildx driver has its
    # own image store separate from dockerd, so a `--load`'d base from
    # the smoke path isn't visible here — skipped to avoid 404 on FROM.
    case "$ARCH" in
        "musa" | "vulkan")
            if [ "$PUSH_IMAGES" != "true" ]; then
                log_info "Skipping sd-server layer for $ARCH (smoke build, base not in registry)"
            else
                log_info "Adding sd-server to $CONTAINER_TAG"
                SD_CACHE=$(buildx_cache_flags "${ARCH}-${PLATFORM_SLUG}-${CONTAINER_TYPE}-sd")
                buildx_run "$DIGEST_NAME" $BUILDX_OUTPUT $SD_CACHE --provenance=false \
                    --platform "$PLATFORMS" \
                    -f llama-swap-sd.Containerfile \
                    --build-arg BASE=${CONTAINER_TAG} \
                    --build-arg SD_IMAGE=${SD_IMAGE} --build-arg SD_TAG=${SD_TAG} \
                    --build-arg UID=${USER_UID} --build-arg GID=${USER_GID} \
                    -t ${CONTAINER_TAG} -t ${CONTAINER_LATEST} .
            fi ;;
    esac
done
