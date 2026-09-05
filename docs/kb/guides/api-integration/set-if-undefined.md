---
title: Set-if-undefined — set a parameter only if the request didn't
summary: Append ? to a setParams or setParamsByID key to apply the value only when the request doesn't carry that parameter.
category: guides
tags: [filters, set-params, set-if-undefined, aliases, sampling]
config_keys: [models.*.filters.setParams, models.*.filters.setParamsByID, peers.*.filters.setParams]
updated: 2026-09-02
---

# Set-if-undefined — set a parameter only if the request didn't

`setParams` and `setParamsByID` normally force their values, overriding
whatever the client sent. Appending `?` to a parameter key makes it
**set-if-undefined**: the value is applied only when the request does not
already carry that parameter.

```yaml
models:
  qwen:
    filters:
      setParamsByID:
        "${MODEL_ID}:extraction":
          temperature: 0.2      # forced — the client cannot change it
          max_tokens?: 32768    # default — a request with max_tokens wins
```

A request to `qwen:extraction` without `max_tokens` gets 32768. A request
that sets `max_tokens: 4096` keeps 4096. `temperature` stays 0.2 either way.

This fills the gap between "the server has no opinion" (parameter absent from
the config) and "the client has no say" (plain `setParams`): the server
supplies a tuned value, the client can still deviate for a single request
without anyone adding a new alias or copying the value into every client.

It works the same everywhere `setParams` works: model filters, per-alias
`setParamsByID` entries, and peer filters.

## Semantics

Filters apply like a pipe — `stripParams | setParams | setParamsByID` — and a
`?` key only fills what is undefined at its own stage:

- **Any value present in the body counts as defined** — including `0`,
  `false`, `""` and `null`. A `?` key only fills a genuinely missing key.
- **Stripped parameters count as undefined.** `stripParams` runs first, so a
  stripped key can be refilled by a later `?` key.
- **A value set by an earlier stage counts as defined.** A `?` key in
  `setParamsByID` is a no-op when `setParams` already set that key — only a
  hard `setParamsByID` key overrides `setParams`.
- **A hard spelling wins over a `?` one.** If the same block contains both
  `max_tokens` and `max_tokens?`, the hard value is used and the `?` one is
  ignored.
- **Keys are paths, as everywhere in filters.** A dotted key like
  `chat_template_kwargs.enable_thinking?` checks and sets the nested
  value; the presence check targets exactly the location the write
  would modify, same as a hard `setParams` key.
- Protected params (`model`) cannot be set, with or without `?`.

## When to use which

| Spelling | Meaning |
| --- | --- |
| key absent from config | client decides, upstream server's own default applies |
| `max_tokens?: 32768` | server suggests — request wins if it carries the key |
| `max_tokens: 32768` | server enforces — request value is overwritten |

## Related

- `guides/api-integration/filters-and-request-rewriting` — the full filter
  pipeline and order of operations
