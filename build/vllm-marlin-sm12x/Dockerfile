ARG BASE_IMAGE=nvcr.io/nvidia/vllm:26.01-py3
FROM ${BASE_IMAGE}

# vLLM custom build for Spark/Blackwell with marlin_sm12x C++ patch compiled in.
ENV VLLM_BASE_DIR=/workspace/vllm

# CUDA dev headers (cusparse/cusolver/cufft) are required to compile vLLM C++/CUDA extensions.
RUN apt-get update -qq && \
    apt-get install -y --no-install-recommends git wget && \
    ARCH_DIR=$(dpkg --print-architecture | sed 's/arm64/sbsa/;s/amd64/x86_64/') && \
    wget -qO /tmp/cuda-keyring.deb \
      "https://developer.download.nvidia.com/compute/cuda/repos/ubuntu2404/${ARCH_DIR}/cuda-keyring_1.1-1_all.deb" && \
    dpkg -i /tmp/cuda-keyring.deb && rm /tmp/cuda-keyring.deb && \
    apt-get update -qq && \
    apt-get install -y --no-install-recommends \
      libcusparse-dev-13-1 \
      libcusolver-dev-13-1 \
      libcufft-dev-13-1 && \
    rm -rf /var/lib/apt/lists/*

RUN pip install --no-cache-dir cmake ninja setuptools-scm

# CUTLASS 4.3.5 for SM120a/SM121a
RUN git clone --depth 1 --branch v4.3.5 \
      https://github.com/NVIDIA/cutlass.git /opt/cutlass
ENV VLLM_CUTLASS_SRC_DIR=/opt/cutlass

# Build vLLM from source
ARG VLLM_REF=fd267bc7b7cd3d001ac5a893eacb9e56ff256822
RUN git clone https://github.com/vllm-project/vllm.git /tmp/vllm-build && \
    cd /tmp/vllm-build && \
    git checkout ${VLLM_REF}
WORKDIR /tmp/vllm-build

COPY marlin_sm12x.patch /tmp/
COPY marlin_tma.cuh /tmp/

# Compile-time Marlin patches (C++ + Python-side capability checks)
RUN git apply --check /tmp/marlin_sm12x.patch && \
    git apply /tmp/marlin_sm12x.patch && \
    cp -f /tmp/marlin_tma.cuh csrc/quantization/marlin/marlin_tma.cuh

# Additional MoE Marlin SM12x fix for vLLM >= 0.16:
# csrc/moe/marlin_moe_wna16/ops.cu still checks for exact SM120.
RUN python3 - <<'PY'
from pathlib import Path
p = Path('csrc/moe/marlin_moe_wna16/ops.cu')
t = p.read_text()
old = 'major_capability * 10 + minor_capability == 120'
new = 'major_capability == 12'
if old in t:
    t = t.replace(old, new)
    t = t.replace('SM89 or SM120 device', 'SM89 or SM12x device')
    p.write_text(t)
    print('Patched', p)
else:
    print('Pattern not found in', p)
PY

RUN python use_existing_torch.py
RUN pip install --no-cache-dir -r requirements/build.txt
RUN pip uninstall -y vllm

ENV TORCH_CUDA_ARCH_LIST="12.0a;12.1a"
ENV VLLM_TARGET_DEVICE=cuda
ENV MAX_JOBS=16
RUN pip install --no-build-isolation --no-cache-dir .

WORKDIR /
RUN rm -rf /tmp/vllm-build

# Runtime deps/patches used by MiniMax/GLM flows in this environment
RUN pip install --no-cache-dir --no-deps "transformers>=5.0" && \
    pip install --no-cache-dir --upgrade "huggingface_hub>=0.32" && \
    pip install --no-cache-dir --no-deps "compressed-tensors>=0.13"

COPY patch_transformers.py /tmp/
RUN python3 /tmp/patch_transformers.py

COPY patch_streaming.py /tmp/
RUN python3 /tmp/patch_streaming.py

COPY moe-configs/*.json /usr/local/lib/python3.12/dist-packages/vllm/model_executor/layers/fused_moe/configs/

# Runtime compatibility with spark-vllm-docker launch scripts.
COPY run-cluster-node.sh ${VLLM_BASE_DIR}/
RUN chmod +x ${VLLM_BASE_DIR}/run-cluster-node.sh && \
    pip install --no-cache-dir "ray[default]" fastsafetensors nvidia-nvshmem-cu13

WORKDIR ${VLLM_BASE_DIR}

RUN python3 -c "import vllm, torch; print('vLLM:', vllm.__version__); print('torch:', torch.__version__)"
