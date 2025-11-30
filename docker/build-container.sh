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

# Set llama.cpp base image, customizable using the BASE_LLAMACPP_IMAGE environment
# variable, this permits testing with forked llama.cpp repositories
BASE_IMAGE=${BASE_LLAMACPP_IMAGE:-ghcr.io/ggml-org/llama.cpp}

# Set llama-swap repository, automatically uses GITHUB_REPOSITORY variable
# to enable easy container builds on forked repos
LS_REPO=${GITHUB_REPOSITORY:-mostlygeek/llama-swap}

# the most recent llama-swap tag
# have to strip out the 'v' due to .tar.gz file naming
LS_VER=$(curl -s https://api.github.com/repos/${LS_REPO}/releases/latest | jq -r .tag_name | sed 's/v//')

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

  echo "Building $CONTAINER_TYPE $CONTAINER_TAG $LS_VER"
  docker build -f llama-swap.Containerfile --build-arg BASE_TAG=${BASE_TAG} --build-arg LS_VER=${LS_VER} --build-arg UID=${USER_UID} \
    --build-arg LS_REPO=${LS_REPO} --build-arg GID=${USER_GID} --build-arg USER_HOME=${USER_HOME} -t ${CONTAINER_TAG} -t ${CONTAINER_LATEST} \
    --build-arg BASE_IMAGE=${BASE_IMAGE} .
  if [ "$PUSH_IMAGES" == "true" ]; then
    docker push ${CONTAINER_TAG}
    docker push ${CONTAINER_LATEST}
  fi
done
