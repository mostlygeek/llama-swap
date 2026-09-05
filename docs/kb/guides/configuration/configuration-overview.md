---
title: Configuration overview
summary: Start with one small config.yaml, get the Docs agent running, then grow into config.example.yaml as you need more.
category: guides
tags: [getting-started, configuration, minimal, docs-agent, cmd]
config_keys: [models.*.cmd, models.*.capabilities]
updated: 2026-09-05
---

# Configuration overview

llama-swap's whole setup lives in one YAML file, passed with `-config` when
you start the binary. Models, routing, profiles, peers — everything is one
file you edit incrementally, not separate settings scattered across several
places. (`-config-dir` can split it across multiple files later, but you
don't need that yet.)

## Start smaller than you think

Nothing requires a full configuration before llama-swap will run. The most
useful first step is getting one model running well enough to ask the Help
page's Docs agent about everything else, instead of reading it all here.

`cmd` is the only required setting on a model — see
`guides/model-runtime/writing-cmd` for the full picture. Here's a minimal
config for gemma-4-12B, the model the Docs agent is evaluated against and a
good default because it's small, capable, and runs without a GPU:

```yaml
models:
  gemma-4-12B:
    cmd: |
      llama-server --port ${PORT}
      --model /path/to/gemma-4-12b-it-Q4_K_M.gguf
      --jinja
    capabilities:
      tools: true
```

`--jinja` is required for tool calling — without it llama-server silently
ignores the request's `tools` array and the Docs agent can't call anything.
`capabilities.tools: true` only advertises that support in `/v1/models`; it
doesn't enable it by itself. See
`guides/model-runtime/capabilities-and-model-listings` for more on that
distinction.

Start llama-swap and open http://localhost:8080:

```bash
llama-swap -config config.yaml -listen localhost:8080
```

`-validate` checks a config file and exits without starting anything, which
is useful while you're still getting the YAML right:

```bash
llama-swap -config config.yaml -validate
```

## Ask, don't grep

Once a model is running, open **Help** in the sidebar, pick a tool-capable
model, and ask it directly — how do I add a second model, what does `ttl`
do, why won't my model load. It reads the same knowledge base and
`config.example.yaml` that back these articles, so its answers point at the
same real settings instead of guessing.

## When you need everything

[`config.example.yaml`](../../../config.example.yaml) documents every setting
llama-swap has, each with its own comment. It's long and covers advanced
cases most configs never touch — treat it as a reference to search, not
something to read top to bottom.

## Related

- `guides/model-runtime/writing-cmd` — the full `cmd` and `proxy` picture
- `guides/model-runtime/capabilities-and-model-listings` — what `capabilities` does and doesn't do
- `guides/model-runtime/troubleshooting-model-wont-load` — when a model won't start
- `reference/config/*` — every setting in `config.example.yaml`, searchable by section
