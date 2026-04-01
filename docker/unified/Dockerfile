# Unified multi-stage Dockerfile for AI inference tools
# Supports CUDA and Vulkan backends via BACKEND build arg
#
# Usage:
#   docker buildx build --build-arg BACKEND=cuda -t llama-swap:unified-cuda .
#   docker buildx build --build-arg BACKEND=vulkan -t llama-swap:unified-vulkan .
#
# Each project has its own install script that handles cloning, building,
# and installing binaries. Build stages are independent for cache efficiency.

ARG BACKEND=cuda

# ── Builder bases ──────────────────────────────────────────────────────

FROM nvidia/cuda:12.4.0-devel-ubuntu22.04 AS builder-base-cuda

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

# ──

FROM ubuntu:26.04 AS builder-base-vulkan

ENV DEBIAN_FRONTEND=noninteractive
ENV CCACHE_DIR=/ccache
ENV CCACHE_MAXSIZE=2G
ENV PATH="/usr/lib/ccache:${PATH}"

RUN apt-get update && apt-get install -y --no-install-recommends \
    build-essential cmake git python3 python3-pip libssl-dev \
    curl ca-certificates ccache make wget software-properties-common \
    libvulkan-dev glslang-tools spirv-tools vulkan-validationlayers glslc \
    && rm -rf /var/lib/apt/lists/*

WORKDIR /build

# ── Select builder base by BACKEND ────────────────────────────────────

FROM builder-base-${BACKEND} AS builder-base

# ── Build whisper.cpp (fastest build, run first) ──────────────────────

FROM builder-base AS whisper-build
ARG BACKEND=cuda
ARG WHISPER_COMMIT_HASH=master
COPY install-whisper.sh /build/
RUN --mount=type=cache,id=ccache-${BACKEND},target=/ccache \
    --mount=type=cache,id=whisper-${BACKEND},target=/src/whisper.cpp/build \
    BACKEND=${BACKEND} bash /build/install-whisper.sh "${WHISPER_COMMIT_HASH}"

# ── Build stable-diffusion.cpp ────────────────────────────────────────

FROM builder-base AS sd-build
ARG BACKEND=cuda
ARG SD_COMMIT_HASH=master
COPY install-sd.sh /build/
RUN --mount=type=cache,id=ccache-${BACKEND},target=/ccache \
    --mount=type=cache,id=sd-${BACKEND},target=/src/stable-diffusion.cpp/build \
    BACKEND=${BACKEND} bash /build/install-sd.sh "${SD_COMMIT_HASH}"

# ── Build llama.cpp (slowest build, run last) ─────────────────────────

FROM builder-base AS llama-build
ARG BACKEND=cuda
ARG LLAMA_COMMIT_HASH=master
COPY install-llama.sh /build/
RUN --mount=type=cache,id=ccache-${BACKEND},target=/ccache \
    --mount=type=cache,id=llama-${BACKEND},target=/src/llama.cpp/build \
    BACKEND=${BACKEND} bash /build/install-llama.sh "${LLAMA_COMMIT_HASH}"

# ── Build ik_llama.cpp (CUDA only) ────────────────────────────────────
#
# Two named stages allow ARG BACKEND to select at build time:
#   - ik-llama-cuda  : real build (from builder-base-cuda)
#   - ik-llama-vulkan: no-op (empty /install/bin, skips CUDA pull entirely)
# BuildKit only evaluates the selected branch, so vulkan builds never
# pull nvidia/cuda:*-devel or compile ik_llama.cpp.

FROM builder-base-vulkan AS ik-llama-vulkan
RUN mkdir -p /install/bin

FROM builder-base-cuda AS ik-llama-cuda
ARG IK_LLAMA_COMMIT_HASH=main
COPY install-ik-llama.sh /build/
RUN --mount=type=cache,id=ccache-cuda,target=/ccache \
    --mount=type=cache,id=ik-llama-cuda,target=/src/ik_llama.cpp/build \
    bash /build/install-ik-llama.sh "${IK_LLAMA_COMMIT_HASH}"

ARG BACKEND=cuda
FROM ik-llama-${BACKEND} AS ik-llama-build

# ── Download llama-swap release binary ────────────────────────────────

FROM builder-base AS llama-swap-download
ARG LS_VERSION=latest
COPY install-llama-swap.sh /build/
RUN bash /build/install-llama-swap.sh "${LS_VERSION}"

# ── Runtime bases ─────────────────────────────────────────────────────

FROM nvidia/cuda:12.4.0-runtime-ubuntu22.04 AS runtime-cuda

ENV DEBIAN_FRONTEND=noninteractive
ENV LD_LIBRARY_PATH="/usr/local/cuda/lib64:${LD_LIBRARY_PATH}"
ENV PATH="/usr/local/bin:${PATH}"

RUN apt-get update && apt-get install -y --no-install-recommends \
    libgomp1 python3 curl ca-certificates git \
    && rm -rf /var/lib/apt/lists/*

# CUDA stub drivers for container compatibility
COPY --from=builder-base-cuda /usr/local/cuda/lib64/stubs/libcuda.so /usr/local/cuda/lib64/stubs/libcuda.so
COPY --from=builder-base-cuda /usr/local/cuda/lib64/stubs/libcuda.so /usr/local/cuda/lib64/stubs/libcuda.so.1

# ──

FROM ubuntu:26.04 AS runtime-vulkan

ENV DEBIAN_FRONTEND=noninteractive
ENV PATH="/usr/local/bin:${PATH}"

RUN apt-get update && apt-get install -y --no-install-recommends \
    libgomp1 libvulkan1 mesa-vulkan-drivers \
    python3 curl ca-certificates git \
    && rm -rf /var/lib/apt/lists/*

# ── Select runtime base by BACKEND ────────────────────────────────────

FROM runtime-${BACKEND} AS runtime

ARG BACKEND=cuda
ARG LLAMA_COMMIT_HASH=unknown
ARG WHISPER_COMMIT_HASH=unknown
ARG SD_COMMIT_HASH=unknown
ARG IK_LLAMA_COMMIT_HASH=unknown

RUN apt-get update && apt-get install -y --no-install-recommends \
    python3-numpy python3-sentencepiece \
    && rm -rf /var/lib/apt/lists/*

# Create llama-swap user and config directory
RUN useradd --system --create-home --shell /sbin/nologin llama-swap && \
    mkdir -p /etc/llama-swap/config && \
    chown -R llama-swap:llama-swap /etc/llama-swap

WORKDIR /app

# Copy whisper.cpp binaries and libraries
COPY --from=whisper-build /install/bin/whisper-server /usr/local/bin/
COPY --from=whisper-build /install/bin/whisper-cli /usr/local/bin/
COPY --from=whisper-build /install/lib/ /usr/local/lib/

# Copy stable-diffusion.cpp binaries and libraries
COPY --from=sd-build /install/bin/sd-server /usr/local/bin/
COPY --from=sd-build /install/bin/sd-cli /usr/local/bin/
COPY --from=sd-build /install/lib/ /usr/local/lib/

# Copy llama.cpp binaries (statically linked)
COPY --from=llama-build /install/bin/llama-server /usr/local/bin/
COPY --from=llama-build /install/bin/llama-cli /usr/local/bin/

# Copy ik-llama-server (CUDA only; empty copy for vulkan)
COPY --from=ik-llama-build /install/bin/ /usr/local/bin/

# Copy llama-swap binary
COPY --from=llama-swap-download /install/bin/llama-swap /usr/local/bin/
COPY --from=llama-swap-download /install/llama-swap-version /tmp/

RUN ldconfig

COPY config.example.yaml /etc/llama-swap/config/config.yaml

# Version tracking
RUN echo "llama.cpp: ${LLAMA_COMMIT_HASH}" > /versions.txt && \
    echo "whisper.cpp: ${WHISPER_COMMIT_HASH}" >> /versions.txt && \
    echo "stable-diffusion.cpp: ${SD_COMMIT_HASH}" >> /versions.txt && \
    echo "ik_llama.cpp: ${IK_LLAMA_COMMIT_HASH}" >> /versions.txt && \
    echo "llama-swap: $(cat /tmp/llama-swap-version)" >> /versions.txt && \
    echo "backend: ${BACKEND}" >> /versions.txt && \
    echo "build_timestamp: $(date -u +%Y-%m-%dT%H:%M:%SZ)" >> /versions.txt

WORKDIR /models
USER llama-swap
ENTRYPOINT ["llama-swap"]
CMD ["-config", "/etc/llama-swap/config/config.yaml", "-listen", "0.0.0.0:8080"]
