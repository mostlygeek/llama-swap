#!/usr/bin/env bash
# Build a llama.cpp-compatible fork inside the current container.
# Output: <out>/llama-server and any other selected binaries that were built.

set -euo pipefail

DEFAULT_REPO="https://github.com/ggml-org/llama.cpp.git"
DEFAULT_BRANCH="master"
DEFAULT_OUT="/backends/llamacpp"
DEFAULT_BACKEND="${BACKEND:-cuda}"

usage() {
  cat <<EOF
Usage: $(basename "$0") [OPTIONS]

Build a llama.cpp-compatible fork in this container.

Options:
  --repo <url>       Git repository to clone  (default: $DEFAULT_REPO)
  --branch <name>    Branch/ref to build      (default: $DEFAULT_BRANCH)
  --out <dir>        Output directory         (default: $DEFAULT_OUT)
  --backend <name>   cuda or vulkan           (default: $DEFAULT_BACKEND)
  --cmake-arg <arg>  Extra CMake configure arg; may be repeated
  --help             Show this help and exit
EOF
}

repo="$DEFAULT_REPO"
branch="$DEFAULT_BRANCH"
dest="$DEFAULT_OUT"
backend="$DEFAULT_BACKEND"
extra_cmake_args=()

while [[ $# -gt 0 ]]; do
  case "$1" in
    --repo)
      repo="${2:?--repo requires a value}"
      shift 2
      ;;
    --branch)
      branch="${2:?--branch requires a value}"
      shift 2
      ;;
    --out)
      dest="${2:?--out requires a value}"
      shift 2
      ;;
    --backend)
      backend="${2:?--backend requires a value}"
      shift 2
      ;;
    --cmake-arg)
      extra_cmake_args+=("${2:?--cmake-arg requires a value}")
      shift 2
      ;;
    --help)
      usage
      exit 0
      ;;
    *)
      echo "Unknown option: $1" >&2
      usage >&2
      exit 1
      ;;
  esac
done

case "$backend" in
  cuda|vulkan) ;;
  *)
    echo "Unsupported backend: $backend" >&2
    exit 1
    ;;
esac

if [[ "$repo" != *"://"* && "$repo" != git@* ]]; then
  repo="https://${repo}"
fi

build_id="$(date +%s)-$$"
src="/tmp/llamacpp-build-${build_id}/src"
mkdir -p "$dest" "$(dirname "$src")"
dest="$(realpath "$dest")"

cleanup() {
  rm -rf "/tmp/llamacpp-build-${build_id}"
}
trap cleanup EXIT

echo "=== Cloning ${repo}@${branch} ==="
mkdir -p "$src"
cd "$src"
git init
git remote add origin "$repo"
git fetch --depth=1 origin "$branch"
git checkout FETCH_HEAD

cmake_flags=(
  -DGGML_NATIVE=OFF
  -DBUILD_SHARED_LIBS=OFF
  -DCMAKE_BUILD_TYPE=Release
  -DLLAMA_BUILD_TESTS=OFF
)

if command -v ccache >/dev/null 2>&1; then
  cmake_flags+=(
    -DCMAKE_C_COMPILER_LAUNCHER=ccache
    -DCMAKE_CXX_COMPILER_LAUNCHER=ccache
  )
fi

if [[ "$backend" == "cuda" ]]; then
  if ! command -v nvcc >/dev/null 2>&1; then
    echo "FATAL: CUDA builds require nvcc. Use a CUDA devel image, not runtime." >&2
    exit 127
  fi
  cmake_flags+=(
    -DGGML_CUDA=ON
    -DGGML_VULKAN=OFF
    "-DCMAKE_CUDA_ARCHITECTURES=${CMAKE_CUDA_ARCHITECTURES:-75;86;89}"
    "-DCMAKE_CUDA_FLAGS=-allow-unsupported-compiler"
    "-DCMAKE_EXE_LINKER_FLAGS=-Wl,-rpath-link,/usr/local/cuda/lib64/stubs -lcuda"
  )
else
  cmake_flags+=(
    -DGGML_CUDA=OFF
    -DGGML_VULKAN=ON
  )
fi

echo "=== Configuring llama.cpp for ${backend} ==="
cmake -B build -G Ninja "${cmake_flags[@]}" "${extra_cmake_args[@]}"

echo "=== Building llama-server ==="
cmake --build build --config Release -j"${MAX_BUILD_JOBS:-4}" --target llama-server

optional_targets=(
  llama-cli
  llama-diffusion-cli
  llama-diffusion-server
  llama-diffusion-gemma-cli
  llama-diffusion-gemma-server
)

for target in "${optional_targets[@]}"; do
  echo "=== Building optional target ${target} ==="
  cmake --build build --config Release -j"${MAX_BUILD_JOBS:-4}" --target "$target" || true
done

for bin in llama-server "${optional_targets[@]}"; do
  if [[ -f "build/bin/${bin}" ]]; then
    cp "build/bin/${bin}" "$dest/"
    chmod +x "$dest/${bin}"
  fi
done

if [[ ! -x "$dest/llama-server" ]]; then
  echo "FATAL: llama-server was not built" >&2
  exit 1
fi

echo "=== Built backend binaries ==="
ls -lh "$dest"
