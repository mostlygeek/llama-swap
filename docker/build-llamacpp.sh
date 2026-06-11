#!/bin/bash
# Build llama.cpp with CUDA
# Output: <out_folder>/llama-server + llama-diffusion-cli
#
# Note: llama-swap uses llama-server; llama-diffusion-cli is the CLI tool
# from unsloth docs. Both are built so you can test which works for serving.
#
# GPU targets: SM89 (RTX 4060 Ti) + SM75 (RTX 2080)

set -e

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"

DEFAULT_REPO="https://github.com/danielhanchen/llama.cpp"
DEFAULT_BRANCH="main"
DEFAULT_OUT="$SCRIPT_DIR/llamacpp"
PARALLEL_PROCESSES=3

usage() {
  cat <<EOF
Usage: $(basename "$0") [OPTIONS]

Build llama.cpp (llama-server + llama-diffusion-cli) with CUDA inside Docker.

Options:
  --repo <url>      Git repository to clone  (default: $DEFAULT_REPO)
  --branch <name>   Branch to build          (default: $DEFAULT_BRANCH)
  --out <dir>       Output directory         (default: $DEFAULT_OUT)
  --help            Show this help and exit
EOF
}

REPO="$DEFAULT_REPO"
BRANCH="$DEFAULT_BRANCH"
DEST="$DEFAULT_OUT"

while [[ $# -gt 0 ]]; do
  case "$1" in
    --repo)   REPO="$2";   shift 2 ;;
    --branch) BRANCH="$2"; shift 2 ;;
    --out)    DEST="$2";   shift 2 ;;
    --help)   usage; exit 0 ;;
    *) echo "Unknown option: $1" >&2; usage; exit 1 ;;
  esac
done

mkdir -p "$DEST"
DEST="$(realpath "$DEST")"

echo "==> Building $REPO@$BRANCH (CUDA SM89+SM75)..."

docker run --rm \
  --gpus all \
  -v "$DEST":/output \
  -e BUILD_REPO="$REPO" \
  -e BUILD_BRANCH="$BRANCH" \
  nvidia/cuda:12.8.1-devel-ubuntu24.04 \
  bash -c '
    set -e
    export DEBIAN_FRONTEND=noninteractive
    apt-get update -qq
    apt-get install -y -qq git cmake build-essential ninja-build

    git clone --depth 1 --branch "$BUILD_BRANCH" \
      "$BUILD_REPO" /build

    cd /build

    cmake -B build -G Ninja \
      -DGGML_CUDA=ON \
      -DBUILD_SHARED_LIBS=OFF \
      -DCMAKE_BUILD_TYPE=Release \
      -DCMAKE_CUDA_ARCHITECTURES="89;75"

    cmake --build build --parallel $PARALLEL_PROCESSES \
      --target llama-server llama-diffusion-cli

    cp build/bin/llama-server /output/
    cp build/bin/llama-diffusion-cli /output/

    echo "==> Built:"
    ls -lh /output/
  '

chmod +x "$DEST/llama-server" "$DEST/llama-diffusion-cli"
echo "==> Done. Binaries in $DEST"
