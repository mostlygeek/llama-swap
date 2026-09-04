#!/usr/bin/env tsx
import { parseArgs } from "node:util";
import { readFile, writeFile, mkdir } from "node:fs/promises";
import path from "node:path";
import { fileURLToPath } from "node:url";

import { installFetchBase } from "./fetchBase";
import { loadCases, type EvalCase } from "./cases";
import {
  gradeRun,
  summarizeCase,
  summarizeSuite,
  type RunRecord,
  type ToolInvocation,
  type CaseResult,
} from "./grade";
import { formatSummary, formatCaseLine, formatFailureReport } from "./report";
import { judgeCase, summarizeJudge, type JudgeVerdict } from "./judge";
import { recordRun, evaluateStop, DEFAULT_HISTORY } from "./history";

import { runAgent, DEFAULT_MAX_ITERATIONS } from "../lib/agentLoop";
import { streamChatCompletion } from "../lib/chatApi";
import { fetchToolDefinitions, callTool } from "../lib/agentTools";
import { DOCS_AGENT_SYSTEM_PROMPT } from "../lib/prompts/docsAgent";
import type { ChatMessage } from "../lib/types";

/**
 * Headless driver for the Docs Agent behind the UI's Help page.
 *
 * The whole point is that this imports agentLoop.ts, chatApi.ts and
 * agentTools.ts unmodified, so what is measured here is exactly what the
 * browser runs. installFetchBase() is the only adaptation, and it lives
 * outside src/lib for that reason. See evals/docs-agent/README.md.
 */

const HERE = path.dirname(fileURLToPath(import.meta.url));
const REPO_ROOT = path.resolve(HERE, "../../..");
const DEFAULT_CASES = path.join(REPO_ROOT, "evals/docs-agent/cases");
const DEFAULT_RUNS = path.join(REPO_ROOT, "evals/docs-agent/runs");

const DEFAULT_BASE_URL = process.env.LLAMA_SWAP_URL ?? "http://localhost:8080";
const DEFAULT_MODEL = process.env.DOCS_AGENT_MODEL ?? "sippy/gemma-4-12B";
const DEFAULT_JUDGE_MODEL =
  process.env.DOCS_AGENT_JUDGE_MODEL ?? "sippy/gptoss-120B";

function die(message: string, code = 1): never {
  process.stderr.write(`error: ${message}\n`);
  process.exit(code);
}

const USAGE = `Usage: npm run agent -- <command> [options]

Commands:
  ask <question>     Ask one question and print the answer and tool trace.
  eval               Run the case suite and score it deterministically.
  judge              Score a saved results file with an LLM judge.

Common options:
  --base-url URL     llama-swap serving both /v1 and /api/mcp (default ${DEFAULT_BASE_URL})
  --model NAME       agent model (default ${DEFAULT_MODEL})
  --api-key KEY      bearer token, if the server sets apiKeys
  --system-prompt F  file to use instead of DOCS_AGENT_SYSTEM_PROMPT ("none" for empty)
  --max-iterations N agent loop ceiling (default ${DEFAULT_MAX_ITERATIONS})
  --temperature N    default 0
  --max-tokens N     default 2048
  --timeout N        per-turn seconds (default 300)

eval options:
  --cases DIR        default evals/docs-agent/cases
  --repeat N         attempts per case, default 1; use 3 to decide on a change
  --concurrency N    cases to run at once, default 1
  --only PATTERN     run only cases whose id matches this regex
  --group NAME       run a group; repeat to run the union of groups
  --out FILE         results JSON (default evals/docs-agent/runs/<ts>-<label>.json)
  --report FILE      markdown digest of failures (default alongside --out)
  --label NAME       names the run in output files
  --history FILE     append one entry per run (default evals/docs-agent/runs/history.md)
  --no-history       do not append to the history log
  --stop-after N     warn when N runs in a row fail to beat the best (default 5)

judge options:
  --results FILE     results JSON from a previous eval (required)
  --judge-model NAME default ${DEFAULT_JUDGE_MODEL}
  --judge-base-url U default: same as --base-url
  --judge-api-key K
  --min-score N      refuse to judge below this deterministic pass rate (0-1)
`;

interface CommonOpts {
  baseUrl: string;
  model: string;
  apiKey?: string;
  systemPrompt: string;
  systemPromptSource: string;
  maxIterations: number;
  temperature: number;
  maxTokens: number;
  timeoutMs: number;
}

type CLIValues = Record<string, string | string[] | boolean | undefined>;

async function resolveSystemPrompt(
  file: string | undefined,
): Promise<{ text: string; source: string }> {
  if (file === undefined)
    return {
      text: DOCS_AGENT_SYSTEM_PROMPT,
      source: "DOCS_AGENT_SYSTEM_PROMPT",
    };
  if (file === "none") return { text: "", source: "(empty - control run)" };
  const text = await readFile(file, "utf8");
  // /dev/null and an empty file are the documented way to run the control.
  return { text, source: text.trim() ? file : `${file} (empty - control run)` };
}

function intOpt(
  raw: string | undefined,
  fallback: number,
  name: string,
): number {
  if (raw === undefined) return fallback;
  const value = Number(raw);
  if (!Number.isFinite(value)) die(`${name} must be a number`);
  return value;
}

function positiveIntOpt(
  raw: string | undefined,
  fallback: number,
  name: string,
): number {
  const value = intOpt(raw, fallback, name);
  if (!Number.isInteger(value) || value < 1)
    die(`${name} must be a positive integer`);
  return value;
}

async function common(values: CLIValues): Promise<CommonOpts> {
  const prompt = await resolveSystemPrompt(
    values["system-prompt"] as string | undefined,
  );
  return {
    baseUrl: (values["base-url"] as string) ?? DEFAULT_BASE_URL,
    model: (values.model as string) ?? DEFAULT_MODEL,
    apiKey: values["api-key"] as string | undefined,
    systemPrompt: prompt.text,
    systemPromptSource: prompt.source,
    maxIterations: intOpt(
      values["max-iterations"] as string,
      DEFAULT_MAX_ITERATIONS,
      "--max-iterations",
    ),
    temperature: intOpt(values.temperature as string, 0, "--temperature"),
    maxTokens: intOpt(values["max-tokens"] as string, 2048, "--max-tokens"),
    timeoutMs: intOpt(values.timeout as string, 300, "--timeout") * 1000,
  };
}

/**
 * Fails loudly when the server has no MCP tools.
 *
 * fetchToolDefinitions() swallows a 404 and a 503 and returns [], because in
 * the browser that correctly means "this build has no docs, hide agent mode".
 * Here it would mean the whole suite silently ran with no tools and scored
 * terribly for a reason unrelated to whatever was being tested -- and pointing
 * --base-url at a release build that predates /api/mcp is an easy mistake.
 */
async function preflight(opts: CommonOpts) {
  const hint =
    `  The server at ${opts.baseUrl} must be built from this branch.\n` +
    `  A release build predating /api/mcp answers 404 there, and a build with no\n` +
    `  indexed documentation answers 503.\n` +
    `  Start one with: go run . -config <your config> -listen :8080`;

  let tools;
  try {
    // A 404 throws here; a 503 and an unsupported protocol version are
    // swallowed and come back as [], because in the browser both correctly
    // mean "this build has no docs, hide agent mode". Neither is survivable
    // for an eval: the suite would run with no tools and score terribly for a
    // reason unrelated to whatever was being tested.
    tools = await fetchToolDefinitions();
  } catch (error) {
    die(
      `cannot reach ${opts.baseUrl}/api/mcp: ${error instanceof Error ? error.message : String(error)}\n${hint}`,
    );
  }
  if (!tools.length) {
    die(`no MCP tools at ${opts.baseUrl}/api/mcp.\n${hint}`);
  }
  return tools;
}

async function runOnce(
  question: string,
  opts: CommonOpts,
  tools: Awaited<ReturnType<typeof preflight>>,
  onDelta?: (text: string) => void,
): Promise<RunRecord> {
  const seed: ChatMessage[] = [];
  if (opts.systemPrompt.trim())
    seed.push({ role: "system", content: opts.systemPrompt.trim() });
  seed.push({ role: "user", content: question });

  const controller = new AbortController();
  const timer = setTimeout(() => controller.abort(), opts.timeoutMs);
  const startedAt = Date.now();

  let answer = "";
  let reasoning = "";
  let iterations = 0;
  let doneReason = "error";
  let error: string | undefined;
  const toolCalls: ToolInvocation[] = [];

  try {
    const deps = {
      streamChat: (msgs: ChatMessage[], signal: AbortSignal) =>
        streamChatCompletion(opts.model, msgs, signal, {
          temperature: opts.temperature,
          max_tokens: opts.maxTokens,
          tools,
        }),
      callTool,
    };

    for await (const event of runAgent(seed, deps, {
      maxIterations: opts.maxIterations,
      signal: controller.signal,
    })) {
      switch (event.type) {
        case "iteration":
          iterations = event.n;
          break;
        case "content":
          answer += event.delta;
          onDelta?.(event.delta);
          break;
        case "reasoning":
          reasoning += event.delta;
          break;
        case "assistant_end":
          // Only the final assistant message is the answer; earlier ones are
          // the model narrating its tool use.
          if (
            !event.message.tool_calls?.length &&
            typeof event.message.content === "string"
          ) {
            answer = event.message.content;
          }
          break;
        case "tool_end":
          toolCalls.push({
            name: event.call.function.name,
            args: event.call.function.arguments,
            ok: event.ok,
            durationMs: event.durationMs,
          });
          break;
        case "error":
          error = event.message;
          break;
        case "done":
          doneReason = event.reason;
          break;
      }
    }
  } finally {
    clearTimeout(timer);
  }

  if (doneReason === "aborted" && !error)
    error = `timed out after ${opts.timeoutMs / 1000}s`;

  return {
    caseId: "",
    attempt: 1,
    answer,
    reasoning,
    toolCalls,
    iterations,
    doneReason,
    durationMs: Date.now() - startedAt,
    error,
  };
}

async function cmdAsk(question: string, values: CLIValues) {
  const opts = await common(values);
  const restore = installFetchBase({
    baseUrl: opts.baseUrl,
    apiKey: opts.apiKey,
  });
  try {
    const tools = await preflight(opts);
    process.stderr.write(
      `model ${opts.model} @ ${opts.baseUrl}, ${tools.length} tools, prompt: ${opts.systemPromptSource}\n\n`,
    );

    const run = await runOnce(question, opts, tools, (delta) =>
      process.stdout.write(delta),
    );
    process.stdout.write("\n");

    if (run.toolCalls.length) {
      process.stderr.write("\ntools:\n");
      for (const [i, t] of run.toolCalls.entries()) {
        process.stderr.write(
          `  ${i + 1}. ${t.name}(${t.args})${t.ok ? "" : "  <- tool error"}  ${t.durationMs}ms\n`,
        );
      }
    } else {
      process.stderr.write("\ntools: none called\n");
    }
    process.stderr.write(
      `\n${run.iterations} iteration(s), ${(run.durationMs / 1000).toFixed(1)}s, ${run.doneReason}\n`,
    );
    if (run.error) die(run.error);
  } finally {
    restore();
  }
}

async function cmdEval(values: CLIValues) {
  const opts = await common(values);
  const casesDir = (values.cases as string) ?? DEFAULT_CASES;
  const repeat = intOpt(values.repeat as string, 1, "--repeat");
  const concurrency = positiveIntOpt(
    values.concurrency as string,
    1,
    "--concurrency",
  );
  const label = (values.label as string) ?? "run";

  let cases = await loadCases(casesDir);
  const groups =
    values.group === undefined
      ? []
      : Array.isArray(values.group)
        ? (values.group as string[])
        : [values.group as string];
  const availableGroups = [...new Set(cases.map((c) => c.group))].sort();
  if (groups.length) {
    const unknown = groups.filter((group) => !availableGroups.includes(group));
    if (unknown.length)
      die(
        `unknown --group ${unknown.join(", ")}; available groups: ${availableGroups.join(", ")}`,
      );
    cases = cases.filter((c) => groups.includes(c.group));
    if (!cases.length)
      die(
        `--group selected no cases; available groups: ${availableGroups.join(", ")}`,
      );
  }
  if (values.only) {
    const pattern = new RegExp(values.only as string);
    cases = cases.filter((c) => pattern.test(c.id));
    if (!cases.length) die(`--only ${values.only} matched no cases`);
  }

  const stamp = new Date().toISOString().replace(/[:.]/g, "-").slice(0, 19);
  const outPath =
    (values.out as string) ?? path.join(DEFAULT_RUNS, `${stamp}-${label}.json`);
  const reportPath =
    (values.report as string) ?? outPath.replace(/\.json$/, "") + ".md";

  const restore = installFetchBase({
    baseUrl: opts.baseUrl,
    apiKey: opts.apiKey,
  });
  try {
    const tools = await preflight(opts);
    process.stderr.write(
      `${cases.length} cases x ${repeat}, concurrency ${concurrency} against ${opts.model} @ ${opts.baseUrl}\n` +
        `${tools.length} tools, prompt: ${opts.systemPromptSource}\n\n`,
    );

    let nextCase = 0;
    const results: CaseResult[] = new Array(cases.length);
    const worker = async () => {
      while (nextCase < cases.length) {
        const index = nextCase++;
        const c = cases[index];
        if (!c) continue;

        const runs = [];
        for (let attempt = 1; attempt <= repeat; attempt++) {
          const record = await runOnce(c.question, opts, tools);
          runs.push(gradeRun(c, { ...record, caseId: c.id, attempt }));
        }
        const result = summarizeCase(c, runs);
        results[index] = result;
        process.stderr.write(
          `[${String(index + 1).padStart(3)}/${cases.length}]${formatCaseLine(result)}\n`,
        );
      }
    };
    await Promise.all(
      Array.from({ length: Math.min(concurrency, cases.length) }, worker),
    );

    const summary = summarizeSuite(results);
    process.stderr.write(formatSummary(summary, label));

    await mkdir(path.dirname(outPath), { recursive: true });
    await writeFile(
      outPath,
      JSON.stringify(
        {
          label,
          startedAt: stamp,
          model: opts.model,
          baseUrl: opts.baseUrl,
          systemPromptSource: opts.systemPromptSource,
          systemPrompt: opts.systemPrompt,
          casesDir,
          groups,
          repeat,
          concurrency,
          summary,
          results,
        },
        null,
        2,
      ),
    );
    await writeFile(
      reportPath,
      formatFailureReport({
        label,
        model: opts.model,
        baseUrl: opts.baseUrl,
        systemPromptSource: opts.systemPromptSource,
        groups,
        summary,
        results,
      }),
    );

    process.stderr.write(
      `results  ${path.relative(process.cwd(), outPath)}\nfailures ${path.relative(process.cwd(), reportPath)}\n`,
    );

    if (!values["no-history"]) {
      const historyFile = (values.history as string) ?? DEFAULT_HISTORY;
      const rows = await recordRun(historyFile, {
        timestamp: new Date().toISOString(),
        label,
        repeat,
        model: opts.model,
        casesDir,
        promptSource: opts.systemPromptSource,
        summary,
        results,
        only: values.only as string | undefined,
        groups,
      });
      process.stderr.write(`history  ${historyFile}\n`);

      // The loop is long and spans sessions, so the stop rule is evaluated
      // from the durable log rather than from anything held in memory.
      const limit = intOpt(values["stop-after"] as string, 5, "--stop-after");
      const stop = evaluateStop(
        rows,
        casesDir,
        limit,
        values.only as string | undefined,
        groups,
      );
      if (stop.shouldStop) {
        process.stderr.write(
          `\n  STOP: ${stop.sinceBest} runs in a row have not beaten ${(stop.best * 100).toFixed(1)}% (${stop.bestLabel}).\n` +
            `  Diminishing returns on ${path.basename(casesDir)}. Report what was tried and stop tuning.\n`,
        );
      } else if (stop.sinceBest > 0) {
        process.stderr.write(
          `  best ${(stop.best * 100).toFixed(1)}% (${stop.bestLabel}); ${stop.sinceBest}/${limit} runs since it was beaten\n`,
        );
      }
    }
    process.stderr.write("\n");
  } finally {
    restore();
  }
}

async function cmdJudge(values: CLIValues) {
  const resultsPath = values.results as string | undefined;
  if (!resultsPath) die("--results is required");

  const saved = JSON.parse(await readFile(resultsPath, "utf8")) as {
    model: string;
    casesDir: string;
    groups?: string[];
    summary: { passRate: number };
    results: CaseResult[];
  };

  const minScore = intOpt(values["min-score"] as string, 0, "--min-score");
  if (saved.summary.passRate < minScore) {
    die(
      `deterministic pass rate ${(saved.summary.passRate * 100).toFixed(1)}% is below --min-score ${(minScore * 100).toFixed(1)}%.\n` +
        `  Fix the failing assertions before spending judge tokens.`,
      2,
    );
  }

  const baseUrl =
    (values["judge-base-url"] as string) ??
    (values["base-url"] as string) ??
    DEFAULT_BASE_URL;
  const judgeModel = (values["judge-model"] as string) ?? DEFAULT_JUDGE_MODEL;
  if (judgeModel === saved.model) {
    die(
      `--judge-model is the same as the agent model (${judgeModel}); a model grading itself ratifies its own mistakes`,
    );
  }

  const cases = await loadCases(saved.casesDir);
  const byId = new Map(cases.map((c) => [c.id, c]));
  const judgeOpts = {
    baseUrl,
    model: judgeModel,
    apiKey:
      (values["judge-api-key"] as string) ??
      (values["api-key"] as string | undefined),
    timeoutMs: intOpt(values.timeout as string, 300, "--timeout") * 1000,
  };

  const judgeable = saved.results.filter(
    (r) => (byId.get(r.caseId)?.rubric.length ?? 0) > 0,
  );
  process.stderr.write(
    `judging ${judgeable.length} cases with ${judgeModel} @ ${baseUrl}\n\n`,
  );

  const verdicts: JudgeVerdict[] = [];
  for (const [index, result] of judgeable.entries()) {
    const c = byId.get(result.caseId) as EvalCase;
    const verdict = await judgeCase(c, result, judgeOpts);
    verdicts.push(verdict);
    const passed = verdict.rubric.filter((r) => r.ok).length;
    process.stderr.write(
      `[${String(index + 1).padStart(3)}/${judgeable.length}]  ${verdict.score.toFixed(1)}/5  ${passed}/${verdict.rubric.length}  ${verdict.caseId}` +
        `${verdict.error ? `  <- ${verdict.error}` : ""}\n`,
    );
  }

  const summary = summarizeJudge(verdicts);
  process.stderr.write(
    `\n  mean score       ${summary.meanScore.toFixed(2)}/5\n` +
      `  rubric pass rate ${(summary.rubricPassRate * 100).toFixed(1)}%\n` +
      `  judge errors     ${summary.errors}\n\n`,
  );

  const merged = { ...saved, judge: { model: judgeModel, summary, verdicts } };
  await writeFile(resultsPath, JSON.stringify(merged, null, 2));
  process.stderr.write(
    `merged into ${path.relative(process.cwd(), resultsPath)}\n`,
  );
}

async function main() {
  const { values, positionals } = parseArgs({
    allowPositionals: true,
    strict: true,
    options: {
      "base-url": { type: "string" },
      model: { type: "string" },
      "api-key": { type: "string" },
      "system-prompt": { type: "string" },
      "max-iterations": { type: "string" },
      temperature: { type: "string" },
      "max-tokens": { type: "string" },
      timeout: { type: "string" },
      cases: { type: "string" },
      repeat: { type: "string" },
      concurrency: { type: "string" },
      only: { type: "string" },
      group: { type: "string", multiple: true },
      out: { type: "string" },
      report: { type: "string" },
      label: { type: "string" },
      history: { type: "string" },
      "no-history": { type: "boolean" },
      "stop-after": { type: "string" },
      results: { type: "string" },
      "judge-model": { type: "string" },
      "judge-base-url": { type: "string" },
      "judge-api-key": { type: "string" },
      "min-score": { type: "string" },
      help: { type: "boolean", short: "h" },
    },
  });

  if (values.help || !positionals.length) {
    process.stdout.write(USAGE);
    return;
  }

  const [command, ...rest] = positionals;
  switch (command) {
    case "ask": {
      const question = rest.join(" ").trim();
      if (!question) die("ask needs a question");
      return cmdAsk(question, values);
    }
    case "eval":
      return cmdEval(values);
    case "judge":
      return cmdJudge(values);
    default:
      die(`unknown command ${JSON.stringify(command)}\n\n${USAGE}`);
  }
}

main().catch((error) =>
  die(error instanceof Error ? error.message : String(error)),
);
