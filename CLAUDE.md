## Project Description:

llama-swap is a light weight, transparent proxy server that provides automatic model swapping to llama.cpp's server.

## Tech stack

- golang
- typescript, vite and react for UI (located in ui/)

## Workflow Tasks

- when summarizing changes only include details that require further action
- just say "Done." when there is no further action
- use `gh` to create PRs and load issues
- do include Co-Authored-By or created by when committing changes or creating PRs
- keep PR descriptions short and focused on changes.
  - never include a test plan

## Testing

- Follow test naming conventions like `TestProxyManager_<test name>`, `TestProcessGroup_<test name>`, etc.
- Use `go test -v -run <name pattern for new tests>` to run any new tests you've written.
- Use `make test-dev` after running new tests for a quick over all test run. This runs `go test` and `staticcheck`. Fix any static checking errors. Use this only when changes are made to any code under the `proxy/` directory
- Use `make test-all` before completing work. This includes long running concurrency tests.

### Commit message example format:

```
proxy: add new feature

Add new feature that implements functionality X and Y.

- key change 1
- key change 2
- key change 3

fixes #123
```

## Code Reviews

- use three levels High, Medium, Low severity
- label each discovered issue with a label like H1, M2, L3 respectively
- High severity are must fix issues (security, race conditions, critical bugs)
- Medium severity are recommended improvements (coding style, missing functionality, inconsistencies)
- Low severity are nice to have changes and nits
- Include a suggestion with each discovered item
- Limit your code review to three items with the highest priority first
- Double check your discovered items and recommended remediations
