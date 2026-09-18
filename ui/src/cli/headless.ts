import { runAgent, DEFAULT_MAX_ITERATIONS } from "../lib/agentLoop";
import { streamChatCompletion, type ToolDefinition } from "../lib/chatApi";
import { fetchToolDefinitions, callTool } from "../lib/agentTools";
import { DOCS_AGENT_SYSTEM_PROMPT } from "../lib/prompts/docsAgent";
import type { ChatMessage } from "../lib/types";

/**
 * The Docs Agent, driven headlessly from Node.
 *
 * This is the one place outside the browser that runs the Help page's agent:
 * the eval CLI (docsAgent.ts) and the GitHub bot (cmd/gh-helpbot) both call
 * askDocsAgent(). It imports agentLoop.ts, chatApi.ts and agentTools.ts
 * unmodified, so what either of them runs is exactly what the browser runs.
 *
 * Keep this module free of imports from the rest of src/cli: the bot is built
 * from src/lib plus this file and fetchBase.ts alone, and cases.ts pulls in
 * the `yaml` package which the bot does not install.
 *
 * Callers install fetchBase.ts first; nothing here knows about base URLs or
 * API keys beyond the text of the startup hint.
 */

/** One tool invocation, as recorded from the agent event stream. */
export interface ToolInvocation {
  name: string;
  args: string;
  ok: boolean;
  durationMs: number;
}

/** Everything one headless run produced. */
export interface HeadlessRun {
  answer: string;
  reasoning: string;
  toolCalls: ToolInvocation[];
  iterations: number;
  /** The agent loop's own termination reason: stop | max_iterations | aborted | error. */
  doneReason: string;
  durationMs: number;
  error?: string;
}

export interface HeadlessOptions {
  model: string;
  /** Empty or whitespace means no system message (a control run). */
  systemPrompt?: string;
  maxIterations?: number;
  temperature?: number;
  maxTokens?: number;
  /** Ceiling for the whole turn, tool calls included. */
  timeoutMs?: number;
}

export const DEFAULT_TIMEOUT_MS = 300_000;

/**
 * Lists the server's MCP tools and fails loudly when there are none.
 *
 * fetchToolDefinitions() swallows a 503 and an unsupported protocol version
 * and returns [], because in the browser that correctly means "this build has
 * no docs, hide agent mode". Headless it would mean every question is
 * answered from memory with no docs behind it -- and pointing at a release
 * build that predates /api/mcp is an easy mistake.
 */
export async function loadTools(baseUrl: string): Promise<ToolDefinition[]> {
  const hint =
    `  The server at ${baseUrl} must be built from this branch.\n` +
    `  A release build predating /api/mcp answers 404 there, and a build with no\n` +
    `  indexed documentation answers 503.\n` +
    `  Start one with: go run . -config <your config> -listen :8080`;

  let tools: ToolDefinition[];
  try {
    tools = await fetchToolDefinitions();
  } catch (error) {
    throw new Error(
      `cannot reach ${baseUrl}/api/mcp: ${error instanceof Error ? error.message : String(error)}\n${hint}`,
    );
  }
  if (!tools.length) {
    throw new Error(`no MCP tools at ${baseUrl}/api/mcp.\n${hint}`);
  }
  return tools;
}

/** Runs one question through the agent and collects what it produced. */
export async function askDocsAgent(
  question: string,
  opts: HeadlessOptions,
  tools: ToolDefinition[],
  onDelta?: (text: string) => void,
): Promise<HeadlessRun> {
  const systemPrompt = (opts.systemPrompt ?? DOCS_AGENT_SYSTEM_PROMPT).trim();
  const timeoutMs = opts.timeoutMs ?? DEFAULT_TIMEOUT_MS;

  const seed: ChatMessage[] = [];
  if (systemPrompt) seed.push({ role: "system", content: systemPrompt });
  seed.push({ role: "user", content: question });

  const controller = new AbortController();
  const timer = setTimeout(() => controller.abort(), timeoutMs);
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
      maxIterations: opts.maxIterations ?? DEFAULT_MAX_ITERATIONS,
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
    error = `timed out after ${timeoutMs / 1000}s`;

  return {
    answer,
    reasoning,
    toolCalls,
    iterations,
    doneReason,
    durationMs: Date.now() - startedAt,
    error,
  };
}
