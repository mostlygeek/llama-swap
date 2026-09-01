---
title: Soft defaults — set a parameter only if the request didn't
summary: Append ? to a setParams or setParamsByID key to apply the value only when the request doesn't carry that parameter.
category: guides
tags: [filters, set-params, soft-defaults, aliases, sampling]
config_keys: [models.*.filters.setParams, models.*.filters.setParamsByID, peers.*.filters.setParams]
updated: 2026-09-01
---

# Soft defaults — set a parameter only if the request didn't

`setParams` and `setParamsByID` normally force their values, overriding
whatever the client sent. Appending `?` to a parameter key turns the value
into a **soft default**: it is applied only when the request does not already
carry that parameter.

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

- **Any value present in the request counts as sent** — including `0`,
  `false`, `""` and `null`. The soft default only fills a genuinely missing
  key.
- **Stripped parameters count as not sent.** `stripParams` runs first, so
  stripping a key and soft-setting it means "replace the client's value with
  the default, but only because the strip removed it" — usually you want
  either strip+set (force) or a soft default alone.
- **A hard spelling wins over a soft one.** If the same block contains both
  `max_tokens` and `max_tokens?`, the hard value is used and the soft one is
  ignored.
- **`setParamsByID` still overrides `setParams`.** A soft `setParamsByID`
  value yields to the client, not to `setParams`: when the client didn't send
  the key, the per-alias soft default wins over a plain `setParams` value.
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
