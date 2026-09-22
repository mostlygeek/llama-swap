# capcompat: automatic model capability discovery

Design notes for issue #1105. This describes what was built; the package
doc comments carry the detail.

## Goal

Fill `models.*.capabilities` automatically instead of making every user type
it. llama-swap already renders that block into `/v1/models` as
`architecture`, `capabilities`, `supported_parameters` and `context_length`.
The upstream servers already know the answers, so llama-swap asks them.

The obstacle is that a server has to be running to be asked. Discovery runs
when a model reaches the ready state, and the answer is cached in the SQLite
store so `/v1/models` can serve it while the model is unloaded.

## Precedence

Configured values win **field by field**. Discovery only fills fields the
config leaves at their zero value, so a user who hand-sets one field still
benefits from discovery for the rest. This matters most for vLLM, where
context length is discoverable but tool support is not.

`ModelCapConfig` uses plain `bool`, `int` and slice fields, so a field
explicitly set to its zero value cannot be told apart from an omitted one.
Converting the struct to pointers would ripple through the renderer, the JSON
surface and 70-odd test references for a case that already has an answer:
`capabilities.disableAuto: true` takes full control of a model's listing.
That tradeoff is documented in the guide rather than hidden.

Because field presence cannot be detected, discovery runs for every model that
does not set `disableAuto`, including one with a fully specified block. That
costs one HTTP request per model start and keeps the cache warm.

## Probing

Probes dial the model's own `proxy` address with a plain `http.Client`.
`${PORT}` is resolved at config load, so `mc.Proxy` holds a concrete URL at
runtime.

The alternative was a synthetic request through `s.local.ServeHTTP`, the way
`startPreload` works. It was rejected: `baseRouter.trackedServe` resets the
model's TTL idle window and takes a concurrency slot, and a state race could
let the probe itself start a model that had already stopped.

A probe retries three times a second apart, under a ten second overall
deadline, because a model with `checkEndpoint: none` is reported ready before
it is listening.

## Upstream support

`Detect` fetches `/v1/models` once and picks a prober from the `owned_by`
field. A server nobody recognises returns `ErrUnsupportedUpstream`, which is
logged at debug and cached as a miss. That is the normal outcome for the
image, speech and transcription servers llama-swap also fronts.

**llama-server** (`owned_by: llamacpp`, covers forks such as ik_llama.cpp)
reads `/props`:

- `in` is `text` plus `image`, `audio` and `video` from `modalities.vision`,
  `modalities.audio` and `modalities.video`. All three key names are
  confirmed against a running server, which reports
  `{"vision":false,"video":false,"audio":false}` on a text-only build.
  Modalities are decoded as a map so a new one upstream does not break
  parsing, and keys with no mapping are dropped rather than passed through,
  where they would fail `ModelCapConfig.Validate`.
- The listing also carries an Ollama-style `models[]` block with its own
  `capabilities` array. It is not read. A capture of a multimodal model
  served without a projector lists `"multimodal"` there while `/props`
  reports every modality false, and `/props` is what the server will honour.
- `out` is `text`.
- `tools` requires both `supports_tools` and `supports_tool_calls` in
  `chat_template_caps`. Several flags in llama.cpp's caps struct are
  initialised to `true`, so requiring both guards against a default leaking
  through on a template llama.cpp could not fully inspect. Builds predating
  `chat_template_caps` fall back to looking for a tools loop in the template
  source.
- `context` is `default_generation_settings.n_ctx`, the window a request can
  actually use. It is not `meta.n_ctx` from the listing: a captured server
  reports 220160 in `/props` while the listing says 262144 for both `n_ctx`
  and `n_ctx_train`, so reading the listing would advertise a window the
  server refuses to fill.

**vLLM** (`owned_by: vllm`) reads only the listing it already fetched:
`max_model_len` on the matched entry. Entry selection prefers an exact name
match, then the first entry with no `parent`, so a LoRA adapter's context
length is not mistaken for the base model's. Nothing vLLM serves reveals
tool support or modalities.

**halogen-flash-server** (`owned_by: halogen`) reads `/health`, which that
project documents as the authoritative account of what the running build
accepts:

- `in` is `text` plus `image` when `vision.enabled`. Images are off unless
  the server was started with a vision tower, so this varies per deployment
  and is read rather than assumed for the engine.
- `tools` is true when `supported` lists `tools`, which is the server naming
  the request fields it takes. A non-empty `tool_calls.wire_format` is a
  second signal for a build that omits that list.
- `context` is `/health.context`, falling back to the listing, which
  publishes the same number three times over as `max_model_len`,
  `context_length` and `meta.n_ctx`.
- `max_tokens_cap` and `max_tokens_default` bound one request's output rather
  than the window, and have no field to map onto, so they are ignored.

Context is read through `ModelEntry.ContextTokens`, shared by the
listing-only probers. It tries `max_model_len`, then `context_length`, then
`meta.n_ctx`, and never `meta.n_ctx_train`: that is what the model was
trained for, not what the server loaded.

Fixture provenance under `testdata/`:

- `llama-server/props_capture.json` and `v1_models_capture.json`, and all
  four halogen files, come from running servers. The halogen variants are
  that capture with the vision tower off and with the tool signals removed.
  The llama-server listing capture was truncated in transit inside its `meta`
  block; the fields after `n_params` were dropped rather than invented, and
  the prober does not read them.
- The remaining llama-server fixtures and the vLLM ones are built from
  documented response shapes. Their modality blocks now carry the real key
  set, but `chat_template_caps` is still unconfirmed against a live build:
  before relying on the `tools` mapping in production, capture `/props` from
  a tool-capable model and a plain completion model and confirm
  `supports_tools` actually differs.

## Caching

`Service` wraps the store's `CacheRepository` with an in-memory memo. Clients
poll `/v1/models`, and every model in the listing is a lookup, so without the
memo each listing would be a burst of SQLite reads. Misses are memoized too.

Cache keys are `capcompat:v1:local:<modelID>:<hash>`, where the hash covers
`cmd`, `proxy` and `useModelName`. Editing a model's command invalidates its
entry rather than serving values discovered from the old one.

Entries carry a 30 day TTL, which only sweeps up models deleted from the
config; every model start rewrites its entry. A per-key in-flight guard stops
a flapping model from stacking probes.

The store is in memory unless `store.path` is set, so discovered values only
survive a restart on a configured store. The guide says so.

## Wiring

`server.New` subscribes to `swaputil.ProcessStateChangeEvent` and keeps the
returned `context.CancelFunc` on the `Server`, released in `Shutdown`. The
dispatcher is process-wide, so a hot config reload would otherwise leave the
retired server probing alongside the new one.

The handler returns immediately and does the probe in its own goroutine.
Event delivery is asynchronous, but the dispatcher blocks a publisher whose
consumer queue fills, and the publisher here is the process state machine.

`handleListModels` resolves capabilities once per model and reuses the result
for that model's alias records, so an alias never disagrees with the model it
points at. Peer, selector and profile-pin records are unchanged.

## Not done: peers

A peer is usually another llama-swap, whose `/v1/models` already carries
rendered capability fields, so the peer path needs a prober that reverses
`renderCapabilities`, matched on `owned_by: llama-swap`. A peer pointed
straight at llama-server or vLLM works through the existing probers unchanged.

It also needs an accessor on `router.Peer` to expose each peer's transport,
base URL and API key, which `peerMember` holds unexported. Refresh would run
in the background when `/v1/models` is requested and the peer's entry is
missing or stale, serving whatever is cached now so the listing never blocks
on the network. Key: `capcompat:v1:peer:<peerID>:<hash>:<modelID>`.

tabbyAPI and sglang probers are each one file plus fixtures.
