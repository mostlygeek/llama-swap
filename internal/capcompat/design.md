# capcompat: automatic model capability discovery

Design and implementation plan for issue #1105.

## Goal

Fill `models.*.capabilities` automatically when the config leaves it
undefined. llama-swap already renders the `capabilities` block into
`/v1/models` (`architecture`, `capabilities`, `supported_parameters`,
`context_length`). Today every field is typed by hand. This plan adds a
package that asks the upstream server what it can do, caches the answer in
the SQLite store, and serves the cached answer from `/v1/models` even while
the model is unloaded.

Rules, in priority order:

1. A non-empty `capabilities` block in the config is used verbatim. No probe
   runs for that model.
2. `capabilities.disableAuto: true` turns discovery off for the model. With an
   otherwise empty block the listing shows no capability fields, as it does
   today.
3. Otherwise the cached, auto-discovered capabilities are used. A model that
   has never been started has no cache entry and shows no capability fields.

Discovery happens when a local model reaches the `ready` state. The cache is
refreshed on every start, so a changed `cmd` corrects itself the next time the
model loads. Between loads the cache can be wrong. That is the accepted
tradeoff from the issue discussion.

## Packages touched

| package | change |
| --- | --- |
| `internal/store` | new `CacheRepository` interface and `CacheEntry` type, `Cache()` accessor on `Store` |
| `internal/store/sqlite` | migration `00003_create_cache.sql`, `cacheRepository` implementation |
| `internal/capcompat` | new package: upstream probers, sniffing, cache keys, in-memory memo |
| `internal/config` | `DisableAuto` on `ModelCapConfig`, json tags, validation |
| `internal/server` | probe on process ready, cache lookup in `handleListModels` |
| `config-schema.json`, `docs/config.example.yaml` | `disableAuto` |
| `docs/kb/guides/model-runtime/capabilities-and-model-listings.md` | auto discovery section |

Nothing in `internal/process` or `internal/router` changes. The process
package already emits `swaputil.ProcessStateChangeEvent` on every transition,
and that is the trigger.

## Part 1: store cache repository

Add a generic key/blob cache to the store. It is not capcompat specific so
other features can reuse it.

```go
// internal/store/cache.go
type CacheEntry struct {
    Key       string
    Data      []byte
    TTL       time.Duration // 0 means the entry never expires
    Timestamp time.Time     // when the entry was written
}

// Expired reports whether the entry is past its TTL at the given time.
func (e CacheEntry) Expired(now time.Time) bool

type CacheRepository interface {
    // Get returns the entry for key. found is false for a missing or expired
    // entry. Expired rows are deleted on read.
    Get(ctx context.Context, key string) (entry CacheEntry, found bool, err error)

    // Set inserts or replaces the entry. A zero Timestamp is replaced with
    // the current time.
    Set(ctx context.Context, entry CacheEntry) error

    // Delete removes one entry. Deleting a missing key is not an error.
    Delete(ctx context.Context, key string) error

    // Prune deletes every expired entry.
    Prune(ctx context.Context) error
}
```

`store.Store` gains `Cache() CacheRepository`. The sqlite store is the only
implementation, so no test fakes need updating.

Migration `internal/store/sqlite/migrations/00003_create_cache.sql`:

```sql
-- +goose Up
CREATE TABLE cache (
    key         TEXT PRIMARY KEY,
    data        BLOB NOT NULL,
    ttl_seconds INTEGER NOT NULL DEFAULT 0,
    ts_created  INTEGER NOT NULL
);

-- +goose Down
DROP TABLE cache;
```

Expiry is computed as `ts_created + ttl_seconds < now` when `ttl_seconds > 0`.
`Prune` runs once at store open so stale rows do not accumulate across
restarts.

Tests in `internal/store/sqlite/cache_test.go`: `TestStore_CacheSetGet`,
`TestStore_CacheOverwrite`, `TestStore_CacheExpiry` (write an entry with a Timestamp in the past so the
test never sleeps),
`TestStore_CachePrune`, `TestStore_CacheDeleteMissing`.

## Part 2: the capcompat package

### Types

```go
// internal/capcompat/capcompat.go
package capcompat

// Info is what a probe returns and what the cache stores.
type Info struct {
    Upstream     string                `json:"upstream"`     // "llama-server", "vllm"
    Capabilities config.ModelCapConfig `json:"capabilities"`
    DetectedAt   time.Time             `json:"detected_at"`
}

// Client is the minimal view of an upstream the probers need. It hides
// whether the upstream is a local process or a peer behind a transport.
type Client interface {
    // GetJSON performs GET path against the upstream base URL and decodes
    // the body into v. Non-2xx responses return an error carrying the status.
    GetJSON(ctx context.Context, path string, v any) error
}

// Prober knows one upstream server family.
type Prober interface {
    // Name is the value stored in Info.Upstream.
    Name() string
    // Matches reports whether the sniffed /v1/models listing came from this
    // server family.
    Matches(models ModelsResponse) bool
    // Probe queries the upstream and maps its answers onto capabilities.
    Probe(ctx context.Context, c Client, models ModelsResponse, modelName string) (config.ModelCapConfig, error)
}
```

`config.ModelCapConfig` gains json tags (`in`, `out`, `tools`, `reranker`,
`context`) so `Info` marshals with stable lowercase keys. `capcompat` imports
`config`; `config` does not import `capcompat`, so there is no cycle.

`NewClient(base *url.URL, rt http.RoundTripper, headers http.Header) Client`
builds the default implementation. Local models use `mc.Proxy` and a plain
`http.Transport`. Peers will reuse the transport the peer router already
builds, plus the `Authorization: Bearer <apiKey>` header (Part 5).

### Detection flow

`Detect(ctx, c Client, modelName string) (Info, error)`:

1. `GET /v1/models` once. Every supported server implements it and the
   `owned_by` field identifies the family:
   - `llamacpp` for llama-server and forks such as ik_llama.cpp
   - `vllm` for vLLM
2. Ask each registered prober `Matches`. The first match runs `Probe`.
3. No match returns `ErrUnsupportedUpstream`. The caller logs at debug level
   and writes nothing to the cache.

`modelName` is `useModelName` when set, otherwise the llama-swap model ID.
Probers use it to pick the right entry in `/v1/models` when the upstream
lists more than one (vLLM with LoRA adapters). When no entry matches they
fall back to the first entry with an empty `parent`.

Probing runs with a 10 second overall timeout and two retries one second
apart. A model with `checkEndpoint: none` is marked ready before it listens,
so the first attempt can fail on connection refused.

### llama-server prober (`internal/capcompat/llamaserver.go`)

Endpoints: `/v1/models` (already fetched) and `/props`.

| capability | source | notes |
| --- | --- | --- |
| `in` | always `text`; add `image` when `/props.modalities.vision` is true; add `audio` when `modalities.audio` is true | keys come from the server, so unknown keys are ignored |
| `out` | always `text` | llama-server does not generate other modalities |
| `tools` | `/props.chat_template_caps.supports_tools` | when `chat_template_caps` is absent or empty (older builds) fall back to `strings.Contains(chat_template, "tools")` |
| `context` | `/props.default_generation_settings.n_ctx` | this is the per-slot context actually loaded, which is what clients need. `/v1/models.meta.n_ctx_train` is the training limit and is not used |
| `reranker` | not discoverable | stays `false`; set it by hand |

Fixtures under `internal/capcompat/testdata/llama-server/`: `props.json` and
`v1_models.json` captured from a real llama-server (text model), plus
`props_vision.json` from a model started with `--mmproj`. Capture from the
unified docker image so the build number is recorded in the fixture header
comment (fixtures are JSON, so record the build in the test file instead).

Verify the exact key names of `chat_template_caps` against the fixture before
writing the mapping. The README documents the object but not its keys.

### vLLM prober (`internal/capcompat/vllm.go`)

Endpoints: `/v1/models` only.

| capability | source | notes |
| --- | --- | --- |
| `context` | `data[].max_model_len` for the matched entry | |
| `in` / `out` | `text` / `text` | vLLM does not expose modalities |
| `tools` | not discoverable | depends on `--enable-auto-tool-choice`, not reported by any endpoint |
| `reranker` | not discoverable | |

vLLM discovery is thin. Context length is the most requested field (#1083),
so it is still worth shipping. The guide must say that `tools` and modalities
for vLLM need a manual block, and that a manual block turns discovery off for
that model entirely.

Fixture: `internal/capcompat/testdata/vllm/v1_models.json` captured with
`cmd/vllm-wrapper` or a real vLLM server. Include one fixture with a LoRA
adapter entry (non-empty `parent`) to test entry selection.

### Cache keys and the memo

```go
// LocalKey returns the cache key for a local model. The hash covers cmd,
// proxy and useModelName so an edited command does not serve stale data
// while the model is unloaded.
func LocalKey(modelID string, mc config.ModelConfig) string
// "capcompat:v1:local:<modelID>:<8 hex chars of sha256>"
```

`Service` wraps the repository:

```go
type Service struct {
    cache  store.CacheRepository
    logger *logmon.Monitor
    probers []Prober
    mu   sync.RWMutex
    memo map[string]memoEntry // key -> Info or a remembered miss
}

func New(cache store.CacheRepository, logger *logmon.Monitor) *Service

// Refresh probes the upstream and writes the result to the cache and memo.
func (s *Service) Refresh(ctx context.Context, key string, c Client, modelName string) error

// Lookup returns cached capabilities for key. The first call for a key
// reads the store; later calls are served from memory until Refresh.
func (s *Service) Lookup(ctx context.Context, key string) (config.ModelCapConfig, bool)
```

`/v1/models` is polled by some clients, so `Lookup` must not hit SQLite on
every request. Misses are memoized too; `Refresh` replaces the memo entry.

Local entries are written with a 30 day TTL. The TTL only bounds garbage from
models removed from the config. Every start rewrites the entry.

### Tests

`internal/capcompat/*_test.go`, naming `TestCapcompat_<name>`:

- `TestCapcompat_DetectLlamaServer`, `TestCapcompat_DetectVLLM`: httptest
  server serving the fixtures, assert the full `ModelCapConfig`.
- `TestCapcompat_LlamaServerVision`, `TestCapcompat_LlamaServerNoTemplateCaps`
  (fallback path).
- `TestCapcompat_VLLMPicksBaseModel` (LoRA entries skipped).
- `TestCapcompat_DetectUnsupported` (`owned_by: openai`).
- `TestCapcompat_RefreshRetries` (first attempt refused, second succeeds).
- `TestCapcompat_LookupMemoizes` (store read count stays at one).
- `TestCapcompat_LocalKeyChangesWithCmd`.

## Part 3: config

```go
type ModelCapConfig struct {
    In          []string `yaml:"in" json:"in"`
    Out         []string `yaml:"out" json:"out"`
    Tools       bool     `yaml:"tools" json:"tools"`
    Reranker    bool     `yaml:"reranker" json:"reranker"`
    Context     int      `yaml:"context" json:"context"`
    DisableAuto bool     `yaml:"disableAuto" json:"-"`
}
```

- `Empty()` ignores `DisableAuto`. A block containing only
  `disableAuto: true` is still "not configured" for rendering purposes.
- `Validate()` is unchanged.
- `config-schema.json`: add `disableAuto` (boolean, default false) under the
  model `capabilities` properties. Description: "Disable automatic capability
  discovery for this model. Ignored when any other capabilities field is set,
  because a manual block already disables discovery."
- `docs/config.example.yaml`: add the key to the `capabilities` block with a
  comment explaining the three rules from the top of this document.

## Part 4: server wiring

In `server.New`:

```go
s.capcompat = capcompat.New(st.Cache(), proxylog)
s.capcompatOff = event.On(func(e swaputil.ProcessStateChangeEvent) {
    if e.NewState != string(process.StateReady) { return }
    mc, ok := s.cfg.Models[e.ProcessName]
    if !ok || !mc.Capabilities.Empty() || mc.Capabilities.DisableAuto { return }
    go s.refreshCapabilities(e.ProcessName, mc)
})
```

`refreshCapabilities` builds a client from `mc.Proxy`, derives
`capcompat.LocalKey`, and calls `Service.Refresh` with `s.shutdownCtx` and
the 10 second timeout. Failures log at debug: an unsupported upstream is
normal for stable-diffusion.cpp, whisper.cpp, ComfyUI and similar.

`Server.Shutdown` calls `s.capcompatOff()` so a hot reload does not leave the
old server subscribed. The event dispatcher is process-global, so this
matters.

`handleListModels` changes only where `mc.Capabilities` is passed:

```go
caps := mc.Capabilities
source := "config"
if caps.Empty() {
    source = "none"
    if !caps.DisableAuto {
        if auto, ok := s.capcompat.Lookup(r.Context(), capcompat.LocalKey(id, mc)); ok {
            caps, source = auto, "auto"
        }
    }
}
```

Aliases reuse the same `caps`. Add `capabilitiesSource` to the
`meta.llamaswap` block only when the value is `auto`, so operators can tell
in one request whether a field came from discovery. This is optional and can
be dropped if it feels like noise.

Selectors keep their current behaviour.

Tests in `internal/server/api_test.go`, naming `TestAPI_<name>`:

- `TestAPI_ListModelsUsesCachedCapabilities`: seed the in-memory store with
  an entry, assert `context_length` and `architecture` appear.
- `TestAPI_ListModelsConfigBeatsCache`: same seed plus a manual block, assert
  the manual values.
- `TestAPI_ListModelsDisableAuto`: seed plus `disableAuto: true`, assert no
  capability fields.
- `TestAPI_ListModelsRefreshOnReady`: model backed by `cmd/simple-responder`
  extended to answer `/v1/models` and `/props` like llama-server, load it,
  wait for the cache write, assert `/v1/models`. Add the two endpoints to
  simple-responder behind a flag so other tests are unaffected.

## Part 5: peers (follow-up, same issue)

The peer router builds one `http.Transport` per peer and stores the API key.
Expose them through a small accessor on `router.Peer`
(`Member(peerID) (rt http.RoundTripper, base *url.URL, apiKey string, ok bool)`)
and build a `capcompat.Client` from it.

A peer is usually another llama-swap, whose `/v1/models` already carries the
rendered capability fields. So the peer path needs a third prober,
`llamaswap.go`, matched on `owned_by: llama-swap`, that reverses
`renderCapabilities`: `context_length` to `context`,
`architecture.input_modalities` to `in`, `output_modalities` to `out`,
`capabilities.function_calling` to `tools`, `capabilities.reranker` to
`reranker`. A peer pointed straight at llama-server or vLLM works through the
existing probers with no extra code.

Refresh timing follows the issue: on `/v1/models`, when the peer's entry is
missing or older than a refresh interval (5 minutes), start one background
refresh per peer (guard with a per-peer in-flight flag) and serve whatever is
cached now. The listing never blocks on the network. Cache key:
`capcompat:v1:peer:<peerID>:<hash of proxy URL>:<modelID>`, TTL 30 days.

Peers ship as a separate PR after local discovery has landed.

## Part 6: documentation

Extend `docs/kb/guides/model-runtime/capabilities-and-model-listings.md`
rather than adding a file:

- New section "Automatic discovery": which servers are supported, which
  fields each can and cannot fill (the two tables above, condensed), when the
  cache is refreshed, and that a never-started model shows nothing.
- The three priority rules and a `disableAuto` example.
- "What goes wrong": stale cache after changing `cmd` until the next load,
  vLLM never reports tools, `checkEndpoint: none` models may be probed before
  they listen (retried).
- Add `models.*.capabilities.disableAuto` to `config_keys` and bump
  `updated`. `TestKB_FrontmatterIsValid` checks the key resolves in the
  schema, so the schema change lands in the same PR.

## PR sequence

Each PR is small enough to review on its own and references #1105.

1. `internal/store: add key/blob cache repository` (Part 1). `update: #1105`
2. `internal/capcompat: add llama-server and vllm probers` (Part 2, no
   wiring). `update: #1105`
3. `internal/server: auto discover model capabilities` (Parts 3, 4, 6).
   `update: #1105`
4. `internal/router: discover peer model capabilities` (Part 5).
   `fix: #1105` once tabby and sglang are agreed to be follow-ups, otherwise
   `update`.

tabbyAPI and sglang probers are each one file plus fixtures once the
framework exists, and can follow as separate PRs.

## Decisions to confirm

- **Block-level precedence.** A manual block disables discovery for the whole
  model, as specified. Per-field merging (manual `tools: true` plus
  discovered `context`) would help vLLM users but blurs the rule. Not planned;
  easy to add later because `Empty()` already treats the block as one unit.
- **Reranker stays manual** for both upstreams. Reading `--reranking` out of
  the model's `cmd` is a cheap heuristic that could be added to the local
  path later.
- **No global toggle.** Only the per-model `disableAuto` was asked for. A
  global `capabilities.disableAuto` is a one-line addition if wanted.
- **`capabilitiesSource` in `meta.llamaswap`** is optional. Keep or drop.
