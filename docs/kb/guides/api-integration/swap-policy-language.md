---
title: Rewriting and rejecting requests with SPL
summary: Write conditional rules in the Swap Policy Language to set defaults, remove parameters, tag requests and deny them before they reach the upstream server.
category: guides
tags: [spl, policy, filters, deny, hooks, request-rewriting]
config_keys: [policies, hooks.on_request, models.*.filters.policy, peers.*.filters.policy]
updated: 2026-09-10
---

# Rewriting and rejecting requests with SPL

The Swap Policy Language ("spell") describes what should happen to a request
when a condition matches. Filters (`stripParams`, `setParams`) always apply;
SPL adds conditions, reusable policies and the ability to reject a request.

A program is a list of clauses, one per line, evaluated top to bottom:

```text
ACTION
  when CONDITION
```

## A working config

```yaml
policies:
  coding-defaults: |
    default temperature to 0.2
    set context.workload to "coding"

  authenticated: |
    deny 401 "API key required"
      when auth.key is missing

hooks:
  on_request: |
    deny 400 "messages is required"
      when messages is missing
    apply authenticated
      when request.model matches /^private-/
    apply coding-defaults
      when request.model matches /coder/

models:
  qwen-coder:
    cmd: llama-server --port ${PORT} -m qwen-coder.gguf
    filters:
      policy: |
        remove reasoning_effort
        default max_tokens to 4096
```

`policies` holds named programs. `hooks.on_request` runs for every JSON
request. A model's or peer's `filters.policy` runs only for that model.

## Actions

| action | effect |
| --- | --- |
| `default <path> to <value>` | set only when the path is missing |
| `set <path> to <value>` | always set, overwriting |
| `remove <path>` | delete the value; a missing path is a no-op |
| `apply <name>` | run a policy from `policies`, then continue |
| `deny "<message>"` | reject with HTTP 403 |
| `deny <status> "<message>"` | reject with a status from 400 to 599 |

Values are `"strings"`, numbers, `true`, `false`, `null`, lists like
`["</s>", "\n"]` and JSON objects like `{"reasoning_effort": "high"}`.

`deny` stops everything: later clauses, the policy that applied it, and any
program that would run after it. The client gets the same error envelope as
other llama-swap errors, with the message in `error.message`.

The request's `model` parameter is protected. A program that sets or removes
it fails to load.

## Conditions

| condition | true when |
| --- | --- |
| `<path> = <value>` | same type and equal; also `!=`, `<`, `<=`, `>`, `>=` |
| `<path> matches /<regex>/` | the value is a string matching a Go regular expression |
| `<path> is present` | the path exists; a JSON `null` counts as present |
| `<path> is missing` | the path does not exist |
| `<path> in [<value>, ...]` | equal to any listed value |

Combine conditions with `and`, `or`, `not` and parentheses. `and` binds
tighter than `or`. A `when` may sit on the action's line or on the next
lines; a line starting with `and` or `or` continues the condition.

A missing path is neither equal to nor ordered against anything, so
`temperature > 1` is false when `temperature` is absent. `!=` is the exact
opposite of `=`, which means `messages[-1].role != "user"` is also true when
there are no messages. Write `x is present and x != "y"` when absence must
not match. Ordering works for numbers and, byte by byte, for strings; mixed
types are never ordered.

## Paths

Body fields are addressed as in JSON, with array indexes in brackets:
`messages[0].role`, `messages[-1].content`, `tools[0].function.name`. A
negative index counts from the end. If the array is shorter than the index,
reads are absent and writes do nothing.

A few prefixes read outside the body:

| path | value |
| --- | --- |
| `auth.key` | the API key sent with the request, missing when there is none |
| `request.model` | the model ID the client asked for, before `useModelName` rewrote the body |
| `request.path`, `request.method` | the HTTP path and method |
| `request.header.<name>` | a request header, missing when empty |
| `context.<key>` | a string bag that `set` can write; it appears in the activity log's metadata |

These are read-only apart from `context`. If a request body has a top-level
field named `context`, `auth`, `request` or `body`, address it as
`body.context` and so on.

## Order of operations

1. `useModelName`, `stripParams`, `setParams` and `setParamsByID` run first
2. `hooks.on_request` runs
3. The model's or peer's `filters.policy` runs
4. The request is proxied

Each stage sees the previous stage's output. A key `stripParams` removed is
missing for a later `default`. Only `application/json` bodies are processed;
multipart uploads and `/upstream/` passthrough requests skip SPL.

Macros expand before a program is parsed, so `${MODEL_ID}` and model-level
macros work inside `filters.policy`. Note that `${...}` inside an SPL `#`
comment is also expanded and must name a real macro.

## What goes wrong

- **`policies.coding-defaults: line 2:7: unexpected "1", expected 'to'`**:
  a syntax error. Line and column count from the start of that program, not
  the YAML file. The same format is used for `hooks.on_request` and
  `model <id>: filters.policy`.
- **`apply references unknown policy "x"`**: `apply` names a key that is not
  under `policies`.
- **`policies: cycle detected: a -> b -> a`**: a policy applies itself
  through a chain. Break the loop; policies cannot recurse.
- **`cannot set protected parameter "model"`**: use `useModelName` or aliases
  to change what the upstream sees as the model.
- **A rule never fires**: check the type. `"0.7"` is a string and `0.7` is a
  number; they are not equal. Use `is present` to test existence rather than
  comparing against `null`.
- **`deny` returns 403 for a validation error**: pass the status you want,
  such as `deny 400 "..."`.

There is no user or claims concept yet; `auth.key` is the only
authentication fact a policy can see.

## Related

- `guides/api-integration/filters-and-request-rewriting` — the always-on filters SPL runs after
- `guides/api-integration/api-keys-and-auth` — where `auth.key` comes from
- `reference/config/policies` — the annotated `policies` block with the full syntax
- `reference/config/hooks` — `hooks.on_request`
