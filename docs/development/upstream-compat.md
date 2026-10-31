# Adding upstream compatibility

How llama-swap identifies the server behind a model or peer, discovers its
capabilities, and reads token usage from its responses. Use this when adding
support for a new inference server or fixing how an existing one is read.

Three packages are involved:

| Piece | Package | Job |
| --- | --- | --- |
| hints | `internal/hint` | Shared names: `Upstream`, `Task`, per-request `Request` |
| capcompat | `internal/capcompat` | Detect the upstream from `/v1/models`, probe capabilities |
| usage extraction | `internal/server` (`usage*.go`) | Turn a response body into a normalized `Usage` |

## Request flow

1. A local model becomes ready. `ProcessCommand.detectUpstream`
   (`internal/process/process_command.go`) runs `capcompat.Detect` against the
   model's `proxy` URL, stores the `Info`, and emits a
   `capcompat.DetectedEvent`. The server caches it via `capcompat.Service`.
2. A request arrives. The metrics middleware creates a `hint.Request` with
   `hint.New` and takes the `Task` from `meteredRoutes`
   (`internal/server/server.go`).
3. The router fills the hint through `hint.From`: `internal/router/base.go`
   sets `Route` local and `Upstream` from the process's detected `Info`;
   `internal/router/peer.go` sets `Route`, `PeerID` and the `Upstream`
   guessed from the peer's proxy host with `hint.ForURL`.
4. After the handler returns, `metricsMonitor.record`
   (`internal/server/metrics.go`) calls `extractUsage` with the hinted
   upstream, which picks an extractor with `extractorFor`.

## Hints (`internal/hint`)

- `Upstream` constants live in `upstream.go`. Add new ones there and to
  `Known()`.
- Remote providers are matched by host in `hostUpstreams` (`ForHost`).
- `Task` (`task.go`) is the kind of inference, derived from the endpoint
  path. New metered endpoints go in `meteredRoutes`.
- The package imports nothing from llama-swap. Keep it to types and small
  helpers. Only the request goroutine writes a `Request`, before the handler
  returns, so no lock is needed.
- A missing or `Unknown` hint is valid. Consumers fall back to a best guess.

## capcompat (`internal/capcompat`)

`Detect` fetches `/v1/models` once and asks each `Prober` in
`defaultProbers()` (order matters) whether it `Matches`. The first match runs
`Probe` and names the family with `Upstream`.

| Prober | Matches | Reads |
| --- | --- | --- |
| `llamaServerProber` | `owned_by` `llamacpp` | `/v1/models`, `/props` |
| `halogenProber` | `owned_by` `halogen` | `/v1/models`, `/health` |
| `defaultProber` | any entry with a context length | `/v1/models` only |

Pick the smallest change that works:

- **The server only needs a name.** Add its `owned_by` value to
  `ownerUpstreams` in `default.go`. The default prober reports context length
  and text in/out. This is how vLLM, kyojin (`yamz`) and radiance are done.
- **The server exposes more capabilities** (tools, vision, audio) on another
  endpoint. Write a prober in its own file (`<server>.go`), register it in
  `defaultProbers()` before `defaultProber`, and use `c.GetJSON` for extra
  requests.

Rules:

- Never guess a capability. Leave fields the server does not report at their
  zero value; configured values win field by field.
- Use the loaded context, not the training limit (`n_ctx`, not
  `n_ctx_train`). Add new context field names to `ModelEntry` and
  `ContextTokens()`.
- `defaultProber.Matches` needs at least one entry with a context length.
  A listing without one returns `ErrUnsupportedUpstream`, which is expected
  for image, speech and transcription servers.
- Cached `Info` outlives upgrades. `Service.read` maps unknown cached
  upstream values to `hint.Unknown`, so renaming a constant is safe.

Tests: put real captures in `testdata/<server>/` (`v1_models.json` plus any
extra endpoints) and add `TestCapcompat_Detect<Server>` in
`capcompat_test.go` using `newUpstream` and `fixture`.

## Usage extraction (`internal/server`)

Each upstream has an extractor in `usage_<upstream>.go` implementing
`usageExtractor`. A parser receives every JSON event (each SSE `data:` line,
or the whole body once) and returns a `Usage` at the end.

Building blocks in `usage.go`:

- `apiReaderFor(path)` picks the wire format: Anthropic for paths ending in
  `/messages` (`readAnthropicUsage`), OpenAI for everything else
  (`readOpenAIUsage`, which covers chat, completions, embeddings and the
  Responses API).
- `apiUsage.toUsage(start)` builds a `Usage` with rates and draft counts set
  to -1 and the duration set to wall time.
- Server extras are small `apply*` functions: `applyTimings`
  (llama-server style `timings`), `applyVLLMMetrics`, `applyTabbyAPIRates`,
  `applyOMLXRates`, `applyKyojinDrafts`, `applyRadianceTotal`.
- `autoExtractor` applies every `apply*` it finds. It serves `Unknown` and
  llama-swap peers. When you add an `apply*`, add it to auto too, ordered so
  the more precise source runs last.

Adding an extractor:

1. If the server adds nothing to the standard usage block, add it to the
   `apiExtractor` case in `extractorFor`. Otherwise create
   `usage_<upstream>.go`. Reuse an existing parser when the format is the
   same (`halogenExtractor` reuses llama-server's), and keep server-specific
   fields in the server's own file.
2. Add the case to `extractorFor` and to `TestServer_Usage_ExtractorFor`.
3. Normalize to the field meanings below. Convert in the extractor, not in
   the UI.

### Field meanings (`TokenMetrics`)

Every extractor must produce these meanings. -1 means not reported; 0 is a
real answer.

| Field | Meaning | Known traps |
| --- | --- | --- |
| `InputTokens` | Whole prompt, cached tokens included | llama-server `prompt_n` excludes `cache_n`; Anthropic `input_tokens` excludes cache reads and writes |
| `CachedTokens` | Part of the prompt served from cache | Anthropic cache writes are not cached |
| `OutputTokens` | All generated tokens, reasoning included | |
| `ReasoningTokens` | Reasoning share of `OutputTokens` | Anthropic does not report it |
| `PromptPerSecond` | Uncached prompt tokens per second of prefill | vLLM TTFT includes queue time |
| `TokensPerSecond` | Output tokens per second of decode, prefill excluded | vLLM's own `tokens_per_second` includes TTFT; use `mean_itl_ms` |
| `DraftTokens` / `DraftAccTokens` | Speculative tokens drafted / accepted | Require both counters; kyojin reports accepted + rejected |
| `DurationMs` | Whole request time | Use a server total only when longer than wall time |

If a server's numbers cannot be mapped with confidence, keep what it reports
and say so in the extractor's doc comment.

### Tests

- Put real captures in `testdata/usage/<upstream>/` as `.json` (one body)
  or `.sse` (a stream). Name them after the endpoint, e.g.
  `chat_completion_stream.sse`, `messages_stream.sse`,
  `responses_stream.sse`.
- One test file per extractor, `usage_<upstream>_test.go`, with tests named
  `TestServer_Usage<Upstream>_<Case>`. Load fixtures with
  `parseUsageFixture`; use `parseUsageJSON` / `parseUsageStream` for small
  synthetic edge cases.
- Add each new fixture to `TestServer_UsageAuto_ReadsEachServer` so auto
  gives the same result as the dedicated extractor.
- Assert every normalized field the fixture carries, with a message when the
  value is derived (e.g. `"prompt_n + cache_n"`).

## Checklist for a new server

1. `hint.Upstream` constant, plus `Known()`.
2. Detection: an `ownerUpstreams` entry or a prober, with a `/v1/models`
   fixture and a `TestCapcompat_Detect<Server>` test.
3. Usage: an extractor, or an `apiExtractor` mapping, with fixtures and
   tests.
4. `make test-dev`.
