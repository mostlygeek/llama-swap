---
title: Connecting an MCP client to llama-swap
summary: Point any MCP client at /api/mcp to search llama-swap's documentation and source code.
category: guides
tags: [mcp, tools, agent, api, integration, source, code, help, disable]
config_keys: [apiKeys, helpagent.disable]
updated: 2026-10-23
---

# Connecting an MCP client to llama-swap

llama-swap serves its own documentation as an MCP server at `/api/mcp`. Any
MCP client can point at a running instance and ask about llama-swap
configuration — the same tools the web UI's Help page uses.

```
http://localhost:8080/api/mcp
```

## Requirements

The endpoint speaks **MCP revision 2026-07-28 and nothing else.** That is the
revision which removed the `initialize`/`initialized` handshake and the
`Mcp-Session-Id` header, which is what lets llama-swap serve MCP from a single
stateless POST route with no session bookkeeping.

A client pinned to an older revision gets a clear JSON-RPC error rather than
mysterious behaviour:

```json
{"jsonrpc":"2.0","id":0,"error":{
  "code":-32022,
  "message":"unsupported protocol version; this server speaks MCP 2026-07-28 only",
  "data":{"supported":["2026-07-28"],"requested":"2025-06-18"}}}
```

If you see that, the client needs updating; there is nothing to configure on
the llama-swap side.

## Authentication

If your config sets `apiKeys`, the MCP endpoint requires one like every other
llama-swap API. Present it as a bearer token:

```
Authorization: Bearer sk-your-key
```

`x-api-key` and HTTP Basic work too. With no `apiKeys` configured the endpoint
is open, so treat it the same way you treat the rest of llama-swap — see
`guides/api-integration/api-keys-and-auth`.

## Turning it off

`helpagent.disable` turns off both the UI's Help page and this endpoint:

```yaml
helpagent:
  disable: true
```

The Help item leaves the sidebar, `/help` in the UI goes to the home page, and
`/api/mcp` answers `404`, so no MCP client can use these tools. It takes effect
on a config reload. The default is `false`.

Turn it off when nobody uses Help, or when this instance should not offer its
configuration and source code tools at all. Protecting the endpoint with
`apiKeys` is the alternative when Help should stay.

## The tools

Tool names are namespaced `<provider>__<tool>`. The prefix says where a tool comes
from, which keeps names unique once llama-swap starts proxying other MCP servers.

**`docs__*` — llama-swap's own documentation**

| tool | what it does |
| --- | --- |
| `docs__list_docs` | every document's id, title and summary; filterable by category or tag |
| `docs__get_doc` | one document by id, including sections of `config.example.yaml` verbatim |
| `docs__search_docs` | keyword search across all of it, returning snippets with ids |
| `docs__get_config_schema` | the JSON schema for a config key: type, default, allowed values, child keys |

**`config__*` — the running configuration**

| tool | what it does |
| --- | --- |
| `config__get_config` | the configuration this instance is running right now, as YAML, with credentials redacted; pass a jq `query` to select part of it |

`config__get_config` returns the *effective* config: defaults filled in, `${env.*}`
and macro references already expanded. `apiKeys`, `peers.<id>.apiKey`, secret-looking
`cmd`/`env` arguments, and any recognizable credential token are replaced with
`[REDACTED]` before the result leaves the server. It is what lets an agent give advice
about the setup actually in front of it.

The `query` argument is a [jq](https://jqlang.org/manual/) expression, evaluated by
[gojq](https://github.com/itchyny/gojq), so a client asks for exactly what it needs
instead of reading the whole document and throwing most of it away:

| query | what comes back |
| --- | --- |
| omitted, or `.` | the whole config |
| `.models \| keys` | the configured model ids |
| `.models.qwen3` | one model |
| `.models["qwen3-8b"].ttl` | one field of a model whose id jq will not take bare |
| `.models \| to_entries \| map({id: .key, ttl: .value.ttl})` | one field from every model |
| `.models \| to_entries \| map(select(.value.ttl == null)) \| map(.key)` | the models with no `ttl` set |

Results are YAML. A query producing more than one result returns them as several
YAML documents separated by `---`. `null` means nothing is set at that key: either
it does not exist, or its value is the default, since defaults are omitted. `env`
and `$ENV` are not available: the process environment holds exactly the credentials
that redaction keeps out of the result.

A query is bounded three ways, and each refusal explains itself so a client can
retry with something narrower: evaluation is capped at five seconds, a result
larger than 32KB is refused rather than returned truncated, and a query that
builds a value too large to hold — `[range(0; 1000000000)]` and its relatives —
is stopped while it is still building it.

**`sys__*` — facts about the machine**

| tool | what it does |
| --- | --- |
| `sys__now` | current date and time, in UTC, server local time, and an optional named timezone |

**`code__*` — llama-swap's source code**

| tool | what it does |
| --- | --- |
| `code__grep` | regex search over the source, ripgrep style, returning `path:line: text` matches; narrow with a directory or file `path` such as `internal/config` and a `glob` such as `*.go` |
| `code__read` | one file, lines numbered `L12:`, with a GitHub link to the same lines; `offset` and `limit` page through it |
| `code__find` | file paths matching a glob such as `**/*_test.go` |
| `code__ls` | one directory's entries, directories marked with a trailing `/` |
| `code__status` | whether the source is downloaded, its commit, and whether it is out of date against the repository and against this build |
| `code__update` | download the source, or download it again when it is out of date; `force` downloads even when it looks current |

These let an agent check the documentation against the code that implements it.
The documentation is in the source too: document id
`guides/model-runtime/ttl-and-unloading` is the file
`docs/kb/guides/model-runtime/ttl-and-unloading.md`.

The source is a shallow git checkout of the release this binary was built from
(`v266` for llama-swap v266; `main` for a development build). It is fetched from
github.com by `code__update` or the first grep, read, find or ls call, not at
startup, and kept under the user cache directory (`~/.cache/llama-swap/source` on
Linux) so a restart reuses it. A checkout on disk is never refreshed on its own:
`code__status` asks the repository which commit the ref is at now, and
`code__update` downloads it again when that has moved. A release tag does not
move, so this matters for `main`.

The `code__*` tools need the `git` command line tool on the machine running
llama-swap, on its `PATH`. That includes inside a container: install git in the
image if you want these tools there.

What goes wrong:

- **git is not installed.** Every `code__*` tool says so and tells the agent to
  ask you to install git. `code__status` reports `git: not installed`.
- **No network access.** The call returns `isError` saying the source is not
  available, and retries at most once a minute; `code__update` retries at once.
  The other tools keep working, including an existing checkout.
- **`code__status` cannot reach the repository.** The checkout stays usable;
  `code__update` with `force` skips the check and clones again.
- **The first call says "still downloading".** The fetch took longer than 30
  seconds. It carries on in the background; call again.

Every tool except `code__update` is annotated `readOnlyHint`, so clients that ask
before running tools can safely auto-approve them. `code__update` replaces the
cached checkout on the server, and changes nothing else. `sys__now` and
`code__status` are not `idempotentHint` — their answers are supposed to change
between calls.

A good first request is `docs__list_docs` — it is the map of everything available.

## Listing and caching

`tools/list` is paginated. When the response carries a `nextCursor`, pass it back as
`params.cursor` to get the next page; an absent `nextCursor` means you have everything.
With only a handful of tools today one page holds them all, but a client should follow
the cursor rather than assume that.

The response also carries `ttlMs` and `cacheScope`. Because the endpoint is stateless it
can never send `notifications/tools/list_changed`, so `ttlMs` is how a client knows how
often to re-list rather than being told when something changed.

## Calling it by hand

Three headers are required from 2026-07-28 onward, so proxies can route without
parsing the body: `Mcp-Protocol-Version`, `Mcp-Method` (must match the body's
`method`), and `Mcp-Name` (must match `params.name`, on `tools/call` only).

```console
$ curl -s localhost:8080/api/mcp \
    -H 'Content-Type: application/json' \
    -H 'Mcp-Protocol-Version: 2026-07-28' \
    -H 'Mcp-Method: tools/list' \
    -d '{"jsonrpc":"2.0","id":1,"method":"tools/list"}'
```

```console
$ curl -s localhost:8080/api/mcp \
    -H 'Content-Type: application/json' \
    -H 'Mcp-Protocol-Version: 2026-07-28' \
    -H 'Mcp-Method: tools/call' \
    -H 'Mcp-Name: docs__search_docs' \
    -d '{"jsonrpc":"2.0","id":2,"method":"tools/call",
         "params":{"name":"docs__search_docs","arguments":{"query":"ttl unload"}}}'
```

## What to expect back

A tool that ran but could not answer — an unknown document id, a search with no
matches — returns a **normal result** with `isError: true` and text explaining
the problem, so a model can read the correction and try again:

```json
{"content":[{"type":"text","text":"Error: no document with id \"guides/model-runtime/ttl\". Did you mean one of: guides/model-runtime/ttl-and-unloading?"}],
 "isError":true}
```

Only protocol problems — an unknown method, a header mismatch, a malformed body
— come back as JSON-RPC errors.

## Notes

- `GET` and `DELETE` return `405`. The endpoint is POST only; it never streams,
  and there is no session to delete.
- JSON-RPC batching is not supported. MCP removed it in revision 2025-06-18.
- Results are size-capped so they fit a local model's context window.
  `docs__get_doc` says so when it truncates and tells you the `offset` to continue
  from; `config__get_config` never truncates, it asks you for a narrower `query`.
  A cut-off `code__*` result ends with a bracketed notice saying what was left out
  and how to get it, such as `[Showing lines 1-200 of 387. Use offset=201 to continue.]`.

## Related

- `guides/api-integration/api-keys-and-auth` — protecting the endpoint
