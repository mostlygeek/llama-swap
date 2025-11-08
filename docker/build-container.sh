#!/bin/bash

cd $(dirname "$0")

ARCH=$1
PUSH_IMAGES=${2:-false}

# List of allowed architectures
ALLOWED_ARCHS=("intel" "vulkan" "musa" "cuda" "cpu")

# Check if ARCH is in the allowed list
if [[ ! " ${ALLOWED_ARCHS[@]} " =~ " ${ARCH} " ]]; then
  echo "Error: ARCH must be one of the following: ${ALLOWED_ARCHS[@]}"
  exit 1
fi

# Check if GITHUB_TOKEN is set and not empty
if [[ -z "$GITHUB_TOKEN" ]]; then
  echo "Error: GITHUB_TOKEN is not set or is empty."
  exit 1
fi

# the most recent llama-swap tag
# have to strip out the 'v' due to .tar.gz file naming
LS_VER=$(curl -s https://api.github.com/repos/mostlygeek/llama-swap/releases/latest | jq -r .tag_name | sed 's/v//')

if [ "$ARCH" == "cpu" ]; then
    # cpu only containers just use the server tag
    LCPP_TAG=$(curl -s -H "Authorization: Bearer $GITHUB_TOKEN" \
        "https://api.github.com/users/ggml-org/packages/container/llama.cpp/versions" \
        | jq -r '.[] | select(.metadata.container.tags[] | startswith("server")) | .metadata.container.tags[]' \
        | sort -r | head -n1 | awk -F '-' '{print $3}')
    BASE_TAG=server-${LCPP_TAG}
else
    LCPP_TAG=$(curl -s -H "Authorization: Bearer $GITHUB_TOKEN" \
        "https://api.github.com/users/ggml-org/packages/container/llama.cpp/versions" \
        | jq -r --arg arch "$ARCH" '.[] | select(.metadata.container.tags[] | startswith("server-\($arch)")) | .metadata.container.tags[]' \
        | sort -r | head -n1 | awk -F '-' '{print $3}')
    BASE_TAG=server-${ARCH}-${LCPP_TAG}
fi

# Abort if LCPP_TAG is empty.
if [[ -z "$LCPP_TAG" ]]; then
    echo "Abort: Could not find llama-server container for arch: $ARCH"
    exit 1
fi

CONTAINER_TAG="ghcr.io/mostlygeek/llama-swap:v${LS_VER}-${ARCH}-${LCPP_TAG}"
CONTAINER_LATEST="ghcr.io/mostlygeek/llama-swap:${ARCH}"
echo "Building ${CONTAINER_TAG} $LS_VER"
docker build -f llama-swap.Containerfile --build-arg BASE_TAG=${BASE_TAG} --build-arg LS_VER=${LS_VER} -t ${CONTAINER_TAG} -t ${CONTAINER_LATEST} .
if [ "$PUSH_IMAGES" == "true" ]; then
  docker push ${CONTAINER_TAG}
  docker push ${CONTAINER_LATEST}
fi
