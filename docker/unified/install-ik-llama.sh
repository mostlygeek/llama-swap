#!/bin/bash
# Install ik_llama.cpp - clone, build, and install binaries
# Usage: BACKEND=cuda|vulkan ./install-ik-llama.sh <commit_hash>
# Supports CUDA, Vulkan, and CPU-only builds
set -e

COMMIT_HASH="${1:-main}"
BACKEND="${BACKEND:-cpu}"

mkdir -p /install/bin

# Clone and checkout (init-based so cache-mounted build dir doesn't break clone)
echo "=== Cloning ik_llama.cpp at ${COMMIT_HASH} ==="
mkdir -p /src/ik_llama.cpp
cd /src/ik_llama.cpp
if [ ! -d .git ]; then
    git init
    git remote add origin https://github.com/ikawrakow/ik_llama.cpp.git
fi
git fetch --depth=1 origin "${COMMIT_HASH}"
git checkout FETCH_HEAD

CMAKE_FLAGS=(
    -DGGML_NATIVE=OFF
    -DBUILD_SHARED_LIBS=OFF
    -DCMAKE_BUILD_TYPE=Release
    -DCMAKE_C_COMPILER_LAUNCHER=ccache
    -DCMAKE_CXX_COMPILER_LAUNCHER=ccache
)

case "$BACKEND" in
    cuda)
        CMAKE_FLAGS+=(
            -DGGML_CUDA=ON
            "-DCMAKE_CUDA_ARCHITECTURES=${CMAKE_CUDA_ARCHITECTURES:?CMAKE_CUDA_ARCHITECTURES must be set}"
            "-DCMAKE_CUDA_FLAGS=-allow-unsupported-compiler"
            "-DCMAKE_EXE_LINKER_FLAGS=-Wl,-rpath-link,/usr/local/cuda/lib64/stubs -lcuda -Wl,--allow-shlib-undefined"
        )
        ;;
    vulkan)
        CMAKE_FLAGS+=(-DGGML_VULKAN=ON)
        ;;
    *)
        echo "INFO: Building ik_llama.cpp CPU-only (BACKEND=${BACKEND})" >&2
        ;;
esac

rm -rf build/CMakeCache.txt build/CMakeFiles 2>/dev/null || true

echo "=== Building ik_llama.cpp ==="
cmake -B build "${CMAKE_FLAGS[@]}"
cmake --build build --config Release -j"$(nproc)" --target llama-server

if [ ! -f "build/bin/llama-server" ]; then
    echo "FATAL: llama-server not found in build/bin/" >&2
    exit 1
fi

# Install as ik-llama-server to avoid collision with llama.cpp's llama-server
cp "build/bin/llama-server" "/install/bin/ik-llama-server"
echo "=== ik_llama.cpp build complete ==="
ls -la /install/bin/
