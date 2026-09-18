# gh-helpbot

Answers `@help <question>` on GitHub issues and discussions with the same
Help agent the llama-swap UI runs. Someone writes:

> @help how do I make a model unload after five minutes?

and the bot replies with the agent's answer, built from the documentation
served by a running llama-swap.

It runs one of three ways, all from the same code:

| Mode | When to use it |
|---|---|
| `serve` | Your box can receive HTTPS from GitHub (directly or through a tunnel). GitHub pushes each comment as a webhook. Default. |
| `poll` | Your box cannot take inbound connections. The bot asks the GitHub API for recent activity every minute. |
| `action` | You have a self-hosted GitHub Actions runner that can reach llama-swap. A workflow runs the bot once per comment. |

## How it works

1. A mention is detected: `@help` at the start of a line or after a space,
   ignoring quoted lines and fenced code (so quoting an earlier answer does
   not trigger it again).
2. The text after the mention becomes the question. If the comment is only
   `@help`, the issue or discussion's title and opening post are the question.
3. The question goes through the Help agent: `ui/src/lib/agentLoop.ts` calling
   llama-swap's `/v1/chat/completions` with the MCP documentation tools from
   `/api/mcp`. This is the code the Help page runs, imported unmodified.
4. The answer is posted as a reply, with a footer naming the model and a hidden
   marker so the same mention is never answered twice. Answers never ping
   anyone: any `@login` in the model's output is wrapped in backticks.

Pull request comments are ignored. Comments by the bot itself, or by any
GitHub App, are ignored.

## What you need

- **A llama-swap built from this repository** with the documentation tools at
  `/api/mcp` and a tool-capable model. Check with:

  ```sh
  cd cmd/gh-helpbot && npm ci
  npm run start -- ask "How do I set a ttl?" --base-url http://localhost:8080 --model your-model
  ```

  `/api/mcp` sits behind the same API-key middleware as `/v1`, so one
  `--api-key` (or `LLAMA_SWAP_API_KEY`) covers both when llama-swap sets `apiKeys`.

- **A GitHub token** in `GITHUB_TOKEN`. A fine-grained personal access token
  with repository permissions *Issues: read and write*, *Discussions: read and
  write* and *Metadata: read*. For an organization-owned repository the
  organization must allow fine-grained tokens; a classic token with `repo` and
  `write:discussion` works as a fallback. In `action` mode the workflow token
  is used instead.

The bot posts as the token's account. It reads that login at startup and
never answers its own comments.

## Configuration

Every flag has an environment variable; a flag wins when both are set.

| Flag | Environment | Default |
|---|---|---|
| `--model` | `DOCS_AGENT_MODEL` | required |
| `--base-url` | `LLAMA_SWAP_URL` | `http://localhost:8080` |
| `--api-key` | `LLAMA_SWAP_API_KEY` | none |
| | `GITHUB_TOKEN` | required except with `--dry-run` |
| | `GITHUB_WEBHOOK_SECRET` | required for `serve` |
| `--repo owner/name` | `HELPBOT_REPOS` (comma separated) | `poll`: required. Others: an allowlist; empty accepts any repository the event is for |
| `--mention` | `HELPBOT_MENTION` | `help` |
| `--self-login` | `HELPBOT_SELF_LOGIN` | the token's own login |
| `--listen` | `HELPBOT_LISTEN` | `0.0.0.0:8085` |
| `--path` | `HELPBOT_WEBHOOK_PATH` | `/webhook` |
| `--interval` | `HELPBOT_INTERVAL` | `60s` |
| `--state-file` | `HELPBOT_STATE_FILE` | `./gh-helpbot-state.json` (`/data/state.json` in the container) |
| `--since` | | `poll` start time when there is no state file; default now |
| `--once` | | `poll` one pass and exit |
| `--max-iterations` | | 16 |
| `--timeout` | | 600 seconds per question |
| `--docs-url` | `HELPBOT_DOCS_URL` | the docs link in the footer |
| `--dry-run` | | print replies instead of posting |

The timeout covers a whole answer, tool calls included. When the model has
been unloaded by a `ttl`, llama-swap holds the first request while it loads
again, so give the bot's model a generous `ttl` or none at all.

## Running the container

Build from the repository root; the bot shares code with `ui/`:

```sh
docker build -f cmd/gh-helpbot/Dockerfile -t gh-helpbot .
```

or `make gh-helpbot-image`.

### serve (webhook)

```sh
docker run -d --name helpbot -p 8085:8085 \
  -e GITHUB_TOKEN=github_pat_... \
  -e GITHUB_WEBHOOK_SECRET=choose-a-long-random-string \
  -e LLAMA_SWAP_URL=http://llama-box:8080 \
  -e DOCS_AGENT_MODEL=your-model \
  -e HELPBOT_REPOS=mostlygeek/llama-swap \
  gh-helpbot
```

Then on GitHub, under the repository's *Settings, Webhooks, Add webhook*:

- Payload URL: `https://your-host/webhook` (put a reverse proxy with TLS, or a
  tunnel, in front of port 8085)
- Content type: `application/json`
- Secret: the same value as `GITHUB_WEBHOOK_SECRET`
- Events: *Let me select individual events*, then Issues, Issue comments,
  Discussions and Discussion comments

`GET /healthz` answers 200 for the container's health check. Deliveries are
acknowledged at once and answered in the background, one at a time. On
`docker stop` the bot finishes the answer in progress and logs any mention it
had queued but not answered; redeliver those from the webhook's *Recent
Deliveries* page.

### poll

```sh
docker run -d --name helpbot --no-healthcheck \
  -v helpbot-data:/data \
  -e GITHUB_TOKEN=github_pat_... \
  -e LLAMA_SWAP_URL=http://llama-box:8080 \
  -e DOCS_AGENT_MODEL=your-model \
  gh-helpbot poll --repo mostlygeek/llama-swap --interval 60s
```

The state file on the `/data` volume remembers where the last pass ended and
what was answered. Without it the bot starts from *now* (or `--since`) and
does not go back through old mentions. Losing the file is safe: a reply
already posted carries a marker, and a mention whose reply is in the thread is
never answered again.

`poll --once` runs a single pass and exits, for cron.

### action

Copy `examples/helpbot-action.yml` to `.github/workflows/` in the repository
that should get answers and set the secrets it names. The job needs a runner
that can reach llama-swap, which for a box on your LAN means a self-hosted
runner. The workflow token posts as `github-actions[bot]`.

## Running without the container

```sh
cd cmd/gh-helpbot
npm ci
npm run start -- serve --model your-model      # or poll, action
npm run build && node dist/gh-helpbot.js --help
```

`npm run build` bundles everything, including the shared `ui/src` code, into
`dist/gh-helpbot.js`. The bundle needs only Node 22 or newer.

## Testing a setup

- `ask` talks to the agent and prints the answer with its tool calls; it does
  not touch GitHub.
- `replay` runs a saved webhook payload through the whole path. With
  `--dry-run` it prints the reply instead of posting and needs no token:

  ```sh
  npm run start -- replay testdata/issue_comment.json --event issue_comment --dry-run --model your-model
  ```

- `serve --dry-run` receives real webhooks and prints what it would post.

## Notes

- All of the bot's llama-swap traffic carries one `X-Session-ID`, so it shows
  up as a single Playground session in llama-swap's activity view.
- `@help` is also an ordinary GitHub login. GitHub notifies that account on
  every mention. Use `--mention` to pick another word if that matters.
- The shared agent code lives in `ui/src/lib` and `ui/src/cli/headless.ts`;
  `make test-gh-helpbot` type-checks and tests the bot, and `make test-ui`
  covers the shared side.
