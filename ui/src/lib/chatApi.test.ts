import { describe, expect, it } from "vitest";
import { buildRequest, parseChatCompletionsLine, type ChatOptions, type ToolDefinition } from "./chatApi";
import type { ChatMessage, ToolCall } from "./types";

const sse = (payload: unknown) => `data: ${JSON.stringify(payload)}`;

const delta = (d: Record<string, unknown>, extra: Record<string, unknown> = {}) =>
  sse({ choices: [{ delta: d, ...extra }] });

describe("parseChatCompletionsLine", () => {
  it("reads a text delta", () => {
    expect(parseChatCompletionsLine(delta({ content: "hi" }))).toEqual({
      content: "hi",
      reasoning_content: "",
      tool_calls: undefined,
      finish_reason: undefined,
      done: false,
    });
  });

  it("reads reasoning under either key", () => {
    expect(parseChatCompletionsLine(delta({ reasoning_content: "a" }))?.reasoning_content).toBe("a");
    expect(parseChatCompletionsLine(delta({ reasoning: "b" }))?.reasoning_content).toBe("b");
  });

  // The old parser returned null for anything that was not text or reasoning,
  // which silently discarded every tool call.
  it("reads a tool call delta that carries no text", () => {
    const line = delta({
      content: null,
      tool_calls: [{ index: 0, id: "a", function: { name: "get_doc", arguments: "{}" } }],
    });

    const chunk = parseChatCompletionsLine(line);
    expect(chunk?.tool_calls).toEqual([{ index: 0, id: "a", function: { name: "get_doc", arguments: "{}" } }]);
    expect(chunk?.content).toBe("");
    expect(chunk?.done).toBe(false);
  });

  it("reads a finish_reason with an empty delta", () => {
    expect(parseChatCompletionsLine(delta({}, { finish_reason: "tool_calls" }))?.finish_reason).toBe("tool_calls");
  });

  it("ignores tool_calls that is not an array", () => {
    expect(parseChatCompletionsLine(delta({ content: "x", tool_calls: "nope" }))?.tool_calls).toBeUndefined();
  });

  it("marks [DONE]", () => {
    expect(parseChatCompletionsLine("data: [DONE]")).toEqual({ content: "", done: true });
  });

  it("returns null for lines with nothing to report", () => {
    for (const line of ["", "   ", ": keepalive", "event: message", "data: {not json", delta({})]) {
      expect(parseChatCompletionsLine(line)).toBeNull();
    }
  });
});

describe("buildRequest for v1/chat/completions", () => {
  const build = (messages: ChatMessage[], options: ChatOptions = {}) =>
    buildRequest("v1/chat/completions", "m", messages, options).body as any;

  it("targets the right url", () => {
    expect(buildRequest("v1/chat/completions", "m", [], {}).url).toBe("/v1/chat/completions");
  });

  it("preserves tool_calls on an assistant turn", () => {
    const tool_calls: ToolCall[] = [
      { id: "a", type: "function", function: { name: "list_docs", arguments: "{}" } },
    ];
    const body = build([{ role: "assistant", content: "", tool_calls }]);

    expect(body.messages[0].tool_calls).toEqual(tool_calls);
  });

  it("preserves tool_call_id and name on a tool turn", () => {
    const body = build([{ role: "tool", tool_call_id: "a", name: "list_docs", content: "output" }]);

    expect(body.messages[0]).toEqual({
      role: "tool",
      content: "output",
      tool_call_id: "a",
      name: "list_docs",
    });
  });

  // An assistant turn that only made tool calls has no text, and several
  // backends reject a message whose content field is missing.
  it("coerces missing content to an empty string", () => {
    const body = build([{ role: "assistant", content: undefined as any }]);
    expect(body.messages[0].content).toBe("");
  });

  it("drops UI-only fields", () => {
    const body = build([
      {
        role: "tool",
        tool_call_id: "a",
        content: "output",
        toolOk: true,
        toolDurationMs: 12,
      },
      { role: "assistant", content: "hi", reasoning_content: "think", reasoningTimeMs: 5 },
    ]);

    expect(body.messages[0]).not.toHaveProperty("toolOk");
    expect(body.messages[0]).not.toHaveProperty("toolDurationMs");
    expect(body.messages[1]).not.toHaveProperty("reasoning_content");
    expect(body.messages[1]).not.toHaveProperty("reasoningTimeMs");
  });

  it("does not put tool_calls on a non-assistant message", () => {
    const body = build([
      {
        role: "user",
        content: "hi",
        tool_calls: [{ id: "a", type: "function", function: { name: "x", arguments: "{}" } }],
      },
    ]);
    expect(body.messages[0]).not.toHaveProperty("tool_calls");
  });

  const tools: ToolDefinition[] = [
    { type: "function", function: { name: "list_docs", description: "d", parameters: {} } },
  ];

  it("omits tools when there are none", () => {
    const body = build([{ role: "user", content: "hi" }]);
    expect(body).not.toHaveProperty("tools");
    expect(body).not.toHaveProperty("tool_choice");
  });

  it("sends tools with tool_choice auto by default", () => {
    const body = build([{ role: "user", content: "hi" }], { tools });
    expect(body.tools).toEqual(tools);
    expect(body.tool_choice).toBe("auto");
  });

  it("honours an explicit tool_choice", () => {
    expect(build([{ role: "user", content: "hi" }], { tools, tool_choice: "none" }).tool_choice).toBe("none");
  });
});

describe("buildRequest for the text-only endpoints", () => {
  const history: ChatMessage[] = [
    { role: "system", content: "be helpful" },
    { role: "user", content: "hi" },
    {
      role: "assistant",
      content: "looking",
      tool_calls: [{ id: "a", type: "function", function: { name: "list_docs", arguments: "{}" } }],
    },
    { role: "tool", tool_call_id: "a", name: "list_docs", content: "output" },
    { role: "assistant", content: "answer" },
  ];

  // Tool messages can survive in the history when the user switches endpoints
  // mid-conversation; passing one through is a hard 400 upstream.
  it.each(["v1/messages", "v1/responses"] as const)("%s drops tool messages", (endpoint) => {
    const body = buildRequest(endpoint, "m", history, {}).body as any;
    const list = body.messages ?? body.input;

    expect(list.some((m: any) => m.role === "tool")).toBe(false);
    expect(list.some((m: any) => m.tool_calls)).toBe(false);
    expect(list.map((m: any) => m.role)).toEqual(["user", "assistant", "assistant"]);
  });

  it.each(["v1/messages", "v1/responses"] as const)("%s refuses tools loudly", (endpoint) => {
    const tools: ToolDefinition[] = [
      { type: "function", function: { name: "list_docs", description: "d", parameters: {} } },
    ];
    expect(() => buildRequest(endpoint, "m", history, { tools })).toThrow(/only supported on \/v1\/chat\/completions/);
  });

  it("still extracts the system prompt", () => {
    const body = buildRequest("v1/messages", "m", history, {}).body as any;
    expect(body.system).toBe("be helpful");
  });
});
