## Architecture

- Use the existing technologies (Go, typescript, YAML, etc) when making changes

## Contribution Guidelines

- Follow CONTRIBUTING.md guidelines before submitted an issue or pull request (PR)
- When asked to create a pull request or issue ask the user for a decision if a violation of the rules is discovered.

## Testing Changes

- Use test naming convention like  `TestProxy_<test name>`, `TestProcessGroup_<test name>`, etc.
- Use `go test -v -run <new tests>` to quickly check new tests
- Use `make test-dev` after any changes to Go source
- Use `make test-ui` after any changes in ui/
- Use `make test-all` for commiting changes
- Use the ./build subdirectory for testing binary builds
- When synthetic metrics data is needed follow `docs/testing/synthetic-data.md`.
- Only when the user asks use `make eval-docs-agent` to score the Help page's Docs Agent against a local model after changing its system prompt, `docs/kb/` content, the MCP tool descriptions, or the docs search ranking. See `evals/docs-agent/README.md` for the tuning loop.

## Documentation

- When a change adds, removes, or changes the meaning of a configuration
  option, add or update a knowledge base guide under `docs/kb/guides/` for
  it, not just `docs/config.example.yaml` and `config-schema.json`.
- Prefer extending an existing guide covering the same topic over creating a
  new file for a single setting.
- Follow the frontmatter contract and writing guidelines in `docs/kb/README.md`
  (required `title`/`summary`/`category`, `config_keys` referencing real
  schema keys, keep it short, show a working config, say what goes wrong).
- Run `make test-dev` afterward; `TestKB_FrontmatterIsValid` in
  `internal/docagent` checks the frontmatter and that `config_keys` resolve.

### Tools under `cmd/`

- Before adding, changing or documenting a program under `cmd/` (or any
  standalone tool), read `cmd/AGENTS.md`.

### git commit rules

- Run `gofmt -w <file>` before committing to fix any formatting
- When referencing issues use "fix: #123", "update: #123"
- Use "fix" when the branch resolves an issue or update when the branch only contributes to it
- Hardwrap commit messages to 80 characters wide
- Use this format for commit messages:

```
internal/server: short clear description of change

Add new feature that implements functionality X and Y.

- key change 1
- key change 2
- key change 3

fix: #123
update: #456
```
