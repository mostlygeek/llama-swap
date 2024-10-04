# LLaMAGate

A golang gateway that automatically manages [llama-server](https://github.com/ggerganov/llama.cpp/tree/master/examples/server) to serve the requested `model` in the HTTP request. Serve all the models you have downloaded without manually swapping between them.

Created because I wanted:

- ✅ easy to deploy: single binary with no dependencies
- ✅ full control over llama-server's startup settings
- ✅ ❤️ for Nvidia P40 users who are rely on llama.cpp row split mode for large models

## YAML Configuration

```yaml
# Seconds to wait for llama.cpp to be available to serve requests
# Default (and minimum): 15 seconds
healthCheckTimeout: 60

# define models
models:
  "llama":
    cmd: "llama-server --port 8999 -m Llama-3.2-1B-Instruct-Q4_K_M.gguf"

    # address where llama-ser
    proxy: "http://127.0.0.1:8999"

    # list of aliases this llama.cpp instance can also serve
    aliases:
    - "gpt-4o-mini"
    - "gpt-3.5-turbo"

  "qwen":
    cmd: "llama-server --port 8999 -m path/to/Qwen2.5-1.5B-Instruct-Q4_K_M.gguf"
    proxy: "http://127.0.0.1:8999"
    aliases:
```

## Testing with CURL

```bash
> curl http://localhost:8080/v1/chat/completions -N -d '{"messages":[{"role":"user","content":"write a 3 word story"}], "model":"llama"}'| jq -c '.choices[].message.content'

# will reuse the llama-server instance
> curl http://localhost:8080/v1/chat/completions -N -d '{"messages":[{"role":"user","content":"write a 3 word story"}], "model":"gpt-4o-mini"}'| jq -c '.choices[].message.content'

# swap to Qwen2.5-1.5B-Instruct-Q4_K_M.gguf
> curl http://localhost:8080/v1/chat/completions -N -d '{"messages":[{"role":"user","content":"write a 3 word story"}], "model":"qwen"}'| jq -c '.choices[].message.content'
```