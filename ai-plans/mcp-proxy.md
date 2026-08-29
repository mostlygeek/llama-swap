# MCP proxy: forward design

`/api/mcp` is a stateless MCP 2026-07-28 server exposing llama-swap's own tools. The
intended future is that it also **proxies other 2026-07-28 MCP endpoints**, configured in
`config.yaml`, aggregating many tools behind one endpoint.

The current implementation was shaped for that but deliberately does not build it. This
records what was decided, what was deferred, and the traps found along the way, so none of
it has to be re-derived.

## What is already in place

- `internal/mcptools` — `Provider` interface, `Registry`, and two providers (`docs`,
  `sys`). Adding a proxy provider means implementing `Provider`; nothing in the transport
  changes.
- **Namespacing.** Tool names are `<provider>__<tool>`. Providers deal in local names
  throughout; the registry qualifies on the way out and strips on the way in. A proxy
  provider therefore forwards whatever names an upstream advertises without having to
  know its own prefix.
- **`context.Context`** reaches `Provider.Call`, so an upstream request can honour client
  cancellation.
- **`tools/list` paging** with opaque cursors over a stable sort.
- **Cache hints** (`ttlMs`, `cacheScope`) on `tools/list` and `server/discover`.
- **`Registry.Shutdown`** is called from `Server.Shutdown`.

## Name constraints

Enforced by `mcptools.ValidateName`, and worth restating because the limits are not MCP's:

| | MCP | OpenAI function name | enforced |
| --- | --- | --- | --- |
| charset | `[a-zA-Z0-9_-.]` | `[a-zA-Z0-9_-]` | the intersection |
| length | 128 | 64 | 64 |

Tool names reach models through an OpenAI-shaped `tools` array, so a name outside the
intersection is unusable even though MCP permits it. An upstream advertising
`github.search` must be renamed or dropped, not passed through. `ui/src/lib/agentTools.ts`
drops unusable names defensively and logs them.

The registry also rejects names that do not round-trip through `QualifyName`/`SplitName` —
otherwise a tool could be listed and never callable.

## The `mcp:` config section

`peers` is the template (`internal/config/peer.go`). The checklist is longer than it
looks, and two items are easy to miss:

1. `internal/config/mcp.go` — `MCPDictionaryConfig map[string]MCPConfig`, with a raw
   string field plus a `yaml:"-"` parsed companion (the `Proxy`/`ProxyURL` pattern).
   `UnmarshalYAML` follows the house idiom: `type rawMCPConfig MCPConfig`, a defaults
   value, `unmarshal(&defaults)`, validate, assign back. **Seed `Timeouts` explicitly** or
   every timeout silently becomes 0 — disabled — rather than the schema default.
2. `internal/config/config.go` — add the field to `Config`. That alone gets it macro
   validation, via the reflection over yaml tags in `macros.go:307-349`.
3. `internal/config/load.go` — call `ValidateMCPNamespace(config)` beside the other
   cross-section validators (~line 296). Iterate ids **sorted** so errors are
   deterministic.
4. **`internal/config/merge.go` — add `"mcp": true` to `identityMapPaths`.** Without it,
   duplicate ids across `-config-dir` files silently deep-merge instead of erroring.
5. `config-schema.json` — add `properties.mcp`. Improve on `peers` while you are there:
   use `{"$ref": "#/definitions/timeouts"}` rather than inlining the block, and set
   `additionalProperties: false` so typos are caught.
6. `config.example.yaml` — add the block. Required by `TestConfig_ExampleMatchesSchema`,
   and it is **free documentation**: `internal/reference` auto-publishes every top-level
   key as `reference/config/<key>`, so an `mcp:` block becomes a model-queryable doc with
   no code change, and the knowledge base's `config_keys` validation starts covering it.
7. `llama-swap.go:78` — extend the `-validate` summary line.

Validation is structural and at load time; nothing is probed over the network. An
unreachable peer is not a config error, it is a runtime 502. An MCP upstream should behave
the same unless a startup probe is added deliberately.

## Ownership and reload

A config reload builds an entirely new `*Server` and swaps it in
(`llama-swap.go:271-286`). A `Server`-owned registry is therefore torn down and rebuilt on
**every** config edit, including edits that do not touch `mcp:`.

- For HTTP upstreams that is merely wasteful — connections are re-established lazily.
- For stdio-launched MCP subprocesses it means killing and re-spawning every child on any
  edit, which is not acceptable.

The alternative has precedent: hoist the registry into `main` alongside `store` and
`perfMon`, pass it into `server.New`, and rebuild only when the `mcp:` section actually
changed (the `storeChanged` comparison at `llama-swap.go:255-269` is the pattern; perf's
`UpdateConfig` is the other). **Decide before the first stdio upstream, not after.**

Either way the reload must stay fail-safe: a construction error logs a warning and leaves
the old server serving.

## Error mapping

`Provider.Call` returns an `error` only for protocol-level problems. Anything a model
could react to — including an upstream that timed out or returned a 500 — belongs in
`Result.IsError` with an explanation. Getting this backwards turns a recoverable hiccup
into a dead conversation, and is the single most important convention for a proxy
provider to follow.

## Tool discovery at scale

MCP has no tool-search method, so discovery has to be built. The agreed shape:

- **`mcp__find_tools(query)`** — a registry-level meta-tool searching names, descriptions
  and `Tool.Tags`. The `mcp__` prefix is reserved for registry-level tools that belong to
  no provider. `Tool.Tags` exists today and is populated but unused, waiting for this.
- **Paging** — already implemented.
- The same problem shows up inside a tool: `docs__get_doc`'s `id` enum is ~1KB at 39
  documents and does not scale past a few hundred. Both are the same context-budget
  problem.

## Notifications

The endpoint is POST-only and stateless, so it can never send
`notifications/tools/list_changed`. That is a deliberate trade: any instance can serve any
request behind a plain load balancer, which is the reason 2026-07-28 exists. `ttlMs` is
the substitute. Revisit only if push turns out to matter more than statelessness.

## Deferred, smaller

- **`TimeoutsConfig` → `http.Transport` is already copy-pasted twice** (`peer.go:62-75`,
  `process_command.go:460-473`). A proxy would be the third; that is the moment to extract
  a helper.
- **`/api/mcp` is on `apiChain` (auth only)**, so it gets no inflight or metrics
  middleware. Proxied tool calls will not appear in Activity unless wired explicitly.
- **Capability aggregation.** `mcpDiscover` computes `capabilities` from the registry, but
  only for tools. Unioning an upstream's `resources`/`prompts` is a change to that one
  function.
- **`resources/*` and `prompts/*` passthrough**, and `Mcp-Param-*` header passthrough.
- **`sys__performance` and `sys__hardware`.** `perf.Monitor` and `hw.HardwareSnapshot` are
  already fields on `*Server`, and `SysProvider` is constructed in `server.New` where both
  are in scope. Before adding them, decide an exposure policy: the time is uninteresting
  to leak, but GPU model, VRAM, CPU count and hostname are mild fingerprinting, and
  `/api/mcp` is only as protected as `apiKeys` makes it.

## Verifying a proxy implementation

The check that caught the design error last time was running the **official MCP Go SDK as
a real client** against the endpoint — it revealed that a conforming 2026-07-28 client
opens with `server/discover`, not `tools/list`, so a server without it cannot be connected
to at all. Keep doing that: `github.com/modelcontextprotocol/go-sdk`, a throwaway client
outside this repo so it adds no dependency, calling `ListTools` and `CallTool`.
