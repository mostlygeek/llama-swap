# Tools under `cmd/`

Read this when adding, changing or documenting a program under `cmd/`, or any
other standalone tool-like program in the repository.

## READMEs

Every tool that users run (`wol-proxy`, `fake-model`, `vllm-wrapper`,
`kubeswap`, ...) has a `cmd/<tool>/README.md` that says why the tool exists,
how to build it, and how to use it, with basic usage examples.

## Knowledge base articles

- Tool articles live in `docs/kb/guides/tools/`, named after the binary
  (`guides/tools/<tool>.md`; extra articles use `<tool>-<topic>.md`). Follow
  the frontmatter contract in `docs/kb/README.md`.
- When adding a new tool, ask the user whether to write a knowledge base
  article for it. Do not add one without asking: not everything under `cmd/`
  is a user-facing tool (e.g. `simple-responder`, `test-concurrency`,
  `monitor-test`).
- When changing a tool that has an article, update both `cmd/<tool>/README.md`
  and its article in the same change (flags, behavior, build, defaults). The two
  may overlap; the article feeds the Help page's Docs agent and is the shorter
  of the two.
- Run `make test-dev` afterward; `TestKB_FrontmatterIsValid` checks the
  article frontmatter.
