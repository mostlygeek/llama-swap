import { parseArgs } from "node:util";

import { DEFAULT_MAX_ITERATIONS } from "../../../ui/src/lib/agentLoop";

import { DEFAULT_MENTION } from "./mention";

/**
 * Flags and environment, resolved once. A flag wins over its variable.
 */

export type Command = "serve" | "poll" | "action" | "replay" | "ask";

export const COMMANDS: Command[] = ["serve", "poll", "action", "replay", "ask"];

export interface Config {
  command: Command;
  positionals: string[];
  help: boolean;

  // GitHub
  token?: string;
  webhookSecret?: string;
  repos: string[];
  selfLogin?: string;
  mention: string;

  // serve
  listen: string;
  webhookPath: string;

  // poll
  intervalMs: number;
  stateFile: string;
  since?: string;
  once: boolean;

  // replay
  event?: string;

  // agent
  baseUrl: string;
  model?: string;
  apiKey?: string;
  maxIterations: number;
  timeoutMs: number;
  docsUrl?: string;

  dryRun: boolean;
}

export const DEFAULT_TIMEOUT_SECONDS = 600;

export const USAGE = `Usage: gh-helpbot <command> [options]

Answers @${DEFAULT_MENTION} questions on GitHub issues and discussions with
llama-swap's Help agent.

Commands:
  serve                Receive GitHub webhooks (default)
  poll                 Poll the GitHub API on an interval; no inbound port
  action               Handle the one event of a GitHub Actions job
  replay <file>        Handle a saved webhook payload (with --event)
  ask <question>       Ask the agent directly, without GitHub

GitHub (env in brackets):
  --repo owner/name    repeatable; required for poll, an allowlist otherwise [HELPBOT_REPOS, comma separated]
  --mention NAME       trigger word after @ (default ${DEFAULT_MENTION}) [HELPBOT_MENTION]
  --self-login LOGIN   never answer this login; default: the token's own [HELPBOT_SELF_LOGIN]
                       [GITHUB_TOKEN] a token with Issues and Discussions read/write
                       [GITHUB_WEBHOOK_SECRET] the secret set on the webhook (serve)

serve:
  --listen HOST:PORT   default 0.0.0.0:8085 [HELPBOT_LISTEN]
  --path PATH          webhook URL path, default /webhook [HELPBOT_WEBHOOK_PATH]

poll:
  --interval DURATION  default 60s; accepts 30s, 5m, 1h [HELPBOT_INTERVAL]
  --state-file FILE    default ./gh-helpbot-state.json [HELPBOT_STATE_FILE]
  --since TIME         ISO time to start from when there is no state file (default: now)
  --once               one pass, then exit

replay:
  --event NAME         the X-GitHub-Event of the saved payload, e.g. issue_comment

Agent:
  --base-url URL       llama-swap serving /v1 and /api/mcp, default http://localhost:8080 [LLAMA_SWAP_URL]
  --model NAME         required [DOCS_AGENT_MODEL]
  --api-key KEY        bearer token when llama-swap sets apiKeys [LLAMA_SWAP_API_KEY]
  --max-iterations N   agent loop ceiling, default ${DEFAULT_MAX_ITERATIONS}
  --timeout SECONDS    per question, default ${DEFAULT_TIMEOUT_SECONDS}
  --docs-url URL       documentation link in the reply footer [HELPBOT_DOCS_URL]

  --dry-run            print replies instead of posting them; no GITHUB_TOKEN needed
  -h, --help
`;

export function parseDuration(text: string, name: string): number {
  const match = /^(\d+(?:\.\d+)?)\s*(ms|s|m|h)?$/.exec(text.trim());
  if (!match) throw new Error(`${name}: ${JSON.stringify(text)} is not a duration (try 30s, 5m, 1h)`);
  const value = Number(match[1]);
  const unit = match[2] ?? "s";
  const factor = { ms: 1, s: 1000, m: 60_000, h: 3_600_000 }[unit] as number;
  return Math.round(value * factor);
}

function positiveInt(raw: string | undefined, fallback: number, name: string): number {
  if (raw === undefined) return fallback;
  const value = Number(raw);
  if (!Number.isInteger(value) || value < 1) throw new Error(`${name} must be a positive integer`);
  return value;
}

function splitList(raw: string | undefined): string[] {
  return (raw ?? "")
    .split(",")
    .map((s) => s.trim())
    .filter(Boolean);
}

export function parseConfig(argv: string[], env: NodeJS.ProcessEnv = process.env): Config {
  const { values, positionals } = parseArgs({
    args: argv,
    allowPositionals: true,
    strict: true,
    options: {
      repo: { type: "string", multiple: true },
      mention: { type: "string" },
      "self-login": { type: "string" },
      listen: { type: "string" },
      path: { type: "string" },
      interval: { type: "string" },
      "state-file": { type: "string" },
      since: { type: "string" },
      once: { type: "boolean" },
      event: { type: "string" },
      "base-url": { type: "string" },
      model: { type: "string" },
      "api-key": { type: "string" },
      "max-iterations": { type: "string" },
      timeout: { type: "string" },
      "docs-url": { type: "string" },
      "dry-run": { type: "boolean" },
      help: { type: "boolean", short: "h" },
    },
  });

  const [first, ...rest] = positionals;
  let command: Command = "serve";
  let commandArgs = positionals;
  if (first !== undefined) {
    if (!COMMANDS.includes(first as Command)) throw new Error(`unknown command ${JSON.stringify(first)}\n\n${USAGE}`);
    command = first as Command;
    commandArgs = rest;
  }

  const since = values.since;
  if (since !== undefined && Number.isNaN(Date.parse(since))) throw new Error(`--since ${JSON.stringify(since)} is not a timestamp`);

  const timeoutSeconds = values.timeout === undefined ? DEFAULT_TIMEOUT_SECONDS : Number(values.timeout);
  if (!Number.isFinite(timeoutSeconds) || timeoutSeconds <= 0) throw new Error("--timeout must be a positive number of seconds");

  return {
    command,
    positionals: commandArgs,
    help: Boolean(values.help),

    token: env.GITHUB_TOKEN || undefined,
    webhookSecret: env.GITHUB_WEBHOOK_SECRET || undefined,
    repos: values.repo?.length ? values.repo : splitList(env.HELPBOT_REPOS),
    selfLogin: values["self-login"] ?? env.HELPBOT_SELF_LOGIN ?? undefined,
    mention: (values.mention ?? env.HELPBOT_MENTION ?? DEFAULT_MENTION).replace(/^@/, ""),

    listen: values.listen ?? env.HELPBOT_LISTEN ?? "0.0.0.0:8085",
    webhookPath: values.path ?? env.HELPBOT_WEBHOOK_PATH ?? "/webhook",

    intervalMs: parseDuration(values.interval ?? env.HELPBOT_INTERVAL ?? "60s", "--interval"),
    stateFile: values["state-file"] ?? env.HELPBOT_STATE_FILE ?? "./gh-helpbot-state.json",
    since,
    once: Boolean(values.once),

    event: values.event,

    baseUrl: (values["base-url"] ?? env.LLAMA_SWAP_URL ?? "http://localhost:8080").replace(/\/+$/, ""),
    model: values.model ?? env.DOCS_AGENT_MODEL ?? undefined,
    apiKey: values["api-key"] ?? env.LLAMA_SWAP_API_KEY ?? undefined,
    maxIterations: positiveInt(values["max-iterations"], DEFAULT_MAX_ITERATIONS, "--max-iterations"),
    timeoutMs: Math.round(timeoutSeconds * 1000),
    docsUrl: values["docs-url"] ?? env.HELPBOT_DOCS_URL ?? undefined,

    dryRun: Boolean(values["dry-run"]),
  };
}

/** What each command needs before it can start. Returns the complaint, or nothing. */
export function validate(cfg: Config): string | undefined {
  if (!cfg.model) return "--model (or DOCS_AGENT_MODEL) is required";
  const needsGitHub = cfg.command !== "ask" && !cfg.dryRun;
  if (needsGitHub && !cfg.token) return "GITHUB_TOKEN is required (or use --dry-run)";
  switch (cfg.command) {
    case "serve":
      if (!cfg.webhookSecret) return "GITHUB_WEBHOOK_SECRET is required for serve";
      if (!/^[^:]+:\d+$/.test(cfg.listen)) return `--listen ${JSON.stringify(cfg.listen)} must be HOST:PORT`;
      if (!cfg.webhookPath.startsWith("/")) return "--path must start with /";
      break;
    case "poll":
      if (!cfg.repos.length) return "poll needs at least one --repo owner/name";
      break;
    case "replay":
      if (!cfg.positionals[0]) return "replay needs a payload file";
      if (!cfg.event) return "replay needs --event, the X-GitHub-Event of the payload";
      break;
    case "ask":
      if (!cfg.positionals.join(" ").trim()) return "ask needs a question";
      break;
  }
  return undefined;
}
