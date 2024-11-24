# llama-swap

![llama-swap header image](header.jpeg)

llama-swap is a golang server that automatically swaps the llama.cpp server on demand. Since [llama.cpp's server](https://github.com/ggerganov/llama.cpp/tree/master/examples/server) can't swap models, let's swap the server instead!

Features:

- ✅ Easy to deploy: single binary with no dependencies
- ✅ Single yaml configuration file
- ✅ Automatically switching between models
- ✅ Full control over llama.cpp server settings per model
- ✅ OpenAI API support (`v1/completions` and `v1/chat/completions`)
- ✅ Multiple GPU support
- ✅ Run multiple models at once with `profiles`
- ✅ Remote log monitoring at `/log`

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

# profiles make it easy to managing multi model (and gpu) configurations.
#
# Tips:
#  - each model must be listening on a unique address and port
#  - the model name is in this format: "profile_name/model", like "coding/qwen"
#  - the profile will load and unload all models in the profile at the same time
profiles:
  coding:
    - "qwen"
    - "llama"
```

## Installation

1. Create a configuration file, see [config.example.yaml](config.example.yaml)
1. Download a [release](https://github.com/mostlygeek/llama-swap/releases) appropriate for your OS and architecture.
    * _Note: Windows currently untested._
1. Run the binary with `llama-swap --config path/to/config.yaml`

## Monitoring Logs

The `/logs` endpoint is available to monitor what llama-swap is doing. It will send the last 10KB of logs. Useful for monitoring the output of llama-server. It also supports streaming of logs.

Usage:

```
# sends up to the last 10KB of logs
curl http://host/logs'

# streams logs using chunk encoding
curl -Ns 'http://host/logs/stream'

# skips history and just streams new log entries
curl -Ns 'http://host/logs/stream?no-history'

# streams logs using Server Sent Events
curl -Ns 'http://host/logs/streamSSE'
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

## Building from Source

1. Install golang for your system
1. run `make clean all`
1. binaries will be built into `build/` directory
