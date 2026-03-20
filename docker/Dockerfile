# Unified multi-stage Dockerfile for GPU-accelerated AI inference tools
# Includes: llama.cpp, whisper.cpp, stable-diffusion.cpp, llama-swap
#
# Usage:
#   docker buildx build --build-arg BACKEND=cuda  -t llama-swap:cuda .
#   docker buildx build --build-arg BACKEND=vulkan -t llama-swap:vulkan .
#
# Adding a new server project:
#   1. Add a case to install.sh with project-specific cmake flags and targets
#   2. Add a source clone stage (FROM builder-base AS <project>-source)
#   3. Add a build stage that runs: bash /build/install.sh "$BACKEND" <project>
#   4. Add COPY lines in the runtime stage
#   5. Add the binary name(s) to the validation RUN step
#   6. Update build-image.sh with the new repo URL and commit hash

ARG BACKEND=cuda

# Builder base: CUDA devel image + Vulkan SDK (supports both backends)
FROM nvidia/cuda:12.4.0-devel-ubuntu22.04 AS builder-base

ENV DEBIAN_FRONTEND=noninteractive
ENV CMAKE_CUDA_ARCHITECTURES="60;61;75;86;89"
ENV CCACHE_DIR=/ccache
ENV CCACHE_MAXSIZE=2G
ENV PATH="/usr/lib/ccache:${PATH}"

RUN apt-get update && apt-get install -y --no-install-recommends \
    build-essential cmake git python3 python3-pip libssl-dev \
    curl ca-certificates ccache make wget xz-utils unzip \
    && rm -rf /var/lib/apt/lists/*

ARG VULKAN_SDK_VERSION=1.3.275.0
RUN wget -q --show-progress \
    https://sdk.lunarg.com/sdk/download/${VULKAN_SDK_VERSION}/linux/vulkansdk-linux-x86_64-${VULKAN_SDK_VERSION}.tar.xz \
    -O /tmp/vulkan-sdk.tar.xz && \
    mkdir -p /opt && \
    tar -xf /tmp/vulkan-sdk.tar.xz -C /opt && \
    rm /tmp/vulkan-sdk.tar.xz

ENV VULKAN_SDK=/opt/${VULKAN_SDK_VERSION}/x86_64
ENV PATH="${VULKAN_SDK}/bin:${PATH}"
ENV CMAKE_PREFIX_PATH="${VULKAN_SDK}"
ENV VULKAN_INCLUDE_DIRS="${VULKAN_SDK}/include"

WORKDIR /src

# Build llama-swap from local source
FROM golang:1.25-alpine AS llama-swap-builder
WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
COPY . .
ENV CGO_ENABLED=0 GOOS=linux GOARCH=amd64
RUN go build -o llama-swap .
RUN if git rev-parse --git-dir > /dev/null 2>&1; then \
        VERSION=$(git rev-parse --short HEAD) && \
        if [ -n "$(git status --porcelain)" ]; then \
            VERSION="${VERSION}+dirty"; \
        fi && \
        echo "$VERSION" > /app/llama-swap-version; \
    else \
        echo "local-build" > /app/llama-swap-version; \
    fi

# Source cloning (cached independently per project)
FROM builder-base AS llama-source
ARG LLAMA_COMMIT_HASH=master
RUN git clone --filter=blob:none --no-checkout https://github.com/ggml-org/llama.cpp.git /src/llama.cpp && \
    cd /src/llama.cpp && \
    git fetch --depth=1 origin ${LLAMA_COMMIT_HASH} && \
    git checkout FETCH_HEAD

FROM builder-base AS whisper-source
ARG WHISPER_COMMIT_HASH=master
RUN git clone --filter=blob:none --no-checkout https://github.com/ggml-org/whisper.cpp.git /src/whisper.cpp && \
    cd /src/whisper.cpp && \
    git fetch --depth=1 origin ${WHISPER_COMMIT_HASH} && \
    git checkout FETCH_HEAD

FROM builder-base AS sd-source
ARG SD_COMMIT_HASH=master
RUN git clone --filter=blob:none --no-checkout https://github.com/leejet/stable-diffusion.cpp.git /src/stable-diffusion.cpp && \
    cd /src/stable-diffusion.cpp && \
    git fetch --depth=1 origin ${SD_COMMIT_HASH} && \
    git checkout FETCH_HEAD && \
    git submodule update --init --recursive --depth=1

# Project builds (ordered by build time: fastest first)
FROM builder-base AS whisper-build
ARG BACKEND=cuda
COPY --from=whisper-source /src/whisper.cpp /build/whisper.cpp
COPY docker/install.sh /build/
WORKDIR /build/whisper.cpp
RUN --mount=type=cache,id=ccache-${BACKEND},target=/ccache \
    --mount=type=cache,id=whisper-${BACKEND},target=/build/whisper.cpp/build \
    bash /build/install.sh "$BACKEND" whisper

FROM builder-base AS sd-build
ARG BACKEND=cuda
ARG SD_COMMIT_HASH=master
COPY --from=sd-source /src/stable-diffusion.cpp /build/stable-diffusion.cpp
COPY docker/install.sh /build/
WORKDIR /build/stable-diffusion.cpp
RUN --mount=type=cache,id=ccache-${BACKEND},target=/ccache \
    --mount=type=cache,id=sd-${BACKEND},target=/build/stable-diffusion.cpp/build \
    RELEASE_TAG="${SD_COMMIT_HASH}" bash /build/install.sh "$BACKEND" sd

FROM builder-base AS llama-build
ARG BACKEND=cuda
ARG LLAMA_COMMIT_HASH=master
COPY --from=llama-source /src/llama.cpp /build/llama.cpp
COPY docker/install.sh /build/
WORKDIR /build/llama.cpp
RUN --mount=type=cache,id=ccache-${BACKEND},target=/ccache \
    --mount=type=cache,id=llama-${BACKEND},target=/build/llama.cpp/build \
    RELEASE_TAG="${LLAMA_COMMIT_HASH}" bash /build/install.sh "$BACKEND" llama

# CUDA runtime
FROM nvidia/cuda:12.4.0-runtime-ubuntu22.04 AS runtime-cuda

ENV DEBIAN_FRONTEND=noninteractive
ENV LD_LIBRARY_PATH="/usr/local/cuda/lib64:${LD_LIBRARY_PATH}"

RUN apt-get update && apt-get install -y --no-install-recommends \
    libgomp1 python3 python3-pip curl ca-certificates git \
    && rm -rf /var/lib/apt/lists/*

COPY --from=builder-base /usr/local/cuda/lib64/stubs/libcuda.so /usr/local/cuda/lib64/stubs/libcuda.so
COPY --from=builder-base /usr/local/cuda/lib64/stubs/libcuda.so /usr/local/cuda/lib64/stubs/libcuda.so.1

# Vulkan runtime
FROM ubuntu:22.04 AS runtime-vulkan

ENV DEBIAN_FRONTEND=noninteractive
ENV VK_DRIVER_FILES=/usr/share/vulkan/icd.d/lvp_icd.x86_64.json:/usr/share/vulkan/icd.d/radeon_icd.x86_64.json:/usr/share/vulkan/icd.d/intel_icd.x86_64.json

RUN apt-get update && apt-get install -y --no-install-recommends \
    libgomp1 libvulkan1 mesa-vulkan-drivers vulkan-tools \
    python3 python3-pip curl ca-certificates git \
    && rm -rf /var/lib/apt/lists/*

# Final runtime
FROM runtime-${BACKEND} AS runtime

ARG BACKEND=cuda
ARG LLAMA_COMMIT_HASH=unknown
ARG WHISPER_COMMIT_HASH=unknown
ARG SD_COMMIT_HASH=unknown

ENV PATH="/usr/local/bin:${PATH}"

COPY --from=llama-swap-builder /app/llama-swap /usr/local/bin/
COPY --from=llama-swap-builder /app/llama-swap-version /tmp/

COPY docker/test-binaries.sh /usr/local/bin/
RUN chmod +x /usr/local/bin/test-binaries.sh

RUN pip3 install --no-cache-dir numpy sentencepiece

WORKDIR /app

COPY --from=llama-build /install/bin/llama-server /usr/local/bin/
COPY --from=llama-build /install/bin/llama-cli /usr/local/bin/
COPY --from=llama-build /install/lib/ /usr/local/lib/

COPY --from=whisper-build /install/bin/whisper-server /usr/local/bin/
COPY --from=whisper-build /install/bin/whisper-cli /usr/local/bin/
COPY --from=whisper-build /install/lib/ /usr/local/lib/

COPY --from=sd-build /install/bin/sd-server /usr/local/bin/
COPY --from=sd-build /install/bin/sd-cli /usr/local/bin/
COPY --from=sd-build /install/lib/ /usr/local/lib/

RUN ldconfig

RUN ln -sf /usr/local/bin/llama-cli /usr/local/bin/llama && \
    ln -sf /usr/local/bin/whisper-cli /usr/local/bin/whisper && \
    ln -sf /usr/local/bin/sd-cli /usr/local/bin/stable-diffusion

RUN set -e && \
    for bin in llama-server llama-cli whisper-server whisper-cli sd-server sd-cli llama-swap; do \
        test -x /usr/local/bin/$bin || { echo "FATAL: $bin missing from /usr/local/bin"; exit 1; }; \
    done && \
    echo "All binaries validated successfully"

RUN echo "llama.cpp: ${LLAMA_COMMIT_HASH}" > /versions.txt && \
    echo "whisper.cpp: ${WHISPER_COMMIT_HASH}" >> /versions.txt && \
    echo "stable-diffusion.cpp: ${SD_COMMIT_HASH}" >> /versions.txt && \
    echo "llama-swap: $(cat /tmp/llama-swap-version)" >> /versions.txt && \
    echo "backend: ${BACKEND}" >> /versions.txt && \
    echo "build_timestamp: $(date -u +%Y-%m-%dT%H:%M:%SZ)" >> /versions.txt

WORKDIR /models
CMD ["bash"]
