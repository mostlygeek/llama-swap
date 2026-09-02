---
title: Running several models at once with groups and matrix
summary: Choosing between the group and matrix routers, and how each decides what gets unloaded.
category: guides
tags: [routing, groups, matrix, concurrency, swap, vram]
config_keys: [routing, routing.router.use, routing.router.settings.groups, routing.router.settings.matrix]
updated: 2026-08-25
---

# Running several models at once: groups and matrix

Out of the box llama-swap runs one model at a time. The `routing` section
changes that. There are two engines and you pick one:

```yaml
routing:
  router:
    use: group     # or: matrix
```

## `group` — the default, simpler

You define named groups of models and set two flags per group.

```yaml
routing:
  router:
    use: group
    settings:
      groups:
        # default behaviour: one model at a time, instance-wide
        main:
          swap: true         # only one member runs at a time
          exclusive: true    # running a member unloads every other group
          members: [llama, qwen]

        # these three run together, but any other group evicts them
        small:
          swap: false        # all members can be loaded at once
          exclusive: false   # loading one doesn't unload other groups
          members: [embeddings, reranker, whisper]

        # never unloaded by anything else
        forever:
          persistent: true
          swap: false
          exclusive: false
          members: [always-on-embeddings]
```

The three flags:

- **`swap`** (default `true`) — how members behave among *themselves*. `true`
  means one at a time; `false` means they all coexist.
- **`exclusive`** (default `true`) — how the group affects *other* groups.
  `true` means loading a member unloads everything else.
- **`persistent`** (default `false`) — other groups can never unload this one.

**A model can belong to only one group**, and every member must be a real model
ID.

The classic setup is one exclusive group for the big LLMs and one non-exclusive
group for small always-useful models like embeddings and rerankers.

## `matrix` — more work, far more flexible

The matrix router takes a list of model combinations that are allowed to run
concurrently and solves for the cheapest way to satisfy each request.

```yaml
routing:
  router:
    use: matrix
    settings:
      matrix:
        vars:
          g: gemma-model
          q: qwen-model
          v: voxtral-model

        evict_costs:
          v: 50              # vLLM, slow cold start — avoid evicting
          llama-70B: 30

        sets:
          standard:    "(g | q) & v"
          creative:    "(g | q) & stable-diffusion"
          full:        "llama-70B"
```

`sets` values are expressions:

| operator | meaning |
| --- | --- |
| `&` | AND — these run together |
| `\|` | OR — alternatives |
| `()` | grouping |
| `+name` | inline another set's expression |

`"(g \| q) & v"` expands to `[gemma, voxtral]` and `[qwen, voxtral]`.
Parenthesize mixed expressions so their intended capacity rule is obvious, and
test every requested combination: an expression that excludes a needed set can
make the router evict a model unexpectedly.

How the solver works when a request for model X arrives:

1. If X is already running, forward the request.
2. Otherwise collect every set containing X.
3. For each set, sum the `evict_costs` of running models *not* in that set.
4. Pick the lowest-cost set, ties broken by definition order.
5. Evict the models outside it, start X, forward the request.

Two things worth internalising:

- **Subsets are permitted.** A set `[a, b, c]` also allows `[a, b]`, `[a]` and
  so on. Only the requested model is started; the rest are not preloaded.
- **A model in no set can only run alone.**

`evict_costs` (default 1) is how you express "this one is painful to reload".
Give slow cold-starting backends a high cost.

## Which one?

Use **group** if your setup is describable as "these run together, those swap
out". It is easier to read and easier to get right.

Use **matrix** when the combinations depend on which models are involved — a
70B that needs every GPU alone, versus several small models that fit together,
versus a mid-size LLM plus TTS. Groups cannot express that; matrix can.

## Request ordering

Queued requests are FIFO. You can give some models priority:

```yaml
routing:
  scheduler:
    use: fifo
    settings:
      fifo:
        priority:
          interactive-model: 10
          batch-model: 1
```

Higher numbers are serviced first. Models default to 0.

## Related

- `reference/config/routing` — the full annotated section
- `guides/model-runtime/ttl-and-unloading` — reclaiming VRAM from idle models
