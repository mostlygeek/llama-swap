# llama-swap

![llama-swap header image](header.jpeg)

# Introduction
llama-swap is an OpenAI API compatible server that gives you complete control over how you use your hardware. It automatically swaps to the configuration of your choice for serving a model. Since [llama.cpp's server](https://github.com/ggerganov/llama.cpp/tree/master/examples/server) can't swap models, let's swap the server instead!

Features:

- ✅ Easy to deploy: single binary with no dependencies
- ✅ Easy to config: single yaml file
- ✅ On-demand model switching
- ✅ Full control over server settings per model
- ✅ OpenAI API support (`v1/completions` and `v1/chat/completions`)
- ✅ Multiple GPU support
- ✅ Run multiple models at once with `profiles`
- ✅ Remote log monitoring at `/log`
- ✅ Automatic unloading of models from GPUs after timeout
- ✅ Use any local OpenAI compatible server (llama.cpp, vllm, tabblyAPI, etc)
- ✅ Direct access to proxied upstream HTTP server via `/upstream/:model_id`

## Releases

Builds for Linux and OSX are available on the [Releases](https://github.com/mostlygeek/llama-swap/releases) page.

### Building from source

1. Install golang for your system
1. `git clone git@github.com:mostlygeek/llama-swap.git`
1. `make clean all`
1. Binaries will be in `build/` subdirectory

## config.yaml

llama-swap's configuration is purposefully simple.

```yaml
# Seconds to wait for llama.cpp to load and be ready to serve requests
# Default (and minimum) is 15 seconds
healthCheckTimeout: 60

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

More [examples](examples/README.md) are available for different use cases.

## Installation

1. Create a configuration file, see [config.example.yaml](config.example.yaml)
1. Download a [release](https://github.com/mostlygeek/llama-swap/releases) appropriate for your OS and architecture.
    * _Note: Windows currently untested._
1. Run the binary with `llama-swap --config path/to/config.yaml`

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
