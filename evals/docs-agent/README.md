# Docs Agent evaluation harness

A headless driver and scored test suite for the Playground's Docs agent. It exists so that a strong coding agent — Claude Code,
Codex — can improve the Docs Agent's accuracy on a small local model by
measuring, changing one thing, and measuring again.

The CLI imports `ui/src/lib/agentLoop.ts`, `chatApi.ts` and `agentTools.ts`
**unmodified**. What is measured here is exactly what the browser runs, so an
improvement to the score is an improvement to the product.

## One server serves everything

```bash
go run . -config evals/config.yaml -listen :8080
```

That single instance serves both halves:

- `/api/mcp` — the documentation tools, compiled from **your working tree**.
- `/v1/chat/completions` — the agent model, which may be local or reached
  through a `peers:` block.

> **Do not point `--base-url` at a remote llama-swap you did not build.**
> `/api/mcp` does not exist in releases and answers 404. The CLI refuses to
> start in that case rather than running a silent, tool-less suite.

## Running it

```bash
# build, start, evaluate, tear down
./evals/docs-agent/run.sh

# attach to a server that is already running (the fast loop)
./evals/docs-agent/run.sh --base-url http://localhost:8080

# what to run before keeping or rejecting a change
./evals/docs-agent/run.sh --repeat 3 --label prompt-v2

# run up to four cases concurrently
./evals/docs-agent/run.sh --concurrency 4

# run one topic, several topics, or their independently worded holdouts
./evals/docs-agent/run.sh --group routing
./evals/docs-agent/run.sh --group routing --group connectivity
./evals/docs-agent/run.sh --holdout --group routing

# one case, printed in full
cd ui && npm run agent -- ask "How do I unload a model after 5 minutes?"
```

Every run writes two files to `evals/docs-agent/runs/` (gitignored):

- `<ts>-<label>.json` — full transcripts, per-assertion results, summary.
- `<ts>-<label>.md` — **failures only**. Read this one. The JSON is for diffing
  and for the judge; the markdown is what fits in a context window.

## The history log

Every run also appends an entry to `evals/docs-agent/runs/history.md`: a row in
a summary table, plus a short plain-English account of what that run got wrong.
Open it to see whether the last few changes did anything.

The narrative matters as much as the number. A run that scores 94% by refusing
to use tools and a run that scores 94% by answering well are the same table row
and very different results, so each entry separates **what it made up** (a
failing `negative` case — it described configuration that does not exist) from
**what it got wrong**, and says how often it answered without consulting the
docs at all.

The table is parsed back to evaluate the stopping rule, so edit the prose
freely but leave the table alone. Disable with `--no-history`, or point it
elsewhere with `--history FILE`.

## The five tuning surfaces

Change **one at a time**, and nothing outside this list.

| # | Surface | Files | Restart needed |
|---|---|---|---|
| 1 | System prompt | `ui/src/lib/prompts/docsAgent.ts` | no |
| 2 | Knowledge base | `internal/docagent/db/**/*.md` | yes |
| 3 | Tool descriptions and schemas | `internal/mcptools/{docs,config,sys}.go` | yes |
| 4 | Search ranking | `internal/reference/search.go` | yes |
| 5 | MCP instructions | `internal/server/apimcp.go` (`mcpInstructions`) | yes |

Surface 1 is the fast loop: the prompt is sent by the client, so an edit is
live on the next run with no restart. Surfaces 2–5 are compiled in — `internal/docagent/db`
reaches the binary through `//go:embed` in `reference_embed.go` — so they need
`run.sh` without `--base-url`.

Surface 5 currently reaches external MCP clients only. `agentTools.ts` calls
`tools/list` and `tools/call` but never `server/discover`, so `mcpInstructions`
never reaches the Playground. Improving it is still worthwhile for other MCP
clients, but it will not move this score. The Playground's equivalent is
surface 1.

## The protocol

1. **Baseline.** `./run.sh --repeat 3 --label baseline`. Record the mean pass
   rate.
2. **Change one surface.** Read the failures markdown first and let it choose
   the surface — a case that fails because the model never called a tool is a
   prompt problem; one that fails because `docs__search_docs` returned nothing
   useful is a KB or ranking problem.
3. **Re-measure** with the same `--repeat`, a new `--label`.
4. **Keep or revert.** `git checkout <file>` if the number did not move.
   Record what was tried either way, including failures — a rejected idea is
   worth as much to the next iteration as an accepted one.
5. **Check for overfitting** every few accepted changes:
   `./run.sh --repeat 3 --holdout --label holdout-N`.

### Use `--repeat 3` for decisions

Local models are stochastic even at `temperature 0`. A single run's pass rate
moves by several points on its own. `--repeat 1` is for a quick look; a
keep-or-revert decision made on `--repeat 1` is a coin flip, and the loop will
happily spend hours chasing noise.

The summary reports **flaky** cases (passed some attempts, failed others)
separately. A change that converts flaky cases into consistent passes is a real
improvement even when the headline pass rate barely moves.

### Stopping rule

**Stop after five consecutive runs that fail to beat the best score.** The CLI
tracks this for you from the history log and prints, on every run:

```
  best 94.9% (baseline); 2/5 runs since it was beaten
```

and, when the limit is reached:

```
  STOP: 5 runs in a row have not beaten 94.9% (baseline).
  Diminishing returns on cases. Report what was tried and stop tuning.
```

Take that literally: stop tuning, and report. A tie does not reset the counter
— a change that did not move the number is an attempt that did not work.
`--stop-after N` changes the limit. The count is scoped to one case set, so
holdout runs never trip it.

Stop early if the training pass rate is climbing but the **holdout is not**.
That is overfitting to the training questions, and further tuning makes the
product worse while making the number better.

When you stop, report the final numbers, the surfaces that moved them, and what
was tried and rejected.

## The cases

`cases/` and `cases-holdout/` are recursively grouped by topic. The first
directory below either root is the group name, such as `routing/` or
`model-runtime/`; `safety/` is eval-only negative coverage. Use repeatable
`--group NAME` to select a union of groups. No `--group` runs every group, and
`--only` further filters the chosen groups. Unknown or empty selections fail
and list available groups.

Selected groups are saved in result JSON and reports and are part of the
history set name, so full and subset runs never share a stop-rule baseline.
Each case is graded on two levels.

**Stage 1, deterministic** — this is the number the loop steers by:

```yaml
- id: ttl-unload-after-5min
  question: How do I make a model unload after 5 minutes of inactivity?
  tags: [ttl]
  expect_regex: ["ttl", "300"]      # all must match the answer
  expect_any: ["second"]            # at least one must match
  forbid_regex: ["unloadTimeout"]   # none may match
  expect_tools: [docs__search_docs] # all must have been called
  expect_tools_any: []              # at least one must have been called
  forbid_tools: []                  # must not have been called
  require_tools: true               # must not answer from memory alone
  max_iterations: 5
  reference: |                      # stage 2 only
    Set `ttl: 300` on the model...
  rubric:                           # stage 2 only
    - States that ttl is measured in seconds
```

Regexes are case-insensitive. A turn that errored or hit the iteration ceiling
fails regardless of its text. A case with no assertions is rejected at load
time rather than passing for free.

`safety/negative.yaml` is the group that matters most and is easiest to overlook: five
questions about features llama-swap **does not have**. The correct answer is to
say so. A model that invents a plausible config key passes nothing there, and
no other case in the suite would catch it.

Each topic may contain a `hard.yaml` companion for multi-hop questions, exact
defaults, and traps where the plausible answer is wrong. Keep a hard case in
the group whose guide answers it; this makes focused tuning runs useful
without mixing unrelated topics.

**Stage 2, LLM judge** — a separate pass over a saved results file:

```bash
cd ui && npm run agent -- judge \
  --results ../evals/docs-agent/runs/<file>.json \
  --judge-model sippy/gptoss-120B \
  --min-score 0.8
```

It re-reads saved answers rather than re-running the agent, so it is cheap and
repeatable, and the same run can be re-judged with a different judge. It
refuses (exit 2) below `--min-score`, because judging answers that already fail
regex checks wastes tokens, and it refuses a judge model equal to the agent
model, because a model grading itself ratifies its own mistakes.

## Adding cases

Every assertion must be grounded in something `internal/docagent/db/` actually says — check
before you write it. A case asserting a fact the documentation does not contain
tests the model's pretraining, not this agent, and it can never be fixed by any
of the five surfaces.

Constraints to respect while editing:

- `internal/docagent/db/` frontmatter is validated by `TestKB_FrontmatterIsValid`
  (`internal/reference/golden_test.go`); every `config_keys` entry must resolve
  in `config-schema.json`. Run `make test-dev`.
- MCP tool names must match `[a-zA-Z0-9_-]{1,64}`. `agentTools.ts` silently
  drops anything else, because these names are forwarded as OpenAI function
  names.
- Run `gofmt -w` on any Go file you touch; CI fails on `gofmt -l`.
- Do not assert on `config__get_config` output. It reflects whichever config
  the server was started with, so such a case is machine-specific.

## Comparing models

`--model` accepts anything the running llama-swap serves. The baseline is
`sippy/gemma-4-12B` and that is the score being optimized, but a case that
fails on **every** model is usually a bad case or missing documentation rather
than a prompt problem — and that distinction decides which surface to edit.
