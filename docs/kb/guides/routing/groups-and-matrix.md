---
title: Running several models at once with groups and matrix
summary: Choosing between the group and matrix routers, and how each decides what gets unloaded.
category: guides
tags: [routing, groups, matrix, concurrency, swap, vram]
config_keys: [routing, routing.router.use, routing.router.settings.groups, routing.router.settings.matrix]
updated: 2026-10-04
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
          v: 3               # protect vLLM beyond its measured load time
          llama-70B: 2

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
3. For each set, sum the eviction cost of running models *not* in that set.
4. Pick the lowest-cost set, ties broken by definition order.
5. Evict the models outside it, start X, forward the request.

Two things worth internalising:

- **Subsets are permitted.** A set `[a, b, c]` also allows `[a, b]`, `[a]` and
  so on. Only the requested model is started; the rest are not preloaded.
- **A model in no set can only run alone.**

A model's eviction cost is `evict_costs` multiplier x its measured median
load time in milliseconds. llama-swap times every load (starting to ready)
and keeps the median of the last few, so models that are painful to reload —
big weights, slow backends — are automatically evicted last with no
configuration. A model never loaded yet is assumed to cost about the fleet
median. Before enough samples exist, a single unusually slow load (say,
while other models loaded at the same time) is shrunk toward the fleet
median rather than defining the model's cost outright.

`evict_costs` (default multiplier 1) is now mostly optional: raise it above 1
to protect a model beyond what its measured reload time implies — licensing
limits, warm caches or prompt caches a stopwatch cannot see.

### Queue-aware reclaim

Measured costs answer "what is this model expensive to reload?" but not
"what will the next request need?" The solver's job is to pick the eviction
for one request, and left alone it evicts the cheapest model overall — which
can be exactly the model the backlog is about to ask for, so queued work
loads, gets evicted for the next request, and loads again.

`settings.matrix.reclaim: queue` changes the objective while the queue is
non-empty: eviction cost is charged only for models the queue references.
An idle model nobody has queued for turns over first (its eviction is free),
a model the queue still needs is protected, and the count stays minimal —
each queued connection meets exactly one evicted model, and because different
queued targets evict different idle models their swaps run in parallel and
the backlog drains one-for-one. With an empty queue the router behaves
equally to the default.

Two picture fixes apply in both reclaim modes, because they are about what
the decision sees rather than what it optimizes:

- **Loading models are not eviction candidates.** A model whose swap is in
  flight is *loading*, not idle, and cancelling that load would only trade
  one wait for another. The scheduler tells the solver which models in-flight
  swaps have claimed as their target, and the solver never evicts one while
  an idle, unclaimed alternative exists — cost cannot rescue a loading
  model. When nothing idle is left (every slot is loading), the solver falls
  back to the plain ranking and the request queues until a slot genuinely
  frees up.
- **Models on the way out are not live.** A model an in-flight swap is
  already evicting holds its GPU until the stop completes, but its slot
  belongs to that swap's target. Counting it as live would inflate the fleet
  by one per in-flight swap, force every decision to evict more models than
  its target needs, and make every queued decision collide with the swap
  already reclaiming one of its candidates. The planner therefore sees the
  steady state the fleet reaches when the in-flight swaps complete: ready
  models plus the in-flight targets, never the dying models.

Together these turn a full-fleet burst from a serial one-model-at-a-time
chain into parallel disjoint swaps with exactly one request queued, and the
queued request takes the first slot that goes idle — the moment a loaded
model finishes its request, it is turned over for the queued target while
the still-loading models are left alone.

```yaml
routing:
  router:
    use: matrix
    settings:
      matrix:
        reclaim: queue   # default: minimal
```

When you deploy the head end with the kubeswap Helm chart, the chart's
`config.matrix` values can *generate* this whole section from the model
roster instead of you writing the DSL (see the kubeswap-kubernetes
article's matrix-builder section); hand-written routing and the builder are
mutually exclusive.

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
- `guides/operations/kubeswap-kubernetes` — the kubeswap Helm chart, whose
  `config.matrix` builds this section from the model roster
