---
title: Speculative decoding with a draft model
summary: A two-GPU llama-server config using a small draft model, with measured tuning results.
category: examples
tags: [speculative-decoding, draft-model, performance, multi-gpu, llama-server]
config_keys: [models.*.cmd, models.*.proxy]
updated: 2026-08-25
---

# Speculative decoding with a draft model

Speculative decoding can substantially raise tokens/second at the cost of extra
VRAM for the draft model. This is a real measured configuration on a machine
with three P40s and one 3090.

```yaml
models:
  "qwen-coder-32b-q4":
    # main model on the 3090 (CUDA0), draft on a P40 (CUDA1)
    cmd: >
      /mnt/nvme/llama-server/llama-server-be0e35
      --host 127.0.0.1 --port ${PORT}
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
```

Models used: [Qwen2.5-Coder-32B-Instruct](https://huggingface.co/bartowski/Qwen2.5-Coder-32B-Instruct-GGUF)
as the main model and [Qwen2.5-Coder-0.5B-Instruct](https://huggingface.co/bartowski/Qwen2.5-Coder-0.5B-Instruct-GGUF)
at Q8_0 as the draft. Quantization hurts small models more, so the draft is
kept at a higher precision than the main model.

## Why the draft is on the second GPU

Both models fit on the 3090, but moving the draft to the P40 freed space for a
larger context. Even though the P40 is roughly a third the speed of the 3090,
the draft model still improved throughput — drafting is cheap relative to the
verification pass on the big model.

## Measured results

Baseline: **33.92 tok/s** on the 3090 with no draft model.

| draft-max | draft-min | draft-p-min | python | TS | swift |
|-----------|-----------|-------------|--------|----|-------|
| 16 | 1 | 0.9 | 71.64 | 55.55 | 48.06 |
| 16 | 1 | 0.4 | 83.21 | 58.55 | 45.50 |
| 16 | 1 | 0.1 | 79.72 | 55.66 | 43.94 |
| 16 | 2 | 0.4 | 82.82 | 57.42 | 48.83 |
| 16 | 4 | 0.9 | 66.44 | 48.49 | 42.40 |
| 16 | 4 | 0.4 | **83.62** | **58.29** | **50.17** |
| 16 | 4 | 0.1 | 82.46 | 51.45 | 40.71 |
| 8  | 1 | 0.4 | 67.07 | 55.17 | 48.46 |
| 4  | 1 | 0.4 | 50.13 | 44.96 | 40.79 |

Roughly 2.5x on Python. Two patterns hold across the runs:

- **`--draft-p-min 0.4` is the sweet spot.** 0.9 is too conservative — the
  draft gets rejected too often to pay for itself. 0.1 accepts too much
  low-confidence drafting and wastes verification.
- **`--draft-max 16` beats 8 and 4.** Longer drafts win when acceptance is
  good.

Gains are largest on the most predictable code (Python here) and smallest on
the least predictable — which is the nature of speculative decoding, not a
tuning failure.

## Notes

- Prefer `${PORT}` over a hardcoded port so llama-swap manages it. If you do
  hardcode one, set `proxy` to match. See `guides/model-runtime/writing-cmd`.
- Tune on your own hardware and your own workload. These numbers are specific
  to this GPU pair and these models.
- Cold start is slower with a draft model — two sets of weights to load.
  Consider a longer `ttl` so you don't pay it repeatedly. See
  `guides/model-runtime/ttl-and-unloading`.
