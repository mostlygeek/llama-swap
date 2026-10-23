# Tools under `cmd/`

- Every user-run tool has `cmd/<tool>/README.md`: why it exists, how to build
  and use it, basic examples.
- KB articles: `docs/kb/guides/tools/<tool>.md` (extras: `<tool>-<topic>.md`).
- New tool: ask the user before writing a KB article. Not every `cmd/` program
  is user-facing (`simple-responder`, `test-concurrency`, `monitor-test`).
- Changing a tool that has an article: update its README and article together.
  Overlap is fine; the article is the shorter one.
