#!/bin/bash
set -euo pipefail

# 1. cd into the directory where this script lives
cd "$(dirname "$0")"

# 2. Fixed values -------------------------------------------------
IMAGE_TAG="llama-swap-asr:v148-cuda-b6097"
BASE_TAG="server-cuda-b6097"   # upstream llama.cpp tag
LS_VER="148"                   # llama-swap release version

# 3. Build --------------------------------------------------------
echo "Building ${IMAGE_TAG} …"
docker build \
  -f llama-swap-asr.Containerfile \
  --build-arg BASE_TAG="${BASE_TAG}" \
  --build-arg LS_VER="${LS_VER}" \
  -t "${IMAGE_TAG}" \
  .

echo "Done. Local image ready → ${IMAGE_TAG}"
