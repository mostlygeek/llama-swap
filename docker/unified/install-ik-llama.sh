#!/bin/bash
# Install ik_llama.cpp - clone, build, and install binaries
# Usage: ./install-ik-llama.sh <commit_hash>
# Note: CUDA only; always built against builder-base-cuda
set -e

COMMIT_HASH="${1:-main}"

mkdir -p /install/bin

# ik_llama.cpp main requires CUDA C++20; Ubuntu 22.04 ships CMake 3.22 which
# lacks the NVCC flag mapping for it. Upgrade cmake before building.
pip3 install --quiet --upgrade cmake

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
    -DGGML_CUDA=ON
    "-DCMAKE_CUDA_ARCHITECTURES=${CMAKE_CUDA_ARCHITECTURES:-60;61;75;86;89}"
    "-DCMAKE_C_FLAGS=-mcmodel=large"
    "-DCMAKE_CXX_FLAGS=-mcmodel=large"
    "-DCMAKE_CUDA_FLAGS=-allow-unsupported-compiler -Xcompiler=-mcmodel=large"
    "-DCMAKE_EXE_LINKER_FLAGS=-fuse-ld=mold -Wl,-rpath-link,/usr/local/cuda/lib64/stubs -lcuda -Wl,--allow-shlib-undefined"
)

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
