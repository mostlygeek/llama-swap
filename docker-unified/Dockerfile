# Unified multi-stage Dockerfile for CUDA-accelerated AI inference tools
# Includes: llama.cpp, whisper.cpp, stable-diffusion.cpp, llama-swap
#
# Usage:
#   docker buildx build -t llama-swap:unified .
#
# Each project has its own install script that handles cloning, building,
# and installing binaries. Build stages are independent for cache efficiency.

# Builder base: CUDA devel image with build tools
FROM nvidia/cuda:12.4.0-devel-ubuntu22.04 AS builder-base

ENV DEBIAN_FRONTEND=noninteractive
ENV CMAKE_CUDA_ARCHITECTURES="60;61;75;86;89"
ENV CCACHE_DIR=/ccache
ENV CCACHE_MAXSIZE=2G
ENV PATH="/usr/lib/ccache:${PATH}"

RUN apt-get update && apt-get install -y --no-install-recommends \
    build-essential cmake git python3 python3-pip libssl-dev \
    curl ca-certificates ccache make wget \
    && rm -rf /var/lib/apt/lists/*

WORKDIR /build

# Build whisper.cpp (fastest build, run first)
FROM builder-base AS whisper-build
ARG WHISPER_COMMIT_HASH=master
COPY install-whisper.sh /build/
RUN --mount=type=cache,id=ccache-cuda,target=/ccache \
    --mount=type=cache,id=whisper-cuda,target=/src/whisper.cpp/build \
    bash /build/install-whisper.sh "${WHISPER_COMMIT_HASH}"

# Build stable-diffusion.cpp
FROM builder-base AS sd-build
ARG SD_COMMIT_HASH=master
COPY install-sd.sh /build/
RUN --mount=type=cache,id=ccache-cuda,target=/ccache \
    --mount=type=cache,id=sd-cuda,target=/src/stable-diffusion.cpp/build \
    bash /build/install-sd.sh "${SD_COMMIT_HASH}"

# Build llama.cpp (slowest build, run last)
FROM builder-base AS llama-build
ARG LLAMA_COMMIT_HASH=master
COPY install-llama.sh /build/
RUN --mount=type=cache,id=ccache-cuda,target=/ccache \
    --mount=type=cache,id=llama-cuda,target=/src/llama.cpp/build \
    bash /build/install-llama.sh "${LLAMA_COMMIT_HASH}"

# Download llama-swap release binary
FROM builder-base AS llama-swap-download
ARG LS_VERSION=latest
COPY install-llama-swap.sh /build/
RUN bash /build/install-llama-swap.sh "${LS_VERSION}"

# Runtime image (no build tooling)
FROM nvidia/cuda:12.4.0-runtime-ubuntu22.04 AS runtime

ARG LLAMA_COMMIT_HASH=unknown
ARG WHISPER_COMMIT_HASH=unknown
ARG SD_COMMIT_HASH=unknown

ENV DEBIAN_FRONTEND=noninteractive
ENV LD_LIBRARY_PATH="/usr/local/cuda/lib64:${LD_LIBRARY_PATH}"
ENV PATH="/usr/local/bin:${PATH}"

RUN apt-get update && apt-get install -y --no-install-recommends \
    libgomp1 python3 python3-pip curl ca-certificates git \
    && rm -rf /var/lib/apt/lists/*

# CUDA stub drivers for container compatibility
COPY --from=builder-base /usr/local/cuda/lib64/stubs/libcuda.so /usr/local/cuda/lib64/stubs/libcuda.so
COPY --from=builder-base /usr/local/cuda/lib64/stubs/libcuda.so /usr/local/cuda/lib64/stubs/libcuda.so.1

RUN pip3 install --no-cache-dir numpy sentencepiece

WORKDIR /app

# Copy whisper.cpp binaries and libraries
COPY --from=whisper-build /install/bin/whisper-server /usr/local/bin/
COPY --from=whisper-build /install/bin/whisper-cli /usr/local/bin/
COPY --from=whisper-build /install/lib/ /usr/local/lib/

# Copy stable-diffusion.cpp libraries
COPY --from=sd-build /install/lib/ /usr/local/lib/

# Copy llama.cpp binaries and libraries
COPY --from=llama-build /install/bin/llama-server /usr/local/bin/
COPY --from=llama-build /install/bin/llama-cli /usr/local/bin/
COPY --from=llama-build /install/lib/ /usr/local/lib/

# Copy llama-swap binary
COPY --from=llama-swap-download /install/bin/llama-swap /usr/local/bin/
COPY --from=llama-swap-download /install/llama-swap-version /tmp/

RUN ldconfig

# Convenience symlinks
RUN ln -sf /usr/local/bin/llama-cli /usr/local/bin/llama && \
    ln -sf /usr/local/bin/whisper-cli /usr/local/bin/whisper

# Validate all binaries exist
RUN set -e && \
    for bin in llama-server llama-cli whisper-server whisper-cli llama-swap; do \
        test -x /usr/local/bin/$bin || { echo "FATAL: $bin missing from /usr/local/bin"; exit 1; }; \
    done && \
    echo "All binaries validated successfully"

# Version tracking
RUN echo "llama.cpp: ${LLAMA_COMMIT_HASH}" > /versions.txt && \
    echo "whisper.cpp: ${WHISPER_COMMIT_HASH}" >> /versions.txt && \
    echo "stable-diffusion.cpp: ${SD_COMMIT_HASH}" >> /versions.txt && \
    echo "llama-swap: $(cat /tmp/llama-swap-version)" >> /versions.txt && \
    echo "backend: cuda" >> /versions.txt && \
    echo "build_timestamp: $(date -u +%Y-%m-%dT%H:%M:%SZ)" >> /versions.txt

WORKDIR /models
CMD ["bash"]
