![llama-swap header image](header2.png)
![GitHub Downloads (all assets, all releases)](https://img.shields.io/github/downloads/mostlygeek/llama-swap/total)
![GitHub Actions Workflow Status](https://img.shields.io/github/actions/workflow/status/mostlygeek/llama-swap/go-ci.yml)
![GitHub Repo stars](https://img.shields.io/github/stars/mostlygeek/llama-swap)

# llama-swap

llama-swap is a light weight, transparent proxy server that provides automatic model swapping to llama.cpp's server.

Written in golang, it is very easy to install (single binary with no dependencies) and configure (single yaml file). To get started, download a pre-built binary or use the provided docker images.

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
- ✅ Run multiple models at once with `Groups` ([#107](https://github.com/mostlygeek/llama-swap/issues/107))
- ✅ Automatic unloading of models after timeout by setting a `ttl`
- ✅ Use any local OpenAI compatible server (llama.cpp, vllm, tabbyAPI, etc)
- ✅ Docker and Podman support
- ✅ Full control over server settings per model

## How does llama-swap work?

When a request is made to an OpenAI compatible endpoint, lama-swap will extract the `model` value and load the appropriate server configuration to serve it. If the wrong upstream server is running, it will be replaced with the correct one. This is where the "swap" part comes in. The upstream server is automatically swapped to the correct one to serve the request.

In the most basic configuration llama-swap handles one model at a time. For more advanced use cases, the `groups` feature allows multiple models to be loaded at the same time. You have complete control over how your system resources are used.

## config.yaml

llama-swap's configuration is purposefully simple:

```yaml
models:
  "qwen2.5":
    cmd: |
      /app/llama-server
      -hf bartowski/Qwen2.5-0.5B-Instruct-GGUF:Q4_K_M
      --port ${PORT}

  "smollm2":
    cmd: |
      /app/llama-server
      -hf bartowski/SmolLM2-135M-Instruct-GGUF:Q4_K_M
      --port ${PORT}
```

.. but also supports many advanced features:

- `groups` to run multiple models at once
- `macros` for reusable snippets
- `ttl` to automatically unload models
- `aliases` to use familiar model names (e.g., "gpt-4o-mini")
- `env` variables to pass custom environment to inference servers
- `useModelName` to override model names sent to upstream servers
- `healthCheckTimeout` to control model startup wait times
- `${PORT}` automatic port variables for dynamic port assignment
- `cmdStop` for to gracefully stop Docker/Podman containers

Check the [configuration documentation](https://github.com/mostlygeek/llama-swap/wiki/Configuration) in the wiki for all options.

## Docker Install ([download images](https://github.com/mostlygeek/llama-swap/pkgs/container/llama-swap))

Docker is the quickest way to try out llama-swap:

```shell
# use CPU inference comes with the example config above
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
<summary>Docker images are built nightly for cuda, intel, vulcan, etc ...</summary>

They include:

- `ghcr.io/mostlygeek/llama-swap:cpu`
- `ghcr.io/mostlygeek/llama-swap:cuda`
- `ghcr.io/mostlygeek/llama-swap:intel`
- `ghcr.io/mostlygeek/llama-swap:vulkan`
- ROCm disabled until fixed in llama.cpp container

Specific versions are also available and are tagged with the llama-swap, architecture and llama.cpp versions. For example: `ghcr.io/mostlygeek/llama-swap:v89-cuda-b4716`

Beyond the demo you will likely want to run the containers with your downloaded models and custom configuration.

```shell
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
1. Run the binary with `llama-swap --config path/to/config.yaml`.
   Available flags:
   - `--config`: Path to the configuration file (default: `config.yaml`).
   - `--listen`: Address and port to listen on (default: `:8080`).
   - `--version`: Show version information and exit.
   - `--watch-config`: Automatically reload the configuration file when it changes. This will wait for in-flight requests to complete then stop all running models (default: `false`).

### Building from source

1. Install golang for your system
1. `git clone git@github.com:mostlygeek/llama-swap.git`
1. `make clean all`
1. Binaries will be in `build/` subdirectory

## Monitoring Logs

Open the `http://<host>/logs` with your browser to get a web interface with streaming logs.

Of course, CLI access is also supported:

```shell
# sends up to the last 10KB of logs
curl http://host/logs'

# streams combined logs
curl -Ns 'http://host/logs/stream'

# just llama-swap's logs
curl -Ns 'http://host/logs/stream/proxy'

# just upstream's logs
curl -Ns 'http://host/logs/stream/upstream'

# stream and filter logs with linux pipes
curl -Ns http://host/logs/stream | grep 'eval time'

# skips history and just streams new log entries
curl -Ns 'http://host/logs/stream?no-history'
```

## Do I need to use llama.cpp's server (llama-server)?

Any OpenAI compatible server would work. llama-swap was originally designed for llama-server and it is the best supported.

For Python based inference servers like vllm or tabbyAPI it is recommended to run them via podman or docker. This provides clean environment isolation as well as responding correctly to `SIGTERM` signals to shutdown.

## Star History

[![Star History Chart](https://api.star-history.com/svg?repos=mostlygeek/llama-swap&type=Date)](https://www.star-history.com/#mostlygeek/llama-swap&Date)
