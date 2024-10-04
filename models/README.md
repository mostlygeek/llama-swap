TODO improve these docs

1. Download a llama-server suitable for your architecture
1. Fetch some small models for testing / swapping between
    - `huggingface-cli download bartowski/Qwen2.5-1.5B-Instruct-GGUF --include "Qwen2.5-1.5B-Instruct-Q4_K_M.gguf" --local-dir ./`
    - `huggingface-cli download bartowski/Llama-3.2-1B-Instruct-GGUF --include "Llama-3.2-1B-Instruct-Q4_K_M.gguf" --local-dir ./`
1. Create a new config.yaml file (see `config.example.yaml`) pointing to the models