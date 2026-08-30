---
title: Model capabilities and model listings
summary: Advertise images, tools and other model capabilities so clients can choose compatible models.
category: guides
tags: [capabilities, models, tools, vision]
config_keys: [models.*.capabilities]
updated: 2026-08-28
---

# Model capabilities and model listings

Use `capabilities` to advertise what a model can accept or produce. This is
listing metadata only: llama-swap reports it through `/v1/models`, but it does
not change routing or how requests are proxied. In particular,
`capabilities.tools: true` makes the model listing report
`capabilities.function_calling: true` and advertise `tools` and `tool_choice`
as supported parameters. It does not enable tool calling by itself; the model
still needs a compatible chat template and runtime.

```yaml
models:
  vision-model:
    cmd: llama-server -m vision.gguf --port ${PORT}
    capabilities:
      vision: true
      tools: true
```

Set only capabilities the configured model and command really support. A false
claim makes clients send requests that will fail or get ignored.
