#!/bin/bash
# Install llama.cpp - clone, build with CUDA, and install binaries
# Usage: ./install-llama.sh <commit_hash>
set -e

COMMIT_HASH="${1:-master}"

mkdir -p /install/bin /install/lib

# Clone and checkout
echo "=== Cloning llama.cpp at ${COMMIT_HASH} ==="
git clone --filter=blob:none --no-checkout https://github.com/ggml-org/llama.cpp.git /src/llama.cpp
cd /src/llama.cpp
git fetch --depth=1 origin "${COMMIT_HASH}"
git checkout FETCH_HEAD

# CUDA cmake flags + llama-specific flags
CMAKE_FLAGS="
    -DGGML_NATIVE=OFF
    -DCMAKE_BUILD_TYPE=Release
    -DGGML_CUDA=ON
    -DGGML_VULKAN=OFF
    -DCMAKE_CUDA_ARCHITECTURES=${CMAKE_CUDA_ARCHITECTURES:-60;61;75;86;89}
    -DCMAKE_CUDA_FLAGS=-allow-unsupported-compiler
    -DCMAKE_EXE_LINKER_FLAGS=-Wl,-rpath-link,/usr/local/cuda/lib64/stubs -lcuda
    -DCMAKE_SHARED_LINKER_FLAGS=-Wl,-rpath-link,/usr/local/cuda/lib64/stubs -lcuda
    -DCMAKE_C_COMPILER_LAUNCHER=ccache
    -DCMAKE_CXX_COMPILER_LAUNCHER=ccache
    -DLLAMA_BUILD_TESTS=OFF
"

TARGETS="llama-cli llama-server"

rm -rf build/CMakeCache.txt build/CMakeFiles 2>/dev/null || true

echo "=== Building llama.cpp for CUDA ==="
# shellcheck disable=SC2086
cmake -B build $CMAKE_FLAGS
# shellcheck disable=SC2086
cmake --build build --config Release -j"$(nproc)" --target $TARGETS

for bin in $TARGETS; do
    if [ ! -f "build/bin/$bin" ]; then
        echo "FATAL: $bin not found in build/bin/" >&2
        exit 1
    fi
    cp "build/bin/$bin" "/install/bin/"
done
find build -name "*.so*" -type f -exec cp {} /install/lib/ \;

echo "=== llama.cpp build complete ==="
ls -la /install/bin/
