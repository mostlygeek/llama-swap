# Builder base for CUDA builds.
#
# Published as its own image (build-image.sh --stage=base) so each project's
# Dockerfile can start FROM it without carrying the other projects' definitions.
# That is what lets an artifacts image be keyed on this base's tag plus the
# project's own two files, with nothing to infer from a shared Dockerfile.

ARG CUDA_VERSION=12.9.1
FROM nvidia/cuda:${CUDA_VERSION}-devel-ubuntu24.04

# Compute capabilities compiled as SASS, shared by every CUDA build.
# 60/61 are Pascal (P100, GTX 10xx, P40) -- the oldest architecture ggml still
# supports -- through 89 (Ada). CMake emits PTX alongside SASS for each entry,
# so architectures between and above the list (70 Volta, 80 Ampere, 90 Hopper,
# 100 and 120 Blackwell) still run by JIT-compiling the nearest lower PTX; they
# just pay a first-run JIT cost and miss arch-specific kernels. Add those
# numbers here to compile them natively, at the cost of build time in every
# project. Changing this changes this file's hash, which rebuilds the base and
# every CUDA project that starts from it.
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
