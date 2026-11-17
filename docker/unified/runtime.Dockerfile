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
ARG CUDA_VERSION=12.9.1

# The artifacts image of each project that is part of the build. A project that
# is not has no image, so it falls back to the empty stage below: its COPY then
# copies nothing and needs no conditional of its own. build-image.sh passes a
# real artifacts image for every project it was asked to build.
ARG WHISPER_IMAGE=empty
ARG SD_IMAGE=empty
ARG AUDIO_IMAGE=empty
ARG LLAMA_IMAGE=empty
ARG IK_LLAMA_IMAGE=empty

# An empty /install. Must come before the stages that may name it.
FROM ubuntu:24.04 AS empty
RUN mkdir -p /install

FROM ${WHISPER_IMAGE} AS whisper-src
FROM ${SD_IMAGE} AS sd-src
FROM ${AUDIO_IMAGE} AS audio-src
FROM ${LLAMA_IMAGE} AS llama-src
FROM ${IK_LLAMA_IMAGE} AS ik-llama-src

# ── llama-swap release binary ─────────────────────────────────────────

FROM ${BUILDER_BASE} AS llama-swap-download
ARG LS_VERSION=latest
COPY lib-release.sh install-llama-swap.sh /build/
RUN bash /build/install-llama-swap.sh "${LS_VERSION}"

# ── vllm-wrapper ──────────────────────────────────────────────────────
#
# vllm-wrapper is not shipped in the llama-swap release archives, so it is
# compiled from the same revision as the llama-swap binary above.

FROM golang:1.27-bookworm AS vllm-wrapper-build
ARG LS_VERSION=latest
COPY lib-release.sh install-vllm-wrapper.sh /build/
RUN --mount=type=cache,id=go-build,target=/root/.cache/go-build \
    --mount=type=cache,id=go-mod,target=/go/pkg/mod \
    bash /build/install-vllm-wrapper.sh "${LS_VERSION}"

# ── kubeswap ──────────────────────────────────────────────────────────
#
# kubeswap is the Kubernetes backend wrapper (cmd/cmdStop) for managing
# inference servers in a namespace; see cmd/kubeswap/README.md. Like
# vllm-wrapper it is compiled from the same revision as llama-swap.

FROM golang:1.27-bookworm AS kubeswap-build
# Same LS_VERSION as the other Go stages, so one build arg (or the CI's
# single resolved revision) controls llama-swap, vllm-wrapper and
# kubeswap together. If that revision predates cmd/kubeswap (no release
# ships it yet), the installer aborts with a clear error; until the
# first such release is out, build an explicit revision with
# --build-arg LS_VERSION=<commit-or-tag>.
ARG LS_VERSION=latest
COPY lib-release.sh install-kubeswap.sh /build/
RUN --mount=type=cache,id=go-build,target=/root/.cache/go-build \
    --mount=type=cache,id=go-mod,target=/go/pkg/mod \
    bash /build/install-kubeswap.sh "${LS_VERSION}"

# ── Runtime bases ─────────────────────────────────────────────────────

# The CUDA stubs live in the devel base, which is the CUDA builder base. This
# stage is unreachable for vulkan builds, so BuildKit never resolves it there.
FROM ${BUILDER_BASE} AS cuda-stubs

ARG CUDA_VERSION=12.9.1
FROM nvidia/cuda:${CUDA_VERSION}-runtime-ubuntu24.04 AS runtime-cuda

ENV DEBIAN_FRONTEND=noninteractive
ENV LD_LIBRARY_PATH="/usr/local/cuda/lib64:${LD_LIBRARY_PATH}"
ENV PATH="/usr/local/bin:${PATH}"

RUN for i in 1 2 3 4 5; do apt-get -o Acquire::Retries=5 update --error-on=any && break; \
      [ "$i" -lt 5 ] || exit 1; echo "apt-get update failed, retrying in $((i * 30))s"; sleep $((i * 30)); \
    done \
    && apt-get -o Acquire::Retries=5 install -y --no-install-recommends \
    libgomp1 python3 curl ca-certificates \
    libavcodec60 libavformat60 libavutil58 libswresample4 \
    ffmpeg libmp3lame0 \
    && rm -rf /var/lib/apt/lists/*

# CUDA stub drivers for container compatibility
COPY --from=cuda-stubs /usr/local/cuda/lib64/stubs/libcuda.so /usr/local/cuda/lib64/stubs/libcuda.so
COPY --from=cuda-stubs /usr/local/cuda/lib64/stubs/libcuda.so /usr/local/cuda/lib64/stubs/libcuda.so.1

# ──

FROM ubuntu:24.04 AS runtime-vulkan

ENV DEBIAN_FRONTEND=noninteractive
ENV PATH="/usr/local/bin:${PATH}"

RUN for i in 1 2 3 4 5; do apt-get -o Acquire::Retries=5 update --error-on=any && break; \
      [ "$i" -lt 5 ] || exit 1; echo "apt-get update failed, retrying in $((i * 30))s"; sleep $((i * 30)); \
    done \
    && apt-get -o Acquire::Retries=5 install -y --no-install-recommends \
    libgomp1 libvulkan1 mesa-vulkan-drivers \
    rocm-smi \
    python3 curl ca-certificates \
    libavcodec60 libavformat60 libavutil58 libswresample4 \
    ffmpeg libmp3lame0 \
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

RUN for i in 1 2 3 4 5; do apt-get -o Acquire::Retries=5 update --error-on=any && break; \
      [ "$i" -lt 5 ] || exit 1; echo "apt-get update failed, retrying in $((i * 30))s"; sleep $((i * 30)); \
    done \
    && apt-get -o Acquire::Retries=5 install -y --no-install-recommends \
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

# Each project's artifacts image holds exactly what it ships under /install
# (bin/, and lib/ or share/ where it has them), so the tree is copied whole and a
# project that was not built contributes nothing.
#
# whisper.cpp and stable-diffusion.cpp: binaries and their shared libraries.
COPY --from=whisper-src /install/ /usr/local/
COPY --from=sd-src /install/ /usr/local/

# audio.cpp: statically linked, model specs compiled in via
# AUDIOCPP_DEPLOYMENT_BUILD. The on-disk spec catalog is installed too so
# --model-spec-override has a path to point at. eSpeak-ng is linked in
# statically and finds its data directory next to the executable, so
# espeak-ng-data stays in /usr/local/bin. Its GPL license and source
# archive are in /usr/local/share/audiocpp/licenses/espeak-ng.
COPY --from=audio-src /install/ /usr/local/

# llama.cpp (statically linked) and ik_llama.cpp (ik-llama-server)
COPY --from=llama-src /install/ /usr/local/
COPY --from=ik-llama-src /install/ /usr/local/

# Install uv
RUN pip install uv --break-system-packages

# Copy llama-swap binary
COPY --from=llama-swap-download /install/bin/llama-swap /usr/local/bin/
COPY --from=llama-swap-download /install/llama-swap-version /tmp/

# Copy vllm-wrapper binary
COPY --from=vllm-wrapper-build /install/bin/vllm-wrapper /usr/local/bin/

# Copy kubeswap binary
COPY --from=kubeswap-build /install/bin/kubeswap /usr/local/bin/

RUN ldconfig

# The starter config the container runs with: small, and valid, so a container
# started with nothing mounted comes up. docs/config.example.yaml in the repo
# documents every option instead and is reference material -- it uses strict
# ${env.X} macros and does not load on its own.
COPY config.example.yaml /etc/llama-swap/config/config.yaml

# audiocpp_server takes its own JSON config. Ship a starter with this image's
# backend baked in -- the binary defaults to "cuda", so a vulkan image that
# copies an unedited example would try to load every model on a backend it was
# not built with.
COPY audiocpp-server.example.json /etc/llama-swap/audiocpp-server.example.json
RUN sed -i "s/__BACKEND__/${BACKEND}/" /etc/llama-swap/audiocpp-server.example.json && \
    chown -R ${RUN_UID}:${RUN_UID} /etc/llama-swap

# Version tracking
#
# A project that was not built has an empty commit hash and gets no line.
RUN : > /versions.txt && \
    for entry in "llama.cpp:${LLAMA_COMMIT_HASH}" \
                 "whisper.cpp:${WHISPER_COMMIT_HASH}" \
                 "stable-diffusion.cpp:${SD_COMMIT_HASH}" \
                 "ik_llama.cpp:${IK_LLAMA_COMMIT_HASH}" \
                 "audio.cpp:${AUDIO_COMMIT_HASH}"; do \
      [ -n "${entry#*:}" ] && echo "${entry%%:*}: ${entry#*:}" >> /versions.txt; \
    done; \
    echo "llama-swap: $(cat /tmp/llama-swap-version)" >> /versions.txt && \
    echo "backend: ${VARIANT}" >> /versions.txt && \
    if [ "${BACKEND}" = "cuda" ]; then \
      echo "cuda_version: ${CUDA_VERSION}" >> /versions.txt; \
      echo "cuda_architectures: ${CMAKE_CUDA_ARCHITECTURES}" >> /versions.txt; \
    fi && \
    echo "build_timestamp: $(date -u +%Y-%m-%dT%H:%M:%SZ)" >> /versions.txt

RUN mkdir -p /models && chown ${RUN_UID}:${RUN_UID} /models

# Entrypoint that turns LLAMA_SWAP_* environment variables into flags. The
# defaults that used to live in CMD moved into it, so a container started with
# no arguments behaves exactly as before, and arguments passed to the container
# still replace them outright rather than being appended to ours.
COPY --chmod=755 run.sh /usr/local/bin/run.sh

WORKDIR /models
USER ${RUN_UID}
ENTRYPOINT ["run.sh"]
# Explicitly empty: an inherited CMD would arrive as arguments to run.sh, which
# reads arguments as a full override of the environment.
CMD []
