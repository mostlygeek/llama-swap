#!/bin/bash
#
# Build script for unified container with version pinning
#
# Usage:
#   ./build-image-rootless.sh --cuda                              # Build CUDA image
#   ./build-image-rootless.sh --vulkan                            # Build Vulkan image
#   ./build-image-rootless.sh --cuda --no-cache                   # Build without cache
#   LLAMA_REF=b1234 ./build-image-rootless.sh --vulkan            # Pin llama.cpp to a commit hash
#   LLAMA_REF=v1.2.3 ./build-image-rootless.sh --cuda             # Pin llama.cpp to a tag
#   WHISPER_REF=v1.0.0 ./build-image-rootless.sh --vulkan         # Pin whisper.cpp to a tag
#   SD_REF=master ./build-image-rootless.sh --cuda                # Pin stable-diffusion.cpp to a branch
#   LS_VERSION=170 ./build-image-rootless.sh --cuda               # Override llama-swap version
#   IK_LLAMA_REF=main ./build-image-rootless.sh --cuda            # Pin ik_llama.cpp to main branch (CUDA only)
#

set -euo pipefail

BACKEND=""
NO_CACHE=false

for arg in "$@"; do
    case $arg in
        --cuda)
            BACKEND="cuda"
            ;;
        --cuda13)
            BACKEND="cuda13"
            ;;
        --vulkan)
            BACKEND="vulkan"
            ;;
        --no-cache)
            NO_CACHE=true
            ;;
        --help|-h)
            echo "Usage: ./build-image-rootless.sh --cuda|--vulkan"
            echo ""
            echo "Options:"
            echo "  --cuda      Build CUDA image (NVIDIA GPUs)"
            echo "  --cuda13    Build CUDA 13 image (NVIDIA GPUs)"
            echo "  --vulkan    Build Vulkan image (AMD GPUs and compatible hardware)"
            echo "  --no-cache  Force rebuild without using Docker cache"
            echo "  --help, -h  Show this help message"
            echo ""
            echo "Environment variables:"
            echo "  DOCKER_IMAGE_TAG     Set custom image tag (default: llama-swap:unified-cuda or llama-swap:unified-vulkan)"
            exit 0
            ;;
    esac
done

if [[ -z "$BACKEND" ]]; then
    echo "Error: No backend specified. Please use --cuda, --cuda13, or --vulkan."
    echo ""
    echo "Usage: ./build-image-rootless.sh --cuda|--cuda13|--vulkan"
    exit 1
fi

ARCH=$(uname -m)
case "$ARCH" in
    x86_64) ARCH="amd64" ;;
    aarch64|arm64) ARCH="arm64" ;;
    *) echo "FATAL: Unsupported architecture: $ARCH" >&2; exit 1 ;;
esac

DOCKER_IMAGE_TAG="${DOCKER_IMAGE_TAG:-llama-swap:unified-${BACKEND}-${ARCH}}"

echo ""
echo "=========================================="
echo "Building rootless image..."
echo "=========================================="
echo ""

ROOTLESS_TAG="${DOCKER_IMAGE_TAG}-rootless"
BUILD_ARGS=()
if [[ "$NO_CACHE" == "true" ]]; then
    BUILD_ARGS+=(--no-cache)
fi
docker buildx build "${BUILD_ARGS[@]}" --load -t "${ROOTLESS_TAG}" - <<EOF
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
echo "  ${ROOTLESS_TAG}"
echo ""
echo "  llama-swap:           $(docker run --rm --entrypoint cat "${DOCKER_IMAGE_TAG}" /versions.txt | grep llama-swap | cut -d' ' -f2-)"
echo ""
if [[ "$BACKEND" == "vulkan" ]]; then
    echo "Run with:"
    echo "  docker run -it --rm --device /dev/dri:/dev/dri ${ROOTLESS_TAG}"
    echo ""
    echo "Note: For AMD GPUs, you may also need:"
    echo "  docker run -it --rm --device /dev/dri:/dev/dri --group-add video ${ROOTLESS_TAG}"
else
    echo "Run with:"
    echo "  docker run -it --rm --gpus all ${ROOTLESS_TAG}"
fi
