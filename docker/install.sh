#!/bin/bash
# Usage: ./install.sh <cuda|vulkan> <llama|whisper|sd>
#
# For vulkan builds of llama and sd, downloads prebuilt binaries from GitHub
# releases instead of building from source. Requires RELEASE_TAG env var.
# whisper.cpp has no prebuilt vulkan binaries, so it always builds from source.
set -e

BACKEND="$1"
PROJECT="$2"

if [ -z "$BACKEND" ] || [ -z "$PROJECT" ]; then
    echo "Usage: $0 <cuda|vulkan> <llama|whisper|sd>" >&2
    exit 1
fi

mkdir -p /install/bin /install/lib

# ---------------------------------------------------------------------------
# Vulkan prebuilt binary download for llama and sd
# ---------------------------------------------------------------------------
if [ "$BACKEND" = "vulkan" ] && [ "$PROJECT" != "whisper" ]; then
    if [ -z "${RELEASE_TAG:-}" ]; then
        echo "ERROR: RELEASE_TAG env var required for vulkan prebuilt download of $PROJECT" >&2
        exit 1
    fi

    TMPDIR=$(mktemp -d)
    trap 'rm -rf "$TMPDIR"' EXIT

    case "$PROJECT" in
        llama)
            # tag: b8429  asset: llama-b8429-bin-ubuntu-vulkan-x64.tar.gz
            ASSET="llama-${RELEASE_TAG}-bin-ubuntu-vulkan-x64.tar.gz"
            URL="https://github.com/ggml-org/llama.cpp/releases/download/${RELEASE_TAG}/${ASSET}"
            echo "=== Downloading prebuilt llama.cpp vulkan binaries ==="
            echo "URL: $URL"
            curl -fSL -o "${TMPDIR}/release.tar.gz" "$URL"
            tar xzf "${TMPDIR}/release.tar.gz" -C "${TMPDIR}"

            find "${TMPDIR}" -name "llama-server" -type f -exec cp {} /install/bin/ \;
            find "${TMPDIR}" -name "llama-cli" -type f -exec cp {} /install/bin/ \;
            find "${TMPDIR}" -name "*.so*" -type f -exec cp {} /install/lib/ \;
            EXPECTED_BINS="llama-server llama-cli"
            ;;
        sd)
            # tag: master-536-5265a5e  asset: sd-master-5265a5e-bin-Linux-...-vulkan.zip
            # The asset name drops the build number from the tag.
            SD_BRANCH=$(echo "$RELEASE_TAG" | cut -d'-' -f1)
            SD_HASH=$(echo "$RELEASE_TAG" | rev | cut -d'-' -f1 | rev)
            ASSET="sd-${SD_BRANCH}-${SD_HASH}-bin-Linux-Ubuntu-24.04-x86_64-vulkan.zip"
            URL="https://github.com/leejet/stable-diffusion.cpp/releases/download/${RELEASE_TAG}/${ASSET}"
            echo "=== Downloading prebuilt sd.cpp vulkan binaries ==="
            echo "URL: $URL"
            curl -fSL -o "${TMPDIR}/release.zip" "$URL"
            unzip -q "${TMPDIR}/release.zip" -d "${TMPDIR}"

            # sd.cpp release names the CLI binary "sd", rename to sd-cli
            if find "${TMPDIR}" -name "sd" -not -name "sd-*" -type f | grep -q .; then
                find "${TMPDIR}" -name "sd" -not -name "sd-*" -type f -exec cp {} /install/bin/sd-cli \;
            else
                find "${TMPDIR}" -name "sd-cli" -type f -exec cp {} /install/bin/ \;
            fi
            find "${TMPDIR}" -name "sd-server" -type f -exec cp {} /install/bin/ \;
            find "${TMPDIR}" -name "*.so*" -type f -exec cp {} /install/lib/ \;
            EXPECTED_BINS="sd-cli sd-server"
            ;;
    esac

    # Verify expected binaries were extracted
    for bin in $EXPECTED_BINS; do
        if [ ! -f "/install/bin/$bin" ]; then
            echo "ERROR: $bin not found in downloaded release" >&2
            echo "Archive contents:" >&2
            find "${TMPDIR}" -type f >&2
            exit 1
        fi
    done

    chmod +x /install/bin/*
    echo "=== $PROJECT prebuilt vulkan binaries installed ==="
    ls -la /install/bin/
    exit 0
fi

# ---------------------------------------------------------------------------
# Build from source (cuda, or vulkan whisper)
# ---------------------------------------------------------------------------
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

rm -rf build/CMakeCache.txt build/CMakeFiles 2>/dev/null || true

echo "=== Building $PROJECT for $BACKEND ==="

# shellcheck disable=SC2086
cmake -B build $COMMON_FLAGS $PROJECT_FLAGS
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

echo "=== $PROJECT build complete ==="
ls -la /install/bin/
