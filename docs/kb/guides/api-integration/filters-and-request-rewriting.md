---
title: Rewriting requests with filters
summary: Use stripParams, setParams and setParamsByID while preserving the protected model parameter.
category: guides
tags: [filters, strip-params, set-params, soft-defaults, aliases, sampling]
config_keys: [models.*.filters, models.*.filters.stripParams, models.*.filters.setParams, models.*.filters.setParamsByID, peers.*.filters]
updated: 2026-09-01
---

# Rewriting requests with filters

Filters modify the JSON request body before it reaches the upstream server.
They exist on models and on peers, with the same three settings.

The request's `model` parameter is protected: `stripParams` cannot remove it,
and `setParams` cannot override it.

## `stripParams` — remove parameters

A comma-separated list of JSON keys to delete from the request body:

```yaml
models:
  qwen-coder:
    filters:
      stripParams: "temperature, top_p, top_k"
```

Use it when the model has been tuned with specific sampling settings in `cmd`
and you don't want clients overriding them, or when a peer rejects parameters
it doesn't understand.

The `model` parameter can never be stripped. Stick to sampling parameters —
stripping structural fields will break requests.

## `setParams` — force values

```yaml
models:
  qwen-coder:
    filters:
      setParams:
        temperature: 0.7
        top_p: 0.9
```

Runs on every request to the model and overrides whatever the client sent.
Values may be strings, numbers, booleans, arrays or objects. `model` and other
protected params cannot be overridden.

A key ending in `?` is a **soft default** instead: it applies only when the
request does not already carry that parameter, so a client that sends its own
value wins. Works in `setParams` and `setParamsByID` alike — see
`guides/api-integration/soft-defaults`.

```yaml
      setParams:
        temperature: 0.7    # forced
        max_tokens?: 4096   # only if the request didn't set max_tokens
```

For peers this is how you inject provider-specific settings:

```yaml
peers:
  openrouter:
    proxy: https://openrouter.ai/api
    apiKey: ${env.OPENROUTER_API_KEY}
    models: [z-ai/glm-4.7]
    filters:
      setParams:
        provider:
          data_collection: "deny"
          zdr: true
```

## `setParamsByID` — variants without reloading

This is the interesting one. It maps a model ID to a set of parameters, and
**automatically creates a model alias for each key**. One loaded model then
serves several API-visible variants:

```yaml
models:
  gpt-oss-120b:
    filters:
      setParamsByID:
        "${MODEL_ID}":
          chat_template_kwargs: { reasoning_effort: medium }
        "${MODEL_ID}:high":
          chat_template_kwargs: { reasoning_effort: high }
        "${MODEL_ID}:low":
          chat_template_kwargs: { reasoning_effort: low }
```

Clients can now request `gpt-oss-120b`, `gpt-oss-120b:high` or
`gpt-oss-120b:low`. All three hit the same running process — no swap, no
reload — with different parameters applied.

`setParamsByID` runs **after** `setParams`, so it wins on any key they share.

## Order of operations

1. Profile pins resolve the requested ID
2. Aliases resolve
3. `stripParams` removes keys
4. Soft defaults (`key?`) check which parameters the request carries
5. `setParams` applies
6. `setParamsByID` applies, overriding `setParams`
7. The request is proxied

## When not to use filters

If you want a genuinely different configuration — different context size,
different GPU layers — that is a separate model entry, not a filter. Filters
only rewrite the request body; they cannot change how the server was started.

## Related

- `guides/api-integration/soft-defaults` — set a parameter only if the request didn't
- `reference/config/models` — the annotated `filters` block
- `reference/config/peers` — peer filters
