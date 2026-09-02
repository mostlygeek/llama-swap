# aider, QwQ, Qwen-Coder 2.5 and llama-swap

This guide show how to use aider and llama-swap to get a 100% local coding co-pilot setup. The focus is on the trickest part which is configuring aider, llama-swap and llama-server to work together.

## Here's what you you need:

- aider - [installation docs](https://aider.chat/docs/install.html)
- llama-server - [download latest release](https://github.com/ggml-org/llama.cpp/releases)
- llama-swap - [download latest release](https://github.com/mostlygeek/llama-swap/releases)
- [QwQ 32B](https://huggingface.co/bartowski/Qwen_QwQ-32B-GGUF) and [Qwen Coder 2.5 32B](https://huggingface.co/bartowski/Qwen2.5-Coder-32B-Instruct-GGUF) models
- 24GB VRAM video card

## Running aider

The goal is getting this command line to work:

```sh
aider --architect \
    --no-show-model-warnings \
    --model openai/QwQ \
    --editor-model openai/qwen-coder-32B \
    --model-settings-file aider.model.settings.yml \
    --openai-api-key "sk-na" \
    --openai-api-base "http://10.0.1.24:8080/v1" \
```

Set `--openai-api-base` to the IP and port where your llama-swap is running.

## Create an aider model settings file

```yaml
# aider.model.settings.yml

#
# !!! important: model names must match llama-swap configuration names !!!
#

- name: "openai/QwQ"
  edit_format: diff
  extra_params:
    max_tokens: 16384
    top_p: 0.95
    top_k: 40
    presence_penalty: 0.1
    repetition_penalty: 1
    num_ctx: 16384
  use_temperature: 0.6
  reasoning_tag: think
  weak_model_name: "openai/qwen-coder-32B"
  editor_model_name: "openai/qwen-coder-32B"

- name: "openai/qwen-coder-32B"
  edit_format: diff
  extra_params:
    max_tokens: 16384
    top_p: 0.8
    top_k: 20
    repetition_penalty: 1.05
  use_temperature: 0.6
  reasoning_tag: think
  editor_edit_format: editor-diff
  editor_model_name: "openai/qwen-coder-32B"
```

## llama-swap configuration

```yaml
# config.yaml

# The parameters are tweaked to fit model+context into 24GB VRAM GPUs
models:
  "qwen-coder-32B":
    proxy: "http://127.0.0.1:8999"
    cmd: >
      /path/to/llama-server
      --host 127.0.0.1 --port 8999 --flash-attn --slots
      --ctx-size 16000
      --cache-type-k q8_0 --cache-type-v q8_0
       -ngl 99
      --model /path/to/Qwen2.5-Coder-32B-Instruct-Q4_K_M.gguf

  "QwQ":
    proxy: "http://127.0.0.1:9503"
    cmd: >
      /path/to/llama-server
      --host 127.0.0.1 --port 9503 --flash-attn --metrics--slots
      --cache-type-k q8_0 --cache-type-v q8_0
      --ctx-size 32000
      --samplers "top_k;top_p;min_p;temperature;dry;typ_p;xtc"
      --temp 0.6 --repeat-penalty 1.1 --dry-multiplier 0.5
      --min-p 0.01 --top-k 40 --top-p 0.95
      -ngl 99
      --model /mnt/nvme/models/bartowski/Qwen_QwQ-32B-Q4_K_M.gguf
```

## Advanced, Dual GPU Configuration

If you have _dual 24GB GPUs_ you can use llama-swap profiles to avoid swapping between QwQ and Qwen Coder.

In llama-swap's configuration file:

1. add a `profiles` section with `aider` as the profile name
2. using the `env` field to specify the GPU IDs for each model

```yaml
# config.yaml

# Add a profile for aider
profiles:
  aider:
    - qwen-coder-32B
    - QwQ

models:
  "qwen-coder-32B":
    # manually set the GPU to run on
    env:
      - "CUDA_VISIBLE_DEVICES=0"
    proxy: "http://127.0.0.1:8999"
    cmd: /path/to/llama-server ...

  "QwQ":
    # manually set the GPU to run on
    env:
      - "CUDA_VISIBLE_DEVICES=1"
    proxy: "http://127.0.0.1:9503"
    cmd: /path/to/llama-server ...
```

Append the profile tag, `aider:`, to the model names in the model settings file

```yaml
# aider.model.settings.yml
- name: "openai/aider:QwQ"
  weak_model_name: "openai/aider:qwen-coder-32B-aider"
  editor_model_name: "openai/aider:qwen-coder-32B-aider"

- name: "openai/aider:qwen-coder-32B"
  editor_model_name: "openai/aider:qwen-coder-32B-aider"
```

Run aider with:

```sh
$ aider --architect \
    --no-show-model-warnings \
    --model openai/aider:QwQ \
    --editor-model openai/aider:qwen-coder-32B \
    --config aider.conf.yml \
    --model-settings-file aider.model.settings.yml
    --openai-api-key "sk-na" \
    --openai-api-base "http://10.0.1.24:8080/v1"
```
