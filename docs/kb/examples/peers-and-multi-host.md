---
title: Peers - splitting models across machines and providers
summary: Route to another llama-swap, a remote OpenAI-compatible server, or a hosted provider under one endpoint.
category: examples
tags: [peers, remote, multi-host, openrouter, spillover]
config_keys: [peers, peers.*.proxy, peers.*.models, peers.*.apiKey, peers.*.filters, peers.*.timeouts]
updated: 2026-08-25
---

# Peers: splitting models across machines and providers

Peers let one llama-swap instance serve models that live somewhere else — a
second box on your LAN, or a hosted API. Clients see one endpoint and one model
list.

```yaml
peers:
  # another llama-swap on the LAN
  workstation:
    proxy: http://192.168.1.23
    models:
      - qwen3-coder-30b
      - gemma-3-27b

  # a hosted provider
  openrouter:
    proxy: https://openrouter.ai/api
    apiKey: ${env.OPENROUTER_API_KEY}
    models:
      - z-ai/glm-4.7
      - moonshotai/kimi-k2-0905
      - deepseek/deepseek-v3.2
```

A peer can be another llama-swap or **any** server providing the `/v1/`
endpoints llama-swap supports. The request path is appended to `proxy`, so
`proxy: https://openrouter.ai/api` plus `/v1/chat/completions` gives
`https://openrouter.ai/api/v1/chat/completions`.

## Naming

Peer models are **always** addressable as `<peerID>/<modelID>`:

```console
$ curl http://localhost:8080/v1/chat/completions \
    -d '{"model":"workstation/qwen3-coder-30b", ...}'
```

An unqualified ID works too, but only when exactly one peer provides it. Local
model IDs and aliases always take precedence over unqualified peer IDs. When
you have two peers serving the same model name, qualify it.

## API keys

`apiKey` is injected into outgoing requests as both
`Authorization: Bearer <key>` and `x-api-key: <key>`. Blank means no key added.
Use an env macro — see `guides/api-integration/api-keys-and-auth`.

This is the key llama-swap presents *to* the peer, unrelated to the top-level
`apiKeys` clients present to llama-swap.

## Timeouts

Remote peers need more headroom than local processes, especially over a slow
link or to a provider that queues:

```yaml
peers:
  workstation:
    proxy: http://192.168.1.23
    models: [qwen3-coder-30b]
    timeouts:
      connect: 30
      keepalive: 30
      responseHeader: 120
      tlsHandshake: 10
      idleConn: 90
```

All values are seconds. `0` disables that timeout, which is generally a bad
idea. `responseHeader` is the one to raise for a peer that has to cold-start a
model before it responds.

## Filters on peers

Same `stripParams` / `setParams` as models — useful for removing parameters a
provider rejects, or injecting provider-specific policy:

```yaml
peers:
  openrouter:
    proxy: https://openrouter.ai/api
    apiKey: ${env.OPENROUTER_API_KEY}
    models: [z-ai/glm-4.7]
    filters:
      stripParams: "top_k"
      setParams:
        provider:
          data_collection: "deny"
          zdr: true
```

See `guides/api-integration/filters-and-request-rewriting`.

## Local first, remote as overflow

Combine peers with a `spillover` selector to use local capacity until it is
busy, then fall back to remote:

```yaml
selectors:
  llama:
    strategy: spillover
    targets:
      - local-llama                  # local, free, limited
      - workstation/qwen3-coder-30b  # other box
      - openrouter/z-ai/glm-4.7      # paid, unlimited
    settings:
      spillover: 4                   # requests per target before spilling over
```

Clients request `llama` and get the cheapest target with capacity. See
`guides/routing/profiles-and-selectors`.

## Caveats

- Fully qualified peer names are reserved — they are the IDs `/v1/models`
  reports.
- Selectors are not supported on `/upstream/<model>` paths, so peer models are
  not reachable through the upstream passthrough.
- llama-swap does not manage the peer's lifecycle. It cannot start, stop or
  TTL-unload anything on the other side.

## Related

- `reference/config/peers` — the full annotated section
