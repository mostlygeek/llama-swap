# Speculative Decoding

Speculative decoding can have a big improvement to the tokens per second. The trade off is that more VRAM is used for the draft model. These examples are from a server with three P40s and one 3090.

## Coding Use Case

This is using Qwen2.5 Coder 32B with the 0.5B as a draft model. A quantization of Q8_0 was chosen for the draft model as quantization has a greater effect on smaller models.

The models used:

* [Bartowski Qwen2.5-Coder-32B-Instruct](https://huggingface.co/bartowski/Qwen2.5-Coder-32B-Instruct-GGUF)
* [Bartowski Qwen2.5-Coder-0.5B-Instruct](https://huggingface.co/bartowski/Qwen2.5-Coder-0.5B-Instruct-GGUF)

llama-swap config:

```yaml
models:
  "qwen-coder-32b-q4":
    # main model on 3090, draft on P40 #1
    cmd: >
      /mnt/nvme/llama-server/llama-server-be0e35
      --host 127.0.0.1 --port 9503
      --flash-attn --metrics
      --slots
      --model /mnt/nvme/models/Qwen2.5-Coder-32B-Instruct-Q4_K_M.gguf
      -ngl 99
      --ctx-size 19000
      --model-draft /mnt/nvme/models/Qwen2.5-Coder-0.5B-Instruct-Q8_0.gguf
      -ngld 99
      --draft-max 16
      --draft-min 4
      --draft-p-min 0.4
      --device CUDA0
      --device-draft CUDA1
    proxy: "http://127.0.0.1:9503"
```

In this configuration two GPUs are used, a 3090 (CUDA0) for the main model and a P40 (CUDA1) for the draft model. While both models can fit on the 3090, relocating the draft model to the P40 freed up room for a larger context size. Even though the P40 is about 1/3rd the speed of the 3090, with such a small model it still improved token/second.

Multiple tests were run with various parameters and the fastest result chosen for the configuration.  In all testing the 0.5B model produced the largest improvements to tokens per second.

Baseline: 33.92 tokens/second on 3090 without a draft model.

| draft-max | draft-min | draft-p-min | python | TS | swift |
|-----------|-----------|-------------|--------|----|-------|
| 16 | 1 | 0.9 | 71.64 | 55.55 | 48.06 |
| 16 | 1 | 0.4 | 83.21 | 58.55 | 45.50 |
| 16 | 1 | 0.1 | 79.72 | 55.66 | 43.94 |
| 16 | 2 | 0.9 | 68.47 | 55.13 | 43.12 |
| 16 | 2 | 0.4 | 82.82 | 57.42 | 48.83 |
| 16 | 2 | 0.1 | 81.68 | 51.37 | 45.72 |
| 16 | 4 | 0.9 | 66.44 | 48.49 | 42.40 |
| 16 | 4 | 0.4 | _83.62_ (fastest)| _58.29_ | _50.17_ |
| 16 | 4 | 0.1 | 82.46 | 51.45 | 40.71 |
| 8 | 1 | 0.4 | 67.07 | 55.17 | 48.46 |
| 4 | 1 | 0.4 | 50.13 | 44.96 | 40.79 |

The test script can be found in this [gist](https://gist.github.com/mostlygeek/da429769796ac8a111142e75660820f1). It is a simple curl script to that prompts generating a snake game with python, typescript or swift. Evaluation metrics were pulled from llama.cpp's logs.

```
for lang in "python" "typescript" "swift"; do
    echo "Generating Snake Game in $lang using $model"
    curl -s --url http://localhost:8080/v1/chat/completions -d "{\"messages\": [{\"role\": \"system\", \"content\": \"you only write code.\"}, {\"role\": \"user\", \"content\": \"write snake game in $lang\"}], \"temperature\": 0.1, \"model\":\"$model\"}" > /dev/null
done
```

Python was consistently faster than Swift in all tests. This is likely due to the 0.5B draft model being more proficient in generating Python code that was accepted by the larger 32B model.

## Chat

This configuration is for a regular chat use case. This configuration produces about 13 tokens/second in typical use. Up from ~9 tokens/second with only the 3xP40s. Great news for P40 owners.

Models:

* [Bartowswki Meta-Llama-3.1-70B-Instruct-GGUF](https://huggingface.co/bartowski/Meta-Llama-3.1-70B-Instruct-GGUF)
* [Bartowski Llama-3.2-3B-Instruct-GGUF](https://huggingface.co/bartowski/Llama-3.2-3B-Instruct-GGUF)


```yaml
models:
  "llama-70B":
    cmd: >
      /mnt/nvme/llama-server/llama-server-be0e35
      --host 127.0.0.1 --port 9602
      --flash-attn --metrics
      --split-mode row
      --ctx-size 80000
      --model /mnt/nvme/models/Meta-Llama-3.1-70B-Instruct-Q4_K_L.gguf
      -ngl 99
      --model-draft /mnt/nvme/models/Llama-3.2-3B-Instruct-Q4_K_M.gguf
      -ngld 99
      --draft-max 16
      --draft-min 1
      --draft-p-min 0.4
      --device-draft CUDA0
      --tensor-split 0,1,1,1
```

In this configuration Llama-3.1-70B is split across three P40s and Llama-3.2-3B is on the 3090.

There are some flags that deserve a bit more explanation:

* `--split-mode row` - increases inference speeds using multiple P40s by about 30%. It's a P40 thing.
* `--tensor-split 0,1,1,1` - controls how the main model is split across the GPUs. Essentally means 0% on 3090 and evenly split across the P40s. A value of `--tensor-split 0,5,4,1` would mean 0% on 3090, 50%,40%,10% respectively across the other P40s. Of course this would run out of VRAM.
* `--ctx-size 80000` - maximum context size that can fit in the remaining VRAM.

## What is CUDA0, CUDA1, CUDA2, CUDA3?

These devices are the IDs used by llama.cpp.

```
$ ./llama-server --list-devices
ggml_cuda_init: GGML_CUDA_FORCE_MMQ:    no
ggml_cuda_init: GGML_CUDA_FORCE_CUBLAS: no
ggml_cuda_init: found 4 CUDA devices:
  Device 0: NVIDIA GeForce RTX 3090, compute capability 8.6, VMM: yes
  Device 1: Tesla P40, compute capability 6.1, VMM: yes
  Device 2: Tesla P40, compute capability 6.1, VMM: yes
  Device 3: Tesla P40, compute capability 6.1, VMM: yes
Available devices:
  CUDA0: NVIDIA GeForce RTX 3090 (24154 MiB, 23892 MiB free)
  CUDA1: Tesla P40 (24438 MiB, 24290 MiB free)
  CUDA2: Tesla P40 (24438 MiB, 24290 MiB free)
  CUDA3: Tesla P40 (24438 MiB, 24290 MiB free)
```

