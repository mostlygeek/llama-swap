# Fine-tuning a small model for the Docs Agent

How to fine-tune a small model so it can run the Help page's Docs Agent, and how
to reproduce a run for a future model. The goal is a model small enough to
download automatically, or to run in the browser, that scores as well on the
[eval suite](../README.md) as `gemma-4-12B`.

The first run is `LFM3-1.7B`. Its best result, `lfm3-v2`, is published as
[`llama-swap/LFM3-1.7B-HelpAgent`](https://huggingface.co/llama-swap/LFM3-1.7B-HelpAgent).
With every question limited to 60 seconds, its Q4_K_M scores 95.5% on the training
cases and 96.1% on the holdout. `gemma-4-12B`, which was not measured with that
limit, scores 97.3% and 93.5%.

`lfm3-v3` was trained on more data from a smarter teacher and did not do better.
See [the experiment](#the-lfm3-v3-experiment). [Follow-up](#follow-up) lists what is
left before llama-swap can use the model:

| Step | Status |
|---|---|
| 1. Set up the environment | done |
| 2. Measure the stock model | done, on bf16 through SGLang |
| 3. Generate training data | done, 417 conversations in `data/` |
| 4. Train | done, `lfm3-v2` |
| 5. Export to GGUF | done, Q4_K_M, 1.1 GB |
| 6. Measure the fine-tune | done, see [Run log](#run-log) |
| 7. Publish | done, see [Step 7](#step-7-publish-to-hugging-face) |

Update this file with what actually works on each new run.

## Layout

```
evals/docs-agent/training/
  README.md     this file
  download.sh   fetches the weights and notebooks a run needs
  benchmark.py  measures a server directly, at increasing concurrency
  make_fixture.py writes fixture/models.yaml, the training copy of the fixture
  questions.py  asks the teacher to write questions, and drops eval lookalikes
  generate.ts   records full conversations through the unmodified agent loop
  filter.py     keeps the conversations worth training on, and splits them
  train.py      fine-tunes with LoRA, then merges the adapter into a full model
  fixture/      the training fixture
  serve.py      slow transformers server; a fallback, see Step 6
  test_serve.py tests for serve.py's parsing; needs no GPU
  data/         the data `lfm3-v2` trained on, checked in so it can be reused
  data-v3/      the data for `lfm3-v3`, an experiment that did not improve the model
```

Training data is committed because it is small text tied to a specific KB and
system prompt, and a run cannot be reproduced without it. The JSONL is compressed
with zstd: 6.9 MB of conversations take 0.5 MB. Read it with
`zstdcat data/train.jsonl.zst`.

Weights, checkpoints and the Python environment are large and stay out of the
repository. `download.sh` puts them in `$TRAINING_HOME`, by default `~/lfm3/`:

```
~/lfm3/
  base-gguf/    stock GGUF quantizations
  base-hf/      stock safetensors, the training input
  train/.venv/  Python environment
  cookbook/     Liquid's fine-tuning notebooks
  data/         raw questions and conversations from the teacher
  runs/         one directory per run: adapter/, merged/
  gguf/         exported models
  publish/      the files sent to Hugging Face
  logs/         teed output of every long run
```

## The target

Match the baseline on **both** case sets. The numbers for `gemma-4-12B` come
from commit 56f9490:

| Model | Training cases (134) | Holdout cases (51) |
|---|---|---|
| `gemma-4-12B` Q4_K_M | 97.3% | 93.5% |
| `LFM3-1.7B` bf16 stock | 84.8% | 89.5% |
| `LFM3-1.7B` Q4_K_M stock, 60 s limit | 44.8% | 55.6% |
| `lfm3-v2` bf16 | 96.8% | 96.1% |
| `lfm3-v2` Q4_K_M, 60 s limit | 95.5% | 96.1% |
| `lfm3-v3` Q4_K_M, 60 s limit | 95.3% | 92.8% |

The holdout decides the result. A fine-tune can only claim to match the
baseline if its holdout score does.

Measure with `--timeout 60`, because the model has to answer quickly. `--timeout`
applies to each attempt at a question. A timed-out attempt fails.

### What the stock model gets wrong

Across 402 training-case attempts, 61 failed:

| Failure | Attempts |
|---|---|
| Right answer, more steps than the case allows | 29 |
| Ran for 300 s without finishing, at 23 to 87 steps | 28 |
| Wrote something the case forbids | 4 |

So 57 of 61 are about not stopping, rather than not knowing. The timeouts are the
model looping, not server load: SGLang's queue never passed 1. It took a median of
9 steps, with 7 on passing attempts, and spent them searching source code as
often as the docs (1,270 `code__grep` calls against 1,284 searches). 376 of 5,007
calls failed, 161 of them `docs__get_config_schema`, often given a document id
instead of a config path. It also called tools that do not exist, such as
`docs__get_config`.

Regex grading is lenient, so a pass does not prove an answer is good. Use the
[LLM judge](../README.md#the-cases) to compare answer quality before and after.

## Rules for training data

These keep the eval meaningful. Breaking one produces a score that looks like
success and says nothing about the model.

1. **Never train on `cases/` or `cases-holdout/`.** Do not copy their questions,
   paraphrase them, or generate data from their `reference` answers. Once a
   model has seen a case, that case stops measuring anything.
2. **Do not read holdout failures to decide what data to add.** Use only the
   holdout's overall score. Choosing data from holdout failures tunes to the
   holdout just as surely as training on it.
3. **Generate questions from `docs/kb/` and `fixture/models.yaml`,** then reject
   any question too close to an eval case.
4. **Include questions about features that do not exist.** The correct answer
   says so. Without these, a model trained only on answerable questions learns
   to always find an answer.
5. **Keep only conversations that would pass.** Check each one against the same
   kind of assertions the eval uses: it called the tools, and every config key
   in its answer appeared in a tool result. `filter.py` also validates YAML against
   `config-schema.json`, and rejects outside URLs no tool returned.
6. **Keep a validation split of your own** in `data/`, for choosing between
   checkpoints. The eval cases are not for that.
7. **Keep your own setup out of the data.** `config__get_config` returns the
   `peers` block of the server running the tools, so a teacher reached through a
   peer puts that host and its models in the conversation. Reject those with
   `--forbid`. `lfm3-v2`'s data had 11 conversations like this. `--forbid` matches
   only the strings it is given, so scan the result too: see
   [Known problems](#known-problems-in-lfm3-v2-and-lfm3-v3).

## Data format

Data is stored model-independently as OpenAI-style messages, one conversation
per line of JSONL, and rendered through each model's own chat template at
training time. That is what makes it reusable: a future model needs no new data
unless the KB, the tools or the system prompt changed.

Files:

- `data/train.jsonl.zst` and `data/validation.jsonl.zst`, the conversations
- `data/tools.json`, the tool definitions the agent is sent, as captured from the
  server
- `data/manifest.json`, how the data was generated

```json
{
  "id": "ttl-0042",
  "source": "guides/model-runtime/ttl-and-unloading",
  "kind": "answerable",
  "messages": [
    {"role": "system", "content": "..."},
    {"role": "user", "content": "..."},
    {"role": "assistant", "content": "", "tool_calls": []},
    {"role": "tool", "tool_call_id": "call_0", "name": "docs__search_docs", "content": "..."},
    {"role": "assistant", "content": "..."}
  ]
}
```

- `kind` is `answerable`, `nonexistent` or `running-config`.
- An assistant message may carry `reasoning_content`. See
  [Reasoning](#reasoning).
- An assistant message may carry `skip: true`. It stays as context, but is not
  trained on, because its tool call failed and a later turn recovered.

`manifest.json` records what the data depends on, so that stale data can be
detected:

```json
{
  "kb_commit": "",
  "system_prompt_sha256": "",
  "tools_sha256": "",
  "teacher_conversations": {},
  "generated_at": "",
  "kept_by_kind": {},
  "train": 0,
  "validation": 0
}
```

Regenerate when any of these change. The system prompt in
`ui/src/lib/prompts/docsAgent.ts` is the most likely one: a model that trained
on an older prompt is not being measured on what it learned.

## Step 1: Set up the environment

The environment is pinned to Python 3.12 so the ML stack gets a mainstream,
widely supported interpreter. Newer versions were not tested.

```bash
mkdir -p ~/lfm3/train ~/lfm3/base-hf ~/lfm3/base-gguf
cd ~/lfm3/train
uv venv --python 3.12 .venv
uv pip install --python .venv/bin/python torch \
  --index-url https://download.pytorch.org/whl/cu128
uv pip install --python .venv/bin/python transformers accelerate trl peft \
  datasets safetensors sentencepiece pyyaml tensorboard zstandard
```

Versions used for the LFM3 run: torch 2.15.0, transformers 5.22.1, trl 1.17.1,
peft 0.21.2, accelerate 1.15.0, datasets 5.1.0.

Download the weights:

```bash
./evals/docs-agent/training/download.sh --gguf --cookbook
```

LFM3 loads with `trust_remote_code=True`, so read a new model's Python files
before running them.

### GPUs

CUDA numbers GPUs fastest-first by default, which is not `nvidia-smi` order. On
the machine used for this run `nvidia-smi` shows P40s at 0 and 1 and the 3090s at
2 and 3, but a bare `CUDA_VISIBLE_DEVICES=2` selects a P40, which fails with `no
kernel image is available for execution on the device`. Set the order
explicitly, and confirm the card with `torch.cuda.get_device_name(0)`:

```bash
export CUDA_DEVICE_ORDER=PCI_BUS_ID
export CUDA_VISIBLE_DEVICES=3
```

| GPU | `nvidia-smi` index | Use |
|---|---|---|
| `GPU-f10` | 3 | training |
| `GPU-6f0` | 2 | eval |

Train on the cooler card. Under the same 90-second load `GPU-f10` held 62°C at 55%
fan, and `GPU-6f0` reached 75°C at 100% fan.

llama.cpp accepts the short `GPU-6f0` form. vLLM 0.33 does not, and fails in
`pynvml`.

A system llama-swap may also have models pinned to these GPUs, and it listens on
`:8080`. Check `nvidia-smi` before starting, and if it loads a model during a
run, the run is competing with it for memory.

## Step 2: Measure the stock model

Measure before training. The gap decides how much training is needed, and the
failures show which behaviors to teach.

Create `evals/config.yaml`, which is gitignored, with the sampling settings from
the model's card:

```yaml
healthCheckTimeout: 300
macros:
  lfm3-server: |
    /path/to/llama-server
    --host 127.0.0.1 --port ${PORT}
    -ngl 999 --no-warmup --jinja
    --ctx-size 131072 --parallel 4 --kv-unified
    --temp 0.1 --top-k 50 --repeat-penalty 1.1

models:
  lfm3-stock-q4km:
    env:
      - "CUDA_VISIBLE_DEVICES=GPU-6f0"
    cmd: |
      ${lfm3-server}
      --model /path/to/LFM3-1.7B-Q4_K_M.gguf
```

`LFM3-1.7B` requires llama.cpp b11960 or later. Then start a server built from
this working tree, and evaluate:

```bash
make src
go build -tags embed_src -o build/llama-swap .
./build/llama-swap -config evals/config.yaml \
  -config-dir evals/docs-agent/fixture -listen 127.0.0.1:18080

cd ui
npm run agent -- eval --base-url http://127.0.0.1:18080 \
  --model lfm3-stock-q4km --concurrency 4 \
  --cases ../evals/docs-agent/cases --repeat 3 --label lfm3-stock-q4km
npm run agent -- eval --base-url http://127.0.0.1:18080 \
  --model lfm3-stock-q4km --concurrency 4 \
  --cases ../evals/docs-agent/cases-holdout --repeat 3 \
  --label lfm3-stock-q4km-holdout
```

Try one question first, with `npm run agent -- ask`. It proves the server parses
the model's tool-call format; if it does not, every case fails and the score
reports a parsing problem rather than the model.

Measure Q4_K_M rather than a vendor's special quantization. LFM3's QAD-Q4_0 is
its own trained checkpoint, so re-quantizing a fine-tune cannot be expected to
reproduce it, and Q4_K_M is the comparison that carries over.

## Step 3: Generate training data

A stronger model runs the real agent loop, so conversations contain real tool
results and match what the model sees in production.

```bash
# training/fixture stands in for the eval's fixture. Keep it out of eval servers.
./build/llama-swap -config <config with the teacher as a peer> \
  -config-dir evals/docs-agent/training/fixture -listen 127.0.0.1:18081

~/lfm3/train/.venv/bin/python evals/docs-agent/training/questions.py \
  --base-url http://127.0.0.1:18081 --model sippy/Q3.8-27B-rad \
  --per-doc 12 --out ~/lfm3/data/questions.jsonl

cd ui && npx tsx ../evals/docs-agent/training/generate.ts \
  --base-url http://127.0.0.1:18081 --model sippy/Q3.8-27B-rad \
  --questions ~/lfm3/data/questions.jsonl \
  --out ~/lfm3/data/conversations.jsonl

~/lfm3/train/.venv/bin/python evals/docs-agent/training/filter.py \
  ~/lfm3/data/conversations.jsonl --out evals/docs-agent/training/data-next \
  --forbid ~/lfm3/forbid.txt
```

Write new data to a new directory, as `data/` is the record of what `lfm3-v2`
trained on. `filter.py` has changed since then and does not reproduce it. It does
reproduce `data-v3/` byte for byte, apart from the manifest's date.

`filter.py` needs the environment's Python for `pyyaml` and `zstandard`. Add
`--report` to see what would be kept without writing anything. It takes several
conversation files, in order of preference, and keeps the first passing conversation
for each question.
`--forbid` names a local text file of strings that must not appear, one per line.

`generate.ts --record-as <name>` sets the teacher's name in the data, for when the
id used to call a model should not be recorded. Rate limits (429) are retried with
backoff, and a question that still fails is not written, so a re-run picks it up.
Check the provider's limit before setting `--concurrency`: `glm-5.3`'s account has
10 slots, and at 12 it returned 408 rate limit errors in one run. Use 8.

Each step can be re-run, and picks up where it stopped. Send progress to a log with
`tee`, not `tail`, which shows nothing until the process exits.

The running-config conversations use `fixture/`, not the eval's fixture. That has
the same shape with different ids and values, so a model cannot pass the eval's
running-config cases from memory.

Warm the teacher up with one request first. A peer's `sendLoadingState` text arrives
as `reasoning_content`, and `filter.py` rejects any conversation containing it.

`lfm3-v1` and `lfm3-v2` used `Q3.8-27B-rad` as the teacher. On 16 pilot questions:

| Teacher | Time at concurrency 4 | Passed `filter.py` |
|---|---|---|
| `Q3.8-27B-rad` | 158 s | 12 of 16 |
| `qwen-3.8-next` | 333 s | 7 of 16, 4 lost to loading text |

The first batch had 565 questions at `--per-doc 12`, and took 90 minutes. It left
41 running-config and 7 nonexistent conversations, because those are the hardest
to pass the filter. The second batch asked only for those kinds, added 138, and
lifted running-config from the largest group of eval failures to one of the
smallest. Check the balance of `kept by kind` before training, not only the total:

```bash
~/lfm3/train/.venv/bin/python evals/docs-agent/training/questions.py \
  --base-url http://127.0.0.1:18081 --model sippy/Q3.8-27B-rad \
  --out ~/lfm3/data/batch2-questions.jsonl --tag b2 \
  --avoid ~/lfm3/data/batch1-questions.jsonl --only nonexistent --per-area 40
```

Repeat with `--only fixture --per-config 20`. Then record it as above, join the
conversation files, copy one `.tools.json` next to the result, and run `filter.py`.
It also writes `tools.json` and `manifest.json`.

`lfm3-v3` tried a smarter teacher: `glm-5.3` first, then `Q3.8-27B-rad` where
`glm-5.3` failed. Both answered the same 938 questions:

| Teacher | Kept | Median time | Long reasoning | Invented URLs | Tool errors |
|---|---|---|---|---|---|
| `Q3.8-27B-rad` | 434 | 32 s | not counted | 5 | 124 |
| `glm-5.3` | 339 | 12 s | 219, 160 on the final turn | 69 | 89 |

The joined set kept 525: 337 from `glm-5.3` and 188 from `Q3.8-27B-rad`. The model
trained on it was no better. See [the experiment](#the-lfm3-v3-experiment).

Automated checks do not prove accuracy, so read a sample. Its running-config values
matched the fixture, and it recognized features that do not exist.

Record the teacher in the manifest. Check its terms before generating anything,
because a model's terms may restrict using its output to train another model.
Anthropic's terms restrict using its services to build competing products,
including training competing AI models, so get a decision on that before using
Claude as the teacher. Writing the pipeline and the checks is a separate matter
from producing the training examples.

## Step 4: Train

```bash
PYTORCH_CUDA_ALLOC_CONF=expandable_segments:True \
CUDA_DEVICE_ORDER=PCI_BUS_ID CUDA_VISIBLE_DEVICES=3 \
~/lfm3/train/.venv/bin/python evals/docs-agent/training/train.py \
  --model ~/lfm3/base-hf --data evals/docs-agent/training/data \
  --out ~/lfm3/runs/lfm3-v2 2>&1 | tee ~/lfm3/logs/train-lfm3-v2.log
```

The settings follow Liquid's
[SFT notebook](https://github.com/Liquid4All/cookbook/blob/main/finetuning/notebooks/lfm3_sft_with_trl.ipynb),
with its LoRA but the plain `Trainer`, because each example needs a custom loss.

| Setting | Value |
|---|---|
| `r` / `lora_alpha` / `lora_dropout` | 16 / 32 / 0.05 |
| `target_modules` | `q_proj k_proj v_proj o_proj in_proj out_proj w1 w2 w3` |
| Learning rate | 2e-4, cosine, 5% warmup |
| Batch | 1, accumulating 8 |
| Epochs | 2 |
| Precision | bf16 |

`target_modules` are specific to the architecture and must be looked up for a
new model.

The loss fell from about 1.1 to 0.77. Validation loss cannot be compared between
runs whose data differs.

Only assistant turns are trained on. The system prompt, questions and tool results
are context. Two consequences are easy to get wrong:

- Logits are computed only from the first target token. Full logits for a 13,000
  token prompt are 128,000 x 13,000 x 2 bytes, about 7 GB, and caused an OOM.
- A custom `compute_loss` must divide by `num_items_in_batch` when Trainer passes
  it, because Trainer then skips its own division by the accumulation steps.
  A plain mean reports, and steps with, a loss that many times too large.

Before training, confirm rendered prompts are byte-identical to `llama-server`,
using `/upstream/<model>/apply-template`. If they differ, the model trains on text
it will never see. `llama-server` drops every tool field except `name`,
`description` and `parameters`, so `train.py` does too. `lfm3-v2` predates that fix
and trained with tool titles in its prompt, so training on `data/` now gives a close
model, not the same one.

### Reference: one RTX 3090

Measured on an RTX 3090 (24 GB, 280 W power limit) with LFM3-1.7B in bf16, the
settings above, gradient checkpointing, and `--max-length 16384`:

| Run | Turns | Target tokens | Steps | Time | Peak VRAM | Final validation loss |
|---|---|---|---|---|---|---|
| `lfm3-v1` | 918 | 179,815 | 230 | 60 min | 8.9 GB | 0.70 |
| `lfm3-v2` | 1,329 | 258,739 | 334 | 87 min | 9.0 GB | 0.74 |
| `lfm3-v3` | 1,626 | 331,098 | 408 | 104 min | 9.9 GB | 0.73 |

| Measure | Value |
|---|---|
| Throughput | 0.51 to 0.52 turns/s on all three runs |
| Time per step | 12 to 15 s, for 8 turns |
| Planning estimate | 2 s per turn, per epoch |
| VRAM in `nvidia-smi` | 9.5 GB for `lfm3-v2`; not recorded for `lfm3-v3` |
| Longest turn | 13,698 tokens, 15,903 in `lfm3-v3` |
| Validation pass | 40 to 92 s, once per epoch |
| GPU use and power | 100%, held at the 280 W limit |
| Temperature | 64°C at 72% fan, the cooler card |
| GPU energy | about 0.4 kWh for `lfm3-v2` |
| Trainable parameters | 16.1 M of 1.74 B, 0.92% |
| Adapter size | 64 MB |
| Merged model size | 3.3 GB in bf16 |

VRAM depends on the longest turn, not on the size of the dataset. It went from 8.9
to 9.0 GB as the data grew by half, then to 9.9 GB when the longest turn grew 16%.
`--max-length 16384` limits that, and dropped 1 turn from `lfm3-v3`. A 12 GB card
should fit these settings, though that has not been tested. Before the prompt logits
were skipped, a 24-turn dry run peaked at 19.3 GB and the first full run ran out of
memory on the 3090.

### Reasoning

LFM3's template opens `<think>` by default, and drops reasoning from turns before
the last user message. Decide whether to train with reasoning before generating
data, because the data has to match. Reasoning-free is faster, which matters for
WebGPU, and the eval has to run in the same mode as training.

The client never sends `reasoning_content` back, so during a turn the template
renders earlier assistant messages with an empty `<think></think>`. Train the
same way: only the assistant message being predicted may carry reasoning, or the
model trains on a history it will never see.

## Step 5: Export to GGUF

`llama-quantize` is not part of a default server-only build. Build it outside the
llama.cpp checkout, without a GPU backend:

```bash
cd ~/dev/llama.cpp
cmake -S . -B ~/lfm3/llama-build -DCMAKE_BUILD_TYPE=Release -DLLAMA_CURL=OFF \
  -DLLAMA_BUILD_SERVER=OFF -DLLAMA_BUILD_TESTS=OFF -DLLAMA_BUILD_EXAMPLES=OFF
cmake --build ~/lfm3/llama-build --target llama-quantize -j 16
PYTHONPATH=gguf-py ~/lfm3/train/.venv/bin/python convert_hf_to_gguf.py \
  ~/lfm3/runs/lfm3-v2/merged --outtype bf16 \
  --outfile ~/lfm3/gguf/lfm3-v2-bf16.gguf
~/lfm3/llama-build/bin/llama-quantize ~/lfm3/gguf/lfm3-v2-bf16.gguf \
  ~/lfm3/gguf/lfm3-v2-Q4_K_M.gguf Q4_K_M
```

This takes under two minutes, and the result is the same size as the stock Q4_K_M.

## Step 6: Measure the fine-tune

During training iterations, measure the safetensors directly, which skips Step 5.
LFM3 is served with SGLang's `dev-lfm3` image, because LFM3 support is not in a
tagged SGLang release:

```bash
docker run --rm --name lfm3-sglang --runtime=nvidia \
  --gpus '"device=<full GPU UUID>"' \
  --shm-size 32g --ipc=host -p 127.0.0.1:18200:30000 \
  -v ~/lfm3/base-hf:/models/lfm3:ro lmsysorg/sglang:dev-lfm3 \
  sglang serve --model-path /models/lfm3 --served-model-name lfm3-stock-bf16 \
  --host 0.0.0.0 --port 30000 --trust-remote-code \
  --reasoning-parser auto --tool-call-parser auto \
  --context-length 65536 --mem-fraction-static 0.7
```

It reads `temperature`, `top_k` and `repetition_penalty` from the checkpoint's
`generation_config.json`, so the eval, which sends none of them, gets the model
card's settings. Copy that file into any checkpoint you serve. Its repetition
penalty covers generated tokens only, where llama.cpp uses a window of the last
64 tokens, so sampling is close to the GGUF but not identical.

Training and benchmarking do not use llama-swap. The eval does, because the
agent's docs tools come from `/api/mcp`. Run one for the tools and add SGLang as a
peer, then evaluate `sgl/<model>`:

```yaml
startPort: 6100
peers:
  sgl:
    proxy: http://127.0.0.1:18200
    models:
      - lfm3-stock-bf16
models: {}
```

vLLM 0.33 cannot load LFM3: it fails with `Layer 0 (conv) does not dispatch
through the Transformers attention interface`.

`serve.py` is a fallback for checkpoints SGLang cannot load. It runs one request
at a time on transformers, at about 21 tok/s against SGLang's 180. It can load a
LoRA adapter without merging, and applies llama.cpp's 64-token repetition window,
because transformers' default penalizes copying a key out of a tool result.

To run a second llama-swap beside another, give it its own `startPort`. Two
instances otherwise assign the same upstream ports: the second model fails to
bind, llama-swap's health check passes against the *first* instance's server, and
the eval silently measures the wrong model. Confirm with `nvidia-smi` and the
server's own log that the expected process is doing the work.

Compare safetensors runs only with a stock safetensors run, not with the Q4_K_M
numbers, because quantization alone can move the score.

### How long it takes

`benchmark.py` against SGLang on one RTX 3090, with LFM3-1.7B in bf16, about 7,400
prompt tokens with no cache hits and 460 generated tokens per request:

| Concurrency | Generated tok/s | Requests/s | Median latency |
|---|---|---|---|
| 1 | 149 | 0.32 | 3.1 s |
| 4 | 364 | 0.79 | 4.9 s |
| 8 | 503 | 1.09 | 7.3 s |
| 16 | 628 | 1.36 | 11.7 s |
| 32 | 721 | 1.57 | 20.5 s |

In practice, at concurrency 16, the stock model took 13 minutes per pass over the
134 training cases and 5 minutes over the 51 holdout cases, so `--repeat 3` on
both is about 53 minutes. A model that stops sooner takes less. SGLang took 19 GB
of VRAM with `--mem-fraction-static 0.7`.

`lfm3-v2` Q4_K_M on `llama-server`, on one RTX 3090 with `--parallel 4` and all
layers on the GPU, used 3.1 GB of VRAM. Each slot averaged 63 generated tok/s and
5,400 prompt tok/s under load. At concurrency 4, `--repeat 3` and a 60 s limit, the
training cases took 26 minutes and the holdout 10. A question took a median of 12 s,
and 32 s at the 95th percentile. `lfm3-v3` took 11 s and 32 s.

### Before calling it finished

Before calling a model finished, export it as in Step 5, add the GGUF to
`evals/config.yaml` and repeat the commands from Step 2 with `--repeat 3`. The
quantized file is what ships, so its score is the result. Run the training cases
as often as needed, and the holdout rarely.

Follow the [stopping rule](../README.md#stopping-rule). If the training score
climbs while the holdout does not, stop: that is overfitting.

A low regex score can hide behind a good one, so also run the
[LLM judge](../README.md) on the saved results. It reads only the first attempt,
and a timed-out verdict counts as zero, so compare only the cases every run got a
verdict on.

Record each run below, including failures.

## Step 7: Publish to Hugging Face

`lfm3-v2` is published as `llama-swap/LFM3-1.7B-HelpAgent`, a name that leaves out
the run so users are not left wondering what `v2` means. Assemble the files in a new
directory, not from `merged/` alone:

```bash
P=~/lfm3/publish/LFM3-1.7B-HelpAgent
mkdir -p $P
cd ~/lfm3/base-hf
cp LICENSE chat_template.jinja config.json configuration_lfm3.py \
  generation_config.json lfm3_conv.py modeling_lfm3.py special_tokens_map.json \
  tokenizer.json tokenizer_config.json $P/
cp ~/lfm3/runs/lfm3-v2/merged/model.safetensors $P/

cd ~/dev/llama.cpp
PYTHONPATH=gguf-py ~/lfm3/train/.venv/bin/python \
  gguf-py/gguf/scripts/gguf_new_metadata.py \
  --general-name LFM3-1.7B-HelpAgent ~/lfm3/gguf/lfm3-v2-Q4_K_M.gguf \
  $P/LFM3-1.7B-HelpAgent-Q4_K_M.gguf
```

- **Take the config and tokenizer from the base model.** LoRA does not change them.
  When `lfm3-v2` was trained, `train.py` re-saved them into `merged/`, leaving
  `use_cache: false`, which slows generation for anyone using the checkpoint, and
  `is_local: true`. `train.py` now copies the base model's files, so this step is
  needed only for `lfm3-v2`. Check the weights have the same keys and shapes, and
  that prompts render the same.
- **Set the GGUF's name.** `convert_hf_to_gguf.py` names it after the directory, so
  `general.name` was `Merged`. The rewrite changes only that field: all 287 tensors
  are identical to the file that was scored.
- **Write `README.md` and `NOTICE` by hand.** See
  [what the license requires](#license). Both are on the Hub.

Then publish privately, verify, and only then go public:

```bash
hf repo create llama-swap/LFM3-1.7B-HelpAgent --private
hf upload llama-swap/LFM3-1.7B-HelpAgent $P . --commit-message "Add LFM3-1.7B-HelpAgent"
# Compare hashes with the local files, and run the card's llama-server command.
hf repo settings llama-swap/LFM3-1.7B-HelpAgent --no-private
```

These are `hf` 1.4.1 commands. Newer versions use `hf repos`. A fine-grained token
needs `repo.write` on the org: a read-only one fails with `403 Forbidden: You don't
have the rights to create a model under the namespace`.

## Run log

| Date | Model | Data | Training | Holdout | Notes |
|---|---|---|---|---|---|
| 2026-11-05 | `LFM3-1.7B` bf16 stock | none | 84.8% | 89.5% | SGLang, `--repeat 3`, concurrency 16. 13 min per training pass, 5 min per holdout pass. |
| 2026-11-05 | `lfm3-v1` bf16 | 279 conversations, batch 1 | 95.8% | not run | 5.1 steps against 12.5, 13 s against 73 s, 1 turn error against 28. |
| 2026-11-05 | `lfm3-v2` bf16 | 417 conversations, both batches | 96.8% | 96.1% | Trained with tool titles in the prompt. No turn errors. |
| 2026-11-05 | `lfm3-v2` Q4_K_M | same | 95.3% | 97.4% | llama-server, concurrency 4, 300 s limit. 2 attempts lost to `proxy error: EOF`. 4 of 402 hit the limit. |
| 2026-11-05 | `LFM3-1.7B` Q4_K_M stock | none | 44.8% | 55.6% | 60 s limit. 276 of 555 attempts timed out, and the median attempt on the training cases reached the limit. |
| 2026-11-05 | `lfm3-v2` Q4_K_M | 417 conversations | 95.5% | 96.1% | 60 s limit. 9 of 555 timed out, at 4 to 25 steps. 108 of 1,999 tool calls failed. |
| 2026-11-05 | `lfm3-v3` Q4_K_M | 525 conversations, `glm-5.3` and `Q3.8-27B-rad` | 95.3% | 92.8% | 60 s limit. 13 of 555 timed out, 7 of them on the holdout. 83 of 1,868 tool calls failed on the training cases. Judge found no improvement. Holdout reached the stopping rule's limit of 5 runs. |

Repeat penalty, from Liquid's llama.cpp documentation (1.05) against its model card
(1.1), both at the 60 s limit:

| Model | Penalty 1.1, training / holdout | Penalty 1.05, training / holdout |
|---|---|---|
| `LFM3-1.7B` Q4_K_M stock | 44.8% / 55.6% | 42.5% / 50.3% |
| `lfm3-v2` Q4_K_M | 95.5% / 96.1% | 95.5% / 92.8% |

1.05 did not help on the training cases, so 1.1 stayed. These runs used the holdout
again. Do not choose a setting from its difference.

Judge, `Q3.8-27B-rad`, on the 116 cases where all three got a verdict:

| Model | Mean score | Rubric items | Rationales citing invented content |
|---|---|---|---|
| `LFM3-1.7B` bf16 stock | 3.47 | 211 of 243 | 26 |
| `lfm3-v1` bf16 | 3.71 | 216 of 243 | 21 |
| `lfm3-v2` bf16 | 3.83 | 223 of 243 | 20 |

### The `lfm3-v3` experiment

`lfm3-v3` did not improve on `lfm3-v2`, so it was not published. It had 26%
more conversations, a smarter teacher and stricter filters, including schema checks
aimed at invented config. It scored lower on the holdout, tied on the training cases
and on the judge, and invented just as much. Its data stays in `data-v3/` so the
result can be checked, and so it is not tried again unchanged.

The same judge on the Q4_K_M runs at 60 s, on the 119 cases where both got a verdict:

| Model | Mean score | Rubric items | Rationales citing invented content | Scored 0 or 1 |
|---|---|---|---|---|
| `lfm3-v2` Q4_K_M | 3.77 | 222 of 250 | 19 | 16 |
| `lfm3-v3` Q4_K_M | 3.86 | 228 of 250 | 22 | 12 |

`lfm3-v3` scored higher on 34 cases and lower on 29. The difference is 0.08, with a
95% interval of -0.28 to 0.45, so this is a tie. Single verdicts are noisy: 14 cases
rose by 3 or more points and 11 fell by as much. On the holdout, `lfm3-v3` scored
92.8% against 96.1%, with 7 timeouts against 2. That gap is 5 attempts of 153, too
few to prove it is worse, but nothing shows it is better.

### Known problems in `lfm3-v2` and `lfm3-v3`

- 1.6% of `lfm3-v2` attempts and 2.3% of `lfm3-v3` attempts on the Q4_K_M miss the
  60 s limit. Some reason for too long and some loop on tools, at 4 to 25 steps. The
  app needs a token limit on reasoning, or the next run needs training data that
  targets it.
- Both still invent keys, in about 1 of 6 answers by the judge's rationales. A
  regex cannot see most of these, and better data did not reduce it.
- `lfm3-v2` trained on 11 conversations, and validated on 1, that held the teacher
  host's `peers` block. In `data/` the address in those 12 tool results now reads
  `sippy:8080`. No trained text changed: all 1,473 targets are identical, and only
  the prompts of 17 turns differ from what the model saw. `lfm3-v3` removed those
  conversations, but 3 of its 525 conversations still list the peer names
  `sgl` and `sippy`, in `config__get_config` results and in one answer. They hold
  names only, and `sippy` is already the KB's example peer, so the data stays as
  trained. No answer at the 60 s limit repeats the removed text, and `lfm3-v3` names
  those peers only where the KB's example does. A future dataset should use a
  tools server with no peers except the teacher's, under a name nothing else uses.
- `lfm3-v2` Q4_K_M has used the holdout three times and `lfm3-v3` Q4_K_M once. Their
  gap is 5 attempts of 153, about as much as `lfm3-v2` moved between its own
  penalty settings. Do not tune against it.

## Follow-up

Not part of this work, and needed before llama-swap can use the published model:

- Download it automatically from llama-swap.
- Test it in the browser. Whether `LFM3-1.7B` runs on WebGPU is unconfirmed, and it
  is a separate project.
- Add a client-side token limit on reasoning.
- Link the model card to this directory once it is on `main`. It links to #1302 for
  now.

## Doing this for a future model

1. Check its tool calls parse in `llama-server`, with Step 2's single question.
2. Measure it stock. If it already meets the target, stop.
3. Check `data/manifest.json` against the current KB, tools and system prompt,
   and regenerate if any changed.
4. Look up its LoRA `target_modules`, template and sampling settings.
5. Run its template through the check in Step 4. A new template can change how
   tool calls and reasoning render even when the data does not change.
6. Train, export, measure, and add a row to the run log.

### License

Check a new model's license before training on it. `LFM3-1.7B` is under the LFM Open
License v1.0, and [Liquid's guide](https://docs.liquid.ai/lfm/help/model-license)
lists what distributing a fine-tune requires. Downloading one automatically from
llama-swap is distribution too.

| Requirement | `LFM3-1.7B-HelpAgent` |
|---|---|
| Include a copy of the license | `LICENSE`, byte-identical to Liquid's |
| Mark modified files | `NOTICE` and the model card name the weights, the GGUF and the card |
| Keep copyright and attribution notices | All other files are unchanged from Liquid's revision `0dc4268` |
| Include any `NOTICE` file | Liquid ships none, so ours is new |

Failing any of these ends the license automatically, and you must then delete the
model and every derivative. Commercial use is limited to organizations under $10M in
annual revenue, and that applies to each company using the model.

The license grants no rights to Liquid's trademarks, beyond describing where a model
came from. The published name contains `LFM3` for that reason, and the card and
`NOTICE` say the model is not made, affiliated with or endorsed by Liquid AI. Liquid
has not confirmed that this name is acceptable. A repository can be renamed if needed.
