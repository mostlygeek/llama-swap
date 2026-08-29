---
title: Reusing configuration with macros
summary: Global and per-model macros, environment variable macros, and the ordering rules that trip people up.
category: guides
tags: [macros, env, dry, substitution]
config_keys: [macros, models.*.macros]
updated: 2026-08-25
---

# Reusing configuration with macros

Macros are string substitutions that keep long `cmd` lines from repeating
themselves.

```yaml
macros:
  "latest-llama": >
    /path/to/llama-server --port ${PORT}
  "default_ctx": 4096
  "default_args": "--ctx-size ${default_ctx}"

models:
  model1:
    cmd: |
      ${latest-llama}
      -m /models/a.gguf
      ${default_args}
  model2:
    cmd: |
      ${latest-llama}
      -m /models/b.gguf
      ${default_args}
```

## Two scopes

**Global** macros, under the top-level `macros:` key, can be used in any
configuration value.

**Model** macros, under a model's own `macros:` key, are scoped to that model
and **override** a global macro of the same name:

```yaml
macros:
  default_ctx: 4096

models:
  big-context:
    macros:
      default_ctx: 32768      # wins for this model only
    cmd: llama-server --port ${PORT} -m /models/a.gguf --ctx-size ${default_ctx}
```

## The rules that bite

- **Names**: under 64 characters, matching `^[a-zA-Z0-9_-]+$`. Not `PID`,
  `PORT` or `MODEL_ID` — those are reserved.
- **Values** may be strings, numbers or booleans.
- **Macros may reference other macros, but only ones defined before them.** A
  macro referring to a later definition does not resolve. This is the most
  common macro bug — reorder the definitions.

## Types are preserved

A value that is *exactly* one macro keeps its type. A macro interpolated into a
larger string becomes a string:

```yaml
macros:
  temp: 0.7

models:
  m1:
    metadata:
      temperature: ${temp}                    # the number 0.7
      note: "running at temp=${temp}"         # the string "running at temp=0.7"
```

This matters for `metadata` and `filters.setParams`, where a stringified number
may not be what the upstream expects.

## Environment variable macros

`${env.VAR_NAME}` reads from the process environment:

```yaml
macros:
  model_dir: "${env.MODEL_DIR}"

apiKeys:
  - "${env.LLAMA_SWAP_KEY}"
```

Two things to know:

- Env macros are substituted **first**, before regular macros.
- **If the variable is not set, config loading fails with an error.** That is
  deliberate — it fails loudly at startup rather than silently starting with an
  empty API key.

This is the recommended way to keep secrets out of a config file you want to
commit. See `guides/api-integration/api-keys-and-auth`.

## Macros work almost everywhere

Not just `cmd` — `cmdStop`, `proxy`, `checkEndpoint`, `metadata`, `filters`,
`apiKeys` and peer settings all accept macros. Inside `metadata` they even
substitute into nested lists and objects.

## Related

- `reference/config/macros` — the annotated reference
- `guides/model-runtime/writing-cmd` — where macros usually go
