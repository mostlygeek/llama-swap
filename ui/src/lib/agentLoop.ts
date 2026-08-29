import type { ChatMessage, ToolCall } from "./types";
import type { StreamChunk, ToolCallDelta } from "./chatApi";
import type { ToolCallOutcome } from "./agentTools";

/**
 * The agentic chat loop, as a pure dependency-injected async generator.
 *
 * It holds no Svelte state, does no fetching and touches no DOM, so it runs
 * under the project's node-environment vitest with hand-written test doubles.
 * DocsInterface.svelte supplies the real streamChat and callTool.
 */

export const DEFAULT_MAX_ITERATIONS = 8;

export type AgentEvent =
  | { type: "content"; delta: string }
  | { type: "reasoning"; delta: string }
  | { type: "assistant_end"; message: ChatMessage }
  | { type: "tool_start"; call: ToolCall; iteration: number }
  | { type: "tool_end"; call: ToolCall; message: ChatMessage; ok: boolean; durationMs: number }
  | { type: "iteration"; n: number; max: number }
  | { type: "max_iterations"; n: number; pending: ToolCall[] }
  | { type: "error"; message: string }
  | { type: "done"; reason: "stop" | "max_iterations" | "aborted" | "error" };

export interface AgentDeps {
  streamChat: (messages: ChatMessage[], signal: AbortSignal) => AsyncIterable<StreamChunk>;
  callTool: (name: string, args: unknown, signal: AbortSignal) => Promise<ToolCallOutcome>;
  /** Injectable clock, so duration assertions are not wall-clock dependent. */
  now?: () => number;
}

export interface AgentOptions {
  maxIterations?: number;
  signal?: AbortSignal;
}

function isAbort(error: unknown): boolean {
  return error instanceof Error && error.name === "AbortError";
}

/**
 * Merges streamed tool-call fragments into `acc`, keyed by index.
 *
 * The rules here are all real backend behaviour rather than defensive padding:
 * llama.cpp emits `index` while some proxies omit it, and several backends
 * repeat the full function name on every chunk -- concatenating those produces
 * "get_docget_docget_doc", the most common bug in hand-rolled agent loops.
 * Only `arguments` is ever concatenated.
 */
export function accumulateToolCalls(acc: Map<number, Partial<ToolCall>>, deltas: ToolCallDelta[]): void {
  for (const delta of deltas) {
    let index = delta.index;

    if (index === undefined) {
      // No index: match an in-flight call by id, else fall back to slot 0.
      index = 0;
      if (delta.id) {
        for (const [key, value] of acc) {
          if (value.id === delta.id) {
            index = key;
            break;
          }
        }
      }
    }

    const existing = acc.get(index) ?? { type: "function", function: { name: "", arguments: "" } };
    const fn = existing.function ?? { name: "", arguments: "" };

    // First non-empty wins for identity fields.
    if (delta.id && !existing.id) existing.id = delta.id;
    if (delta.function?.name && !fn.name) fn.name = delta.function.name;
    if (delta.function?.arguments) fn.arguments = (fn.arguments ?? "") + delta.function.arguments;

    existing.type = "function";
    existing.function = fn;
    acc.set(index, existing);
  }
}

/** Turns the accumulator into complete tool calls, in index order. */
export function finalizeToolCalls(acc: Map<number, Partial<ToolCall>>): ToolCall[] {
  return [...acc.entries()]
    .sort((a, b) => a[0] - b[0])
    .filter(([, call]) => Boolean(call.function?.name))
    .map(([index, call]) => ({
      id: call.id || `call_${index}`,
      type: "function" as const,
      function: {
        name: call.function!.name,
        arguments: call.function!.arguments || "{}",
      },
    }));
}

/**
 * Runs the model, executes any tools it calls, and repeats until it answers
 * without calling one.
 *
 * Termination is driven by whether the model produced tool calls, not by
 * finish_reason: llama.cpp frequently reports "stop" alongside a populated
 * tool_calls array depending on the chat template.
 */
export async function* runAgent(
  seed: ChatMessage[],
  deps: AgentDeps,
  opts: AgentOptions = {}
): AsyncGenerator<AgentEvent> {
  const max = opts.maxIterations && opts.maxIterations > 0 ? opts.maxIterations : DEFAULT_MAX_ITERATIONS;
  const signal = opts.signal ?? new AbortController().signal;
  const now = deps.now ?? (() => Date.now());

  const messages = [...seed];

  for (let iteration = 1; ; iteration++) {
    if (signal.aborted) {
      yield { type: "done", reason: "aborted" };
      return;
    }

    yield { type: "iteration", n: iteration, max };

    const acc = new Map<number, Partial<ToolCall>>();
    let content = "";
    let reasoning = "";

    try {
      for await (const chunk of deps.streamChat(messages, signal)) {
        if (chunk.done) break;
        if (chunk.reasoning_content) {
          reasoning += chunk.reasoning_content;
          yield { type: "reasoning", delta: chunk.reasoning_content };
        }
        if (chunk.content) {
          content += chunk.content;
          yield { type: "content", delta: chunk.content };
        }
        if (chunk.tool_calls) {
          accumulateToolCalls(acc, chunk.tool_calls);
        }
      }
    } catch (error) {
      if (isAbort(error) || signal.aborted) {
        yield { type: "done", reason: "aborted" };
        return;
      }
      yield { type: "error", message: error instanceof Error ? error.message : "An error occurred" };
      yield { type: "done", reason: "error" };
      return;
    }

    const calls = finalizeToolCalls(acc);

    const assistant: ChatMessage = { role: "assistant", content };
    if (calls.length) assistant.tool_calls = calls;
    if (reasoning) assistant.reasoning_content = reasoning;

    messages.push(assistant);
    yield { type: "assistant_end", message: assistant };

    if (calls.length === 0) {
      yield { type: "done", reason: "stop" };
      return;
    }

    if (iteration >= max) {
      // The pending calls are deliberately not executed. The UI offers a
      // Continue action, which restarts the loop from the current messages.
      yield { type: "max_iterations", n: iteration, pending: calls };
      yield { type: "done", reason: "max_iterations" };
      return;
    }

    for (const call of calls) {
      if (signal.aborted) {
        yield { type: "done", reason: "aborted" };
        return;
      }

      yield { type: "tool_start", call, iteration };

      const startedAt = now();
      let ok = true;
      let text: string;

      let args: unknown;
      try {
        args = JSON.parse(call.function.arguments);
      } catch (error) {
        // The invalid arguments never reach the server. Handing the parse
        // error back as a tool result lets the model re-emit the call, which
        // small models usually get right on the second try.
        ok = false;
        const reason = error instanceof Error ? error.message : "invalid JSON";
        text =
          `Error: arguments were not valid JSON (${reason}). ` +
          `Received: ${call.function.arguments.slice(0, 200)}. ` +
          `Re-emit this tool call with a valid JSON object.`;
      }

      if (ok) {
        try {
          // A tool that ran but could not answer reports isError and carries
          // its own correction text; only a thrown error is a real failure.
          const outcome = await deps.callTool(call.function.name, args, signal);
          text = outcome.text;
          ok = !outcome.isError;
        } catch (error) {
          if (isAbort(error) || signal.aborted) {
            yield { type: "done", reason: "aborted" };
            return;
          }
          // A failing tool never ends the turn; the model decides what to do.
          ok = false;
          text = `Error: ${error instanceof Error ? error.message : "the tool failed"}`;
        }
      }

      const durationMs = now() - startedAt;
      const message: ChatMessage = {
        role: "tool",
        tool_call_id: call.id,
        name: call.function.name,
        content: text!,
        toolOk: ok,
        toolDurationMs: durationMs,
      };

      messages.push(message);
      yield { type: "tool_end", call, message, ok, durationMs };
    }
  }
}

const KNOWN_ROLES = new Set(["user", "assistant", "system", "tool"]);

/**
 * Repairs a persisted conversation before it is sent upstream.
 *
 * A reload or a cancel mid-loop can leave an assistant message whose tool calls
 * were never answered, or a tool message with no matching call. Either one is a
 * hard 400 on the next request, which reads to the user as "the chat is broken
 * and won't recover", so they are pruned on load.
 */
export function sanitizeMessages(raw: unknown): ChatMessage[] {
  if (!Array.isArray(raw)) return [];

  const messages = raw.filter(
    (m): m is ChatMessage => Boolean(m) && typeof m === "object" && KNOWN_ROLES.has((m as ChatMessage).role)
  );

  const out: ChatMessage[] = [];

  for (let i = 0; i < messages.length; i++) {
    const message = messages[i];

    // A tool result only exists as part of its assistant turn's run below.
    // One reached on its own has no matching call and would be rejected.
    if (message.role === "tool") continue;

    if (message.role !== "assistant" || !message.tool_calls?.length) {
      out.push(message);
      continue;
    }

    // Take the contiguous run of tool results that answer this turn.
    const run: ChatMessage[] = [];
    let next = i + 1;
    while (next < messages.length && messages[next].role === "tool") {
      run.push(messages[next]);
      next++;
    }
    i = next - 1;

    const answered = new Set(run.map((m) => m.tool_call_id).filter(Boolean));
    const complete = message.tool_calls.every((call) => answered.has(call.id));

    if (complete) {
      out.push(message);
      const wanted = new Set(message.tool_calls.map((call) => call.id));
      out.push(...run.filter((m) => m.tool_call_id && wanted.has(m.tool_call_id)));
      continue;
    }

    // Partially answered: the whole run has to go with the calls, or the
    // results that did land would themselves become orphans.
    const text = typeof message.content === "string" ? message.content : "";
    if (text.trim() || typeof message.content !== "string") {
      out.push({ ...message, tool_calls: undefined });
    }
  }

  return out;
}
