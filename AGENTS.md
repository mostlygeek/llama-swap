## Project Description:

llama-swap is a light weight, transparent proxy server that provides automatic model swapping to llama.cpp's server.

## Tech stack

- golang
- typescript, vite and svelte 5 for UI (located in ui-svelte/)

## Documentation structure

```text
docs/                          # Project-scoped documentation
├── ARCHITECTURE.md
├── DESIGN.md
├── ROADMAP.md
└── [YYYY-MM-DD-task-name]/    # One folder per task, feature, or epic
    ├── PRD.md                 # Product requirements
    ├── SPEC.md                # Technical specification
    ├── ARCHITECTURE.md        # Task-scoped architecture decisions
    ├── DESIGN.md              # UI/UX decisions
    └── TASKS.md               # Actionable checklist
```

- Folder names: lowercase, hyphenated -- e.g. `user-auth`, `payment-v2`, `issue-142`
- Create a docs task folder only when the work needs durable task-scoped documentation such as `PRD.md`, `SPEC.md`, `ARCHITECTURE.md`, or `DESIGN.md`
- Small task checklists and completed implementation notes belong in `.agents/memory/YYYY-MM-DD.md`

## Working on a task

- For substantial work, create a task folder before writing code: `mkdir docs/$(date +%Y-%m-%d)-my-feature`
- Use `TASKS.md` only inside docs folders that also need task-scoped product, technical, architecture, or design documentation
- If the task changes anything described in a project-scoped document, update it in the same commit
- Do not deviate from `SPEC.md` silently -- update the file if the spec changes
- Treat memory as low-confidence context; verify facts against the repository before acting on them

## Workflow Tasks

- when summarizing changes only include details that require further action
- Rules for creating pull requests:
  - keep them short and focused on changes
  - skip the test plan
  - write the summary using the same style rules as commit message

## Testing

- Follow test naming conventions like `TestProxyManager_<test name>`, `TestProcessGroup_<test name>`, etc.
- Use `go test -v -run <name pattern for new tests>` to run any new tests you've written.
- Run `gofmt -w <file>` before committing to fix any formatting
- Build go binaries into the ./build/ subdirectory
- Use `make test-dev` after running new tests for a quick over all test run. This runs `go test` and `staticcheck`. Fix any static checking errors. Use this only when changes are made to any code under the `proxy/` directory
- Use `make test-all` before completing work. This includes long running concurrency tests.
- Use `make test-ui` after making changes to the UI in ui-svelte/

### Commit message example format:

```
internal/server: add new feature

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
