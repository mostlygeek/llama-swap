# Unified multi-stage Dockerfile for AI inference tools
# Supports CUDA and Vulkan backends via BACKEND build arg
#
# Usage:
#   docker buildx build --build-arg BACKEND=cuda -t llama-swap:unified-cuda .
#   docker buildx build --build-arg BACKEND=vulkan -t llama-swap:unified-vulkan .
#   docker buildx build --build-arg BACKEND=cuda --build-arg CMAKE_CUDA_ARCHITECTURES="86;89" -t llama-swap:unified-cuda .
#
# Each project has its own install script that handles cloning, building,
# and installing binaries. Build stages are independent for cache efficiency.

ARG BACKEND=cuda

# ── Builder bases ──────────────────────────────────────────────────────

FROM nvidia/cuda:12.9.1-devel-ubuntu24.04 AS builder-base-cuda

# Compute capabilities compiled as SASS, shared by every CUDA build stage.
# 60/61 are Pascal (P100, GTX 10xx, P40) -- the oldest architecture ggml still
# supports -- through 89 (Ada). CMake emits PTX alongside SASS for each entry,
# so architectures between and above the list (70 Volta, 80 Ampere, 90 Hopper,
# 100 and 120 Blackwell) still run by JIT-compiling the nearest lower PTX; they
# just pay a first-run JIT cost and miss arch-specific kernels. Add those
# numbers here to compile them natively, at the cost of build time in every
# stage.
ARG CMAKE_CUDA_ARCHITECTURES="60;61;75;86;89"
ENV DEBIAN_FRONTEND=noninteractive
ENV CMAKE_CUDA_ARCHITECTURES=${CMAKE_CUDA_ARCHITECTURES}
ENV CCACHE_DIR=/ccache
ENV CCACHE_MAXSIZE=2G
ENV PATH="/usr/lib/ccache:${PATH}"

RUN apt-get update && apt-get install -y --no-install-recommends \
    build-essential cmake git python3 python3-pip libssl-dev \
    curl ca-certificates ccache make wget \
    pkg-config libavcodec-dev libavformat-dev libavutil-dev libswresample-dev \
    && rm -rf /var/lib/apt/lists/*

WORKDIR /build

# ──

FROM ubuntu:24.04 AS builder-base-vulkan

ENV DEBIAN_FRONTEND=noninteractive
ENV CCACHE_DIR=/ccache
ENV CCACHE_MAXSIZE=2G
ENV PATH="/usr/lib/ccache:${PATH}"

RUN apt-get update && apt-get install -y --no-install-recommends \
    build-essential cmake git python3 python3-pip libssl-dev \
    curl ca-certificates ccache make wget software-properties-common \
    libvulkan-dev glslang-tools spirv-tools vulkan-validationlayers glslc \
    pkg-config libavcodec-dev libavformat-dev libavutil-dev libswresample-dev \
    spirv-headers \
    && rm -rf /var/lib/apt/lists/*

WORKDIR /build

# ── Select builder base by BACKEND ────────────────────────────────────

FROM builder-base-${BACKEND} AS builder-base

# ── Build whisper.cpp (fastest build, run first) ──────────────────────

FROM builder-base AS whisper-build
ARG BACKEND=cuda
ARG WHISPER_FFMPEG=yes
ARG WHISPER_COMMIT_HASH=master
COPY install-whisper.sh /build/
RUN --mount=type=cache,id=ccache-${BACKEND},target=/ccache \
    --mount=type=cache,id=whisper-${BACKEND},target=/src/whisper.cpp/build \
    BACKEND=${BACKEND} WHISPER_FFMPEG=${WHISPER_FFMPEG} \
    bash /build/install-whisper.sh "${WHISPER_COMMIT_HASH}"

# ── Build stable-diffusion.cpp ────────────────────────────────────────

FROM builder-base AS sd-build
ARG BACKEND=cuda
ARG SD_COMMIT_HASH=master
COPY install-sd.sh /build/
RUN --mount=type=cache,id=ccache-${BACKEND},target=/ccache \
    --mount=type=cache,id=sd-${BACKEND},target=/src/stable-diffusion.cpp/build \
    BACKEND=${BACKEND} bash /build/install-sd.sh "${SD_COMMIT_HASH}"

# ── Build audio.cpp ───────────────────────────────────────────────────

FROM builder-base AS audio-build
ARG BACKEND=cuda
ARG AUDIO_COMMIT_HASH=main
COPY install-audio.sh /build/
RUN --mount=type=cache,id=ccache-${BACKEND},target=/ccache \
    --mount=type=cache,id=audio-${BACKEND},target=/src/audio.cpp/build \
    BACKEND=${BACKEND} bash /build/install-audio.sh "${AUDIO_COMMIT_HASH}"

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

# ── Build vllm-wrapper from llama-swap source ─────────────────────────
#
# vllm-wrapper is not shipped in the llama-swap release archives, so it is
# compiled from the same revision as the llama-swap binary above.

FROM golang:1.26-bookworm AS vllm-wrapper-build
ARG LS_VERSION=latest
COPY install-vllm-wrapper.sh /build/
RUN --mount=type=cache,id=go-build,target=/root/.cache/go-build \
    --mount=type=cache,id=go-mod,target=/go/pkg/mod \
    bash /build/install-vllm-wrapper.sh "${LS_VERSION}"

# ── Runtime bases ─────────────────────────────────────────────────────

FROM nvidia/cuda:12.9.1-runtime-ubuntu24.04 AS runtime-cuda

ENV DEBIAN_FRONTEND=noninteractive
ENV LD_LIBRARY_PATH="/usr/local/cuda/lib64:${LD_LIBRARY_PATH}"
ENV PATH="/usr/local/bin:${PATH}"

RUN apt-get update && apt-get install -y --no-install-recommends \
    libgomp1 python3 curl ca-certificates \
    libavcodec60 libavformat60 libavutil58 libswresample4 \
    ffmpeg \
    && rm -rf /var/lib/apt/lists/*

# CUDA stub drivers for container compatibility
COPY --from=builder-base-cuda /usr/local/cuda/lib64/stubs/libcuda.so /usr/local/cuda/lib64/stubs/libcuda.so
COPY --from=builder-base-cuda /usr/local/cuda/lib64/stubs/libcuda.so /usr/local/cuda/lib64/stubs/libcuda.so.1

# ──

FROM ubuntu:24.04 AS runtime-vulkan

ENV DEBIAN_FRONTEND=noninteractive
ENV PATH="/usr/local/bin:${PATH}"

RUN apt-get update && apt-get install -y --no-install-recommends \
    libgomp1 libvulkan1 mesa-vulkan-drivers \
    rocm-smi \
    python3 curl ca-certificates \
    libavcodec60 libavformat60 libavutil58 libswresample4 \
    ffmpeg \
    && rm -rf /var/lib/apt/lists/*

# ── Select runtime base by BACKEND ────────────────────────────────────

FROM runtime-${BACKEND} AS runtime

ARG BACKEND=cuda
ARG LLAMA_COMMIT_HASH=unknown
ARG WHISPER_COMMIT_HASH=unknown
ARG SD_COMMIT_HASH=unknown
ARG IK_LLAMA_COMMIT_HASH=unknown
ARG AUDIO_COMMIT_HASH=unknown
ARG RUN_UID=0

RUN apt-get update && apt-get install -y --no-install-recommends \
    python3-numpy python3-sentencepiece python3-pip \
    && rm -rf /var/lib/apt/lists/*

# Create non-root user when RUN_UID != 0
RUN if [ "$RUN_UID" != "0" ]; then \
      groupadd --system --gid $RUN_UID llama-swap && \
      useradd --system --uid $RUN_UID --gid $RUN_UID \
        --home /app --shell /sbin/nologin llama-swap; \
    fi && \
    mkdir -p /etc/llama-swap/config && \
    chown -R ${RUN_UID}:${RUN_UID} /etc/llama-swap

WORKDIR /app

# Copy whisper.cpp binaries and libraries
COPY --from=whisper-build /install/bin/whisper-server /usr/local/bin/
COPY --from=whisper-build /install/bin/whisper-cli /usr/local/bin/
COPY --from=whisper-build /install/lib/ /usr/local/lib/

# Copy stable-diffusion.cpp binaries and libraries
COPY --from=sd-build /install/bin/sd-server /usr/local/bin/
COPY --from=sd-build /install/bin/sd-cli /usr/local/bin/
COPY --from=sd-build /install/lib/ /usr/local/lib/

# Copy audio.cpp binaries (statically linked, model specs compiled in via
# AUDIOCPP_DEPLOYMENT_BUILD). The on-disk spec catalog is installed too so
# --model-spec-override has a path to point at.
COPY --from=audio-build /install/bin/audiocpp_server /usr/local/bin/
COPY --from=audio-build /install/bin/audiocpp_cli /usr/local/bin/
COPY --from=audio-build /install/share/audiocpp/ /usr/local/share/audiocpp/

# Copy llama.cpp binaries (statically linked)
COPY --from=llama-build /install/bin/llama-server /usr/local/bin/
COPY --from=llama-build /install/bin/llama-cli /usr/local/bin/
COPY --from=llama-build /install/bin/llama-tts /usr/local/bin/
COPY --from=llama-build /install/bin/llama-bench /usr/local/bin/

# Copy ik-llama-server (CUDA only; empty copy for vulkan)
COPY --from=ik-llama-build /install/bin/ /usr/local/bin/

# Install uv
RUN pip install uv --break-system-packages

# Copy llama-swap binary
COPY --from=llama-swap-download /install/bin/llama-swap /usr/local/bin/
COPY --from=llama-swap-download /install/llama-swap-version /tmp/

# Copy vllm-wrapper binary
COPY --from=vllm-wrapper-build /install/bin/vllm-wrapper /usr/local/bin/

RUN ldconfig

COPY config.example.yaml /etc/llama-swap/config/config.yaml

# audiocpp_server takes its own JSON config. Ship a starter with this image's
# backend baked in -- the binary defaults to "cuda", so a vulkan image that
# copies an unedited example would try to load every model on a backend it was
# not built with.
COPY audiocpp-server.example.json /etc/llama-swap/audiocpp-server.example.json
RUN sed -i "s/__BACKEND__/${BACKEND}/" /etc/llama-swap/audiocpp-server.example.json && \
    chown -R ${RUN_UID}:${RUN_UID} /etc/llama-swap

# Version tracking
RUN echo "llama.cpp: ${LLAMA_COMMIT_HASH}" > /versions.txt && \
    echo "whisper.cpp: ${WHISPER_COMMIT_HASH}" >> /versions.txt && \
    echo "stable-diffusion.cpp: ${SD_COMMIT_HASH}" >> /versions.txt && \
    echo "ik_llama.cpp: ${IK_LLAMA_COMMIT_HASH}" >> /versions.txt && \
    echo "audio.cpp: ${AUDIO_COMMIT_HASH}" >> /versions.txt && \
    echo "llama-swap: $(cat /tmp/llama-swap-version)" >> /versions.txt && \
    echo "backend: ${BACKEND}" >> /versions.txt && \
    echo "build_timestamp: $(date -u +%Y-%m-%dT%H:%M:%SZ)" >> /versions.txt

RUN mkdir -p /models && chown ${RUN_UID}:${RUN_UID} /models
WORKDIR /models
USER ${RUN_UID}
ENTRYPOINT ["llama-swap"]
CMD ["-config", "/etc/llama-swap/config/config.yaml", "-listen", "0.0.0.0:8080", "-watch-config"]
