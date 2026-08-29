import { describe, expect, it, vi } from "vitest";
import {
  DEFAULT_MAX_ITERATIONS,
  accumulateToolCalls,
  finalizeToolCalls,
  runAgent,
  sanitizeMessages,
  type AgentDeps,
  type AgentEvent,
} from "./agentLoop";
import type { StreamChunk, ToolCallDelta } from "./chatApi";
import type { ChatMessage, ToolCall } from "./types";

function accumulate(...batches: ToolCallDelta[][]): ToolCall[] {
  const acc = new Map<number, Partial<ToolCall>>();
  for (const batch of batches) accumulateToolCalls(acc, batch);
  return finalizeToolCalls(acc);
}

describe("accumulateToolCalls", () => {
  it("assembles one call from fragments", () => {
    const calls = accumulate(
      [{ index: 0, id: "call_a", function: { name: "get_doc", arguments: "" } }],
      [{ index: 0, function: { arguments: '{"id":' } }],
      [{ index: 0, function: { arguments: '"guides/configuration/macros"}' } }]
    );

    expect(calls).toEqual([
      { id: "call_a", type: "function", function: { name: "get_doc", arguments: '{"id":"guides/configuration/macros"}' } },
    ]);
  });

  it("keeps parallel calls separate and ordered by index", () => {
    const calls = accumulate([
      { index: 1, id: "b", function: { name: "search_docs", arguments: '{"query":"ttl"}' } },
      { index: 0, id: "a", function: { name: "list_docs", arguments: "{}" } },
    ]);

    expect(calls.map((c) => c.function.name)).toEqual(["list_docs", "search_docs"]);
  });

  // Several backends repeat the full name on every chunk. Concatenating it
  // yields "get_docget_doc" and every call fails.
  it("does not concatenate a repeated name or id", () => {
    const calls = accumulate(
      [{ index: 0, id: "call_a", function: { name: "get_doc", arguments: "{" } }],
      [{ index: 0, id: "call_a", function: { name: "get_doc", arguments: "}" } }]
    );

    expect(calls[0].function.name).toBe("get_doc");
    expect(calls[0].id).toBe("call_a");
    expect(calls[0].function.arguments).toBe("{}");
  });

  it("synthesizes an id when the backend omits one", () => {
    expect(accumulate([{ index: 2, function: { name: "list_docs", arguments: "{}" } }])[0].id).toBe("call_2");
  });

  it("defaults empty arguments to an empty object", () => {
    expect(accumulate([{ index: 0, id: "a", function: { name: "list_docs" } }])[0].function.arguments).toBe("{}");
  });

  it("falls back to slot 0 when index is absent", () => {
    const calls = accumulate(
      [{ function: { name: "get_doc", arguments: '{"id":' } }],
      [{ function: { arguments: '"guides/configuration/macros"}' } }]
    );

    expect(calls).toHaveLength(1);
    expect(calls[0].function.arguments).toBe('{"id":"guides/configuration/macros"}');
  });

  it("matches an indexless fragment to an in-flight call by id", () => {
    const calls = accumulate(
      [
        { index: 0, id: "a", function: { name: "list_docs", arguments: "{}" } },
        { index: 1, id: "b", function: { name: "get_doc", arguments: "{" } },
      ],
      [{ id: "b", function: { arguments: "}" } }]
    );

    expect(calls).toHaveLength(2);
    expect(calls[1].function.arguments).toBe("{}");
  });

  it("drops fragments that never produced a name", () => {
    expect(accumulate([{ index: 0, function: { arguments: "{}" } }])).toEqual([]);
  });
});

/** Builds a streamChat double that replays one scripted turn per call. */
function scriptedStream(turns: StreamChunk[][]) {
  let turn = 0;
  return async function* () {
    const chunks = turns[Math.min(turn, turns.length - 1)];
    turn++;
    for (const chunk of chunks) yield chunk;
  };
}

function text(...parts: string[]): StreamChunk[] {
  return parts.map((delta) => ({ content: delta, done: false }));
}

function toolChunk(index: number, id: string, name: string, args: string): StreamChunk {
  return { content: "", tool_calls: [{ index, id, function: { name, arguments: args } }], done: false };
}

async function collect(gen: AsyncGenerator<AgentEvent>): Promise<AgentEvent[]> {
  const events: AgentEvent[] = [];
  for await (const event of gen) events.push(event);
  return events;
}

function deps(overrides: Partial<AgentDeps> = {}): AgentDeps {
  let clock = 0;
  return {
    streamChat: scriptedStream([text("hello")]),
    callTool: vi.fn(async () => ({ text: "result", isError: false })),
    now: () => (clock += 5),
    ...overrides,
  };
}

const seed: ChatMessage[] = [{ role: "user", content: "hi" }];

describe("runAgent", () => {
  it("streams a plain answer without calling any tool", async () => {
    const callTool = vi.fn();
    const events = await collect(runAgent(seed, deps({ callTool })));

    expect(callTool).not.toHaveBeenCalled();
    expect(events.filter((e) => e.type === "content").map((e: any) => e.delta)).toEqual(["hello"]);
    expect(events.at(-1)).toEqual({ type: "done", reason: "stop" });

    const end = events.find((e) => e.type === "assistant_end") as any;
    expect(end.message).toEqual({ role: "assistant", content: "hello" });
  });

  it("runs a tool then continues to a final answer", async () => {
    const callTool = vi.fn(async () => ({ text: "the docs say X", isError: false }));
    const events = await collect(
      runAgent(
        seed,
        deps({
          streamChat: scriptedStream([[toolChunk(0, "a", "list_docs", "{}")], text("X it is")]),
          callTool,
        })
      )
    );

    expect(callTool).toHaveBeenCalledWith("list_docs", {}, expect.anything());
    expect(events.map((e) => e.type)).toEqual([
      "iteration",
      "assistant_end",
      "tool_start",
      "tool_end",
      "iteration",
      "content",
      "assistant_end",
      "done",
    ]);

    const toolEnd = events.find((e) => e.type === "tool_end") as any;
    expect(toolEnd.ok).toBe(true);
    expect(toolEnd.message).toMatchObject({
      role: "tool",
      tool_call_id: "a",
      name: "list_docs",
      content: "the docs say X",
      toolOk: true,
    });
    expect(toolEnd.durationMs).toBeGreaterThan(0);
  });

  it("executes parallel calls in index order", async () => {
    const seen: string[] = [];
    const events = await collect(
      runAgent(
        seed,
        deps({
          streamChat: scriptedStream([
            [
              {
                content: "",
                tool_calls: [
                  { index: 1, id: "b", function: { name: "search_docs", arguments: '{"query":"x"}' } },
                  { index: 0, id: "a", function: { name: "list_docs", arguments: "{}" } },
                ],
                done: false,
              },
            ],
            text("done"),
          ]),
          callTool: vi.fn(async (name: string) => {
            seen.push(name);
            return { text: "ok", isError: false };
          }),
        })
      )
    );

    expect(seen).toEqual(["list_docs", "search_docs"]);
    expect(events.filter((e) => e.type === "tool_end")).toHaveLength(2);
  });

  // The invalid arguments must never reach the server; the model gets the
  // parse error back so it can re-emit the call.
  it("reports invalid JSON arguments back to the model without calling the tool", async () => {
    const callTool = vi.fn();
    const events = await collect(
      runAgent(
        seed,
        deps({
          streamChat: scriptedStream([[toolChunk(0, "a", "get_doc", "{not json")], text("sorry")]),
          callTool,
        })
      )
    );

    expect(callTool).not.toHaveBeenCalled();

    const toolEnd = events.find((e) => e.type === "tool_end") as any;
    expect(toolEnd.ok).toBe(false);
    expect(toolEnd.message.content).toContain("not valid JSON");
    expect(toolEnd.message.content).toContain("{not json");
    expect(events.at(-1)).toEqual({ type: "done", reason: "stop" });
  });

  // A tool that ran but could not answer reports isError; the card must show a
  // warning rather than a check, and the loop must continue either way.
  it("marks a tool result that reports isError", async () => {
    const events = await collect(
      runAgent(
        seed,
        deps({
          streamChat: scriptedStream([[toolChunk(0, "a", "get_doc", "{}")], text("retrying")]),
          callTool: vi.fn(async () => ({ text: "Error: no document with id \"x\"", isError: true })),
        })
      )
    );

    const toolEnd = events.find((e) => e.type === "tool_end") as any;
    expect(toolEnd.ok).toBe(false);
    expect(toolEnd.message.toolOk).toBe(false);
    expect(toolEnd.message.content).toContain("no document with id");
    // The model still gets another turn to correct itself.
    expect(events.at(-1)).toEqual({ type: "done", reason: "stop" });
  });

  it("keeps going when a tool fails", async () => {
    const events = await collect(
      runAgent(
        seed,
        deps({
          streamChat: scriptedStream([[toolChunk(0, "a", "get_doc", "{}")], text("recovered")]),
          callTool: vi.fn(async () => {
            throw new Error("boom");
          }),
        })
      )
    );

    const toolEnd = events.find((e) => e.type === "tool_end") as any;
    expect(toolEnd.ok).toBe(false);
    expect(toolEnd.message.content).toBe("Error: boom");
    expect(events.at(-1)).toEqual({ type: "done", reason: "stop" });
  });

  it("stops at the iteration cap without running the pending calls", async () => {
    const callTool = vi.fn(async () => ({ text: "again", isError: false }));
    const events = await collect(
      runAgent(
        seed,
        deps({ streamChat: scriptedStream([[toolChunk(0, "a", "list_docs", "{}")]]), callTool }),
        { maxIterations: 3 }
      )
    );

    expect(callTool).toHaveBeenCalledTimes(2); // iterations 1 and 2; 3 stops first
    const limit = events.find((e) => e.type === "max_iterations") as any;
    expect(limit.n).toBe(3);
    expect(limit.pending).toHaveLength(1);
    expect(events.at(-1)).toEqual({ type: "done", reason: "max_iterations" });
  });

  it("uses the default cap when none is given", async () => {
    const callTool = vi.fn(async () => ({ text: "again", isError: false }));
    await collect(
      runAgent(seed, deps({ streamChat: scriptedStream([[toolChunk(0, "a", "list_docs", "{}")]]), callTool }))
    );
    expect(callTool).toHaveBeenCalledTimes(DEFAULT_MAX_ITERATIONS - 1);
  });

  it("stops immediately on an already aborted signal", async () => {
    const controller = new AbortController();
    controller.abort();

    const streamChat = vi.fn();
    const events = await collect(runAgent(seed, deps({ streamChat: streamChat as any }), { signal: controller.signal }));

    expect(streamChat).not.toHaveBeenCalled();
    expect(events).toEqual([{ type: "done", reason: "aborted" }]);
  });

  it("aborts when the stream is cancelled mid-turn", async () => {
    const events = await collect(
      runAgent(
        seed,
        deps({
          // eslint-disable-next-line require-yield
          streamChat: async function* () {
            const error = new Error("cancelled");
            error.name = "AbortError";
            throw error;
          },
        })
      )
    );

    expect(events.at(-1)).toEqual({ type: "done", reason: "aborted" });
    expect(events.some((e) => e.type === "error")).toBe(false);
  });

  it("aborts when a tool call is cancelled", async () => {
    const events = await collect(
      runAgent(
        seed,
        deps({
          streamChat: scriptedStream([[toolChunk(0, "a", "list_docs", "{}")]]),
          callTool: vi.fn(async () => {
            const error = new Error("cancelled");
            error.name = "AbortError";
            throw error;
          }),
        })
      )
    );

    expect(events.at(-1)).toEqual({ type: "done", reason: "aborted" });
  });

  it("surfaces a transport failure and does not retry", async () => {
    const streamChat = vi.fn(async function* () {
      throw new Error("Chat API error: 500");
    });
    const events = await collect(runAgent(seed, deps({ streamChat: streamChat as any })));

    expect(streamChat).toHaveBeenCalledTimes(1);
    expect(events.find((e) => e.type === "error")).toMatchObject({ message: "Chat API error: 500" });
    expect(events.at(-1)).toEqual({ type: "done", reason: "error" });
  });

  // llama.cpp often reports "stop" alongside a populated tool_calls array, so
  // the loop must key off the calls themselves.
  it("continues on tool calls even when finish_reason says stop", async () => {
    const callTool = vi.fn(async () => ({ text: "ok", isError: false }));
    await collect(
      runAgent(
        seed,
        deps({
          streamChat: scriptedStream([
            [
              toolChunk(0, "a", "list_docs", "{}"),
              { content: "", finish_reason: "stop", done: false },
            ],
            text("final"),
          ]),
          callTool,
        })
      )
    );

    expect(callTool).toHaveBeenCalledTimes(1);
  });

  it("carries reasoning onto the assistant message", async () => {
    const events = await collect(
      runAgent(
        seed,
        deps({
          streamChat: scriptedStream([
            [{ content: "", reasoning_content: "thinking", done: false }, ...text("answer")],
          ]),
        })
      )
    );

    const end = events.find((e) => e.type === "assistant_end") as any;
    expect(end.message.reasoning_content).toBe("thinking");
    expect(events.filter((e) => e.type === "reasoning")).toHaveLength(1);
  });

  it("feeds tool results back into the next request", async () => {
    const seenRequests: ChatMessage[][] = [];
    await collect(
      runAgent(
        seed,
        deps({
          streamChat: (messages) => {
            seenRequests.push([...messages]);
            const turn = seenRequests.length;
            return (async function* () {
              if (turn === 1) yield toolChunk(0, "a", "list_docs", "{}");
              else yield { content: "final", done: false };
            })();
          },
          callTool: vi.fn(async () => ({ text: "tool output", isError: false })),
        })
      )
    );

    expect(seenRequests).toHaveLength(2);
    expect(seenRequests[1].map((m) => m.role)).toEqual(["user", "assistant", "tool"]);
    expect(seenRequests[1][2].content).toBe("tool output");
  });
});

describe("sanitizeMessages", () => {
  const call = (id: string): ToolCall => ({
    id,
    type: "function",
    function: { name: "list_docs", arguments: "{}" },
  });

  it("returns an empty array for anything that is not an array", () => {
    for (const input of [null, undefined, {}, "nope", 42]) {
      expect(sanitizeMessages(input)).toEqual([]);
    }
  });

  it("leaves a plain legacy conversation untouched", () => {
    const messages: ChatMessage[] = [
      { role: "system", content: "be helpful" },
      { role: "user", content: "hi" },
      { role: "assistant", content: "hello", reasoning_content: "think", reasoningTimeMs: 5 },
    ];
    expect(sanitizeMessages(messages)).toEqual(messages);
  });

  it("keeps a complete assistant turn with its results", () => {
    const messages: ChatMessage[] = [
      { role: "user", content: "hi" },
      { role: "assistant", content: "", tool_calls: [call("a"), call("b")] },
      { role: "tool", tool_call_id: "a", content: "one" },
      { role: "tool", tool_call_id: "b", content: "two" },
      { role: "assistant", content: "done" },
    ];
    expect(sanitizeMessages(messages)).toEqual(messages);
  });

  it("drops an orphan tool message", () => {
    const got = sanitizeMessages([
      { role: "user", content: "hi" },
      { role: "tool", tool_call_id: "ghost", content: "stale" },
      { role: "assistant", content: "hello" },
    ]);
    expect(got.map((m) => m.role)).toEqual(["user", "assistant"]);
  });

  // Dropping only the unanswered call would orphan the result that did land.
  it("drops the whole run when a turn is only partly answered", () => {
    const got = sanitizeMessages([
      { role: "user", content: "hi" },
      { role: "assistant", content: "working", tool_calls: [call("a"), call("b")] },
      { role: "tool", tool_call_id: "a", content: "one" },
    ]);

    expect(got.map((m) => m.role)).toEqual(["user", "assistant"]);
    expect(got[1].tool_calls).toBeUndefined();
  });

  it("removes a trailing assistant turn with unanswered calls and no text", () => {
    const got = sanitizeMessages([
      { role: "user", content: "hi" },
      { role: "assistant", content: "", tool_calls: [call("a")] },
    ]);
    expect(got.map((m) => m.role)).toEqual(["user"]);
  });

  it("drops unknown roles", () => {
    const got = sanitizeMessages([
      { role: "user", content: "hi" },
      { role: "function", content: "from another build" },
      { role: "assistant", content: "hello" },
    ]);
    expect(got.map((m) => m.role)).toEqual(["user", "assistant"]);
  });

  it("produces a conversation that is stable under a second pass", () => {
    const messy: unknown[] = [
      { role: "tool", tool_call_id: "x", content: "orphan" },
      { role: "user", content: "hi" },
      { role: "assistant", content: "", tool_calls: [call("a"), call("b")] },
      { role: "tool", tool_call_id: "a", content: "one" },
      { role: "user", content: "again" },
    ];
    const once = sanitizeMessages(messy);
    expect(sanitizeMessages(once)).toEqual(once);
  });
});
