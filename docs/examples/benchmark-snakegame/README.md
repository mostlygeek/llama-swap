# Optimizing Code Generation with llama-swap

Finding the best mix of settings for your hardware can be time consuming. This example demonstrates using a custom configuration file to automate testing different scenarios to find the an optimal configuration.

The benchmark writes a snake game in Python, TypeScript, and Swift using the Qwen 2.5 Coder models. The experiments were done using a 3090 and a P40.

**Benchmark Scenarios**

Three scenarios are tested:

- 3090-only: Just the main model on the 3090
- 3090-with-draft: the main and draft models on the 3090
- 3090-P40-draft: the main model on the 3090 with the draft model offloaded to the P40

**Available Devices**

Use the following command to list available devices IDs for the configuration:

```
$ /mnt/nvme/llama-server/llama-server-f3252055 --list-devices
ggml_cuda_init: GGML_CUDA_FORCE_MMQ:    no
ggml_cuda_init: GGML_CUDA_FORCE_CUBLAS: no
ggml_cuda_init: found 4 CUDA devices:
  Device 0: NVIDIA GeForce RTX 3090, compute capability 8.6, VMM: yes
  Device 1: Tesla P40, compute capability 6.1, VMM: yes
  Device 2: Tesla P40, compute capability 6.1, VMM: yes
  Device 3: Tesla P40, compute capability 6.1, VMM: yes
Available devices:
  CUDA0: NVIDIA GeForce RTX 3090 (24154 MiB, 406 MiB free)
  CUDA1: Tesla P40 (24438 MiB, 22942 MiB free)
  CUDA2: Tesla P40 (24438 MiB, 24144 MiB free)
  CUDA3: Tesla P40 (24438 MiB, 24144 MiB free)
```

**Configuration**

The configuration file, `benchmark-config.yaml`, defines the three scenarios:

```yaml
models:
  "3090-only":
    proxy: "http://127.0.0.1:9503"
    cmd: >
      /mnt/nvme/llama-server/llama-server-f3252055
      --host 127.0.0.1 --port 9503
      --flash-attn
      --slots

      --model /mnt/nvme/models/Qwen2.5-Coder-32B-Instruct-Q4_K_M.gguf
      -ngl 99
      --device CUDA0

      --ctx-size 32768
      --cache-type-k q8_0 --cache-type-v q8_0

  "3090-with-draft":
    proxy: "http://127.0.0.1:9503"
    # --ctx-size 28500 max that can fit on 3090 after draft model
    cmd: >
      /mnt/nvme/llama-server/llama-server-f3252055
      --host 127.0.0.1 --port 9503
      --flash-attn
      --slots

      --model /mnt/nvme/models/Qwen2.5-Coder-32B-Instruct-Q4_K_M.gguf
      -ngl 99
      --device CUDA0

      --model-draft /mnt/nvme/models/Qwen2.5-Coder-0.5B-Instruct-Q8_0.gguf
      -ngld 99
      --draft-max 16
      --draft-min 4
      --draft-p-min 0.4
      --device-draft CUDA0

      --ctx-size 28500
      --cache-type-k q8_0 --cache-type-v q8_0

  "3090-P40-draft":
    proxy: "http://127.0.0.1:9503"
    cmd: >
      /mnt/nvme/llama-server/llama-server-f3252055
      --host 127.0.0.1 --port 9503
      --flash-attn --metrics
      --slots
      --model /mnt/nvme/models/Qwen2.5-Coder-32B-Instruct-Q4_K_M.gguf
      -ngl 99
      --device CUDA0

      --model-draft /mnt/nvme/models/Qwen2.5-Coder-0.5B-Instruct-Q8_0.gguf
      -ngld 99
      --draft-max 16
      --draft-min 4
      --draft-p-min 0.4
      --device-draft CUDA1

      --ctx-size 32768
      --cache-type-k q8_0 --cache-type-v q8_0
```

> Note in the `3090-with-draft` scenario the `--ctx-size` had to be reduced from 32768 to to accommodate the draft model.


**Running the Benchmark**

To run the benchmark, execute the following commands:

1. `llama-swap -config benchmark-config.yaml`
1. `./run-benchmark.sh http://localhost:8080 "3090-only" "3090-with-draft" "3090-P40-draft"`

The [benchmark script](run-benchmark.sh) generates a CSV output of the results, which can be converted to a Markdown table for readability.

**Results (tokens/second)**

| model           | python | typescript | swift |
|-----------------|--------|------------|-------|
| 3090-only       | 34.03  | 34.01      | 34.01 |
| 3090-with-draft | 106.65 | 70.48      | 57.89 |
| 3090-P40-draft  | 81.54  | 60.35      | 46.50 |

Many different factors, like the programming language, can have big impacts on the performance gains. However, with a custom configuration file for benchmarking it is easy to test the different variations to discover what's best for your hardware.

Happy coding!