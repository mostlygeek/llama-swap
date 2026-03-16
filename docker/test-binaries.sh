#!/bin/bash
# Test script for verifying CUDA-enabled binaries work
# Automatically detects real NVIDIA drivers vs stub drivers

set -e

# Colors for output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
NC='\033[0m' # No Color

# Function to print colored output
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

# Main execution
print_info "Starting binary tests..."

# Check for real drivers
if detect_cuda_drivers; then
    print_info "Using real NVIDIA drivers"
    # Unset any stub-related LD_LIBRARY_PATH to avoid conflicts
    export LD_LIBRARY_PATH="/usr/local/cuda/lib64${LD_LIBRARY_PATH:+:$LD_LIBRARY_PATH}"
else
    print_warn "No real NVIDIA drivers detected"
    print_warn "Falling back to stub drivers for testing"
    print_warn "GPU functionality will NOT be available"
    
    # Add stubs to LD_LIBRARY_PATH for testing
    export LD_LIBRARY_PATH="/usr/local/cuda/lib64/stubs:/usr/local/cuda/lib64${LD_LIBRARY_PATH:+:$LD_LIBRARY_PATH}"
    print_info "LD_LIBRARY_PATH set to: $LD_LIBRARY_PATH"
fi

# Test llama-server
print_info "Testing llama-server..."
if command -v llama-server &> /dev/null; then
    if llama-server --help > /dev/null 2>&1 || llama-server -h > /dev/null 2>&1; then
        print_info "✓ llama-server: OK"
    else
        print_error "✗ llama-server: Failed to run"
        exit 1
    fi
else
    print_error "✗ llama-server: Not found in PATH"
    exit 1
fi

# Test whisper-server
print_info "Testing whisper-server..."
if command -v whisper-server &> /dev/null; then
    if whisper-server --help > /dev/null 2>&1 || whisper-server -h > /dev/null 2>&1; then
        print_info "✓ whisper-server: OK"
    else
        print_error "✗ whisper-server: Failed to run"
        exit 1
    fi
else
    print_error "✗ whisper-server: Not found in PATH"
    exit 1
fi

# Test sd-server (stable-diffusion)
print_info "Testing sd-server..."
if command -v sd-server &> /dev/null; then
    if sd-server --help > /dev/null 2>&1 || sd-server -h > /dev/null 2>&1; then
        print_info "✓ sd-server: OK"
    else
        print_error "✗ sd-server: Failed to run"
        exit 1
    fi
else
    print_error "✗ sd-server: Not found in PATH"
    exit 1
fi

print_info "All binary tests passed!"

# Additional info about environment
print_info "Environment information:"
echo "  LD_LIBRARY_PATH: $LD_LIBRARY_PATH"
echo "  CUDA_VISIBLE_DEVICES: ${CUDA_VISIBLE_DEVICES:-not set}"

# Check if nvidia-smi is available
if command -v nvidia-smi &> /dev/null; then
    print_info "nvidia-smi output:"
    nvidia-smi --query-gpu=name,driver_version,memory.total --format=csv,noheader 2>/dev/null || \
        print_warn "nvidia-smi found but could not query GPU information"
else
    print_warn "nvidia-smi not available (expected on CPU-only hosts)"
fi

exit 0
