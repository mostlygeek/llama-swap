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

### Use `+auto:orphans` for the models that no set names

A leaf is a model ID or a var in a set expression. The reserved reference
`+auto:orphans` includes each model that is not a leaf in your sets. llama-swap
changes a var to its model ID before this test. A reference such as `+base` is
not a leaf and adds no models.

The name of the set is `auto:orphans`. The first `+` is the usual syntax for a
reference to a set. Only a reference name can contain a colon. A model name or a
var name cannot contain a colon.

llama-swap makes the orphan list one time, when it reads the configuration, and
sorts the list by model ID. The expression is therefore always the same. For
example, a leaf names `a`, but no leaf names `b` or `c`. Then `+auto:orphans`
operates as `(b | c)`:

```yaml
sets:
  always:  "task-small & embed-model"
  main:    "+always & moe-medium & (dense-a | dense-b)"
  scratch: "+always & +auto:orphans"
```

If there are no orphan models, llama-swap removes the `+auto:orphans` term. An
empty term also moves through references. For example, the set `empty` resolves
only to an empty `+auto:orphans`. The set `outer: "+empty & x"` then operates as
`x`. llama-swap can never select a set that contains only an empty
`+auto:orphans`.

If you define a set with the name `auto:orphans`, llama-swap uses your set and
does not make the automatic set.

If a set refers to `+auto:orphans`, llama-swap writes one of these lines to the
log at startup and at each reload of the configuration:

- `matrix: synthesized set "auto:orphans" = (modelA | modelB | ...)`
- `matrix: synthesized set "auto:orphans" is empty; +auto:orphans terms dropped`
- `matrix: set "auto:orphans" is user-defined; orphan synthesis disabled`

If you change `models:`, llama-swap makes a new orphan list at the next reload.
This is the intended behavior.

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
