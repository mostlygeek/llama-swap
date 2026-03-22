#!/bin/bash
# Install stable-diffusion.cpp - clone and build with CUDA, install binaries and library
# Usage: ./install-sd.sh <commit_hash>
set -e

COMMIT_HASH="${1:-master}"

mkdir -p /install/bin /install/lib

# Clone and checkout (init-based so cache-mounted /src/stable-diffusion.cpp/build dir doesn't break clone)
echo "=== Cloning stable-diffusion.cpp at ${COMMIT_HASH} ==="
mkdir -p /src/stable-diffusion.cpp
cd /src/stable-diffusion.cpp
if [ ! -d .git ]; then
    git init
    git remote add origin https://github.com/leejet/stable-diffusion.cpp.git
fi
git fetch --depth=1 origin "${COMMIT_HASH}"
git checkout FETCH_HEAD
git submodule update --init --recursive --depth=1

# CUDA cmake flags + sd-specific flags
CMAKE_FLAGS=(
    -DGGML_NATIVE=OFF
    -DCMAKE_BUILD_TYPE=Release
    -DGGML_CUDA=ON
    -DGGML_VULKAN=OFF
    "-DCMAKE_CUDA_ARCHITECTURES=${CMAKE_CUDA_ARCHITECTURES:-60;61;75;86;89}"
    "-DCMAKE_CUDA_FLAGS=-allow-unsupported-compiler"
    "-DCMAKE_EXE_LINKER_FLAGS=-Wl,-rpath-link,/usr/local/cuda/lib64/stubs -lcuda"
    "-DCMAKE_SHARED_LINKER_FLAGS=-Wl,-rpath-link,/usr/local/cuda/lib64/stubs -lcuda"
    -DCMAKE_C_COMPILER_LAUNCHER=ccache
    -DCMAKE_CXX_COMPILER_LAUNCHER=ccache
    -DSD_BUILD_EXAMPLES=ON
    -DSD_CUDA=ON
)

TARGETS=(stable-diffusion sd-cli sd-server)

rm -rf build/CMakeCache.txt build/CMakeFiles 2>/dev/null || true

echo "=== Building stable-diffusion.cpp for CUDA ==="
cmake -B build "${CMAKE_FLAGS[@]}"
cmake --build build --config Release -j"$(nproc)" --target "${TARGETS[@]}"

for bin in sd-cli sd-server; do
    if [ ! -f "build/bin/$bin" ]; then
        echo "FATAL: $bin not found in build/bin/" >&2
        exit 1
    fi
    cp "build/bin/$bin" "/install/bin/"
done
find build -name "*.so*" -type f -exec cp {} /install/lib/ \;

echo "=== stable-diffusion.cpp build complete ==="
ls -la /install/bin/ /install/lib/
