# --- llama-swap-asr.Containerfile ----------------
ARG BASE_TAG=server-cuda-b6097
FROM ghcr.io/ggml-org/llama.cpp:${BASE_TAG}

WORKDIR /app

# 1. copy the binary from the build-context root (same dir as this file)
COPY llama-swap-linux-amd64 /app/llama-swap
RUN chmod +x /app/llama-swap

HEALTHCHECK CMD curl -f http://localhost:8080/health || exit 1
ENTRYPOINT ["/app/llama-swap", "-config", "/app/config.yaml"]
