---
title: Model capabilities and model listings
summary: Advertise images, tools and context length in /v1/models, automatically or by hand.
category: guides
tags: [capabilities, models, tools, vision, context, autodetect, llama-server, vllm, halogen]
config_keys: [models.*.capabilities, models.*.capabilities.disableAuto, store.path]
updated: 2026-09-22
---

# Model capabilities and model listings

`capabilities` describes what a model can accept or produce. llama-swap reports
it through `/v1/models` so clients can pick a compatible model. This is listing
metadata only: it does not change routing or how requests are proxied. In
particular `tools: true` makes the listing report
`capabilities.function_calling: true` and advertise `tools` and `tool_choice`
as supported parameters. It does not enable tool calling by itself; the model
still needs a compatible chat template and runtime.

## Automatic discovery

llama-swap asks the upstream server what it supports the first time a model
becomes ready, and caches the answer. Anything you do not set yourself is
filled in from that answer.

```yaml
models:
  vision-model:
    cmd: llama-server -m vision.gguf --mmproj mmproj.gguf --port ${PORT}
    # no capabilities block needed
```

After this model has been loaded once, `/v1/models` reports its context length
and image input on its own.

The context length reported is the window one request can actually use, not
the one the model was trained for. A llama-server started with a smaller
`--ctx-size` than the model supports advertises the smaller number, and a
server whose slots are capped advertises the per-slot window rather than the
total it loaded.

What each server can report:

| | llama-server | vLLM | halogen |
| --- | --- | --- | --- |
| context length | yes, the loaded `n_ctx` | yes, `max_model_len` | yes |
| image input | yes | no | yes |
| audio or video input | yes | no | no |
| tools | yes, from the chat template | no | yes |
| reranker | no | no | no |

llama-server is read from `/props`, and halogen-flash-server from its
`/health`, which that project documents as the authoritative report of what
the running build accepts. Both vary per deployment: halogen serves images
only when it was started with a vision tower, and that is read rather than
assumed.

vLLM is the thin one. Nothing it serves says whether it was started with
`--enable-auto-tool-choice`, or whether the model takes images, so set those
by hand on vLLM models.

Servers other than these three are left alone. That includes image, speech and
transcription servers, which have no capability surface to read.

## Setting capabilities by hand

Values you write win, field by field, as long as they are not the zero value
for their type. Discovery only fills the gaps, so you can correct one field
without giving up the rest:

```yaml
models:
  vllm-model:
    cmd: vllm serve my-model --port ${PORT} --enable-auto-tool-choice
    capabilities:
      # vLLM cannot report this, so say it here
      tools: true
      # context is still filled in automatically from max_model_len
```

Set only what the model really supports. A false claim makes clients send
requests that fail or get ignored.

## Turning discovery off

A field set to its zero value (`false`, `0`, `[]`) cannot be told apart from
one you never wrote, so `tools: false` will not override a discovered `true`.
To take full control of a model's listing, turn discovery off:

```yaml
models:
  my-model:
    cmd: llama-server -m model.gguf --port ${PORT}
    capabilities:
      disableAuto: true
      in: [text]
      out: [text]
      context: 8192
```

With `disableAuto: true` the model advertises exactly what you wrote, and
nothing else. On its own, with no other fields, it advertises nothing.

## What goes wrong

**A model that has never been loaded shows nothing.** The server has to be
running to be asked. Load the model once and the values appear, including
after it unloads again.

**Discovered values do not survive a restart unless you configure a store.**
The cache lives in llama-swap's SQLite database, which is in memory unless
`store.path` is set:

```yaml
store:
  path: /var/lib/llama-swap/llama-swap.db
```

Without it, every restart starts from an empty cache and each model has to be
loaded again before its capabilities reappear.

**The cache can be stale.** Values are refreshed each time a model starts, so
between loads they describe the last run. Changing a model's `cmd` invalidates
its entry immediately, so shrinking `--ctx-size` will not keep advertising the
old number, but other changes to the same command (a different `--mmproj`, for
example) only take effect the next time the model loads.

**Tool support is read from the chat template.** llama-server reports what the
template can do, not what the model was trained for. A template that renders a
tool list is advertised as supporting tools even if the model is bad at using
them.
