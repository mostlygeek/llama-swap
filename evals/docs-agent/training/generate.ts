#!/usr/bin/env tsx
/**
 * Records complete Docs Agent conversations for training data.
 *
 * The eval's result files keep tool names and arguments but not their output,
 * which is not enough to train on. This drives the same unmodified agentLoop.ts,
 * chatApi.ts and agentTools.ts as the eval, and writes every message.
 *
 * One JSON line is appended per question. Finished ids are skipped on a re-run.
 *
 *   cd ui && npx tsx ../evals/docs-agent/training/generate.ts \
 *     --base-url http://127.0.0.1:18081 \
 *     --model sippy/Q3.8-27B-rad \
 *     --questions ~/lfm3/data/questions.jsonl \
 *     --out ~/lfm3/data/conversations.jsonl
 */
import { parseArgs } from "node:util";
import { appendFile, mkdir, readFile, writeFile } from "node:fs/promises";
import { existsSync } from "node:fs";
import path from "node:path";

import { installFetchBase } from "../../../ui/src/cli/fetchBase";
import { runAgent } from "../../../ui/src/lib/agentLoop";
import { streamChatCompletion } from "../../../ui/src/lib/chatApi";
import { callTool, fetchToolDefinitions } from "../../../ui/src/lib/agentTools";
import { DOCS_AGENT_SYSTEM_PROMPT } from "../../../ui/src/lib/prompts/docsAgent";
import type { ChatMessage } from "../../../ui/src/lib/types";

interface Question {
  id: string;
  source: string;
  kind: string;
  question: string;
}

const { values } = parseArgs({
  options: {
    "base-url": { type: "string" },
    model: { type: "string" },
    questions: { type: "string" },
    out: { type: "string" },
    concurrency: { type: "string", default: "4" },
    "max-iterations": { type: "string", default: "12" },
    timeout: { type: "string", default: "900" },
    limit: { type: "string" },
    "record-as": { type: "string" },
  },
});

for (const name of ["base-url", "model", "questions", "out"] as const) {
  if (!values[name]) {
    console.error(`error: --${name} is required`);
    process.exit(1);
  }
}

const baseUrl = values["base-url"]!;
const model = values.model!;
// The name written to the conversations, and from there to the manifest.
const recordedModel = values["record-as"] ?? model;
const out = values.out!;
const maxIterations = Number(values["max-iterations"]);
const timeoutMs = Number(values.timeout) * 1000;

const isRateLimit = (error: unknown) => String(error).includes("Chat API error: 429");

/** A provider's concurrency limit is not the teacher's mistake, so wait it out. */
async function* withRetry(open: () => ReturnType<typeof streamChatCompletion>) {
  for (let attempt = 0; ; attempt++) {
    try {
      yield* open();
      return;
    } catch (error) {
      if (!isRateLimit(error) || attempt === 8) throw error;
      const delay = Math.min(2 ** attempt, 30) * (1 + Math.random());
      await new Promise((resolve) => setTimeout(resolve, delay * 1000));
    }
  }
}

function readJsonl<T>(text: string): T[] {
  return text
    .split("\n")
    .filter((line) => line.trim())
    .map((line) => JSON.parse(line) as T);
}

async function record(item: Question, tools: Awaited<ReturnType<typeof fetchToolDefinitions>>) {
  const messages: ChatMessage[] = [
    { role: "system", content: DOCS_AGENT_SYSTEM_PROMPT.trim() },
    { role: "user", content: item.question },
  ];
  const controller = new AbortController();
  const timer = setTimeout(() => controller.abort(), timeoutMs);
  const startedAt = Date.now();
  let iterations = 0;
  let doneReason = "error";
  let error: string | undefined;

  try {
    const deps = {
      streamChat: (msgs: ChatMessage[], signal: AbortSignal) =>
        withRetry(() => streamChatCompletion(model, msgs, signal, { tools })),
      callTool,
    };
    for await (const event of runAgent(messages, deps, {
      maxIterations,
      signal: controller.signal,
    })) {
      if (event.type === "iteration") iterations = event.n;
      if (event.type === "assistant_end") messages.push(event.message);
      if (event.type === "tool_end") messages.push(event.message);
      if (event.type === "error") error = event.message;
      if (event.type === "done") doneReason = event.reason;
    }
  } finally {
    clearTimeout(timer);
  }

  return {
    ...item,
    model: recordedModel,
    doneReason,
    error,
    iterations,
    durationMs: Date.now() - startedAt,
    messages,
  };
}

async function main() {
  installFetchBase({ baseUrl });
  await mkdir(path.dirname(out), { recursive: true });

  const done = new Set(
    existsSync(out)
      ? readJsonl<{ id: string }>(await readFile(out, "utf8")).map((r) => r.id)
      : [],
  );
  let queue = readJsonl<Question>(await readFile(values.questions!, "utf8")).filter(
    (q) => !done.has(q.id),
  );
  if (values.limit) queue = queue.slice(0, Number(values.limit));

  const tools = await fetchToolDefinitions();
  if (!tools.length) throw new Error(`no MCP tools at ${baseUrl}/api/mcp`);
  await writeFile(`${out}.tools.json`, JSON.stringify(tools, null, 2));

  const total = queue.length;
  let finished = 0;
  console.log(`${total} questions to run, ${done.size} already done`);

  async function worker() {
    for (;;) {
      const item = queue.shift();
      if (!item) return;
      try {
        const result = await record(item, tools);
        if (isRateLimit(result.error)) throw new Error(result.error);
        await appendFile(out, JSON.stringify(result) + "\n");
        finished++;
        console.log(
          `[${finished}/${total}] ${result.doneReason} ${result.iterations} steps ` +
            `${Math.round(result.durationMs / 1000)}s ${item.id}`,
        );
      } catch (error) {
        finished++;
        console.log(`[${finished}/${total}] FAILED ${item.id}: ${error}`);
      }
    }
  }

  await Promise.all(Array.from({ length: Number(values.concurrency) }, worker));
}

main().catch((error) => {
  console.error(error);
  process.exit(1);
});
