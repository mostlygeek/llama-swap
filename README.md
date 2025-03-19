![llama-swap header image](header.jpeg)
![GitHub Downloads (all assets, all releases)](https://img.shields.io/github/downloads/mostlygeek/llama-swap/total)
![GitHub Actions Workflow Status](https://img.shields.io/github/actions/workflow/status/mostlygeek/llama-swap/go-ci.yml)
![GitHub Repo stars](https://img.shields.io/github/stars/mostlygeek/llama-swap)



# llama-swap

llama-swap is a light weight, transparent proxy server that provides automatic model swapping to llama.cpp's server.

Written in golang, it is very easy to install (single binary with no dependancies) and configure (single yaml file). To get started, download a pre-built binary or use the provided docker images.

## Features:

- ✅ Easy to deploy: single binary with no dependencies
- ✅ Easy to config: single yaml file
- ✅ On-demand model switching
- ✅ OpenAI API supported endpoints:
  - `v1/completions`
  - `v1/chat/completions`
  - `v1/embeddings`
  - `v1/rerank`
  - `v1/audio/speech` ([#36](https://github.com/mostlygeek/llama-swap/issues/36))
  - `v1/audio/transcriptions` ([docs](https://github.com/mostlygeek/llama-swap/issues/41#issuecomment-2722637867))
- ✅ llama-swap custom API endpoints
  - `/log` - remote log monitoring
  - `/upstream/:model_id` - direct access to upstream HTTP server ([demo](https://github.com/mostlygeek/llama-swap/pull/31))
  - `/unload` - manually unload running models ([#58](https://github.com/mostlygeek/llama-swap/issues/58))
  - `/running` - list currently running models ([#61](https://github.com/mostlygeek/llama-swap/issues/61))
- ✅ Run multiple models at once with `profiles` ([docs](https://github.com/mostlygeek/llama-swap/issues/53#issuecomment-2660761741))
- ✅ Automatic unloading of models after timeout by setting a `ttl`
- ✅ Use any local OpenAI compatible server (llama.cpp, vllm, tabbyAPI, etc)
- ✅ Docker and Podman support
- ✅ Full control over server settings per model

## How does llama-swap work?

When a request is made to an OpenAI compatible endpoint, lama-swap will extract the `model` value and load the appropriate server configuration to serve it. If the wrong upstream server is running, it will be replaced with the correct one. This is where the "swap" part comes in. The upstream server is automatically swapped to the correct one to serve the request.

In the most basic configuration llama-swap handles one model at a time. For more advanced use cases, the `profiles` feature can load multiple models at the same time. You have complete control over how your system resources are used.

## config.yaml

llama-swap's configuration is purposefully simple.

```yaml
models:
  "qwen2.5":
    proxy: "http://127.0.0.1:9999"
    cmd: >
      /app/llama-server
      -hf bartowski/Qwen2.5-0.5B-Instruct-GGUF:Q4_K_M
      --port 9999

  "smollm2":
    proxy: "http://127.0.0.1:9999"
    cmd: >
      /app/llama-server
      -hf bartowski/SmolLM2-135M-Instruct-GGUF:Q4_K_M
      --port 9999
```

<details>
<summary>But also very powerful ...</summary>

```yaml
# Seconds to wait for llama.cpp to load and be ready to serve requests
# Default (and minimum) is 15 seconds
healthCheckTimeout: 60

# Write HTTP logs (useful for troubleshooting), defaults to false
logRequests: true

# define valid model values and the upstream server start
models:
  "llama":
    # multiline for readability
    cmd: >
      llama-server --port 8999
      --model path/to/Qwen2.5-1.5B-Instruct-Q4_K_M.gguf

    # environment variables to pass to the command
    env:
      - "CUDA_VISIBLE_DEVICES=0"

    # where to reach the server started by cmd, make sure the ports match
    proxy: http://127.0.0.1:8999

    # aliases names to use this model for
    aliases:
      - "gpt-4o-mini"
      - "gpt-3.5-turbo"

    # check this path for an HTTP 200 OK before serving requests
    # default: /health to match llama.cpp
    # use "none" to skip endpoint checking, but may cause HTTP errors
    # until the model is ready
    checkEndpoint: /custom-endpoint

    # automatically unload the model after this many seconds
    # ttl values must be a value greater than 0
    # default: 0 = never unload model
    ttl: 60

    # `useModelName` overrides the model name in the request
    # and sends a specific name to the upstream server
    useModelName: "qwen:qwq"

  # unlisted models do not show up in /v1/models or /upstream lists
  # but they can still be requested as normal
  "qwen-unlisted":
    unlisted: true
    cmd: llama-server --port 9999 -m Llama-3.2-1B-Instruct-Q4_K_M.gguf -ngl 0

  # Docker Support (v26.1.4+ required!)
  "docker-llama":
    proxy: "http://127.0.0.1:9790"
    cmd: >
      docker run --name dockertest
      --init --rm -p 9790:8080 -v /mnt/nvme/models:/models
      ghcr.io/ggerganov/llama.cpp:server
      --model '/models/Qwen2.5-Coder-0.5B-Instruct-Q4_K_M.gguf'

# profiles eliminates swapping by running multiple models at the same time
#
# Tips:
#  - each model must be listening on a unique address and port
#  - the model name is in this format: "profile_name:model", like "coding:qwen"
#  - the profile will load and unload all models in the profile at the same time
profiles:
  coding:
    - "llama"
    - "qwen-unlisted"
```

### Use Case Examples

- [config.example.yaml](config.example.yaml) includes example for supporting `v1/embeddings` and `v1/rerank` endpoints
- [Speculative Decoding](examples/speculative-decoding/README.md) - using a small draft model can increase inference speeds from 20% to 40%. This example includes a configurations Qwen2.5-Coder-32B (2.5x increase) and Llama-3.1-70B (1.4x increase) in the best cases.
- [Optimizing Code Generation](examples/benchmark-snakegame/README.md) - find the optimal settings for your machine. This example demonstrates defining multiple configurations and testing which one is fastest.
- [Restart on Config Change](examples/restart-on-config-change/README.md) - automatically restart llama-swap when trying out different configurations.

## Configuration

llama-s

</details>

## Docker Install ([download images](https://github.com/mostlygeek/llama-swap/pkgs/container/llama-swap))

Docker is the quickest way to try out llama-swap:

```
# use CPU inference
$ docker run -it --rm -p 9292:8080 ghcr.io/mostlygeek/llama-swap:cpu


# qwen2.5 0.5B
$ curl -s http://localhost:9292/v1/chat/completions \
    -H "Content-Type: application/json" \
    -H "Authorization: Bearer no-key" \
    -d '{"model":"qwen2.5","messages": [{"role": "user","content": "tell me a joke"}]}' | \
    jq -r '.choices[0].message.content'


# SmolLM2 135M
$ curl -s http://localhost:9292/v1/chat/completions \
    -H "Content-Type: application/json" \
    -H "Authorization: Bearer no-key" \
    -d '{"model":"smollm2","messages": [{"role": "user","content": "tell me a joke"}]}' | \
    jq -r '.choices[0].message.content'
```

<details>
<summary>Docker images are nightly ...</summary>

They include:

- `ghcr.io/mostlygeek/llama-swap:cpu`
- `ghcr.io/mostlygeek/llama-swap:cuda`
- `ghcr.io/mostlygeek/llama-swap:intel`
- `ghcr.io/mostlygeek/llama-swap:vulkan`
- ROCm disabled until fixed in llama.cpp container

Specific versions are also available and are tagged with the llama-swap, architecture and llama.cpp versions. For example: `ghcr.io/mostlygeek/llama-swap:v89-cuda-b4716`

Beyond the demo you will likely want to run the containers with your downloaded models and custom configuration.

```
$ docker run -it --rm --runtime nvidia -p 9292:8080 \
  -v /path/to/models:/models \
  -v /path/to/custom/config.yaml:/app/config.yaml \
  ghcr.io/mostlygeek/llama-swap:cuda
```

</details>

## Bare metal Install ([download](https://github.com/mostlygeek/llama-swap/releases))

Pre-built binaries are available for Linux, FreeBSD and Darwin (OSX). These are automatically published and are likely a few hours ahead of the docker releases. The baremetal install works with any OpenAI compatible server, not just llama-server.

1. Create a configuration file, see [config.example.yaml](config.example.yaml)
1. Download a [release](https://github.com/mostlygeek/llama-swap/releases) appropriate for your OS and architecture.
1. Run the binary with `llama-swap --config path/to/config.yaml`

### Building from source

1. Install golang for your system
1. `git clone git@github.com:mostlygeek/llama-swap.git`
1. `make clean all`
1. Binaries will be in `build/` subdirectory

## Monitoring Logs

Open the `http://<host>/logs` with your browser to get a web interface with streaming logs.

Of course, CLI access is also supported:

```
# sends up to the last 10KB of logs
curl http://host/logs'

# streams logs
curl -Ns 'http://host/logs/stream'

# stream and filter logs with linux pipes
curl -Ns http://host/logs/stream | grep 'eval time'

# skips history and just streams new log entries
curl -Ns 'http://host/logs/stream?no-history'
```

## Do I need to use llama.cpp's server (llama-server)?

Any OpenAI compatible server would work. llama-swap was originally designed for llama-server and it is the best supported.

For Python based inference servers like vllm or tabbyAPI it is recommended to run them via podman or docker. This provides clean environment isolation as well as responding correctly to `SIGTERM` signals to shutdown.

## Systemd Unit Files

Use this unit file to start llama-swap on boot. This is only tested on Ubuntu.

`/etc/systemd/system/llama-swap.service`

```
[Unit]
Description=llama-swap
After=network.target

[Service]
User=nobody

# set this to match your environment
ExecStart=/path/to/llama-swap --config /path/to/llama-swap.config.yml

Restart=on-failure
RestartSec=3
StartLimitBurst=3
StartLimitInterval=30

[Install]
WantedBy=multi-user.target
```

## Star History

<picture>
   <source media="(prefers-color-scheme: dark)" srcset="https://api.star-history.com/svg?repos=mostlygeek/llama-swap&type=Date&theme=dark" />
   <source media="(prefers-color-scheme: light)" srcset="https://api.star-history.com/svg?repos=mostlygeek/llama-swap&type=Date" />
   <img alt="Star History Chart" src="https://api.star-history.com/svg?repos=mostlygeek/llama-swap&type=Date" />
</picture>
