![llama-swap header image](header.jpeg)

# llama-swap
llama-swap is a light weight, transparent proxy server that provides automatic model swapping to llama.cpp's server.

Written in golang, it is very easy to install (single binary with no dependancies) and configure (single yaml file).

Download a pre-built [release](https://github.com/mostlygeek/llama-swap/releases) or build it yourself from source with `make clean all`.

## How does it work?
When a request is made to an OpenAI compatible endpoint, lama-swap will extract the `model` value and load the appropriate server configuration to serve it. If a server is already running it will stop it and start the correct one. This is where the "swap" part comes in. The upstream server is automatically swapped to the correct one to serve the request.

In the most basic configuration llama-swap handles one model at a time. For more advanced use cases, the `profiles` feature can load multiple models at the same time. You have complete control over how your system resources are used.

## Do I need to use llama.cpp's server (llama-server)?
Any OpenAI compatible server would work. llama-swap was originally designed for llama-server and it is the best supported. For Python based inference servers like vllm or tabbyAPI it is recommended to run them via podman. This provides clean environment isolation as well as responding correctly to `SIGTERM` signals to shutdown.

## Features:

- ✅ Easy to deploy: single binary with no dependencies
- ✅ Easy to config: single yaml file
- ✅ On-demand model switching
- ✅ Full control over server settings per model
- ✅ OpenAI API supported endpoints:
  - `v1/completions`
  - `v1/chat/completions`
  - `v1/embeddings`
  - `v1/rerank`
  - `v1/audio/speech` ([#36](https://github.com/mostlygeek/llama-swap/issues/36))
- ✅ Multiple GPU support
- ✅ Docker and Podman support
- ✅ Run multiple models at once with `profiles`
- ✅ Remote log monitoring at `/log`
- ✅ Automatic unloading of models from GPUs after timeout
- ✅ Use any local OpenAI compatible server (llama.cpp, vllm, tabbyAPI, etc)
- ✅ Direct access to upstream HTTP server via `/upstream/:model_id` ([demo](https://github.com/mostlygeek/llama-swap/pull/31))

## config.yaml

llama-swap's configuration is purposefully simple.

```yaml
# Seconds to wait for llama.cpp to load and be ready to serve requests
# Default (and minimum) is 15 seconds
healthCheckTimeout: 60

# Write HTTP logs (useful for troubleshooting), defaults to false
logRequests: true

# define valid model values and the upstream server start
models:
  "llama":
    cmd: llama-server --port 8999 -m Llama-3.2-1B-Instruct-Q4_K_M.gguf

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

  "qwen":
    # environment variables to pass to the command
    env:
      - "CUDA_VISIBLE_DEVICES=0"

    # multiline for readability
    cmd: >
      llama-server --port 8999
      --model path/to/Qwen2.5-1.5B-Instruct-Q4_K_M.gguf
    proxy: http://127.0.0.1:8999

  # unlisted models do not show up in /v1/models or /upstream lists
  # but they can still be requested as normal
  "qwen-unlisted":
    cmd: llama-server --port 9999 -m Llama-3.2-1B-Instruct-Q4_K_M.gguf -ngl 0
    unlisted: true

  # Docker Support (v26.1.4+ required!)
  "docker-llama":
    proxy: "http://127.0.0.1:9790"
    cmd: >
      docker run --name dockertest
      --init --rm -p 9790:8080 -v /mnt/nvme/models:/models
      ghcr.io/ggerganov/llama.cpp:server
      --model '/models/Qwen2.5-Coder-0.5B-Instruct-Q4_K_M.gguf'

# profiles make it easy to managing multi model (and gpu) configurations.
#
# Tips:
#  - each model must be listening on a unique address and port
#  - the model name is in this format: "profile_name:model", like "coding:qwen"
#  - the profile will load and unload all models in the profile at the same time
profiles:
  coding:
    - "qwen"
    - "llama"
```

### Advanced Examples

- [config.example.yaml](config.example.yaml) includes example for supporting `v1/embeddings` and `v1/rerank` endpoints
- [Speculative Decoding](examples/speculative-decoding/README.md) - using a small draft model can increase inference speeds from 20% to 40%. This example includes a configurations Qwen2.5-Coder-32B (2.5x increase) and Llama-3.1-70B (1.4x increase) in the best cases.
- [Optimizing Code Generation](examples/benchmark-snakegame/README.md) - find the optimal settings for your machine. This example demonstrates defining multiple configurations and testing which one is fastest.

### Installation

1. Create a configuration file, see [config.example.yaml](config.example.yaml)
1. Download a [release](https://github.com/mostlygeek/llama-swap/releases) appropriate for your OS and architecture.
    * _Note: Windows currently untested._
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
