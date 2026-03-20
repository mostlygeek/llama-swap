# Unified multi-stage Dockerfile for GPU-accelerated AI inference tools
# Includes: llama.cpp, whisper.cpp, stable-diffusion.cpp, llama-swap
#
# Supports CUDA and Vulkan backends via the BACKEND build arg:
#   docker buildx build --build-arg BACKEND=cuda  -t llama-swap:cuda .
#   docker buildx build --build-arg BACKEND=vulkan -t llama-swap:vulkan .
#
# Features:
# - Single Dockerfile for all GPU backends (BuildKit conditional FROM)
# - Separate git clone stages for independent caching
# - ccache + BuildKit cache mounts for incremental builds
# - Backend-scoped cache IDs to prevent cross-contamination
# - Binary validation: build fails if any expected binary is missing
#
# Adding a new server project:
#   1. Add a source clone stage (FROM builder-base AS <project>-source)
#   2. Add a build stage (FROM builder-base AS <project>-build)
#   3. Add COPY lines in the runtime stage
#   4. Add the binary name(s) to the validation RUN step
#   5. Update build-image.sh with the new repo URL and commit hash

ARG BACKEND=cuda

# ============================================================================
# Stage 0a: CUDA Builder Base
# ============================================================================
FROM nvidia/cuda:12.4.0-devel-ubuntu22.04 AS builder-base-cuda

ENV DEBIAN_FRONTEND=noninteractive
ENV CMAKE_CUDA_ARCHITECTURES="60;61;75;86;89"
ENV CCACHE_DIR=/ccache
ENV CCACHE_MAXSIZE=2G
ENV PATH="/usr/lib/ccache:${PATH}"

RUN apt-get update && apt-get install -y --no-install-recommends \
    build-essential \
    cmake \
    git \
    python3 \
    python3-pip \
    libssl-dev \
    curl \
    ca-certificates \
    ccache \
    make \
    && rm -rf /var/lib/apt/lists/*

WORKDIR /src

# ============================================================================
# Stage 0b: Vulkan Builder Base
# ============================================================================
FROM ubuntu:22.04 AS builder-base-vulkan

ENV DEBIAN_FRONTEND=noninteractive
ENV CCACHE_DIR=/ccache
ENV CCACHE_MAXSIZE=2G

RUN apt-get update && apt-get install -y --no-install-recommends \
    build-essential \
    cmake \
    git \
    python3 \
    python3-pip \
    libssl-dev \
    curl \
    ca-certificates \
    ccache \
    wget \
    xz-utils \
    && rm -rf /var/lib/apt/lists/*

# Install Vulkan SDK (pinned version for whisper.cpp compatibility)
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

# Create ccache symlinks
RUN mkdir -p /usr/lib/ccache && \
    ln -sf /usr/bin/ccache /usr/lib/ccache/gcc && \
    ln -sf /usr/bin/ccache /usr/lib/ccache/g++ && \
    ln -sf /usr/bin/ccache /usr/lib/ccache/cc && \
    ln -sf /usr/bin/ccache /usr/lib/ccache/c++
ENV PATH="/usr/lib/ccache:${PATH}"

WORKDIR /src

# ============================================================================
# Stage 0: Select builder base by BACKEND arg
# BuildKit only builds the selected branch; the other is skipped entirely.
# ============================================================================
FROM builder-base-${BACKEND} AS builder-base

# ============================================================================
# Stage 1: Build llama-swap from local source (backend-agnostic)
# ============================================================================
FROM golang:1.25-alpine AS llama-swap-builder
WORKDIR /app

# Copy Go module files first for layer caching
COPY go.mod go.sum ./
RUN go mod download

# Copy source and build
COPY . .
ENV CGO_ENABLED=0 GOOS=linux GOARCH=amd64
RUN go build -o llama-swap .

# Capture version: git hash + dirty flag, or 'local-build' fallback
RUN if git rev-parse --git-dir > /dev/null 2>&1; then \
        VERSION=$(git rev-parse --short HEAD) && \
        if [ -n "$(git status --porcelain)" ]; then \
            VERSION="${VERSION}+dirty"; \
        fi && \
        echo "$VERSION" > /app/llama-swap-version; \
    else \
        echo "local-build" > /app/llama-swap-version; \
    fi

# ============================================================================
# Stage 2: Source Cloning (cached independently per project)
# These stages are identical regardless of backend.
# ============================================================================

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

# ============================================================================
# Stage 3: Project Builds (ordered by build time: fastest first)
#
# Each stage uses a shell conditional on BACKEND to select cmake flags.
# Cache IDs are scoped by backend to prevent cross-contamination.
# ============================================================================

# --- whisper.cpp (fast: 8-12 min) ---
FROM builder-base AS whisper-build
ARG BACKEND=cuda
COPY --from=whisper-source /src/whisper.cpp /build/whisper.cpp
WORKDIR /build/whisper.cpp
RUN --mount=type=cache,id=ccache-${BACKEND},target=/ccache \
    --mount=type=cache,id=whisper-${BACKEND},target=/build/whisper.cpp/build \
    set -e && \
    rm -rf build/CMakeCache.txt build/CMakeFiles 2>/dev/null || true && \
    if [ "$BACKEND" = "cuda" ]; then \
        cmake -B build \
            -DGGML_NATIVE=OFF \
            -DGGML_CUDA=ON \
            -DGGML_VULKAN=OFF \
            -DCMAKE_CUDA_ARCHITECTURES="${CMAKE_CUDA_ARCHITECTURES}" \
            -DCMAKE_BUILD_TYPE=Release \
            -DCMAKE_CUDA_FLAGS="-allow-unsupported-compiler" \
            -DCMAKE_EXE_LINKER_FLAGS="-Wl,-rpath-link,/usr/local/cuda/lib64/stubs -lcuda" \
            -DCMAKE_SHARED_LINKER_FLAGS="-Wl,-rpath-link,/usr/local/cuda/lib64/stubs -lcuda" \
            -DCMAKE_C_COMPILER_LAUNCHER=ccache \
            -DCMAKE_CXX_COMPILER_LAUNCHER=ccache; \
    elif [ "$BACKEND" = "vulkan" ]; then \
        cmake -B build \
            -DGGML_NATIVE=OFF \
            -DGGML_VULKAN=ON \
            -DVulkan_INCLUDE_DIR="${VULKAN_SDK}/include" \
            -DVulkan_LIBRARY="${VULKAN_SDK}/lib/libvulkan.so" \
            -DCMAKE_BUILD_TYPE=Release; \
    fi && \
    cmake --build build --config Release -j$(nproc) --target whisper-cli whisper-server && \
    mkdir -p /install/bin /install/lib && \
    cp build/bin/whisper-cli build/bin/whisper-server /install/bin/ && \
    test -f /install/bin/whisper-server || { echo "FATAL: whisper-server not built"; exit 1; } && \
    find build -name "*.so*" -type f -exec cp {} /install/lib/ \;

# --- stable-diffusion.cpp (medium: 15-25 min) ---
FROM builder-base AS sd-build
ARG BACKEND=cuda
COPY --from=sd-source /src/stable-diffusion.cpp /build/stable-diffusion.cpp
WORKDIR /build/stable-diffusion.cpp
RUN --mount=type=cache,id=ccache-${BACKEND},target=/ccache \
    --mount=type=cache,id=sd-${BACKEND},target=/build/stable-diffusion.cpp/build \
    set -e && \
    rm -rf build/CMakeCache.txt build/CMakeFiles 2>/dev/null || true && \
    if [ "$BACKEND" = "cuda" ]; then \
        cmake -B build \
            -DSD_CUDA=ON \
            -DGGML_CUDA=ON \
            -DGGML_NATIVE=OFF \
            -DGGML_VULKAN=OFF \
            -DCMAKE_CUDA_ARCHITECTURES="${CMAKE_CUDA_ARCHITECTURES}" \
            -DCMAKE_BUILD_TYPE=Release \
            -DCMAKE_CUDA_FLAGS="-allow-unsupported-compiler" \
            -DCMAKE_EXE_LINKER_FLAGS="-Wl,-rpath-link,/usr/local/cuda/lib64/stubs -lcuda" \
            -DCMAKE_SHARED_LINKER_FLAGS="-Wl,-rpath-link,/usr/local/cuda/lib64/stubs -lcuda" \
            -DCMAKE_C_COMPILER_LAUNCHER=ccache \
            -DCMAKE_CXX_COMPILER_LAUNCHER=ccache; \
    elif [ "$BACKEND" = "vulkan" ]; then \
        cmake -B build \
            -DSD_VULKAN=ON \
            -DGGML_VULKAN=ON \
            -DSD_BUILD_EXAMPLES=OFF \
            -DGGML_NATIVE=OFF \
            -DVulkan_INCLUDE_DIR="${VULKAN_SDK}/include" \
            -DVulkan_LIBRARY="${VULKAN_SDK}/lib/libvulkan.so" \
            -DCMAKE_BUILD_TYPE=Release; \
    fi && \
    cmake --build build --config Release -j$(nproc) --target sd-cli sd-server && \
    mkdir -p /install/bin /install/lib && \
    cp build/bin/sd-cli build/bin/sd-server /install/bin/ && \
    test -f /install/bin/sd-server || { echo "FATAL: sd-server not built"; exit 1; } && \
    find build -name "*.so*" -type f -exec cp {} /install/lib/ \;

# --- llama.cpp (slow: 60-90 min) ---
FROM builder-base AS llama-build
ARG BACKEND=cuda
COPY --from=llama-source /src/llama.cpp /build/llama.cpp
WORKDIR /build/llama.cpp
RUN --mount=type=cache,id=ccache-${BACKEND},target=/ccache \
    --mount=type=cache,id=llama-${BACKEND},target=/build/llama.cpp/build \
    set -e && \
    rm -rf build/CMakeCache.txt build/CMakeFiles 2>/dev/null || true && \
    if [ "$BACKEND" = "cuda" ]; then \
        cmake -B build \
            -DGGML_NATIVE=OFF \
            -DGGML_CUDA=ON \
            -DGGML_VULKAN=OFF \
            -DCMAKE_CUDA_ARCHITECTURES="${CMAKE_CUDA_ARCHITECTURES}" \
            -DLLAMA_BUILD_TESTS=OFF \
            -DCMAKE_BUILD_TYPE=Release \
            -DCMAKE_CUDA_FLAGS="-allow-unsupported-compiler" \
            -DCMAKE_EXE_LINKER_FLAGS="-Wl,-rpath-link,/usr/local/cuda/lib64/stubs -lcuda" \
            -DCMAKE_SHARED_LINKER_FLAGS="-Wl,-rpath-link,/usr/local/cuda/lib64/stubs -lcuda" \
            -DCMAKE_C_COMPILER_LAUNCHER=ccache \
            -DCMAKE_CXX_COMPILER_LAUNCHER=ccache; \
    elif [ "$BACKEND" = "vulkan" ]; then \
        cmake -B build \
            -DGGML_NATIVE=OFF \
            -DGGML_VULKAN=ON \
            -DGGML_BACKEND_DL=ON \
            -DVulkan_INCLUDE_DIR="${VULKAN_SDK}/include" \
            -DVulkan_LIBRARY="${VULKAN_SDK}/lib/libvulkan.so" \
            -DLLAMA_BUILD_TESTS=OFF \
            -DCMAKE_BUILD_TYPE=Release; \
    fi && \
    cmake --build build --config Release -j$(nproc) --target llama-cli llama-server && \
    mkdir -p /install/bin /install/lib && \
    cp build/bin/llama-cli build/bin/llama-server /install/bin/ && \
    test -f /install/bin/llama-server || { echo "FATAL: llama-server not built"; exit 1; } && \
    find build -name "*.so*" -type f -exec cp {} /install/lib/ \;

# ============================================================================
# Stage 4: Runtime (backend-conditional)
# ============================================================================

# --- CUDA runtime ---
FROM nvidia/cuda:12.4.0-runtime-ubuntu22.04 AS runtime-cuda

ENV DEBIAN_FRONTEND=noninteractive
ENV LD_LIBRARY_PATH="/usr/local/cuda/lib64:${LD_LIBRARY_PATH}"

RUN apt-get update && apt-get install -y --no-install-recommends \
    libgomp1 \
    python3 \
    python3-pip \
    curl \
    ca-certificates \
    git \
    && rm -rf /var/lib/apt/lists/*

# Copy libcuda stubs for CPU-only testing (real drivers injected at runtime via --gpus all)
COPY --from=builder-base-cuda /usr/local/cuda/lib64/stubs/libcuda.so /usr/local/cuda/lib64/stubs/libcuda.so
COPY --from=builder-base-cuda /usr/local/cuda/lib64/stubs/libcuda.so /usr/local/cuda/lib64/stubs/libcuda.so.1

# --- Vulkan runtime ---
FROM ubuntu:22.04 AS runtime-vulkan

ENV DEBIAN_FRONTEND=noninteractive
ENV VK_DRIVER_FILES=/usr/share/vulkan/icd.d/lvp_icd.x86_64.json:/usr/share/vulkan/icd.d/radeon_icd.x86_64.json:/usr/share/vulkan/icd.d/intel_icd.x86_64.json

RUN apt-get update && apt-get install -y --no-install-recommends \
    libgomp1 \
    libvulkan1 \
    mesa-vulkan-drivers \
    vulkan-tools \
    python3 \
    python3-pip \
    curl \
    ca-certificates \
    git \
    && rm -rf /var/lib/apt/lists/*

# --- Select runtime by backend ---
FROM runtime-${BACKEND} AS runtime

ARG BACKEND=cuda
ARG LLAMA_COMMIT_HASH=unknown
ARG WHISPER_COMMIT_HASH=unknown
ARG SD_COMMIT_HASH=unknown

ENV PATH="/usr/local/bin:${PATH}"

# Copy llama-swap binary and version info
COPY --from=llama-swap-builder /app/llama-swap /usr/local/bin/
COPY --from=llama-swap-builder /app/llama-swap-version /tmp/

# Copy test script
COPY docker/test-binaries.sh /usr/local/bin/
RUN chmod +x /usr/local/bin/test-binaries.sh

RUN pip3 install --no-cache-dir numpy sentencepiece

WORKDIR /app

# Copy binaries and libraries from each project build stage
COPY --from=llama-build /install/bin/llama-server /usr/local/bin/
COPY --from=llama-build /install/bin/llama-cli /usr/local/bin/
COPY --from=llama-build /install/lib/ /usr/local/lib/

COPY --from=whisper-build /install/bin/whisper-server /usr/local/bin/
COPY --from=whisper-build /install/bin/whisper-cli /usr/local/bin/
COPY --from=whisper-build /install/lib/ /usr/local/lib/

COPY --from=sd-build /install/bin/sd-server /usr/local/bin/
COPY --from=sd-build /install/bin/sd-cli /usr/local/bin/
COPY --from=sd-build /install/lib/ /usr/local/lib/

# Update library cache
RUN ldconfig

# Create convenience symlinks
RUN ln -sf /usr/local/bin/llama-cli /usr/local/bin/llama && \
    ln -sf /usr/local/bin/whisper-cli /usr/local/bin/whisper && \
    ln -sf /usr/local/bin/sd-cli /usr/local/bin/stable-diffusion

# Validate all expected binaries exist — fail the build if any are missing
RUN set -e && \
    for bin in llama-server llama-cli whisper-server whisper-cli sd-server sd-cli llama-swap; do \
        test -x /usr/local/bin/$bin || { echo "FATAL: $bin missing from /usr/local/bin"; exit 1; }; \
    done && \
    echo "All binaries validated successfully"

# Write version information
RUN echo "llama.cpp: ${LLAMA_COMMIT_HASH}" > /versions.txt && \
    echo "whisper.cpp: ${WHISPER_COMMIT_HASH}" >> /versions.txt && \
    echo "stable-diffusion.cpp: ${SD_COMMIT_HASH}" >> /versions.txt && \
    echo "llama-swap: $(cat /tmp/llama-swap-version)" >> /versions.txt && \
    echo "backend: ${BACKEND}" >> /versions.txt && \
    echo "build_timestamp: $(date -u +%Y-%m-%dT%H:%M:%SZ)" >> /versions.txt

WORKDIR /models
CMD ["bash"]
