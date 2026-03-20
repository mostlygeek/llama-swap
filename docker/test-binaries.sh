#!/bin/bash
# Test script for verifying GPU-accelerated binaries work correctly
# Supports both CUDA and Vulkan backends, auto-detecting the environment

set -e

# Colors for output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
NC='\033[0m' # No Color

print_info() {
    echo -e "${GREEN}[INFO]${NC} $1"
}

print_warn() {
    echo -e "${YELLOW}[WARN]${NC} $1"
}

print_error() {
    echo -e "${RED}[ERROR]${NC} $1"
}

# Detect if real NVIDIA drivers are available
detect_cuda_drivers() {
    local real_driver_paths=(
        "/lib/x86_64-linux-gnu/libcuda.so.1"
        "/usr/lib/x86_64-linux-gnu/libcuda.so.1"
        "/usr/local/cuda/lib64/libcuda.so.1"
    )

    for path in "${real_driver_paths[@]}"; do
        if [ -f "$path" ]; then
            print_info "Real NVIDIA drivers found at: $path"
            return 0
        fi
    done

    return 1
}

# Detect Vulkan ICD availability
detect_vulkan() {
    if [ -d "/usr/share/vulkan/icd.d" ] && ls /usr/share/vulkan/icd.d/*.json >/dev/null 2>&1; then
        print_info "Vulkan ICDs found:"
        ls /usr/share/vulkan/icd.d/*.json 2>/dev/null | while read -r f; do echo "  $f"; done
        return 0
    fi
    return 1
}

# Main execution
print_info "Starting binary tests..."

# Set up GPU library environment
if detect_cuda_drivers; then
    print_info "Using real NVIDIA drivers"
    export LD_LIBRARY_PATH="/usr/local/cuda/lib64${LD_LIBRARY_PATH:+:$LD_LIBRARY_PATH}"
elif [ -d "/usr/local/cuda/lib64/stubs" ]; then
    print_warn "No real NVIDIA drivers detected"
    print_warn "Falling back to stub drivers for testing"
    print_warn "GPU functionality will NOT be available"
    export LD_LIBRARY_PATH="/usr/local/cuda/lib64/stubs:/usr/local/cuda/lib64${LD_LIBRARY_PATH:+:$LD_LIBRARY_PATH}"
    print_info "LD_LIBRARY_PATH set to: $LD_LIBRARY_PATH"
elif detect_vulkan; then
    print_info "Vulkan backend detected"
else
    print_warn "No GPU drivers detected (CPU-only mode)"
fi

# Test all expected server binaries
BINARIES=(llama-server whisper-server sd-server)
FAILED=0

for binary in "${BINARIES[@]}"; do
    print_info "Testing ${binary}..."
    if command -v "$binary" &> /dev/null; then
        if "$binary" --help > /dev/null 2>&1 || "$binary" -h > /dev/null 2>&1; then
            print_info "  $binary: OK"
        else
            print_error "  $binary: Failed to run"
            FAILED=1
        fi
    else
        print_error "  $binary: Not found in PATH"
        FAILED=1
    fi
done

if [ "$FAILED" -ne 0 ]; then
    print_error "Some binary tests failed!"
    exit 1
fi

print_info "All binary tests passed!"

# Additional environment info
print_info "Environment information:"
echo "  LD_LIBRARY_PATH: ${LD_LIBRARY_PATH:-not set}"
echo "  CUDA_VISIBLE_DEVICES: ${CUDA_VISIBLE_DEVICES:-not set}"

if command -v nvidia-smi &> /dev/null; then
    print_info "nvidia-smi output:"
    nvidia-smi --query-gpu=name,driver_version,memory.total --format=csv,noheader 2>/dev/null || \
        print_warn "nvidia-smi found but could not query GPU information"
elif command -v vulkaninfo &> /dev/null; then
    print_info "Vulkan device info:"
    vulkaninfo --summary 2>/dev/null | head -20 || \
        print_warn "vulkaninfo found but could not query device information"
else
    print_warn "No GPU query tools available (expected on CPU-only hosts)"
fi

exit 0
