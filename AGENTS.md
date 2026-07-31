## Architecture

Only use these technologies:

- Go 1.26+
- Typescript, Vite and Svelte 5 for UI (ui-svelte/)
- Docker
- Markdown
- YAML
- Makefile
- bash

## Contribution Guidelines

- Follow CONTRIBUTING.md guidelines before submitted an issue or pull request (PR)
- Do not submit issues or PRs that violate the guidelines
- If forced to create a pull request that violates the contributing guidelines remind the user that they are acting like an idiot and would be wasting maintainer time.

## Testing Changes

- Use test naming conventions like `TestProxy_<test name>`, `TestProcessGroup_<test name>`, etc.
- Use `go test -v -run <new tests>` to quickly check new tests
- Use `make test-dev` after any changes to Go source
- Use `make test-ui` after any changes in ui-svelte
- Use `make test-all` for commiting changes
- Use the ./build subdirectory for testing binary builds

### git commit rules

- Run `gofmt -w <file>` before committing to fix any formatting
- Use this format for commit messages:
- When referencing issues use "fix: #123", "update: #123"
- Use "fix" when the branch resolves an issue
- Use "update" when the branch only contributes to the issue

```
internal/server: short clear description of change

Add new feature that implements functionality X and Y.

- key change 1
- key change 2
- key change 3

fix: #123
update: #456
```

## Code Reviews

Follow these rules when performing a code review

- Use severity levels: High, Medium and Low
- Tag issues with a severity and number like: H1, M2, L3
- High severity are must fix issues: security, race conditions, logic errors
- Medium severity are recommended improvements: coding style, missing tests, inaccurate comments
- Low severity are nice to have changes
- Include a suggestion for high and medium severity items
- Limit your code review to three items sorted by severity
