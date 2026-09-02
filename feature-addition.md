Problem:

llama-swap shows unknown matrics in Prompt speed and gen speed when vllm is used.


LLM Diagnostics:

⏺ Diagnosed. Here's what's actually happening:

  Root cause: llama-swap surfaces prompt_per_second / tokens_per_second by parsing a "timings" object out of the upstream response body (see proxy/metrics_monitor.go). Two compounding gaps
   mean that object never appears for the traffic you're sending:

  1. vLLM (qwen3.6-27b, phi-4, phi-4-reasoning-plus) never embeds a timings object in response bodies on either /v1/chat/completions or /v1/messages. It only exposes per-request timing via
   Prometheus /metrics (vllm:time_to_first_token_seconds, vllm:prompt_tokens_total, etc.). llama-swap doesn't scrape upstream /metrics, so vLLM rates always come back -1.

  2. llama-server (qwen3.6-35b-a3b, glm-4.7-flash, gemma4-*, sarvam-30b) does emit a timings block — but only on /v1/chat/completions. I verified directly: hitting /v1/messages against
  llama-server returns Anthropic-format JSON with usage only, no timings. Claude Code talks exclusively to /v1/messages, so the llama.cpp models look just as broken in the dashboard.

  I confirmed with /api/metrics on bigdumbo: 303 real generations all show prompt_per_second: -1, tokens_per_second: -1. The only entries with non--1 values are 18
  /v1/messages/count_tokens calls — those are zero-token introspection calls with no real timing.

  Why cache_tokens differs (0 vs −1): llama.cpp returns usage.cache_read_input_tokens; vLLM doesn't. PR #419 (merged Nov 2025, present in v217) wired up the usage/cache_read_input_tokens
  mapping for /v1/messages, but it didn't address timings because neither upstream emits them on that route.

  No clean local fix. Three upstream paths exist; none are patches we can make in this repo:
  - llama-swap learns to scrape upstream Prometheus /metrics and stitch timings into the request record (closest match: PR #509, which exports llama-swap's own metrics — still doesn't pull
   from upstream /metrics).
  - llama.cpp's /v1/messages handler starts including a timings block (it has the data — it just doesn't serialize it on the Anthropic-format path).
  - Compute approximations locally from duration_ms + input_tokens + output_tokens (already captured) — would need a llama-swap-side change.

  Want me to file an issue against mostlygeek/llama-swap describing this, or just log the finding under logs/bigdumbo/ and leave it?

  Sources:
  - PR #419 — proxy: extract metrics for v1/messages
  - PR #509 — expose metrics via endpoint for Grafana
  - PR #287 — Support llama.cpp's cache_n in timings info