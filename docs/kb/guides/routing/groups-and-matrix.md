---
title: Running several models at once with groups, matrix and capacity
summary: Choosing between group, matrix and capacity routers, and how each decides what gets unloaded.
category: guides
tags: [routing, groups, matrix, capacity, concurrency, swap, vram, memory]
config_keys: [routing, routing.router.use, routing.router.settings.groups, routing.router.settings.matrix, routing.router.settings.capacity, models.*.capacity]
updated: 2026-10-17
---

# Running several models at once: groups, matrix and capacity

Out of the box llama-swap runs one model at a time. The `routing` section
changes that. There are three engines and you pick one:

```yaml
routing:
  router:
    use: group     # or: matrix, capacity
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

When you deploy the head end with the kubeswap Helm chart, the chart's
`config.matrix` values can *generate* this whole section from the model
roster instead of you writing the DSL (see the kubeswap-kubernetes
article's matrix-builder section); hand-written routing and the builder are
mutually exclusive.

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

## `capacity` — a declared capacity budget

Use this when a memory budget describes which models fit, without listing
combinations. The router's total and each model's usage are positive integers
in the same unit; this example uses MiB. The operator measures and declares
usage, including context and backend overhead. llama-swap does not detect
RAM or VRAM use.

```yaml
routing:
  router:
    use: capacity
    settings:
      capacity:
        total: 10000
models:
  a:
    cmd: llama-server -m /models/a.gguf --port ${PORT}
    capacity: {usage: 4000, evictionPriority: 1}
  b:
    cmd: llama-server -m /models/b.gguf --port ${PORT}
    capacity: {usage: 6000, evictionPriority: 10}
  c:
    cmd: llama-server -m /models/c.gguf --port ${PORT}
    capacity: {usage: 4000, evictionPriority: 10}
```

A and B fit exactly. Requesting C evicts B and retains A: B has a higher
`evictionPriority`. A request that already fits causes no eviction. Only the
requested model starts. The planner chooses a complete eviction set by:

1. Fewest models evicted, regardless of priority.
2. Higher eviction priorities: compare each set's priorities sorted from lowest
   to highest, and prefer the larger value at the first difference. This keeps
   lower-numbered models resident whenever possible within the minimum count.
3. Lexicographically smaller sorted evicted model IDs for remaining ties.

Priority is a non-negative integer, default 0. It breaks ties; it is not a
recovery cost and is not summed. A model with priority 100 can remain if evicting
it would require two evictions while a single priority-0 model would suffice.

Every local model must have a positive `capacity.usage` no larger than the
router's `total`, including unlisted models. Missing usage, negative priorities,
nonpositive usage or total, fractions, and overflowing usage totals are
configuration errors. Peer models use no local budget. Remove group and matrix
settings and legacy top-level `groups`/`matrix` when selecting `capacity`; they
cannot be combined with it.

Pending starts count against the budget. Eviction waits for active requests to
finish. TTL and explicit unload release capacity when the process stops;
preload uses the same router and may evict earlier preloads. Profiles and
selectors resolve to the concrete model before accounting. Local spillover
targets must fit together. Reload builds a fresh router using the new budget,
and the old server shuts down through the existing reload lifecycle.
Accounting is per router instance: reload constructs the new server before
stopping the old one, so new requests or preloads can briefly overlap old
processes. Allow headroom when reloading near the physical memory limit.

The exact search operates only on resident models, with exponential worst-case
time. It is intended for a small concurrently resident set, even with many
configured models. This version supports one pool; use matrix for constraints
involving independent GPUs or allowed combinations beyond a single budget.

## Which one?

Use **group** if your setup is describable as "these run together, those swap
out". It is easier to read and easier to get right.

Use **matrix** when the combinations depend on which models are involved — a
70B that needs every GPU alone, versus several small models that fit together,
versus a mid-size LLM plus TTS. Groups cannot express that; matrix can.

Use **capacity** when every combination within a single declared capacity is
valid and maintaining explicit matrix combinations would be tedious.

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
- `guides/operations/kubeswap-kubernetes` — the kubeswap Helm chart, whose
  `config.matrix` builds this section from the model roster
