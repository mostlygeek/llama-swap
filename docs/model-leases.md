# Model leases and inference serialization

This fork adds two related controls for single-box, memory-constrained setups
(one GPU / UMA iGPU) where loading a model can evict another model that is doing
useful work:

1. **In-flight eviction protection** (always on): a model with active requests
   is never chosen as an eviction victim.
2. **Model leases** (opt-in, per work session): an explicit, refcounted claim
   that protects a model from eviction across an idle-between-requests work
   session (for example an overnight batch). The contract is **refuse-don't-break**:
   a load that would have to evict a leased model is refused with `503`, never
   allowed to break the lease.
3. **Global inference serialization** (opt-in): run at most one upstream
   inference at a time across all models, so two models don't thrash a single
   GPU.

These are independent of the GPU memory-budget gate (`gpuBudgetMB` and the
red-line watchdog); leases sit on top of whatever eviction policy is configured.

## The problem leases solve

On a single-user box, loading model B can silently evict model A under memory
pressure (LRU). If A was mid-batch or held an active session, that work is lost.
Static per-model `fifo.priority` can't help, because usefulness is a property of
the _work_, not the _model_.

The lease reframes accidental eviction from a **silent kill** into a **loud,
refusable event**. A lease is broken only by:

- the host-OOM **hardline** (safety beats a lease: losing a model is better than
  a kernel OOM / watchdog reboot), or
- an explicit operator **`kill`**.

Nothing else. An ordinary competing load is refused, not allowed to evict a
leased model.

## Configuration

All under the top-level `performance:` block.

```yaml
performance:
  # Serialize upstream inference across ALL models: at most one inference runs
  # at a time. Waiters are ordered by routing.scheduler.settings.fifo.priority.
  # Default false (historical parallel behaviour).
  serializeInference: false

  # Cap on how long any single lease can hold (acquire and extend both clamp to
  # it). Bounds the worst case when a client sets a long TTL and then crashes.
  # Default 4h. The lease API is always available; it is inert until a lease is
  # acquired.
  maxLeaseDuration: 4h

  # Persist the lease table to this file so leases survive a llama-swap restart.
  # On startup the table is reconciled: expired entries are dropped. Empty
  # (default) keeps leases in memory only.
  leaseStatePath: "" # e.g. /var/lib/llama-swap/leases.json
```

The lease API endpoints are always registered; with no lease acquired, behaviour
is identical to stock llama-swap (nothing is protected, nothing is refused).

## HTTP API

All endpoints are protected by the same API-key auth as the other llama-swap
control endpoints. Only **local** models can be leased (peer/remote models are
not owned by this instance).

### Acquire: `POST /leases`

```json
{
  "model": "qwen3.5-9b-vision",
  "holder": "run_tagging.py",
  "reason": "tag project-39",
  "ttl": "5h"
}
```

- `ttl` is a Go duration (`30m`, `4h`); omitted or empty means "the server
  maximum". It is clamped to `maxLeaseDuration`.
- `holder` (max 64 chars) and `reason` (max 120 chars) are sanitized to a single
  line.

`201 Created` with the lease:

```json
{
  "id": "01KWQZGPMWTT87NJZ5V3HGMRJX",
  "model": "qwen3.5-9b-vision",
  "holder": "run_tagging.py",
  "reason": "tag project-39",
  "acquired_at": "2026-07-04T18:50:33-07:00",
  "expires_at": "2026-07-04T23:50:33-07:00",
  "state": "active"
}
```

- `404` if the model is unknown to this router.
- `409` if the model is currently mid-eviction (the eviction won the race);
  retry, at which point the model is gone and reloadable.

### List: `GET /leases`

```json
{
  "leases": [
    {
      "id": "...",
      "model": "...",
      "holder": "...",
      "reason": "...",
      "acquired_at": "...",
      "expires_at": "...",
      "state": "active",
      "active_requests": 2,
      "ttl_remaining_ms": 3599895
    }
  ]
}
```

Only live (unexpired) leases are shown. `active_requests` is the model's current
in-flight request count.

### Release: `DELETE /leases/{id}`

`204 No Content` on success; `404` if the id is unknown (already expired,
killed, or never existed, which a client treats as "re-acquire").

### Extend: `POST /leases/{id}/extend`

```json
{ "ttl": "2h" }
```

Pushes the expiry out to `now + ttl` (clamped to `maxLeaseDuration`; never
shortens). `200 OK` with the updated lease; `404` if the id is unknown or
already expired. There is **no heartbeat**: extend is an explicit action, not a
keep-alive.

### Kill: `POST /leases/kill`

Operator override. Exactly one selector:

```json
{ "model": "qwen3.5-9b-vision" } // or {"id": "..."} or {"holder": "run.py"}
```

`200 OK` with `{ "killed": [ ...leases ] }`. `400` if zero or more than one
selector is set.

### Can-I-load (preflight): `GET /leases/can-load/{model}`

Read-only "if I load this model now, what happens?":

```json
{
  "model": "modelB",
  "running": false,
  "would_evict": ["modelA"],
  "blocked": true,
  "blocked_by": [
    {
      "model": "modelA",
      "holder": "run.py",
      "reason": "batch",
      "expires_in_ms": 3599818
    }
  ]
}
```

`blocked: true` means a lease would refuse the load. Note this reports the
**structural + lease** verdict only; a `blocked: false` verdict means "no lease
refuses it", not "guaranteed to fit under `gpuBudgetMB`" (memory admission is
still evaluated at actual load time).

### Refusal shape

When an ordinary inference/load request would have to evict a leased model, it
is refused on the normal request path with `503` and a `blocked_by` body, so the
thing you were trying to load tells you why:

```
HTTP 503
{ "error": "loading modelA refused: blocked by lease(s) on modelA (run.py: batch)",
  "blocked_by": [ { "model": "modelA", "holder": "run.py", "reason": "batch",
                    "expires_in_ms": 3588433 } ] }
```

## Request header: `X-Llama-Swap-Lease`

Optionally tag an inference request with a lease id:

```
X-Llama-Swap-Lease: 01KWQZGPMWTT87NJZ5V3HGMRJX
```

- If the header names a **live lease for a different model** than the request,
  the request is rejected `400` (an obvious mismatch).
- An **unknown/expired id** is logged and ignored (the client re-acquires on its
  own), never a hard failure.

## Client library and CLI

`scripts/model_lease.py` (stdlib only) is both an importable context manager and
the `llama-swap-lease` CLI.

Context manager: acquire on enter, release on exit (and on `SIGTERM`); the TTL is
the crash backstop, so there is no heartbeat thread:

```python
from model_lease import model_lease

with model_lease("qwen3.5-9b-vision", holder="run_tagging.py",
                 reason="tag project-39", ttl="5h") as lease:
    run_batch(extra_headers=lease.header())   # {"X-Llama-Swap-Lease": "<id>"}
```

CLI (base URL from `--url` or `$LLAMA_SWAP_URL`, key from `--api-key` or
`$LLAMA_SWAP_API_KEY`):

```bash
llama-swap-lease --url http://localhost:8080 acquire qwen3.5-9b-vision \
    --holder run.py --reason batch --ttl 4h
llama-swap-lease --url http://localhost:8080 ls
llama-swap-lease --url http://localhost:8080 can-load qwen3.5-9b-vision
llama-swap-lease --url http://localhost:8080 extend <id> --ttl 2h
llama-swap-lease --url http://localhost:8080 release <id>
llama-swap-lease --url http://localhost:8080 kill --model qwen3.5-9b-vision
```

Global flags (`--url`, `--api-key`) precede the subcommand.

## Behaviour and invariants

- **In-flight protection is always on**: a model with more than zero
  scheduler-tracked requests is never an eviction victim (except the host-OOM
  hardline).
- **A model is protected while it holds one or more live leases** (refcounted).
  Releasing one lease does not unprotect a model another lease still holds.
- **Server-side TTL, no client heartbeat.** Clean exit calls `release`; a crash
  is caught by TTL expiry. Expiry is evaluated directly at read time, so a lease
  never outlives its `expires_at` (the background sweeper is GC only).
- **Refuse-don't-break** applies to both eviction paths: the scheduler's
  structural eviction (loading B evicts A) and the memory-gate's top-up LRU
  eviction, plus the runtime red-line watchdog.
- **Leases break only on** the host-OOM hardline or an explicit `kill`.
- **Persistence** (`leaseStatePath`) survives a restart; on reload, expired
  leases are dropped. Across a restart, clients treat an "unknown lease id" as a
  signal to re-acquire.

## For maintainers: implementation notes

Code lives in `internal/router` (lease core + eviction integration),
`internal/server` (HTTP API), and `internal/config` (config fields). Built in
four phases: (1) in-flight protection + serialization, (2) lease core +
refuse-don't-break, (3) persistence, (4) header.

Key files:

- `internal/router/lease.go`: the `leaseTable`: refcounted per-model leases,
  ULID ids, server-side TTL, **eviction claims**, async persistence.
- `internal/router/lease_api.go`: router-level lease methods + `CanLoad`.
- `internal/router/memgate.go`, `redline.go`: victim selection excludes leased
  models (hardline overrides) and claims a victim before stopping it.
- `internal/router/base.go`: wires the table in, implements
  `Effects.TryClaimEviction`, releases claims in `doSwap`, validates the header.
- `internal/router/scheduler/fifo.go`: refuse-don't-break in
  `OnRequest`/`drainQueue`.
- `internal/server/leases.go`: the HTTP handlers.

### The one careful piece: eviction claims

Checking "is this model leased?" at victim-selection time is **not** enough: a
lease can be acquired between the check and the `Stop`. `TryClaimEviction` /
`ReleaseEvictionClaim` make an eviction claim and a lease **mutually exclusive**
under a single lock: a claim refuses if the model is leased, and an acquire
refuses if the model is claimed. Both eviction paths (scheduler structural and
memGate/redline top-up) claim a victim before stopping it, closing the
check-then-stop TOCTOU. The scheduler places the claim on the run loop; `doSwap`
releases it once the stops complete.

### Locking

- `leaseTable.mu` is a strict **leaf** lock: nothing under it calls `Stop`, waits
  on a goroutine or the run loop, takes `memGate.mu`, or does blocking I/O
  (persistence is handed to a dedicated writer goroutine). This is what makes it
  safe to consult from the single-threaded run loop.
- Lock order is `memGate.mu` then `leaseTable.mu` (the memGate top-up path); the
  reverse never happens.
- `List` snapshots live leases under the lock and queries the in-flight callback
  **after** releasing it: the callback round-trips through the run loop, which
  itself takes `leaseTable.mu`, so calling it under the lock would deadlock.

### Deferred (not implemented, by design)

Importance / preemption / auto-break arbitration was rejected (livelock-prone
for this context). The single-user insight, refuse instead of arbitrate, removes
the need for it. Revisit only if two simultaneous leased jobs regularly contend
for the same slot and manual `kill` is too slow.
