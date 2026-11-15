---
title: Profiles and selectors
summary: Switch model sets at runtime with profiles, or resolve a virtual model ID per request with selectors.
category: guides
tags: [profiles, selectors, aliases, routing, virtual-models, fit, eviction, capacity]
config_keys: [profiles, profiles.*.pins, selectors, selectors.*.strategy, selectors.*.targets, selectors.*.settings.no_evict]
updated: 2026-11-15
---

# Profiles and selectors

Both let a client ask for a stable model name and get a different real model
behind it. They solve different problems.

## Profiles: switch a whole set at once

A profile is a named set of model ID replacements. One profile, or none, is
active at a time, and you switch it from the web UI or the API.

```yaml
profiles:
  coding:
    description: "Coding-focused model routing"
    pins:
      llm-code: "qwen3-coder-30b"
      llm-plan: "gpt-oss-120b"
      image-gen: ~

  writing:
    description: "Long-form writing"
    pins:
      llm-code: "gemma-3-27b"
      llm-plan: "gemma-3-27b"
```

Your editor always requests `llm-code`. Flip the active profile and it gets a
different model, with no client-side change.

- Pin targets may be a local model, a fully qualified peer model, an alias, a
  `setParamsByID` alias, or a selector.
- An empty string or YAML `~` **disables** the pin — requests get a 404 and it
  is left out of model listings. Existing local, alias and peer IDs stay
  reachable.
- Pins are applied first, before aliases, filters and routing.
- Startup and config reload select **no** profile.

Switch at runtime:

```console
$ curl http://localhost:8080/api/profiles
$ curl -X PUT http://localhost:8080/api/profiles/active -d '{"name":"coding"}'
$ curl -X PUT http://localhost:8080/api/profiles/active -d '{"name":null}'
```

## Selectors: resolve per request

A selector is a virtual model ID resolved to a concrete target *on every
request*, based on a strategy.

```yaml
selectors:
  coding-model:
    strategy: warm
    targets: [gpt-oss-120b, qwen3-coder-30b]
    name: "Coding Model"
    description: "Best currently loaded coding model"
```

Four strategies:

**`warm`** — use the first target that is already ready; failing that, one that
is already starting; failing that, cold-start the first. This is the one you
want for "just use whatever is already loaded and don't make me wait".

**`pin`** — always the first target. The remaining entries are reference data
only. Useful as a stable indirection you can edit in one place.

**`spillover`** — fill each target up to a reservation count, then move to the
next. Good for spreading load across local and remote capacity:

```yaml
selectors:
  llama:
    strategy: spillover
    targets: [local-llama, peer-a/llama, peer-b/llama]
    settings:
      spillover: 4     # requests per active target before spilling over
```

**`fit`** — pick the target that is cheapest to run right now, based on what
the capacity router would have to unload. Needs `routing.router.use: capacity`.
Targets must be local models.

```yaml
routing:
  router:
    use: capacity
    settings:
      capacity:
        pools: {vram: 24}

models:
  llama-70b:
    cmd: llama-server -m /models/llama-70b-q4.gguf --port ${PORT}
    capacities: {vram: 20}
    evict_cost: 10
  llama-8b-q8:
    cmd: llama-server -m /models/llama-8b-q8.gguf --port ${PORT}
    capacities: {vram: 9}
  llama-8b-q4:
    cmd: llama-server -m /models/llama-8b-q4.gguf --port ${PORT}
    capacities: {vram: 5}

selectors:
  any-llama:
    strategy: fit
    targets: [llama-70b, llama-8b-q8, llama-8b-q4]   # order = preference
    settings:
      no_evict: false   # true: 429 instead of choosing a target that unloads a model
    name: "Any Llama"
```

For each request the selector asks the router which running models each target
would unload, and picks by, in order:

1. lowest total `evict_cost` of the models unloaded
2. fewest models unloaded
3. ready, then starting, then not loaded
4. earliest in `targets`

A target that is already loaded unloads nothing, so it always wins over one that
would unload something. Between targets that both fit without unloading, a
loaded one wins over one that would have to start.

With `no_evict: true`, targets that would unload anything are skipped, even when
the unloaded models have `evict_cost: 0`. If none are left, the selector answers
**429** and the request never reaches the router. Retry later or add a target
that fits.

What `no_evict` does **not** promise: it controls only which target the selector
picks. If another request starts a swap between the selector's check and this
request reaching the router, the router may still unload a model to serve this
request. It never stops a model while it is serving a request; it waits for it
to finish.

Things that go wrong:

- With any router other than `capacity`, fit selectors are **ignored**: the
  config still loads, llama-swap logs
  `selector: ignoring fit selectors [...]` at startup, the selector is left
  out of `/v1/models`, and requests for it get a 404. Other selectors keep
  working.
- A model with no `capacities` uses nothing, so it always fits. Give every fit
  target its real usage.
- `evict_cost` defaults to 0, so without costs the choice falls to "fewest
  models unloaded", then warmth, then order.
- `settings.no_evict` on any strategy other than `fit` is a config error.

Rules worth knowing:

- Selector IDs cannot collide with model IDs, aliases or fully qualified peer
  model names.
- A selector's targets cannot be other selectors.
- Selectors are **not** supported on `/upstream/<model>` paths.
- Profiles run first, so a profile pin may target a selector.

## Which one?

**Profiles** are a mode switch you make deliberately — "I'm coding now". They
change several mappings at once and persist until you change them again.

**Selectors** are automatic, per request, and based on current state. Nobody
flips them.

They compose: pin `llm-code` in the `coding` profile to a `warm` selector, and
you get "when I'm coding, use whichever coding model is already hot".

## Related

- `reference/config/profiles` and `reference/config/selectors`
- `guides/routing/groups-and-matrix` — which models can be loaded at the same time,
  including the capacity router that `fit` requires
