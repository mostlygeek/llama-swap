/*
 * TMA (Tensor Memory Accelerator) primitives for Marlin kernel.
 * Replaces cp.async loads with cp.async.bulk.tensor for SM90+/SM120+.
 *
 * TMA advantages:
 *   - Hardware address computation (tensor descriptors)
 *   - Larger transfer granularity (up to 64KB vs 16 bytes)
 *   - Reduced register pressure (fewer address calculations)
 *   - Barrier-based synchronization
 *
 * SM120/SM121 specifics:
 *   - Uses shared::cta scope (no cluster multicast)
 *   - CUTE_ARCH_TMA_SM120_ENABLED flag
 *   - Same cuTensorMapEncodeTiled API as SM90
 *
 * Activation: -DMARLIN_USE_TMA compile flag + VLLM_MARLIN_USE_TMA=1 env var
 */

#pragma once

#ifndef _marlin_tma_cuh
#define _marlin_tma_cuh

#include <cuda.h>
#include <cuda_runtime.h>
#include <stdint.h>

#ifndef MARLIN_NAMESPACE_NAME
  #define MARLIN_NAMESPACE_NAME marlin
#endif

namespace MARLIN_NAMESPACE_NAME {
namespace tma {

// ============================================================================
// TMA Descriptor Management (Host-side)
// ============================================================================

// TMA descriptor is 128 bytes, 64-byte aligned
struct alignas(64) TmaDescriptor {
  char data[128];
};

// Create a 2D TMA descriptor for weight matrix tiles.
// The weight matrix B is stored as int32 (8 INT4 values packed per int32).
//
// Parameters:
//   desc       - Output TMA descriptor
//   gmem_ptr   - Global memory pointer to weight matrix (16-byte aligned)
//   dim_k      - K dimension of weight matrix (in int32 elements)
//   dim_n      - N dimension of weight matrix (in int32 elements)
//   stride_k   - Stride in K dimension (in bytes)
//   box_k      - Tile size in K dimension (elements to load per TMA op)
//   box_n      - Tile size in N dimension (elements to load per TMA op)
//
// Returns: cudaError_t
inline cudaError_t create_weight_tma_desc(
    TmaDescriptor* desc,
    const void* gmem_ptr,
    uint64_t dim_k,
    uint64_t dim_n,
    uint64_t stride_k,  // in bytes
    uint32_t box_k,
    uint32_t box_n) {
  // Map INT4-packed-as-int32 to CU_TENSOR_MAP_DATA_TYPE_INT32
  CUtensorMapDataType data_type = CU_TENSOR_MAP_DATA_TYPE_INT32;

  // 2D tensor: [K, N]
  uint64_t global_dim[2] = {dim_k, dim_n};
  uint64_t global_stride[1] = {stride_k};  // stride[0] is implicit (sizeof(element))
  uint32_t box_dim[2] = {box_k, box_n};
  uint32_t element_stride[2] = {1, 1};

  CUresult result = cuTensorMapEncodeTiled(
      reinterpret_cast<CUtensorMap*>(desc),
      data_type,
      2,  // tensorRank
      const_cast<void*>(gmem_ptr),
      global_dim,
      global_stride,
      box_dim,
      element_stride,
      CU_TENSOR_MAP_INTERLEAVE_NONE,
      CU_TENSOR_MAP_SWIZZLE_NONE,     // No swizzle for Marlin layout
      CU_TENSOR_MAP_L2_PROMOTION_NONE,
      CU_TENSOR_MAP_FLOAT_OOB_FILL_NONE);

  return (result == CUDA_SUCCESS) ? cudaSuccess : cudaErrorUnknown;
}

// Create a 1D TMA descriptor for scale vectors.
inline cudaError_t create_scale_tma_desc(
    TmaDescriptor* desc,
    const void* gmem_ptr,
    uint64_t dim_n,
    uint32_t box_n) {
  CUtensorMapDataType data_type = CU_TENSOR_MAP_DATA_TYPE_FLOAT16;

  uint64_t global_dim[1] = {dim_n};
  uint32_t box_dim[1] = {box_n};
  uint32_t element_stride[1] = {1};

  CUresult result = cuTensorMapEncodeTiled(
      reinterpret_cast<CUtensorMap*>(desc),
      data_type,
      1,  // tensorRank
      const_cast<void*>(gmem_ptr),
      global_dim,
      nullptr,   // stride not needed for 1D
      box_dim,
      element_stride,
      CU_TENSOR_MAP_INTERLEAVE_NONE,
      CU_TENSOR_MAP_SWIZZLE_NONE,
      CU_TENSOR_MAP_L2_PROMOTION_NONE,
      CU_TENSOR_MAP_FLOAT_OOB_FILL_NONE);

  return (result == CUDA_SUCCESS) ? cudaSuccess : cudaErrorUnknown;
}

// ============================================================================
// TMA Device-side Primitives (Kernel-side)
// ============================================================================

#if defined(__CUDA_ARCH__) && __CUDA_ARCH__ >= 900

// Initialize mbarrier in shared memory.
// Must be called by a single thread before first TMA load.
__device__ inline void barrier_init(uint64_t* mbar, int thread_count = 1) {
  uint32_t smem_ptr = static_cast<uint32_t>(__cvta_generic_to_shared(mbar));
  asm volatile(
      "mbarrier.init.shared::cta.b64 [%0], %1;\n" ::"r"(smem_ptr),
      "r"(thread_count));
}

// Set expected transaction bytes on barrier.
// Call before issuing TMA loads to tell barrier how many bytes to expect.
__device__ inline void barrier_expect_tx(uint64_t* mbar, uint32_t bytes) {
  uint32_t smem_ptr = static_cast<uint32_t>(__cvta_generic_to_shared(mbar));
  asm volatile(
      "mbarrier.arrive.expect_tx.shared::cta.b64 _, [%0], %1;\n" ::"r"(
          smem_ptr),
      "r"(bytes));
}

// Wait on barrier phase. Spins until the specified phase completes.
__device__ inline void barrier_wait(uint64_t* mbar, int phase) {
  uint32_t smem_ptr = static_cast<uint32_t>(__cvta_generic_to_shared(mbar));
  asm volatile(
      "{\n"
      "  .reg .pred P1;\n"
      "  LAB_WAIT_%=:\n"
      "  mbarrier.try_wait.parity.shared::cta.b64 P1, [%0], %1;\n"
      "  @!P1 bra LAB_WAIT_%=;\n"
      "}\n" ::"r"(smem_ptr),
      "r"(phase));
}

// Arrive at barrier (non-TMA thread signaling completion).
__device__ inline void barrier_arrive(uint64_t* mbar) {
  uint32_t smem_ptr = static_cast<uint32_t>(__cvta_generic_to_shared(mbar));
  asm volatile(
      "mbarrier.arrive.shared::cta.b64 _, [%0];\n" ::"r"(smem_ptr));
}

// ============================================================================
// TMA Copy Operations
// ============================================================================

// Issue a 2D TMA load from global to shared memory.
// Only ONE thread should call this per tile load (typically thread 0 of warp 0).
//
// Parameters:
//   desc_ptr  - Device-accessible pointer to TMA descriptor (in constant memory
//               or passed as kernel argument via __grid_constant__)
//   mbar      - Shared memory mbarrier pointer
//   smem_ptr  - Destination in shared memory
//   coord_k   - K coordinate in the global tensor
//   coord_n   - N coordinate in the global tensor
__device__ inline void tma_load_2d(const void* desc_ptr, uint64_t* mbar,
                                   void* smem_ptr, int32_t coord_k,
                                   int32_t coord_n) {
  uint64_t gmem_desc = reinterpret_cast<uint64_t>(desc_ptr);
  uint32_t smem_mbar = static_cast<uint32_t>(__cvta_generic_to_shared(mbar));
  uint32_t smem_dest = static_cast<uint32_t>(__cvta_generic_to_shared(smem_ptr));

#if __CUDA_ARCH__ >= 1200
  // SM120+: shared::cta scope
  asm volatile(
      "cp.async.bulk.tensor.2d.shared::cta.global.mbarrier::complete_tx::bytes"
      " [%0], [%1, {%3, %4}], [%2];\n" ::"r"(smem_dest),
      "l"(gmem_desc), "r"(smem_mbar), "r"(coord_k), "r"(coord_n)
      : "memory");
#else
  // SM90: shared::cluster scope (with potential multicast)
  asm volatile(
      "cp.async.bulk.tensor.2d.shared::cluster.global.mbarrier::complete_tx::"
      "bytes"
      " [%0], [%1, {%3, %4}], [%2];\n" ::"r"(smem_dest),
      "l"(gmem_desc), "r"(smem_mbar), "r"(coord_k), "r"(coord_n)
      : "memory");
#endif
}

// Issue a 1D TMA load (for scales, zero-points).
__device__ inline void tma_load_1d(const void* desc_ptr, uint64_t* mbar,
                                   void* smem_ptr, int32_t coord_0) {
  uint64_t gmem_desc = reinterpret_cast<uint64_t>(desc_ptr);
  uint32_t smem_mbar = static_cast<uint32_t>(__cvta_generic_to_shared(mbar));
  uint32_t smem_dest = static_cast<uint32_t>(__cvta_generic_to_shared(smem_ptr));

#if __CUDA_ARCH__ >= 1200
  asm volatile(
      "cp.async.bulk.tensor.1d.shared::cta.global.mbarrier::complete_tx::bytes"
      " [%0], [%1, {%3}], [%2];\n" ::"r"(smem_dest),
      "l"(gmem_desc), "r"(smem_mbar), "r"(coord_0)
      : "memory");
#else
  asm volatile(
      "cp.async.bulk.tensor.1d.shared::cluster.global.mbarrier::complete_tx::"
      "bytes"
      " [%0], [%1, {%3}], [%2];\n" ::"r"(smem_dest),
      "l"(gmem_desc), "r"(smem_mbar), "r"(coord_0)
      : "memory");
#endif
}

// ============================================================================
// Pipeline Helper: Multi-Stage TMA Pipeline
// ============================================================================

// Pipeline state for multi-buffered TMA loads.
// Manages barriers for N pipeline stages.
template <int NumStages>
struct TmaPipeline {
  uint64_t barriers[NumStages];
  int phase[NumStages];

  // Initialize all barriers. Call from single thread.
  __device__ void init() {
    for (int i = 0; i < NumStages; ++i) {
      barrier_init(&barriers[i], 1);
      phase[i] = 0;
    }
  }

  // Get barrier for given stage.
  __device__ uint64_t* get_barrier(int stage) { return &barriers[stage]; }

  // Signal expected bytes for a stage.
  __device__ void expect_tx(int stage, uint32_t bytes) {
    barrier_expect_tx(&barriers[stage], bytes);
  }

  // Wait for a stage to complete.
  __device__ void wait(int stage) {
    barrier_wait(&barriers[stage], phase[stage]);
    phase[stage] ^= 1;  // Toggle phase for next use
  }
};

#endif  // __CUDA_ARCH__ >= 900

}  // namespace tma
}  // namespace MARLIN_NAMESPACE_NAME

#endif  // _marlin_tma_cuh
