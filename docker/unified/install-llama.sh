#!/bin/bash
# Install llama.cpp - clone, build, and install binaries
# Usage: BACKEND=cuda|vulkan ./install-llama.sh <commit_hash>
set -e

COMMIT_HASH="${1:-master}"
BACKEND="${BACKEND:-cuda}"

mkdir -p /install/bin

# Clone and checkout (init-based so cache-mounted /src/llama.cpp/build dir doesn't break clone)
echo "=== Cloning llama.cpp at ${COMMIT_HASH} ==="
mkdir -p /src/llama.cpp
cd /src/llama.cpp
if [ ! -d .git ]; then
    git init
    git remote add origin https://github.com/ggml-org/llama.cpp.git
fi
git fetch --depth=1 origin "${COMMIT_HASH}"
git checkout FETCH_HEAD

# Apply local patches to llama.cpp before building.
# Each patch undergoes a two-stage check:
#   1. Forward check passes  → apply the patch; abort if the apply itself fails.
#   2. Forward check fails + reverse check passes → fix is already in source; skip safely.
#   3. Both checks fail      → patch is malformed or context-mismatched; abort visibly.
# stderr from the forward check is shown in the build log to aid diagnosis.
PATCH_DIR="/build/patches"
if [ -d "${PATCH_DIR}" ]; then
    for patch in "${PATCH_DIR}"/*.patch; do
        [ -f "${patch}" ] || continue
        name=$(basename "${patch}")
        echo "=== Checking patch: ${name} ==="
        if git apply --check "${patch}" 2>&1; then
            git apply "${patch}" || { echo "FATAL: ${name} failed to apply" >&2; exit 1; }
            echo "    Applied."
        elif git apply --check --reverse "${patch}" 2>/dev/null; then
            echo "    Fix already present upstream — skipping safely."
        else
            echo "FATAL: ${name} did not apply and cannot be confirmed as already merged." >&2
            echo "       The patch is likely malformed or mismatched against this llama.cpp commit." >&2
            exit 1
        fi
    done
fi

# Common cmake flags
CMAKE_FLAGS=(
    -DGGML_NATIVE=OFF
    -DBUILD_SHARED_LIBS=OFF
    -DCMAKE_BUILD_TYPE=Release
    -DCMAKE_C_COMPILER_LAUNCHER=ccache
    -DCMAKE_CXX_COMPILER_LAUNCHER=ccache
    -DLLAMA_BUILD_TESTS=OFF
)

if [ "$BACKEND" = "cuda" ]; then
    CMAKE_FLAGS+=(
        -DGGML_CUDA=ON
        -DGGML_VULKAN=OFF
        "-DCMAKE_CUDA_ARCHITECTURES=${CMAKE_CUDA_ARCHITECTURES:?CMAKE_CUDA_ARCHITECTURES must be set}"
        "-DCMAKE_CUDA_FLAGS=-allow-unsupported-compiler"
        "-DCMAKE_EXE_LINKER_FLAGS=-Wl,-rpath-link,/usr/local/cuda/lib64/stubs -lcuda"
    )
elif [ "$BACKEND" = "vulkan" ]; then
    CMAKE_FLAGS+=(
        -DGGML_CUDA=OFF
        -DGGML_VULKAN=ON
    )
fi

TARGETS=(llama-cli llama-server)

rm -rf build/CMakeCache.txt build/CMakeFiles 2>/dev/null || true

echo "=== Building llama.cpp for ${BACKEND} ==="
cmake -B build "${CMAKE_FLAGS[@]}"
cmake --build build --config Release -j"$(nproc)" --target "${TARGETS[@]}"

for bin in "${TARGETS[@]}"; do
    if [ ! -f "build/bin/$bin" ]; then
        echo "FATAL: $bin not found in build/bin/" >&2
        exit 1
    fi
    cp "build/bin/$bin" "/install/bin/"
done
echo "=== llama.cpp build complete ==="
ls -la /install/bin/
