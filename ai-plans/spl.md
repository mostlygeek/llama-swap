# SPL (Swap Policy Language) implementation plan

## Context

Today a request body can only be rewritten with the predefined `filters` block
(`useModelName`, `stripParams`, `setParams`, `setParamsByID`) implemented in
`internal/server/filters.go`. Every new need (conditional defaults, client
specific overrides, denying requests) means a new Go filter. SPL ("spell") is a
small declarative language written inline in the YAML config. A program is a
list of `ACTION [when CONDITION]` clauses evaluated top to bottom over the JSON
request body. It is meant to be the long-term home for request rewriting,
validation and denial.

Decisions confirmed with the user:

- Attach points: BOTH a global `hooks.on_request` program and a per-model /
  per-peer `filters.policy` program, plus a top-level `policies:` map of named
  reusable programs for `apply`. (The draft called the hook `preHook`; this plan
  uses `hooks.on_request` to match the existing `hooks.on_startup` naming.)
- Ordering: legacy filters first, then `hooks.on_request`, then the matched
  model or peer `filters.policy`. A `deny` anywhere stops processing.
- `deny "msg"` returns 403; `deny 400 "msg"` overrides the status (400-599).
- Full operator set: `=`, `!=`, `<`, `<=`, `>`, `>=`, `matches /re/`,
  `is present`, `is missing`, `in [...]`, `and`, `or`, `not`, parentheses.
- Out of scope for v1: `auth.user` / `auth.claims` (no user or claims concept
  exists in llama-swap). `auth.key` (the extracted API key) is exposed instead.

## Package layout: new `internal/spl`

Standalone package with no import of `internal/config` (config imports spl),
mirroring how `internal/matrix` is wrapped by `internal/config/matrix.go`.
Dependencies: stdlib, `github.com/tidwall/gjson`, `github.com/tidwall/sjson`
(both already in go.mod).

Files: `doc.go`, `token.go`, `lexer.go`, `ast.go`, `parser.go`, `path.go`,
`value.go`, `eval.go`, `library.go`, `errors.go`, plus `*_test.go`.

Exported API:

```go
type Error struct{ Line, Col int; Msg string }   // Error() => "line 3:12: msg"

type Program struct{ /* clauses, applies */ }
func Parse(src string) (*Program, error)         // syntax, regex compile, namespace checks
func (p *Program) Applies() []string             // policy names referenced by apply
func (p *Program) Len() int                      // 0 for blank/comment-only source
func (p *Program) Validate(lib *Library, protected []string) error
    // for standalone programs: protected write targets + apply references resolve

type Library struct{ /* name -> *Program */ }
func NewLibrary(policies map[string]string, protected []string) (*Library, error)
    // parses all, checks apply references, DFS cycle detection, protected paths
func (l *Library) Lookup(name string) (*Program, bool)

type Request struct {
    Body    []byte
    Context map[string]string // context.* (read/write)
    APIKey  string            // auth.key
    Model   string            // request.model (ID the client asked for)
    Path    string            // request.path
    Method  string            // request.method
    Header  http.Header       // request.header.<name>
}
type Denial struct{ Status int; Message string }
func (p *Program) Run(lib *Library, req *Request) (*Denial, error)
    // rewrites req.Body / req.Context in place; Denial non-nil on deny
```

`protected` is passed in from `config.ProtectedParams` (`internal/config/filters.go:11`)
rather than duplicated. `Program` and `Library` are immutable after
construction; `Run` allocates per call, so they are safe for concurrent use.
`apply` resolves by map lookup in the `Library` at run time (validated at
load); a missing name at run time is returned as an `error` (HTTP 500).

## Grammar

Tokens: `IDENT` `[A-Za-z_][A-Za-z0-9_-]*` (keywords are only keywords in
keyword positions, so a body key named `set` or `in` still works as a path
segment); `NUMBER` `-?[0-9]+(\.[0-9]+)?([eE][+-]?[0-9]+)?`; `STRING` double
quoted with JSON escapes; `REGEX` `/.../` with `\/` escape, Go regexp syntax,
compiled at parse time; `OBJECT` `{...}` scanned as balanced braces (string
aware) and required to satisfy `json.Valid`; punctuation `. [ ] ( ) , = == != <
<= > >=`; `NEWLINE`; `EOF`. Comments: `#` to end of line.

Keywords: `default set remove apply deny to when and or not is present missing
in matches true false null`.

Newline rules: `\r\n` normalised; blank and comment-only lines dropped;
newlines inside `()`, `[]`, `{}` ignored; a newline followed by `when`, `and`
or `or` is a continuation (no action begins with those words).

```
program    = [ clause { NEWLINE clause } ] ;
clause     = action [ "when" condition ] ;
action     = "default" wpath "to" value
           | "set"     wpath "to" value
           | "remove"  wpath
           | "apply"   IDENT
           | "deny"    [ NUMBER ] STRING ;           (* integer 400..599 *)
value      = scalar | list | OBJECT ;
scalar     = STRING | NUMBER | "true" | "false" | "null" ;
list       = "[" [ value { "," value } ] "]" ;
condition  = or_expr ;
or_expr    = and_expr { "or" and_expr } ;
and_expr   = not_expr { "and" not_expr } ;
not_expr   = "not" not_expr | primary ;
primary    = "(" condition ")" | predicate ;
predicate  = rpath cmp scalar
           | rpath "matches" REGEX
           | rpath "is" "present" | rpath "is" "missing"
           | rpath "in" "[" [ scalar { "," scalar } ] "]" ;
cmp        = "=" | "!=" | "<" | "<=" | ">" | ">=" ;
path       = IDENT { "." IDENT | "[" NUMBER "]" } ;  (* integer index, may be negative *)
```

Parse-time errors (all `*Error` with line:col): deny status outside 400-599,
object/array literal used in a comparison, write to `auth.*` or `request.*`
(read-only), `context` write path deeper than `context.<key>`, invalid regex,
unexpected token / unexpected end of line with "expected ..." text.

## Evaluation semantics

Namespaces by first path segment:

| segment | meaning | read | write |
|---|---|---|---|
| anything else | JSON request body via gjson/sjson | yes | yes |
| `body.<path>` | explicit body prefix, escape hatch when a body's top-level key is literally `context`/`auth`/`request`/`body` (Ollama sends `context`) | yes | yes |
| `context.<key>` | `Request.Context` string bag (becomes `ReqContextData.Metadata`, copied into the activity log) | yes | yes |
| `auth.key` | API key extracted for the request; absent when empty | yes | no |
| `request.model`, `request.path`, `request.method`, `request.header.<name>` | request facts; header via `Header.Get`, absent when empty | yes | no |

gjson path translation: segments joined with `.`, integer indexes become numeric
segments (`messages.0.content`). IDENT never contains characters that gjson
treats specially, so no escaping. Negative index is resolved at eval time by
reading the array at the prefix and substituting `len+idx`; if the prefix is
not an array or `len+idx < 0` the path is unresolvable: reads are absent,
writes/removes are no-ops.

Value model: absent | null | bool | number (float64) | string | object | array,
built from `gjson.Result`. JSON `null` counts as **present**. Literals are
pre-encoded to raw JSON at parse time (numbers keep source text so large
integers survive) and written with one `sjson.SetRawBytes` call.

Operators:
- `=`: same kind and equal; different kinds, absent, object/array → false.
- `!=`: exact complement of `=` (absent `!= "x"` is true; document "use
  `x is present and x != "y"`" when absence must not match).
- `<` `<=` `>` `>=`: both numbers → numeric; both strings → byte order;
  anything else → false.
- `matches`: strings only; others → false.
- `in [..]`: any element `=`.
- `and` binds tighter than `or`; `not` tighter than both; parentheses override.

Actions:
- `default p to v`: set only when `p` is absent. `set p to v`: always write.
- `remove p`: `sjson.DeleteBytes` when present, else no-op; context → delete key.
- Context writes coerce to string: strings as-is, numbers by source text, bools
  `"true"`/`"false"`, objects/arrays compact JSON, `null` deletes the key.
- `apply name`: run the named program against the same state; depth guard
  `maxApplyDepth = 32` returns an error (cycles are already rejected at load).
  A `Denial` from the applied policy propagates immediately.
- `deny [status] "msg"`: returns `Denial{Status (default 403), Message}` and
  stops everything, including any later program in the chain.

Errors from sjson/json → `error` from `Run` → HTTP 500, like legacy filters.

## Config wiring (`internal/config`)

Struct changes:
- `config.go` `HooksConfig`: add `OnRequest string \`yaml:"on_request"\``.
- `config.go` `Config`: add `Policies map[string]string \`yaml:"policies"\``
  and an unexported `spl *SPLPrograms` with accessor `func (c Config) SPL() *SPLPrograms`.
  **Leave `spl` nil when nothing SPL-related is configured** so the existing
  whole-config equality test in `config_posix_test.go:304` keeps passing.
- `filters.go` `Filters`: add `Policy string \`yaml:"policy"\``. `ModelFilters`
  (inline embed, `model_config.go:171`) and `PeerConfig.Filters` (`peer.go:18`)
  pick it up with no change.

New file `internal/config/spl.go`:

```go
type SPLPrograms struct {
    Library   *spl.Library
    OnRequest *spl.Program            // nil when hooks.on_request is blank
    Models    map[string]*spl.Program // model ID -> compiled filters.policy
    Peers     map[string]*spl.Program // peer ID -> compiled filters.policy
}
var policyNamePattern = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_-]*$`)
func validateSPL(config *Config) error
```

`validateSPL` (sorted iteration for deterministic errors): validate policy
names against `policyNamePattern`; `spl.NewLibrary(config.Policies, ProtectedParams)`;
parse+validate `hooks.on_request`; parse+validate each non-blank model and peer
`filters.policy`; set `config.spl` only if any program exists. Error prefixes:
`policies.<name>: line L:C: ...`, `policies: cycle detected: a -> b -> a`,
`hooks.on_request: line L:C: ...`, `model <id>: filters.policy: line L:C: ...`,
`peer <id>: filters.policy: line L:C: ...`, and
`cannot set protected parameter "model"` for set/default/remove of `model`.

Call site: `internal/config/load.go`, after `validateProfiles` and before
`validateTailcatConfig` so models and peers are fully normalised first.

Macros: no code needed. Global macros already expand into every top-level key
and model-local macros (incl. `${MODEL_ID}`) into model fields, and leftover
`${...}` already fails load via `validateConfigMacroUses` (`macros.go:307`).
Document that `${...}` inside an SPL `#` comment is still substituted.

Merge: add `"policies": true` to `identityMapPaths` in `internal/config/merge.go:17`
so the same policy name in two `-config-dir` files is an error.

## Server wiring (`internal/server/filters.go`)

In `CreateFilterMiddleware`, after `applyFilters` (legacy filters) and before
the body is re-attached:

```go
if programs := cfg.SPL(); programs != nil {
    hook, policy := resolvePolicies(cfg, programs, data.Model) // same lookup order as resolveFilters
    if hook != nil || policy != nil {
        if data.Metadata == nil { // defensive; extractContext normally allocates it
            data.Metadata = make(map[string]string)
            *r = *r.WithContext(swaputil.SetContext(r.Context(), data))
        }
        req := &spl.Request{Body: body, Context: data.Metadata, APIKey: data.ApiKey,
            Model: data.Model, Path: r.URL.Path, Method: r.Method, Header: r.Header}
        denied, err := runPolicies(programs.Library, req, hook, policy)
        if err != nil { swaputil.SendResponse(w, r, http.StatusInternalServerError, err.Error()); return }
        if denied != nil { swaputil.SendResponse(w, r, denied.Status, denied.Message); return }
        body = req.Body
    }
}
```

- `ReqContextData` is stored by value (`swaputil/http.go:494`) but `Metadata` is
  a map, so `context.*` writes are visible to `CreateMetricsMiddleware`
  (`metrics.go:148`), which copies them into `ActivityLogEntry.Metadata`.
- Deny uses `swaputil.SendResponse` (`swaputil/http.go:193`) which emits the
  OpenAI-style error envelope for JSON clients.
- No logger added in v1 (keeps `CreateFilterMiddleware(cfg)` signature).
- `CreateFormFilterMiddleware` (multipart) and the `/upstream/` chain are untouched.

## Tests

`internal/spl` (`TestSPL_*`, table-driven, testify): lexer tokens and newline
rules and errors; parser actions, conditions, precedence, paths, errors with
line:col; negative index resolution; comparison semantics across kinds; actions
(default/set/remove, nested create, context coercion incl. `null` delete);
request namespace reads; deny default/custom status/inside apply; apply order
and depth guard; library unknown apply, cycle (direct and A→B→C→A), protected
path (`model`, `body.model`, `model.x`), empty program; concurrent `Run`.

`internal/config` (`spl_test.go`, via `LoadConfigFromReader`): `TestConfig_SPL_Valid`,
`_ParseErrorLocation` (all four prefixes), `_UnknownPolicy`, `_Cycle`,
`_ProtectedModel`, `_InvalidPolicyName`, `_MacroSubstitution`, `_NoSPLLeavesNil`;
`merge_test.go`: duplicate policy across config-dir files.

`internal/server` (`filters_test.go`, build cfg with `LoadConfigFromReader`,
drive `CreateFilterMiddleware(cfg)(final)` with httptest like
`TestServer_FormFilterMiddleware`): `TestServer_FilterMiddleware_SPL_DefaultSetRemove`,
`_DenyEnvelope` (403 and `deny 429`), `_LegacyFiltersRunFirst` (stripped key
counts as missing), `_GlobalHookBeforeModelPolicy` (override and hook deny
short-circuit), `_ContextLandsInMetadata` (assert via `swaputil.ReadContext` in
`final`), `_PeerPolicy`, `_UseModelNameVisible` (`request.model` is the alias,
body `model` is rewritten), `_NonJSONUntouched`, `_NoSPLConfigured` (hand-built
`config.Config{}` still works).

`internal/docagent`: add `"policies"` to the `want` list in
`TestDocs_RealConfigExample_SectionKeys` (`golden_test.go:168`) at its file
position; KB frontmatter/cross-reference tests cover the new page automatically.

## Docs

- `config-schema.json`: top-level `policies` (object, `propertyNames` pattern
  `^[A-Za-z_][A-Za-z0-9_-]*$`, string values); `hooks.properties.on_request`
  (string) and fix the hooks description that says only `on_startup` exists;
  `filters.properties.policy` (string) on both models and peers.
- `docs/config.example.yaml`: new `policies:` section between `models:` and
  `hooks:` with two or three short programs (default, set with object literal,
  `deny 400`, `matches`, `messages[-1].content`); `hooks.on_request: |` example
  using `apply` and `set context.<key>`; `filters.policy: |` on the model block
  (with `${MODEL_ID}`) and on the openrouter peer block. Keep SPL comment lines
  indented inside the block scalar so the section parser is not confused.
- New KB page `docs/kb/guides/api-integration/swap-policy-language.md`
  (frontmatter per `docs/kb/README.md`; `config_keys: [policies,
  hooks.on_request, models.*.filters.policy, peers.*.filters.policy]`): what
  SPL is, a working config, actions, conditions (absent vs null rules), paths
  and namespaces and negative indexes, `apply` and cycles, `deny` and the error
  envelope, `context.*` and the activity log, order of operations, macros,
  what goes wrong, future work (`auth.user`/`auth.claims`).
- Update "Order of operations" and Related links in
  `docs/kb/guides/api-integration/filters-and-request-rewriting.md`.
- Also save this plan as `ai-plans/spl.md` in the repo (user request).

## Implementation order and verification

1. `internal/spl`: errors → lexer (+tests) → parser (+tests) → path/value →
   eval (+tests) → library (+tests). `go test -race ./internal/spl/...`
2. Config: struct fields, `spl.go`, `load.go` call, `merge.go` entry, tests.
   `go test ./internal/config/...`
3. Server: `filters.go` changes + tests. `go test ./internal/server/...`
4. Schema, `config.example.yaml`, docagent `want` list, KB pages.
   `go test ./internal/config/ -run TestConfig_ExampleMatchesSchema` and
   `go test ./internal/docagent/...`
5. End-to-end: `make simple-responder`, write a config with a policy that
   denies when `messages is missing` and defaults `temperature`, run
   `./build/llama-swap`, curl `/v1/chat/completions` and check the 403 envelope
   and the rewritten body reaching the responder.
6. `gofmt -w` on every touched file, `make test-dev`, then `make test-all`.
   Commit per AGENTS.md format (`internal/spl: add Swap Policy Language`,
   hard-wrapped at 80) and push to `claude/spl-implementation-699fvi`.
