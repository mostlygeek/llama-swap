# llama-swap

[llama.cpp's server](https://github.com/ggerganov/llama.cpp/tree/master/examples/server) can't swap models, so let's swap llama-server instead!

llama-swap is a proxy server that sits in front of llama-server. When a request for `/v1/chat/completions` comes in it will extract the `model` requested and change the underlying llama-server automatically.

- ✅ easy to deploy: single binary with no dependencies
- ✅ full control over llama-server's startup settings
- ✅ ❤️ for nvidia P40 users who are rely on llama.cpp for inference

## config.yaml

llama-swap's configuration purposefully simple.

```yaml
# Seconds to wait for llama.cpp to load and be ready to serve requests
# Default (and minimum) is 15 seconds
healthCheckTimeout: 60

# define valid model values and the upstream server start
models:
  "llama":
    cmd: "llama-server --port 8999 -m Llama-3.2-1B-Instruct-Q4_K_M.gguf"

    # Where to proxy to, important it matches this format
    proxy: "http://127.0.0.1:8999"

    # aliases model names to use this configuration for
    aliases:
    - "gpt-4o-mini"
    - "gpt-3.5-turbo"

  "qwen":
    # environment variables to pass to the command
    env:
      - "CUDA_VISIBLE_DEVICES=0"
    cmd: "llama-server --port 8999 -m path/to/Qwen2.5-1.5B-Instruct-Q4_K_M.gguf"
    proxy: "http://127.0.0.1:8999"
```

## Deployment

1. Create a configuration file, see [config.example.yaml](config.example.yaml)
1. Download a [release](https://github.com/mostlygeek/llama-swap/releases) appropriate for your OS and architecture.
    * _Note: Windows currently untested._
1. Run the binary with `llama-swap --config path/to/config.yaml`

## Systemd Unit Files

Use this unit file to start llama-swap on boot

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