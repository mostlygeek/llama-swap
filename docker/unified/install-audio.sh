#!/bin/bash
# Install audio.cpp - clone, build, and install binaries
# Usage: BACKEND=cuda|vulkan ./install-audio.sh <commit_hash>
set -e

COMMIT_HASH="${1:-main}"
BACKEND="${BACKEND:-cuda}"
CUDA_ROOT="${CUDA_ROOT:-/usr/local/cuda}"

mkdir -p /install/bin /install/share/audiocpp

# Clone and checkout (init-based so cache-mounted /src/audio.cpp/build dir doesn't break clone)
echo "=== Cloning audio.cpp at ${COMMIT_HASH} ==="
mkdir -p /src/audio.cpp
cd /src/audio.cpp
if [ ! -d .git ]; then
    git init
    git remote add origin https://github.com/0xShug0/audio.cpp.git
fi
git fetch --depth=1 origin "${COMMIT_HASH}"
git checkout FETCH_HEAD

# Common cmake flags
#
# AUDIOCPP_DEPLOYMENT_BUILD=ON is what audio.cpp calls a deployment build: it
# compiles the model_specs/*.json catalog into the binaries. Without it a model
# spec is only resolved from a GGUF that embeds one, or from a model_specs/
# directory found by walking up from the process working directory. An image
# that ships bare binaries has neither, so loading a safetensors package or a
# GGUF written before embedded specs fails with
# "model spec not found for family <family>".
#
# ENGINE_ENABLE_NATIVE_CPU=OFF builds portable CPU kernels so the image runs on
# hosts other than the build machine (equivalent to GGML_NATIVE=OFF elsewhere).
# This also keeps the build fully static: audio.cpp only switches to shared
# libraries under ENGINE_ENABLE_CPU_ALL_VARIANTS, which would drop a third,
# ABI-incompatible libggml*.so into the /usr/local/lib shared with whisper.cpp
# and stable-diffusion.cpp. The check after the build enforces that.
CMAKE_FLAGS=(
    -DCMAKE_BUILD_TYPE=Release
    -DCMAKE_C_COMPILER_LAUNCHER=ccache
    -DCMAKE_CXX_COMPILER_LAUNCHER=ccache
    -DAUDIOCPP_DEPLOYMENT_BUILD=ON
    -DAUDIOCPP_MODEL_SET=full
    -DENGINE_ENABLE_NATIVE_CPU=OFF
    -DENGINE_ENABLE_OPENMP=ON
    -DENGINE_BUILD_EXAMPLES=OFF
    -DENGINE_BUILD_TESTS=OFF
    -DENGINE_BUILD_WARMBENCH=OFF
)

if [ "$BACKEND" = "cuda" ]; then
    # docs/build/linux.md: set CUDAToolkit_ROOT *and* CMAKE_CUDA_COMPILER.
    # CMake otherwise takes the first nvcc on PATH and the first CUDA libraries
    # it finds, which can be two different toolkits. That mismatch links without
    # a warning, so the linked runtime is verified after the build below.
    CMAKE_FLAGS+=(
        -DENGINE_ENABLE_CUDA=ON
        -DENGINE_ENABLE_CUDA_GRAPHS=ON
        -DENGINE_ENABLE_VULKAN=OFF
        "-DCUDAToolkit_ROOT=${CUDA_ROOT}"
        "-DCMAKE_CUDA_COMPILER=${CUDA_ROOT}/bin/nvcc"
        "-DCMAKE_CUDA_ARCHITECTURES=${CMAKE_CUDA_ARCHITECTURES:?CMAKE_CUDA_ARCHITECTURES must be set}"
        "-DCMAKE_CUDA_FLAGS=-allow-unsupported-compiler"
        "-DCMAKE_EXE_LINKER_FLAGS=-Wl,-rpath-link,/usr/local/cuda/lib64/stubs -lcuda"
    )
elif [ "$BACKEND" = "vulkan" ]; then
    CMAKE_FLAGS+=(
        -DENGINE_ENABLE_CUDA=OFF
        -DENGINE_ENABLE_VULKAN=ON
    )
fi

TARGETS=(audiocpp_cli audiocpp_server)

rm -rf build/CMakeCache.txt build/CMakeFiles 2>/dev/null || true

echo "=== Building audio.cpp for ${BACKEND} ==="
cmake -B build "${CMAKE_FLAGS[@]}"
cmake --build build --config Release -j"$(nproc)" --target "${TARGETS[@]}"

# Verify each binary got the backend it was configured for. A backend that
# silently failed to enable still produces working binaries -- they just fall
# back to CPU at runtime, on a user's machine, long after the build passed.
verify_linkage() {
    local bin="$1"
    local needed
    needed=$(readelf -d "$bin" | grep NEEDED || true)

    case "$BACKEND" in
        cuda)
            if ! grep -qE 'libcudart\.so\.1[23]' <<<"$needed"; then
                echo "FATAL: $bin is not linked against a CUDA 12/13 runtime." >&2
                echo "       NEEDED entries:" >&2
                echo "$needed" >&2
                exit 1
            fi
            # A mixed build (new nvcc, old distro libcudart) links cleanly and
            # then misbehaves at runtime -- docs/build/linux.md calls this out.
            if grep -qE 'libcudart\.so\.11' <<<"$needed"; then
                echo "FATAL: $bin links libcudart.so.11 (mixed CUDA toolkit build)." >&2
                exit 1
            fi
            ;;
        vulkan)
            if ! grep -q 'libvulkan\.so' <<<"$needed"; then
                echo "FATAL: $bin is not linked against libvulkan." >&2
                echo "       NEEDED entries:" >&2
                echo "$needed" >&2
                exit 1
            fi
            ;;
    esac

    # audio.cpp vendors its own ggml fork. The runtime image already installs
    # whisper.cpp's and stable-diffusion.cpp's libggml*.so into /usr/local/lib,
    # so a shared audio.cpp build would either collide with them or need its own
    # library directory. Keep it static and fail loudly if that ever changes.
    if grep -qE 'libggml|libengine' <<<"$needed"; then
        echo "FATAL: $bin expects audio.cpp shared libraries; only static builds" >&2
        echo "       are installed here (see the CMAKE_FLAGS comment)." >&2
        echo "$needed" >&2
        exit 1
    fi
}

for bin in "${TARGETS[@]}"; do
    if [ ! -f "build/bin/$bin" ]; then
        echo "FATAL: $bin not found in build/bin/" >&2
        exit 1
    fi
    verify_linkage "build/bin/$bin"
    cp "build/bin/$bin" "/install/bin/"
done

# The specs are compiled into the binaries by AUDIOCPP_DEPLOYMENT_BUILD, but the
# catalog is only a fallback: shipping it on disk as well gives users a concrete
# path for --model-spec-override when they need to edit or pin a spec.
rm -rf "/install/share/audiocpp/model_specs"
cp -r model_specs "/install/share/audiocpp/model_specs"

echo "=== audio.cpp build complete ==="
ls -la /install/bin/
