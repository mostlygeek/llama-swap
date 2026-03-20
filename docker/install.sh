#!/bin/bash
# Build a server project against the selected GPU backend.
#
# Usage: ./install.sh <backend> <project>
#   backend: cuda | vulkan
#   project: llama | whisper | sd
#
# Outputs binaries to /install/bin/ and shared libs to /install/lib/
# Fails the build if any expected binary is missing.

set -e

BACKEND="$1"
PROJECT="$2"

if [ -z "$BACKEND" ] || [ -z "$PROJECT" ]; then
    echo "Usage: $0 <cuda|vulkan> <llama|whisper|sd>" >&2
    exit 1
fi

# --- Common cmake flags by backend ---

COMMON_FLAGS="-DGGML_NATIVE=OFF -DCMAKE_BUILD_TYPE=Release"

case "$BACKEND" in
    cuda)
        COMMON_FLAGS="$COMMON_FLAGS
            -DGGML_CUDA=ON
            -DGGML_VULKAN=OFF
            -DCMAKE_CUDA_ARCHITECTURES=${CMAKE_CUDA_ARCHITECTURES:-60;61;75;86;89}
            -DCMAKE_CUDA_FLAGS=-allow-unsupported-compiler
            -DCMAKE_EXE_LINKER_FLAGS=-Wl,-rpath-link,/usr/local/cuda/lib64/stubs -lcuda
            -DCMAKE_SHARED_LINKER_FLAGS=-Wl,-rpath-link,/usr/local/cuda/lib64/stubs -lcuda
            -DCMAKE_C_COMPILER_LAUNCHER=ccache
            -DCMAKE_CXX_COMPILER_LAUNCHER=ccache"
        ;;
    vulkan)
        COMMON_FLAGS="$COMMON_FLAGS
            -DGGML_VULKAN=ON
            -DVulkan_INCLUDE_DIR=${VULKAN_SDK}/include
            -DVulkan_LIBRARY=${VULKAN_SDK}/lib/libvulkan.so"
        ;;
    *)
        echo "Unknown backend: $BACKEND" >&2
        exit 1
        ;;
esac

# --- Per-project cmake flags and targets ---

case "$PROJECT" in
    llama)
        PROJECT_FLAGS="-DLLAMA_BUILD_TESTS=OFF"
        [ "$BACKEND" = "vulkan" ] && PROJECT_FLAGS="$PROJECT_FLAGS -DGGML_BACKEND_DL=ON"
        TARGETS="llama-cli llama-server"
        ;;
    whisper)
        PROJECT_FLAGS=""
        TARGETS="whisper-cli whisper-server"
        ;;
    sd)
        PROJECT_FLAGS="-DSD_BUILD_EXAMPLES=OFF"
        [ "$BACKEND" = "cuda" ]  && PROJECT_FLAGS="$PROJECT_FLAGS -DSD_CUDA=ON"
        [ "$BACKEND" = "vulkan" ] && PROJECT_FLAGS="$PROJECT_FLAGS -DSD_VULKAN=ON"
        TARGETS="sd-cli sd-server"
        ;;
    *)
        echo "Unknown project: $PROJECT" >&2
        exit 1
        ;;
esac

# --- Build ---

rm -rf build/CMakeCache.txt build/CMakeFiles 2>/dev/null || true

echo "=== Building $PROJECT for $BACKEND ==="
echo "Common flags: $COMMON_FLAGS"
echo "Project flags: $PROJECT_FLAGS"
echo "Targets: $TARGETS"

# shellcheck disable=SC2086
cmake -B build $COMMON_FLAGS $PROJECT_FLAGS
# shellcheck disable=SC2086
cmake --build build --config Release -j"$(nproc)" --target $TARGETS

# --- Collect artifacts ---

mkdir -p /install/bin /install/lib
for bin in $TARGETS; do
    if [ ! -f "build/bin/$bin" ]; then
        echo "FATAL: $bin not found in build/bin/" >&2
        exit 1
    fi
    cp "build/bin/$bin" "/install/bin/"
done
find build -name "*.so*" -type f -exec cp {} /install/lib/ \;

echo "=== $PROJECT build complete ==="
ls -la /install/bin/
