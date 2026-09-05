# Runtime image for the unified container.
#
# Every upstream project arrives as an artifacts image holding only /install --
# built by <project>.Dockerfile, either published by CI or built locally by
# build-image.sh. Nothing is compiled here, so this build is minutes.

ARG BACKEND=cuda
# The published flavour name. cuda and cuda13 are both BACKEND=cuda; only the
# CUDA toolkit they were compiled against and the architectures they cover
# differ, so this is recorded in /versions.txt rather than used to build.
ARG VARIANT=cuda
ARG BUILDER_BASE
ARG WHISPER_IMAGE
ARG SD_IMAGE
ARG AUDIO_IMAGE
ARG LLAMA_IMAGE
ARG IK_LLAMA_IMAGE=ik-llama-empty
ARG CUDA_VERSION=12.9.1

FROM ${WHISPER_IMAGE} AS whisper-src
FROM ${SD_IMAGE} AS sd-src
FROM ${AUDIO_IMAGE} AS audio-src
FROM ${LLAMA_IMAGE} AS llama-src

# Vulkan images ship no ik-llama-server. This gives the COPY below a real but
# empty /install/bin so the runtime needs no backend branching. For CUDA the
# arg names a real artifacts image and BuildKit prunes this stage; ubuntu:24.04
# is already pulled as the Vulkan runtime base, so it costs nothing there.
FROM ubuntu:24.04 AS ik-llama-empty
RUN mkdir -p /install/bin
FROM ${IK_LLAMA_IMAGE} AS ik-llama-src

# ── llama-swap release binary ─────────────────────────────────────────

FROM ${BUILDER_BASE} AS llama-swap-download
ARG LS_VERSION=latest
COPY install-llama-swap.sh /build/
RUN bash /build/install-llama-swap.sh "${LS_VERSION}"

# ── vllm-wrapper ──────────────────────────────────────────────────────
#
# vllm-wrapper is not shipped in the llama-swap release archives, so it is
# compiled from the same revision as the llama-swap binary above.

FROM golang:1.27-bookworm AS vllm-wrapper-build
ARG LS_VERSION=latest
COPY install-vllm-wrapper.sh /build/
RUN --mount=type=cache,id=go-build,target=/root/.cache/go-build \
    --mount=type=cache,id=go-mod,target=/go/pkg/mod \
    bash /build/install-vllm-wrapper.sh "${LS_VERSION}"

# ── Runtime bases ─────────────────────────────────────────────────────

# The CUDA stubs live in the devel base, which is the CUDA builder base. This
# stage is unreachable for vulkan builds, so BuildKit never resolves it there.
FROM ${BUILDER_BASE} AS cuda-stubs

ARG CUDA_VERSION=12.9.1
FROM nvidia/cuda:${CUDA_VERSION}-runtime-ubuntu24.04 AS runtime-cuda

ENV DEBIAN_FRONTEND=noninteractive
ENV LD_LIBRARY_PATH="/usr/local/cuda/lib64:${LD_LIBRARY_PATH}"
ENV PATH="/usr/local/bin:${PATH}"

RUN apt-get update && apt-get install -y --no-install-recommends \
    libgomp1 python3 curl ca-certificates \
    libavcodec60 libavformat60 libavutil58 libswresample4 \
    ffmpeg \
    && rm -rf /var/lib/apt/lists/*

# CUDA stub drivers for container compatibility
COPY --from=cuda-stubs /usr/local/cuda/lib64/stubs/libcuda.so /usr/local/cuda/lib64/stubs/libcuda.so
COPY --from=cuda-stubs /usr/local/cuda/lib64/stubs/libcuda.so /usr/local/cuda/lib64/stubs/libcuda.so.1

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
# Bare ARG re-declares the global-scope value inside this stage.
ARG VARIANT
ARG CUDA_VERSION
ARG CMAKE_CUDA_ARCHITECTURES=
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
COPY --from=whisper-src /install/bin/whisper-server /usr/local/bin/
COPY --from=whisper-src /install/bin/whisper-cli /usr/local/bin/
COPY --from=whisper-src /install/lib/ /usr/local/lib/

# Copy stable-diffusion.cpp binaries and libraries
COPY --from=sd-src /install/bin/sd-server /usr/local/bin/
COPY --from=sd-src /install/bin/sd-cli /usr/local/bin/
COPY --from=sd-src /install/lib/ /usr/local/lib/

# Copy audio.cpp binaries (statically linked, model specs compiled in via
# AUDIOCPP_DEPLOYMENT_BUILD). The on-disk spec catalog is installed too so
# --model-spec-override has a path to point at.
COPY --from=audio-src /install/bin/audiocpp_server /usr/local/bin/
COPY --from=audio-src /install/bin/audiocpp_cli /usr/local/bin/
COPY --from=audio-src /install/share/audiocpp/ /usr/local/share/audiocpp/

# Copy llama.cpp binaries (statically linked)
COPY --from=llama-src /install/bin/llama-server /usr/local/bin/
COPY --from=llama-src /install/bin/llama-cli /usr/local/bin/
COPY --from=llama-src /install/bin/llama-tts /usr/local/bin/
COPY --from=llama-src /install/bin/llama-bench /usr/local/bin/

# Copy ik-llama-server (CUDA only; empty copy for vulkan)
COPY --from=ik-llama-src /install/bin/ /usr/local/bin/

# Install uv
RUN pip install uv --break-system-packages

# Copy llama-swap binary
COPY --from=llama-swap-download /install/bin/llama-swap /usr/local/bin/
COPY --from=llama-swap-download /install/llama-swap-version /tmp/

# Copy vllm-wrapper binary
COPY --from=vllm-wrapper-build /install/bin/vllm-wrapper /usr/local/bin/

RUN ldconfig

# config.example.yaml lives at the repo root (docs/), outside this build
# context, so build-image.sh passes it as a named build context.
COPY --from=repo-docs config.example.yaml /etc/llama-swap/config/config.yaml

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
    echo "backend: ${VARIANT}" >> /versions.txt && \
    if [ "${BACKEND}" = "cuda" ]; then \
      echo "cuda_version: ${CUDA_VERSION}" >> /versions.txt; \
      echo "cuda_architectures: ${CMAKE_CUDA_ARCHITECTURES}" >> /versions.txt; \
    fi && \
    echo "build_timestamp: $(date -u +%Y-%m-%dT%H:%M:%SZ)" >> /versions.txt

RUN mkdir -p /models && chown ${RUN_UID}:${RUN_UID} /models
WORKDIR /models
USER ${RUN_UID}
ENTRYPOINT ["llama-swap"]
CMD ["-config", "/etc/llama-swap/config/config.yaml", "-listen", "0.0.0.0:8080", "-watch-config"]
