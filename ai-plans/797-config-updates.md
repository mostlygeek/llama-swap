# Plan: top-level `routing` config — `Config.Routing` as canonical source

## Context

`config.example.yaml` is being restructured to introduce a top-level `routing`
block that consolidates swap + scheduling strategy:

- `routing.router.use` (`group` | `matrix`) + `routing.router.settings.{groups,matrix}`
- `routing.scheduler.use` (`fifo`) + `routing.scheduler.settings.fifo.priority`

Backwards compatibility for the existing **top-level `matrix:` and `groups:`
keys is non-negotiable** — they must keep parsing and working.

**Design decisions (confirmed with user):**

1. The **config package is the single normalization point**. It applies the
   precedence and writes the result into **`Config.Routing`**, which becomes the
   one canonical place every caller looks for the routing/scheduling decision
   *and* the effective settings. (Full migration, not decision-only.)
2. **`fifo.priority` is schema + parsing only** this round — the FIFO scheduler
   ([scheduler/fifo.go](internal/router/scheduler/fifo.go)) is **not** changed to
   honor priority yet. `routing.scheduler` is parsed/validated but not wired in;
   `NewFIFO` remains the only scheduler.
3. The legacy `proxy/` package (`cmd/legacy`) **will be deleted later**, so it is
   not a long-term constraint. But it is *not* deleted in this change, so the
   existing top-level `Config.Matrix`/`Config.Groups`/`Config.ExpandedSets`
   fields are **retained and kept populated** purely so `proxy/` still compiles
   and passes during the transition. New code must not read them.

**Precedence** (applied in config): top-level `matrix` > top-level `groups` >
`routing.router.use`. When a top-level key is present, `routing.router` is
ignored.

**Key constraint:** matrix expansion (`ValidateMatrix` → `ExpandedSets`) and
group default-injection/validation must run against the *effective* router
config, so resolution happens before that validation.

## Approach

### 1. New types + `Config` changes — [internal/config/config.go](internal/config/config.go)

```go
type RoutingConfig struct {
    Scheduler SchedulerConfig `yaml:"scheduler"`
    Router    RouterConfig    `yaml:"router"`
}
type SchedulerConfig struct {
    Use      string            `yaml:"use"`      // default "fifo"
    Settings SchedulerSettings `yaml:"settings"`
}
type SchedulerSettings struct{ Fifo FifoConfig `yaml:"fifo"` }
type FifoConfig struct {
    Priority map[string]int `yaml:"priority"` // model ID -> priority, default 0
}
type RouterConfig struct {
    Use      string         `yaml:"use"`      // default "group"
    Settings RouterSettings `yaml:"settings"`
}
type RouterSettings struct {
    Groups map[string]GroupConfig `yaml:"groups"`
    Matrix *MatrixConfig          `yaml:"matrix"`
}
```

**`ExpandedSets` moves onto `MatrixConfig`** (not `RouterSettings`), and is
**not settable from yaml** — add to [matrix.go](internal/config/matrix.go#L14):

```go
type MatrixConfig struct {
    Var          map[string]string `yaml:"vars"`
    EvictCosts   map[string]int    `yaml:"evict_costs"`
    Sets         OrderedSets       `yaml:"sets"`
    ExpandedSets []ExpandedSet     `yaml:"-"` // populated by ValidateMatrix
}
```

Because `RouterSettings.Matrix` and the transitional `Config.Matrix` are the same
`*MatrixConfig`, the expanded sets are shared through the pointer — one place.

`Config` gains `Routing RoutingConfig \`yaml:"routing"\``.

Retain `Matrix`, `Groups` on `Config` as **transitional** input/compat fields
(comment them as such) — kept only until `proxy/` is removed. **Remove the
standalone `Config.ExpandedSets` field** (now on `MatrixConfig`).

### 2. Normalization + validation — `LoadConfigFromReader`

Insert resolution **before** the existing groups-XOR-matrix /
`ValidateMatrix` / `AddDefaultGroupToConfig` block at
[config.go:458-488](internal/config/config.go#L458-L488):

```go
hasTopMatrix := config.Matrix != nil
hasTopGroups := len(config.Groups) > 0

if !hasTopMatrix && !hasTopGroups {
    rs := config.Routing.Router.Settings
    if rs.Matrix != nil && len(rs.Groups) > 0 {
        return Config{}, fmt.Errorf("routing.router.settings cannot set both 'groups' and 'matrix'")
    }
    switch config.Routing.Router.Use {
    case "matrix":
        if rs.Matrix == nil {
            return Config{}, fmt.Errorf("routing.router.use is 'matrix' but routing.router.settings.matrix is not set")
        }
        config.Matrix = rs.Matrix
    case "group", "":
        config.Groups = rs.Groups
    default:
        return Config{}, fmt.Errorf("routing.router.use: unknown router %q (valid: group, matrix)", config.Routing.Router.Use)
    }
}
```

The **existing** block runs on the now-effective `config.Matrix`/`config.Groups`,
with one tweak: `ValidateMatrix`'s result is assigned to
`config.Matrix.ExpandedSets` instead of the removed `config.ExpandedSets`
([config.go:464-468](internal/config/config.go#L464-L468)). Default-group
injection is unchanged.

Then **build the canonical `Config.Routing`** from the effective result:

```go
if config.Matrix != nil {
    config.Routing.Router.Use = "matrix"
} else {
    config.Routing.Router.Use = "group"
}
config.Routing.Router.Settings.Matrix = config.Matrix // shares ExpandedSets via pointer
config.Routing.Router.Settings.Groups = config.Groups

if config.Routing.Scheduler.Use == "" {
    config.Routing.Scheduler.Use = "fifo"
}
```

**Scheduler/priority validation** (schema-level groundwork), after the model
loop where `config.RealModelName` exists:

- `config.Routing.Scheduler.Use` must be `"fifo"` (it is defaulted above).
- every key in `Routing.Scheduler.Settings.Fifo.Priority` must resolve via
  `config.RealModelName` (mirrors matrix var validation at
  [matrix.go:79](internal/config/matrix.go#L79)). Values are plain ints.

### 3. Router constructors read `Config.Routing` — [internal/router/](internal/router/)

- [group.go](internal/router/group.go): `NewGroup` and `groupSwapper` read
  `conf.Routing.Router.Settings.Groups` instead of `conf.Groups`
  ([group.go:18,73,94](internal/router/group.go#L18)).
- [matrix.go](internal/router/matrix.go): `NewMatrix`/`matrixSwapper` read
  `conf.Routing.Router.Settings.Matrix` and its `.ExpandedSets`
  ([matrix.go:17,22](internal/router/matrix.go#L17)).

Scheduler stays `scheduler.NewFIFO(...)` in both — priority not wired yet.

Also update the still-present `proxy/matrix.go` to read `cfg.Matrix.ExpandedSets`
instead of the removed `cfg.ExpandedSets` ([proxy/matrix.go:180](proxy/matrix.go#L180))
so the transitional package keeps compiling.

### 4. Router selection — [internal/server/server.go:98-112](internal/server/server.go#L98-L112)

Replace the `if cfg.Matrix != nil` block with a switch on the canonical field:

```go
switch cfg.Routing.Router.Use {
case "matrix":
    local, err = router.NewMatrix(cfg, proxylog, upstreamlog)
default: // "group"
    local, err = router.NewGroup(cfg, proxylog, upstreamlog)
}
```

(`Config.Routing.Router.Use` is always populated by config, so no nil checks.)

### 5. JSON schema — [config-schema.json](config-schema.json)

- Move the inline `groups` ([:313](config-schema.json#L313)) and `matrix`
  ([:347](config-schema.json#L347)) blocks into `definitions` (`groupsConfig`,
  `matrixConfig`); top-level `groups`/`matrix` become `$ref`s (still valid for
  backwards compat).
- Add top-level `routing`:
  - `scheduler`: `use` enum `["fifo"]`; `settings.fifo.priority` object with
    `additionalProperties:{type:integer}`.
  - `router`: `use` enum `["group","matrix"]` default `"group"`; `settings` =
    `{groups:{$ref}, matrix:{$ref}}` + `allOf` if/then enforcing groups-XOR-matrix
    (mirrors the top-level rule at [:517-538](config-schema.json#L517-L538)).

Schema has no Go/test consumer — editor/tooling support only.

## Files to modify

- [internal/config/config.go](internal/config/config.go) — types, `Routing`
  field, normalization + scheduler/priority validation.
- [internal/router/group.go](internal/router/group.go),
  [internal/router/matrix.go](internal/router/matrix.go) — read `Config.Routing`.
- [internal/config/matrix.go](internal/config/matrix.go) — `ExpandedSets` field
  on `MatrixConfig`.
- [internal/server/server.go](internal/server/server.go) — select on
  `Routing.Router.Use`.
- [proxy/matrix.go](proxy/matrix.go) — read `cfg.Matrix.ExpandedSets`
  (transitional, until `proxy/` is deleted).
- [config-schema.json](config-schema.json) — `routing` schema + defs refactor.
- Router unit tests (see below).

## Tests

- **config** (`internal/config/config_test.go`, `TestConfig_*`):
  - top-level `matrix` only → `Routing.Router.Use=="matrix"`, settings + expanded
    sets populated.
  - top-level `groups` only → `Use=="group"`, default group injected into
    `Routing.Router.Settings.Groups`.
  - no top-level + `routing.router.use: matrix` → matrix from
    `routing.router.settings.matrix`, expanded.
  - no top-level + `routing.router.use: group` (and default) → groups from
    routing settings.
  - top-level present + `routing` present → top-level wins.
  - errors: `use:matrix` w/o `settings.matrix`; both groups+matrix in routing
    settings; unknown `router.use`; `fifo.priority` key referencing unknown model.
  - `Routing.Scheduler.Use` defaults to `"fifo"`.
- **router** (`internal/router/group_test.go`, `matrix_test.go`): these build
  `config.Config{Groups:...}` / `{Matrix:...}` literals and bypass `LoadConfig`.
  Migrate them to populate `Routing.Router.Settings` (add a small
  `helpers_test.go` builder, e.g. `groupConfig(groups...)` / `matrixConfig(...)`,
  to keep the ~10 call sites terse).
- Add an `internal/server` test asserting `server.New` picks Matrix vs Group per
  `Routing.Router.Use`.

## Verification

- `gofmt -w` touched Go files.
- `go test -v -run TestConfig_ ./internal/config/`
- `go test ./internal/router/... ./internal/server/...`
- `make test-dev` (go test + staticcheck), then `make test-all`.
- Load the updated `config.example.yaml` through `LoadConfig` and assert it
  resolves to `Routing.Router.Use=="group"` (the example's routing block) without
  error. Also confirm a legacy top-level `groups`/`matrix` config still loads.
- Note: `proxy/` still compiles because the transitional `Config.Matrix/Groups`
  fields remain populated and it reads `cfg.Matrix.ExpandedSets`.
