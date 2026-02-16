#!/usr/bin/env bash
set -euo pipefail

IMAGE_TAG="vllm-node-marlin-sm12x"
NODES=""
VLLM_REF="fd267bc7b7cd3d001ac5a893eacb9e56ff256822"
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PROJECT_DIR="$(cd "$SCRIPT_DIR/.." && pwd)"
BUILD_DIR="$PROJECT_DIR/build/vllm-marlin-sm12x"
DEFAULT_SPARK_VLLM_DOCKER_DIR="$(cd "$PROJECT_DIR/.." && pwd)/spark-vllm-docker"
SPARK_VLLM_DOCKER_DIR="${SPARK_VLLM_DOCKER_DIR:-$DEFAULT_SPARK_VLLM_DOCKER_DIR}"
COPY_SCRIPT="$SPARK_VLLM_DOCKER_DIR/build-and-copy.sh"

usage() {
  cat <<EOF
Usage: $0 [--tag IMAGE_TAG] [--nodes ip1,ip2,...] [--vllm-ref REF]

Builds custom vLLM image with marlin_sm12x C++ patch compiled in.
Optional: copies the resulting image to cluster worker nodes.

Options:
  --tag    Image tag (default: vllm-node-marlin-sm12x)
  --nodes  Comma-separated cluster nodes (e.g. 192.168.200.12,192.168.200.13)
  --vllm-ref  vLLM commit/tag/branch for build (default: fd267bc7b7cd3d001ac5a893eacb9e56ff256822)

Environment:
  SPARK_VLLM_DOCKER_DIR  Override spark-vllm-docker location
EOF
}

while [[ $# -gt 0 ]]; do
  case "$1" in
    --tag)
      IMAGE_TAG="$2"
      shift 2
      ;;
    --nodes)
      NODES="$2"
      shift 2
      ;;
    --vllm-ref)
      VLLM_REF="$2"
      shift 2
      ;;
    -h|--help)
      usage
      exit 0
      ;;
    *)
      echo "Unknown arg: $1"
      usage
      exit 1
      ;;
  esac
done

echo "[build-vllm-marlin-sm12x] Building image '$IMAGE_TAG' from $BUILD_DIR"
docker build -f "$BUILD_DIR/Dockerfile" \
  --build-arg "VLLM_REF=$VLLM_REF" \
  -t "$IMAGE_TAG" \
  "$BUILD_DIR"

echo "[build-vllm-marlin-sm12x] Build completed: $IMAGE_TAG"

if [[ -n "$NODES" ]]; then
  if [[ ! -x "$COPY_SCRIPT" ]]; then
    echo "[build-vllm-marlin-sm12x] Copy script not found: $COPY_SCRIPT"
    exit 1
  fi

  echo "[build-vllm-marlin-sm12x] Copying image to nodes: $NODES"
  "$COPY_SCRIPT" --no-build -t "$IMAGE_TAG" -c "$NODES"
fi
