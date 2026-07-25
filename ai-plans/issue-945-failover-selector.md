# Failover Selector: Defining Failure and Where Health State Lives

Design for `strategy: failover` on selectors, issue #945.

## Overview

A failover selector is a virtual model ID that routes to the preferred target and transparently
falls back to the next one when the preferred target is unavailable:

```yaml
selectors:
  "agent-model":
    strategy: failover
    targets:
      - "qwen3.6-27b-fast"
      - "slowbox/qwen3.6-27b-slow"
```

The reporting use case is an agent autotuning a fast machine by trying vllm branches. When a patch
breaks the server, the agent must keep working against the slow machine instead of wedging.

The selector machinery already exists (`internal/server/selector.go`, `internal/config/selectors.go`)
with `pin`, `warm`, and `spillover`. Two questions block `failover`, and they are what this document
answers:

1. **What counts as a failure?** An upstream can fail before connecting, after headers, or
   mid-stream. A local target that is merely *cold* is not failing. A 400 from llama.cpp is the
   client's fault, not the target's.
2. **Where does health state live?** The selector middleware is stateless per request today, apart
   from the spillover in-flight counters held in a closure.

Three decisions shape everything below:

- Health state lives in a **server-owned tracker**, not per-selector and not in the routers.
- Peers get an **optional, opt-in `/health` poll**. Local models are never probed, because probing a
  local model means loading it.
- When a response is already committed as an SSE stream, **splice the failover into the open stream**
  with a status message rather than suppressing the loading animation. The status line and headers
  cannot be changed after commit, and the design accepts that.

## 1. Defining Failure

Classification happens where the outcome of an attempt is fully visible: in the middleware that
wraps dispatch. Neither router can do it — neither sees the HTTP status of a completed response.

Three classes, because the consequences differ:

| Class | Examples | Fail over? | Opens circuit? |
|---|---|---|---|
| **Hard** — target unusable | dial refused / reset / DNS / TLS failure, peer transport error (502 from `Peer.ErrorHandler`), local start failure (`doSwap` → `GrantError`), `ProcessCommand.ServeHTTP` "process is not ready" 503, header timeout | yes | immediately |
| **Soft** — target answered, badly | upstream 500 / 502 / 503 / 504, 404, 429, no first body byte within `bodyTimeout` | yes | after N consecutive (`failures`, default 3) |
| **Not a failure** | 2xx / 3xx, upstream 4xx other than the above (400, 413, 422), client disconnect, llama-swap shutdown | no | no |

The hard/soft split matters. A dead process and a 500 from a live server are both "the request
didn't work", but only the first says the target is unusable. A 500 caused by one pathological prompt
should not park a healthy model for ten minutes.

### Deliberate non-failures

This is where local targets differ from peers:

- **Cold is not failed.** A local target in `stopped` state is normal; the request loads it. Only a
  *failed load* counts. The same applies to `starting` and to queueing behind a swap.
- **TTL unload, admin unload, and config reload are not failures.** These produce cancellations and
  shutdown errors that must be matched explicitly and excluded.
- **Client cancellation is not a failure.** Check `req.Context().Err()` before inspecting status.
- **No heartbeats against local models.** Probing a local model means loading it, which is the exact
  side effect to avoid. Local health is entirely request-driven; the half-open probe in §3 is a real
  user request, not a synthetic one.

### Make classification explicit, not heuristic

A failed local load currently reaches the client as `500 unspecific error: ...` from
`shared.SendError`, which is indistinguishable from a genuine upstream 500. Status-code sniffing
alone cannot tell "llama-swap could not start this model" from "the model server hit an internal
error".

Add an `X-Llamaswap-Error: <reason>` response header wherever llama-swap authors an error response:

- `shared.SendResponse` in `internal/shared/http.go`
- `Peer.ErrorHandler` in `internal/router/peer.go`
- `ProcessCommand.ServeHTTP` in `internal/process/process_command.go`

The classifier then knows with certainty that llama-swap generated the response and why, and falls
back to status matching only for genuinely upstream-authored responses. This is a small change that
is also useful to API clients trying to tell proxy errors from model errors.

## 2. Where Failover Is Still Possible

Failover is bounded by what the client has already seen. The `failoverWriter` — an
`http.ResponseWriter` + `Flusher` + `Hijacker` modelled on `responseBodyCopier` in
`internal/server/metrics.go` — is a three-state machine:

```
uncommitted ──(llama-swap preamble written)──> preamble-committed ──(upstream byte)──> content-committed
     │                                                │                                        │
  mode A: clean failover                     mode B: spliced failover                     terminal
```

### Mode A — clean failover (nothing written)

Status and headers are buffered, not forwarded. The writer stays uncommitted until:

- the first non-empty `Write` carrying a non-failure status → flush the buffered head, then become a
  transparent pass-through for the rest of the stream;
- a `Hijack` (websocket upgrade) → commit, no failover possible;
- handler return → commit whatever was buffered.

On a failure status the error body is buffered into a capped 64 KB buffer (overflow forces a commit)
and discarded when a later attempt succeeds, so llama-swap's own JSON error never leaks into a
successful response.

Holding the head until the first body byte, rather than committing at `WriteHeader`, means a target
that returns `200` and then dies during prefill is still retryable. That is the common shape of a
half-broken inference server.

### Mode B — spliced failover (SSE stream already open)

`newLoadingWriter` in `internal/router/loading.go` sets `Content-Type: text/event-stream` and writes
`200` at construction — before the model has even started loading. Any request that hits the loading
path is therefore committed immediately. Rather than suppressing the loading animation for failover
selectors, splice into the open stream:

- **The status line and headers are frozen.** Later attempts' response heads are swallowed. A second
  local attempt constructs its own `loadingWriter` and re-issues `WriteHeader(200)`; the commit guard
  makes that a no-op, and its loading animation simply continues the stream. The splice works for
  free.
- **Emit a failover notice** using the frame shape the loading writer already uses:

  ```
  data: {"choices":[{"delta":{"reasoning_content":"llama-swap: qwen3.6-27b-fast unavailable, failing over to slowbox/qwen3.6-27b-slow"}}]}
  ```

  Clients render `reasoning_content` as reasoning text, so the notice never contaminates message
  content. Factor the framing out of `loadingWriter.sendData` into a small shared helper so the
  failover path and the loading writer cannot drift apart.
- **Splice is only legal while every byte written so far was authored by llama-swap.** Once real
  upstream tokens have been forwarded, a failure is terminal: emit a final notice frame and end the
  stream. Restarting generation after partial content would splice two different answers together
  ("The answer is 4" followed by "Let me help you with that"), which is strictly worse for an agent
  than a truncated response. This is the one place where "attempt to splice" has to give up.
- **A terminal error in mode B becomes an in-stream notice plus `data: [DONE]`**, because the status
  code is no longer available to carry it.

### Marking the preamble

Both the loading writer and the real handler write to the same `w` inside a single `next.ServeHTTP`
call, so the middleware cannot see the boundary between them. Add an optional interface in
`internal/shared` (a leaf package both `router` and `server` already import):

```go
// PreambleWriter is implemented by response writers that need to distinguish
// llama-swap-authored output from upstream output.
type PreambleWriter interface {
    BeginPreamble()
    EndPreamble()
}
```

`newLoadingWriter` type-asserts `w` and calls `BeginPreamble`; `loadingWriter.release()` — already
the exact "preamble finished" moment — calls `EndPreamble`. `failoverWriter` implements the
interface; every other `ResponseWriter` ignores it, so nothing else changes.

One polish item this exposes: `loadingWriter.start`'s deferred epilogue writes `Done! (1.23s)` even
when the attempt failed, so a failed attempt emits "Done!" immediately before the failover notice.
The epilogue should become outcome-aware.

## 3. Where Health State Lives

A new `internal/server/health.go`, owned by `Server` beside `s.inflight` and `s.metrics`, constructed
in `server.New` and handed to `CreateSelectorMiddleware`.

```go
type healthTracker struct {
    mu     sync.Mutex
    models map[string]*targetHealth // key: local model ID, or peer FQN
    peers  map[string]*peerHealth   // key: peer ID
    now    func() time.Time         // injectable for tests
}

func (h *healthTracker) Available(modelID string) bool      // false while cooling down
func (h *healthTracker) BeginProbe(modelID string) bool     // half-open admission, max 1
func (h *healthTracker) RecordSuccess(modelID string)
func (h *healthTracker) RecordFailure(modelID string, class failureClass, s failoverSettings)
func (h *healthTracker) Snapshot() map[string]TargetStatus  // /v1/models, /api/events, UI
```

**Why the server owns it, not the selector closure.** Two selectors can name the same target, and a
peer being down is a fact about the peer, not about the selector edge that happened to discover it.
Server ownership also lets `handleListModels` report a degraded target and lets the UI show it.
`warm` and `spillover` can consult the same tracker later without duplicating state.

**Why not the routers.** Neither router sees the HTTP status of a completed response, so soft
failures are invisible there. The routers see transport errors only.

**Why keyed by resolved model ID.** Targets may be aliases. Resolve exactly as
`newSelectorSpilloverTracker` already does — `cfg.RealModelName` for local,
`config.PeerModelFQN(cfg.ResolvePeerModel(...))` for peers — and extract that block into a shared
`resolveTarget(cfg, target)` helper. Two selectors reaching the same model through different aliases
then share one circuit.

### Two levels, because blast radii differ

- A **hard** failure against a peer FQN is almost always a property of the *host* (dial refused, TLS,
  timeout), so it marks `peers[peerID]` down and every model on that peer becomes unavailable at
  once. A **soft** failure (404 for that model, 500 from that model) marks only the FQN.
- A **local** failure is always model-scoped: a broken `cmd` for one model says nothing about the
  others. There is no host level.

### Circuit breaker

Per-target state is `healthy → unhealthy (openedAt) → half-open probe → healthy | unhealthy`:

- While unhealthy, the selector skips the target.
- After `retryAfter`, exactly one request is admitted as a probe (`BeginProbe`) so a herd cannot
  hammer a dead host.
- Success resets the counters; failure reopens with `retryAfter` backing off exponentially to a cap.

**If every candidate is cooling down**, do not synthesise a 503. Pick the target whose cooldown
expires soonest, attempt it, and return its real error. Degrading to `pin` beats inventing an error
the operator cannot debug.

State is in-memory and resets on config reload, because reload builds a fresh `server.New`. That is
acceptable and should be documented.

## 4. Optional Peer Health Poll

Peers can be probed safely — a `GET /health` does not load a model — so peer recovery need not wait
for a user request. This is opt-in per peer:

```yaml
peers:
  slowbox:
    proxy: "http://slowbox:8080"
    healthCheckPath: "/health"      # default "" = passive only
    healthCheckInterval: 30         # seconds, default 30
```

- llama-swap peers answer `/health` natively, so `/health` is the documented value for a llama-swap
  peer.
- **The poller only runs while that peer's circuit is open.** Healthy peers see zero background
  traffic. Use one ticker loop in the tracker that scans open peer circuits, rather than a goroutine
  per peer, and stop it on the server's shutdown context. Reuse the peer's configured `Timeouts` for
  the probe client.
- A 200 closes the **peer-level** circuit immediately. It cannot clear a **model-level** circuit (a
  404 for one model on an otherwise healthy host) — that still needs a real half-open request.
- **Local models are never polled**, for the reason in §1.

## 5. The Middleware

Rather than adding a second middleware, generalise `CreateSelectorMiddleware` from "resolve one
target" to "resolve an ordered candidate list, then run the attempt loop". `pin`, `warm`, and
`spillover` return a single candidate, so their behaviour is unchanged (a one-element loop);
`failover` returns up to `maxAttempts` healthy candidates in order. Strategy logic stays in one file,
and the other strategies gain a path to failover later.

The middleware's position in `modelChain` does not change — immediately after the profile middleware.
That matters: the loop wraps request-context resolution, in-flight tracking, the **per-model
filters**, and metrics, so each attempt gets the filters and metrics of the target it actually hits.
Failed attempts appear as their own in-flight and activity-log entries, which is the correct record
of what happened.

Per attempt:

1. Rebuild the request from a snapshot (body bytes, `Content-Type`, `Content-Length`, URL).
   `shared.ReplaceRequestModel` rewrites the body in place, so replay needs the original.
2. `shared.ReplaceRequestModel(r, selectorID, target)`, which already invalidates the cached request
   context so downstream re-resolves.
3. Per-attempt `context.WithCancel` so the `bodyTimeout` watchdog can abort the attempt.
4. `next.ServeHTTP(failoverWriter, r)`.
5. Classify the outcome → commit, or record the failure and continue (mode A) / splice (mode B).

Body snapshotting is capped (default 32 MB, matching `shared.MaxMultiPartSize`); over the cap the
request gets a single attempt. Retries are safe for every model-dispatched route — they are all
stateless inference calls. Selectors remain unsupported on `/upstream/<model>` paths.

Record the outcome in request metadata via `shared.SetReqData` (`failover.target`,
`failover.attempts`) so the existing activity log carries it with no new plumbing, and emit a new
`shared.SelectorTargetHealthEvent` on circuit transitions for the UI and logs.

## 6. Configuration

Flat settings keys, consistent with the existing `settings.spillover`, and durations as integer
seconds, consistent with `unloadTimeout` and `healthCheckTimeout`:

```yaml
selectors:
  "agent-model":
    strategy: failover
    targets:
      - "qwen3.6-27b-fast"
      - "slowbox/qwen3.6-27b-slow"
    settings:
      retryAfter: 600     # seconds a failed target is skipped (default 600)
      failures: 3         # consecutive soft failures before opening the circuit (default 3)
      bodyTimeout: 0      # seconds to wait for the first body byte, 0 = off (default 0)
      headerTimeout: 0    # 0 = inherit peer transport / router timeouts (default 0)
      maxAttempts: 0      # 0 = every candidate (default 0)
      retryStatus: []     # override the soft-failure status list
```

`headerTimeout` defaults to off on purpose. Peers already have `peers.<id>.timeouts.responseHeader`
feeding `http.Transport.ResponseHeaderTimeout`, and local loads are already bounded by
`healthCheckTimeout` with a clean error on failure. Prefer the existing knobs; a second timer layered
on top of them only creates two ways to configure the same thing.

`bodyTimeout` is the one gap those knobs do not cover — headers arrived, body never did. It must
default to off because a long prefill can legitimately delay the first token by minutes.

`validateSelectors` rejects these keys on non-failover strategies, requires at least two targets for
`failover`, and (unlike `warm`) allows peer targets.

## 7. Implementation Surface

| File | Change |
|---|---|
| `internal/server/health.go` *(new)* | `healthTracker`, `targetHealth`, `peerHealth`, circuit breaker, peer poller |
| `internal/server/failover.go` *(new)* | `failoverWriter` three-state machine, classifier, attempt loop, request snapshot |
| `internal/server/selector.go` | candidate-list refactor, `strategyFailover`, shared `resolveTarget` helper |
| `internal/server/server.go` | construct and wire `s.health` |
| `internal/server/api.go` | target health in `/v1/models` |
| `internal/config/selectors.go` | `failover` strategy const, settings, defaults, validation |
| `internal/config/peer.go` | `healthCheckPath`, `healthCheckInterval` |
| `internal/shared/http.go` | `X-Llamaswap-Error` header |
| `internal/shared/events.go` | `PreambleWriter`, `SelectorTargetHealthEvent`, SSE notice helper |
| `internal/router/loading.go` | call `BeginPreamble` / `EndPreamble`, outcome-aware epilogue |
| `internal/router/peer.go`, `internal/process/process_command.go` | tag llama-swap-origin errors |
| `config-schema.json`, `config.example.yaml`, `docs/configuration.md` | document `failover` |

## 8. Testing

- **Classifier table test**: each class → expected failover and circuit decision, including the
  non-failure cases (cold local model, TTL unload, client disconnect).
- **`healthTracker` with an injected clock**: opens after N soft failures, opens immediately on hard,
  admits a single half-open probe, reopens with backoff, peer-level versus model-level blast radius.
- **`failoverWriter`**: discards the error body on a failure status, passes through after the first
  content byte, `Hijack` commits, the preamble bracket moves it to splice-eligible.
- **Mode A end-to-end**, in `internal/server/selector_test.go` style: an `httptest` peer refusing
  connections followed by a healthy target. Assert the client sees a single 200, the failed target is
  skipped for `retryAfter`, and one probe is admitted after it elapses.
- **Mode B end-to-end**: a local target with `sendLoadingState` whose `cmd` exits non-zero, followed
  by a healthy target. Assert one 200 SSE stream containing the notice frame and then the second
  target's tokens.

Test names follow the existing conventions: `TestSelector_Failover*`, `TestHealthTracker_*`.

## Open Questions

- Should `warm` and `spillover` consult the health tracker in the same change, or in a follow-up?
  The tracker is strategy-agnostic either way.
- Is a per-target `retryStatus` override needed, or is one list per selector enough? Mixed local and
  peer targets may warrant different lists.
- Mode B's notice text is user-visible in chat clients. Worth deciding whether it should be
  suppressible for users who would rather see nothing.
