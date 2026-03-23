#!/bin/bash
# Install whisper.cpp - clone, build with CUDA, and install binaries
# Usage: ./install-whisper.sh <commit_hash>
set -e

COMMIT_HASH="${1:-master}"

mkdir -p /install/bin /install/lib

# Clone and checkout (init-based so cache-mounted /src/whisper.cpp/build dir doesn't break clone)
echo "=== Cloning whisper.cpp at ${COMMIT_HASH} ==="
mkdir -p /src/whisper.cpp
cd /src/whisper.cpp
if [ ! -d .git ]; then
    git init
    git remote add origin https://github.com/ggml-org/whisper.cpp.git
fi
git fetch --depth=1 origin "${COMMIT_HASH}"
git checkout FETCH_HEAD

# CUDA cmake flags
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
)

TARGETS=(whisper-cli whisper-server)

rm -rf build/CMakeCache.txt build/CMakeFiles 2>/dev/null || true

echo "=== Building whisper.cpp for CUDA ==="
cmake -B build "${CMAKE_FLAGS[@]}"
cmake --build build --config Release -j"$(nproc)" --target "${TARGETS[@]}"

for bin in "${TARGETS[@]}"; do
    if [ ! -f "build/bin/$bin" ]; then
        echo "FATAL: $bin not found in build/bin/" >&2
        exit 1
    fi
    cp "build/bin/$bin" "/install/bin/"
done
find build -name "*.so*" -type f -exec cp {} /install/lib/ \;

echo "=== whisper.cpp build complete ==="
ls -la /install/bin/
