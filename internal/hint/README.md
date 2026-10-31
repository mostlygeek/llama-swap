# hint

Package `hint` is for hinting about what things are. It holds small shared
types that describe a request, so one layer does not have to guess what
another layer already knows.

- `Upstream` names the kind of server that answers a request (`llama-server`,
  `vllm`, `openai`, ...). capcompat detects it for local models;
  `ForHost` guesses it for peers from their proxy host.
- `Task` names the kind of inference a request performs (`chat`,
  `embedding`, `rerank`, ...).
- `Request` is a per-request hint carried in the request context. The
  metrics middleware creates it with `New` before dispatching; the router and
  peer proxy fill it in through `From`; the middleware reads it after the
  handler returns to pick a usage parser and label the activity record.

Rules:

- Only the request goroutine writes a `Request`, and only before the handler
  returns. No lock is needed.
- A missing or empty hint is valid. Consumers fall back to a best guess.
- This package imports nothing from llama-swap, so any package can use it.
  Keep it to types and small helpers; usage parsing lives in
  `internal/server`.

Adding an upstream: add an `Upstream` constant, a detection rule (a capcompat
`owned_by` match for local servers or a `hostUpstreams` entry for remote
providers), and, if it reports usage differently, an extractor in
`internal/server`.
