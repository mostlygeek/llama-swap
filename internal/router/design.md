# Router design

A developer tutorial for the `internal/router` package and its `scheduler`
sub-package.

## Intro

A llama-swap router is the component that sits behind the proxy and answers one
question for every incoming request: _can this model serve right now, and if
not, what has to happen first?_ Answering it means juggling three concerns that
used to live tangled together in one type:

1. **Process machinery** — owning the OS processes, starting and stopping them,
   running health checks, and shuttling HTTP requests onto the right upstream.
2. **Scheduling strategy** — the queue, in-flight bookkeeping, and the decision
   tree that turns one request into "serve now", "join an existing swap",
   "queue", or "start a swap".
3. **Eviction policy** — given a model we want to load, which currently-running
   models have to be stopped to make room?

The design pulls those three apart into separate, independently replaceable
pieces:

| Concern             | Type                           | Lives in                        |
| ------------------- | ------------------------------ | ------------------------------- |
| Process machinery   | `baseRouter`                   | `internal/router/base.go`       |
| Scheduling strategy | `scheduler.Scheduler` (`FIFO`) | `internal/router/scheduler/`    |
| Eviction policy     | `scheduler.Swapper`            | `groupSwapper`, `matrixSwapper` |

`baseRouter` keeps the channels, run loop, process lifecycle, and shutdown
teardown, and exposes the side-effects a scheduler needs through the
`scheduler.Effects` interface. The scheduler owns the queue and decision tree
but performs no side-effects directly — it calls back through `Effects`. The
`Swapper` is a pure function from "target model + currently running" to "models
to evict", and knows nothing about queues, channels, or processes.

Because the seams are interfaces, you can replace the scheduling strategy
without touching process management, or write a new eviction policy without
touching either. `FIFO` is the first and currently only `Scheduler`;
`groupSwapper` and `matrixSwapper` are the two `Swapper`s.

## Key concepts

### One run loop, no locks

`baseRouter.run()` is a single goroutine selecting over a handful of channels:

```go
for {
    select {
    case req := <-b.shutdownCh:  b.handleShutdown(req); return
    case req := <-b.handlerCh:   b.schedule.OnRequest(req)
    case req := <-b.unloadCh:    b.schedule.OnUnload(req.targets, req.timeout); close(req.respond)
    case ev := <-b.swapDoneCh:   b.schedule.OnSwapDone(ev)
    case ev := <-b.serveDoneCh:  b.schedule.OnServeDone(ev)
    }
}
```

Every `Scheduler` method runs on this one goroutine. That is the single most
important fact about the design: **the scheduler never needs a mutex for its own
state**. All scheduler state is touched only from these callbacks, which are
serialized by the run loop. If you write a new scheduler, you get the same
guarantee for free — and you must not break it by spinning up goroutines that
mutate scheduler state.

### Events flow in, side-effects flow out

The run loop turns external happenings into method calls on the scheduler:

- A new HTTP request becomes `OnRequest(HandlerReq)`.
- A swap goroutine finishing becomes `OnSwapDone(SwapDone)`.
- A tracked request handler returning becomes `OnServeDone(ServeDoneEvent)`.
- An admin unload becomes `OnUnload(targets, timeout)`.
- Shutdown becomes `OnShutdown(err)`.

The scheduler reacts by calling **back out** through `Effects`: inspect a
process state, start a swap, grant a response to a caller, or stop processes. It
never calls `process.Process` directly and never writes to a channel directly.
This keeps the scheduler pure enough to unit-test against a fake `Effects` with
no goroutines or real processes involved (see `scheduler/fifo_test.go`).

```
   HTTP request                          admin Unload / Shutdown
        │                                          │
        ▼                                          ▼
 ServeHTTP ──HandlerReq──▶  baseRouter.run()  ◀──unloadCh/shutdownCh
                                  │  (single goroutine)
                                  ▼
                          Scheduler.On*(...)
                                  │  calls back through
                                  ▼
                          Effects: ModelState / StartSwap /
                                   GrantServe / GrantError / StopProcesses
                                  │
                                  ▼
                  baseRouter side-effects: doSwap goroutine,
                  grant() to caller, process.Stop()
                                  │
            swap completes ──SwapDone──▶ back into run loop
```

### The swap goroutine

Scheduling decisions must be quick and non-blocking, but loading a model is
slow. The two are reconciled by doing the slow part on a separate goroutine.

When the scheduler decides to start a swap, inside `OnRequest` it:

1. records "a swap for X is in flight" in its own state, then
2. calls `Effects.StartSwap(modelID, evict)`.

`StartSwap` does **not** load the model itself — it just launches a detached
goroutine (`doSwap`) and returns straight away. `doSwap` is what does the slow
work: stop the evicted processes, start the target, wait for it to become ready.
Because `StartSwap` returned immediately, `OnRequest` returns too, and the run
loop is free to pick up the next event — another request, a serve-done, an
unload — while `doSwap` runs in the background.

The swap's eventual result comes back as just another event: when `doSwap`
finishes it posts a `SwapDone` onto `swapDoneCh`, which the run loop delivers as
`OnSwapDone`. So a slow load never blocks the run loop; it brackets it with two
quick events (`OnRequest` to start, `OnSwapDone` to finish) and everything in
between is handled normally.

### In-flight tracking and `trackedServe`

When the scheduler grants a request, the handler it hands back is wrapped by
`baseRouter.trackedServe`. The wrapper runs the real `ServeHTTP` and, on return,
posts a `ServeDoneEvent` so the run loop can decrement the per-model in-flight
count. This is why the scheduler can know whether a process is "busy": it counts
grants out and serve-dones in. A swap that would evict a busy process is
deferred until that process's in-flight count hits zero (`OnServeDone` then
re-drains the queue).

The subtle contract here is `GrantServe`'s boolean return. The caller's
`Respond` channel is unbuffered, so a successful send proves the HTTP goroutine
is alive and took the handler. If the caller already disconnected, the send
fails, `trackedServe` never runs, and **no** `ServeDoneEvent` will ever arrive —
so the scheduler must only increment `inFlight` when `GrantServe` returns true.
Incrementing on a false return would strand the counter above zero and the model
could never be evicted again.

## The interfaces

All three live in `scheduler/scheduler.go`.

### `Scheduler`

```go
type Scheduler interface {
    OnRequest(req HandlerReq)
    OnSwapDone(ev SwapDone)
    OnServeDone(ev ServeDoneEvent)
    OnUnload(targets []string, timeout time.Duration)
    OnShutdown(err error)
}
```

Owns the queue, in-flight tracking, and the decision tree. All methods run on
the run-loop goroutine, so no internal locking is needed.

### `Swapper`

```go
type Swapper interface {
    EvictionFor(target string, running []string) []string
    OnSwapStart(target string, running []string)
}
```

The eviction policy. `EvictionFor` is a **pure decision** — given the target and
the complete `running` set, return the running model IDs that must stop. It must
not log or mutate anything, and it does **not** inspect process state itself:
the scheduler hands it `running` already assembled (every non-stopped process,
unioned with the targets of in-flight swaps already committed but not yet
visible in process state). That keeps the swapper a pure function of its inputs,
with no reference to processes.

The reason it must not log is that it is a _speculative_ query — "what would we
evict if we started this swap right now?" — called far more often than swaps
actually happen. The scheduler calls it once per incoming request, and then
**again for every still-queued request on every queue drain** (each `OnSwapDone`,
`OnServeDone`, and `OnUnload`). Most of those calls end in "still queued",
"collides", or "nothing to evict", not a real swap. Logging there would emit
duplicate lines for a request that simply sits in the queue, and lines for
decisions that never happen — the log would stop meaning "a swap occurred".

`OnSwapStart` is the one place a Swapper may log, because it is called exactly
once, at the moment a swap is committed. One log line there equals one real swap,
with the evict set that is genuinely being applied — which is why `matrixSwapper`
re-solves and logs the full decision (set, DSL, cost) in `OnSwapStart` rather
than in `EvictionFor`.

### `Effects`

```go
type Effects interface {
    ModelState(modelID string) (process.ProcessState, bool)
    RunningModels() map[string]process.ProcessState
    StartSwap(modelID string, evict []string)
    GrantError(req HandlerReq, err error)
    GrantServe(req HandlerReq, modelID string) bool
    StopProcesses(timeout time.Duration, ids []string)
}
```

Implemented by `baseRouter`. This is the scheduler's entire window onto the
outside world; everything else about the router is hidden from it. See the
deep-dive below.

### `Factory` — wiring it together

```go
type Factory func(name string, logger *logmon.Monitor, eff Effects) Scheduler
```

`baseRouter` doesn't know which scheduler or swapper it has — it is handed a
`Factory` at construction and calls it once, passing itself as the `Effects`.
The concrete router captures its `Swapper` in the closure. From `group.go`:

```go
swapper := &groupSwapper{ /* ... */ }
base := newBaseRouter("group", conf, processes, proxylog,
    func(name string, logger *logmon.Monitor, eff scheduler.Effects) scheduler.Scheduler {
        return scheduler.NewFIFO(name, logger, swapper, eff)
    })
```

This closure is the single point where the three pieces meet: it binds a
specific `Swapper` (`swapper`) and a specific `Scheduler` (`FIFO`) to the
`baseRouter`'s `Effects` (`eff`).

**The swapper is a separate type from the concrete router.** There are currently two router implementations router.Group and router.Matrix. Each of these has a custom swapper that implements scheduler.Swapper for custom eviction logic. This decoupling of responsibilities makes it easy to implement custom swapping strategies.

### The events

A single goroutine in `baseRouter.run()` owns and serializes all state changes in the router. By processing events one at a time it ensures correctness and eliminates complex mutex lock logic.

These are the events the router currently uses:

```go
type HandlerReq struct {            // one in-flight ServeHTTP awaiting a decision
    Model      string
    Ctx        context.Context
    Respond    chan HandlerResp     // UNBUFFERED — see GrantServe contract
    PositionCh chan int             // queue-position updates for the loading UI
}

type HandlerResp struct {           // the decision handed back to the caller
    HandleFunc http.HandlerFunc     // serve with this, or...
    Err        error                // ...fail with this
}

type SwapDone        struct{ ModelID string; Err error } // swap goroutine finished
type ServeDoneEvent  struct{ ModelID string }            // tracked handler returned
```

## Deep-dive: the `Effects` interface and why it exists

`Effects` is the inversion-of-control boundary that makes the split possible.
The scheduler decides and `baseRouter` _acts_. Pulling the side-effects behind this
interface buys three things:

1. **Purity and testability.** The scheduler performs no I/O, starts no
   goroutines of its own, and touches no real processes. Its tests drive the
   `On*` methods directly and assert on a `fakeEffects` that just records the
   calls — synchronous, deterministic, no sleeps. (`scheduler/fifo_test.go`.)
2. **A single, auditable side-effect surface.** Every externally-visible thing a
   scheduler can do is one of six methods. You can reason about the whole
   contract by reading one interface.
3. **Decoupling lifetime.** The scheduler never holds a `process.Process`,
   never sees a channel, and never learns how shutdown teardown works. It only
   knows model IDs and states.

Method by method, as implemented in `base.go`:

- **`ModelState(modelID) (state, ok)`** — read-only snapshot of a process's
  state, and whether this router handles the model at all. The scheduler uses it
  for the "unknown model" check and the "already ready" fast path. Safe to call
  any time because the process map is fixed at construction and `State()` is a
  snapshot.

- **`RunningModels()`** — the state of every process that isn't stopped or shut
  down. The scheduler unions its keys with its own in-flight swap targets to
  build the `running` set it hands the `Swapper`, so the swapper never has to
  touch process state itself.

- **`StartSwap(modelID, evict)`** — fire-and-forget. `baseRouter` launches the
  `doSwap` goroutine and returns immediately; the result comes back later as a
  `SwapDone`. The scheduler records the swap as active _before_ calling this so
  that requests arriving in the meantime can join it.

- **`GrantError(req, err)`** — hand a caller an error response. Used for unknown
  models, failed swaps, unloads, and shutdown.

- **`GrantServe(req, modelID) bool`** — hand a caller the tracked handler for a
  ready model, returning whether the caller was still there to receive it. The
  scheduler increments the in-flight count **only on a true return** (see the
  in-flight contract above). This is the one `Effects` method whose return value
  carries state-machine significance.

- **`StopProcesses(timeout, ids)`** — stop processes in parallel and **block**
  until all have stopped. Used by `OnUnload` so an admin `Unload` call can
  guarantee the process is dead by the time it returns. (Note `StartSwap` is
  async but `StopProcesses` is sync — the difference is deliberate and tied to
  the caller's expectations.)

A useful way to hold it in your head: `Effects` is the scheduler's syscall
table. The scheduler is a pure state machine; `Effects` is how it touches the
world, and `baseRouter` is the kernel that implements those syscalls with real
goroutines, channels, and processes.

## How to implement a new `Swapper`

A `Swapper` is a pure decision function plus a logging hook — the easiest of the three pieces to replace.

1. **Write the swapper type** and give it whatever config it needs to make a
   decision. It does **not** need the process map — the scheduler supplies the
   running set as an argument. `groupSwapper` holds only its group config;
   `matrixSwapper` holds only its solver and logger:

   ```go
   type mySwapper struct {
       config config.Config
   }
   ```

2. **Implement `EvictionFor(target, running)`** as a _pure_ decision:
   - `running` is the complete live set, already assembled for you: every
     non-stopped process unioned with the targets of in-flight swaps the
     scheduler has committed to. You don't filter process state or fold in
     in-flight targets yourself, that's the scheduler's job. Just decide against the slice you're handed.
   - Return the list of model IDs in `running` that must stop for `target` to
     run. Return `nil`/empty when nothing needs evicting.
   - Do **not** mutate state here.
   - Do **not** log here. It can be called multiple times per request. Since it is pure function have tests verify the expected behaviour.

3. **Implement `OnSwapStart(target, running)`** — called once when a swap
   actually begins, with the same `running` set `EvictionFor` saw. This is the
   right place to log: one call equals one real swap. `matrixSwapper` re-solves
   and logs the chosen set and cost here; `groupSwapper` logs nothing.

4. **Wire it in** by instantiating the swapper in your router's constructor and
   capturing it in the `Factory` closure passed to `newBaseRouter` — exactly as
   `NewGroup` and `NewMatrix` do. The router struct itself only ever embeds
   `*baseRouter`; the swapper reaches the scheduler solely through that closure.

Reference implementations: `groupSwapper` (static group config) in `group.go`
and `matrixSwapper` (cost-based set solver) in `matrix.go`.

## How to implement a new `Scheduler`

Replacing the scheduler means taking over the queue and the entire decision tree. Read `scheduler/fifo.go` end to end first — it is the reference implementation and the rules below are easiest to understand in context.

The rules you must honour:

- **Single goroutine.** Every method runs on the `baseRouter.run()` goroutine. Keep your state in plain maps/slices and never read or write it from another goroutine. If you need slow work done, hand it to `Effects.StartSwap` and react to the resulting `SwapDone` — do not block a method waiting for it.

- **Never block the run loop.** `OnRequest`, `OnSwapDone`, and `OnServeDone` must make a decision and return. The one method allowed to block is `OnUnload`, and only because it must wait on the synchronous `StopProcesses` so the admin caller's guarantee holds.

- **Respect the `GrantServe` boolean.** Only count a request as in-flight when `GrantServe` returns true (see the in-flight contract above). A false return means the caller is gone; no `ServeDoneEvent` will ever arrive, so incrementing on false permanently strands the counter.

- **Account for in-flight swaps in your running set.** When you call `Swapper.EvictionFor`, the running set you pass must include not just live processes (`Effects.RunningModels`) but also the targets of swaps you've already started that aren't yet visible in process state — otherwise the swapper contradicts decisions already in motion.

What each method must do:

- **`OnRequest(req)`** — every request must resolve to exactly one of: granted, errored, joined (piggybacks an in-flight swap), queued, or swap-started. No request may be silently dropped.

- **`OnSwapDone(ev)`** — deliver the result to every waiter that joined this swap (grant on success, error on `ev.Err`), drop the swap from active tracking, then re-examine anything queued — a finished swap may have unblocked it.

- **`OnServeDone(ev)`** — decrement the model's in-flight count; when it hits zero, re-examine the queue. Do **not** clear in-flight counts by hand; the handlers post their own `ServeDoneEvent`s on return.

- **`OnUnload(targets, timeout)`** — error out any waiters or queued requests for the unloaded models, call `Effects.StopProcesses` (synchronously — the admin caller relies on the process being dead afterwards), then re-examine the queue.

- **`OnShutdown(err)`** — error out every waiter you still hold (active swap waiters and queued requests). Don't touch processes; teardown is `baseRouter`'s job.

Expose a constructor matching the `Factory` shape:

```go
func NewMyScheduler(name string, logger *logmon.Monitor, swapper Swapper, eff Effects) *MyScheduler {
    // ...
}

// in the concrete router:
base := newBaseRouter(name, conf, processes, proxylog,
    func(name string, logger *logmon.Monitor, eff scheduler.Effects) scheduler.Scheduler {
        return scheduler.NewMyScheduler(name, logger, swapper, eff)
    })
```

## Testing

- **Schedulers** are tested as pure state machines in the `scheduler` package:
  drive the `On*` methods directly against a `fakeEffects` and assert on the
  recorded grants/starts/stops. No goroutines, no sleeps. See
  `scheduler/fifo_test.go` as the reference; follow the `TestSchedulerName_<scenario>`
  naming convention.
- **`baseRouter` mechanism** (run loop, `grant`/`ServeHTTP`, `Unload`,
  `Shutdown`) is tested in `base_test.go`. The run loop exposes a
  `testProcessed` channel so tests can wait for an event to be fully processed
  instead of sleeping.
- Run new tests with `go test -v -run TestMyScheduler_... ./internal/router/scheduler/`,
  then `make test-dev` for a quick `go test` + `staticcheck` pass over `proxy/`.
