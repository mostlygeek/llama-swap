---
title: Route to remote peers
summary: Expose models from another llama-swap host or API provider, listed by hand or discovered from the peer's model list.
category: guides
tags: [peers, remote, networking, discovery, catalog]
config_keys: [peers, peers.*.proxy, peers.*.apiKey, peers.*.models, peers.*.catalog, peers.*.catalog.path, peers.*.catalog.refresh, peers.*.catalog.limit, peers.*.catalog.transitive]
updated: 2026-11-21
---

# Route to remote peers

Declare a peer with the other llama-swap `proxy` URL and its model list. Its models are addressed as
`peer-name/model-id`, so a peer named `sippy` serving `gemma-4-12B` appears as
`sippy/gemma-4-12B`.

```yaml
peers:
  sippy:
    proxy: http://sippy:8080
    apiKey: ${env.SIPPY_API_KEY}
    models: [gemma-4-12B]
```

The peer must be reachable from this host and its API key must match the remote
server. See `guides/api-integration/api-keys-and-auth` for keeping it out of
the committed file.

## Discover models automatically

Instead of listing every model, let llama-swap read the peer's model list
(`GET /v1/models`) and pick models with `add <regex>` and `remove <regex>`
rules, written as plain strings in `models`:

```yaml
peers:
  kitchen:
    proxy: http://kitchen:8080
    catalog:
      refresh: 120   # seconds between fetches (default 120, minimum 10)
      limit: 512     # most models kept from this peer (default 512)
    models:
      - gemma-4-12B      # plain ID: always routable
      - add qwen         # add catalog models whose ID contains "qwen"
      - remove embed     # then remove the ones containing "embed"
```

- Rules run top to bottom like `grep qwen | grep -v embed`. A `remove` only
  drops models added by earlier `add` rules, so a list cannot start with
  one. Regexes match anywhere in the ID; use `^...$` for an exact match.
- The keyword and regex are separated by whitespace. Quote the entry if the
  regex contains `: ` or starts with a YAML symbol such as `[` or `*`, for
  example `- "add ^qwen: "`. A rule with no regex is an error.
- Plain model IDs are never removed and route even when the peer is down.
- Omitting `models` discovers everything the peer lists, like
  `add .*`. Any `add` or `remove` rule, or a `catalog` block, turns
  discovery on. A peer with only
  plain IDs and no `catalog` is never fetched.
- Discovered models appear in `/v1/models` with the peer's name, description,
  context length and capabilities, and update without a restart.
- Peers that are themselves llama-swap list their own peers' models and
  aliases. Those are skipped unless `catalog.transitive: true`, so two hosts
  that peer with each other do not loop.

## What goes wrong

- **A model is missing.** Check `GET /api/peers`: it shows when each catalog
  was fetched, the last error, how many models were listed and kept, and any
  names dropped by `catalog.limit` or collisions. To fetch a catalog now
  instead of waiting for `catalog.refresh`, use the refresh button beside the
  peer on the Models page, or `POST /api/peers/<peer>/refresh`. It fetches
  at most once every 5 seconds per peer; sooner requests get the last
  result. It does nothing for a peer without discovery.
- **Name collisions.** A discovered `peer/model` that matches a local model,
  alias, selector, profile pin or another peer's model is skipped with a
  warning. Local names always win.
- **Peer offline.** The last good list is kept and cached in the store, so
  discovered models still route after a restart; requests fail only when they
  reach the peer.
- **Selectors and profile pins** can name `kitchen/some-model` before it has
  been discovered. If the model never shows up, the request fails like any
  unknown model.
