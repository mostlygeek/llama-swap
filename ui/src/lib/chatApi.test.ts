import { describe, expect, it } from "vitest";
import {
  buildRequest,
  normalizeAnthropicStopReason,
  normalizeResponsesStop,
  parseChatCompletionsLine,
  type ChatOptions,
  type ToolDefinition,
} from "./chatApi";
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

  // The include_usage chunk has an empty choices array and nothing else.
  it("reads a usage-only chunk", () => {
    const line = sse({ choices: [], usage: { prompt_tokens: 12, completion_tokens: 3, total_tokens: 15 } });
    const chunk = parseChatCompletionsLine(line);
    expect(chunk?.usage).toEqual({ prompt_tokens: 12, completion_tokens: 3 });
    expect(chunk?.content).toBe("");
    expect(chunk?.done).toBe(false);
  });

  // usage.prompt_tokens is always the whole prompt, so it wins over
  // timings.prompt_n, which counts only the tokens actually processed.
  it("merges usage counts with llama.cpp timings", () => {
    const line = sse({
      choices: [{ delta: {}, finish_reason: "stop" }],
      usage: { prompt_tokens: 12, completion_tokens: 3 },
      timings: { prompt_n: 10, prompt_ms: 120.5, predicted_n: 4, predicted_ms: 800, predicted_per_second: 5 },
    });
    expect(parseChatCompletionsLine(line)?.usage).toEqual({
      prompt_tokens: 12,
      completion_tokens: 3,
      prompt_ms: 120.5,
      completion_ms: 800,
    });
  });

  it("reads cache and draft counts from llama.cpp timings", () => {
    const line = sse({
      choices: [],
      timings: { prompt_n: 4, cache_n: 13, prompt_ms: 50, predicted_n: 500, predicted_ms: 14500, draft_n: 120, draft_n_accepted: 96 },
    });
    expect(parseChatCompletionsLine(line)?.usage).toEqual({
      prompt_tokens: 17, // processed + cached
      cached_tokens: 13,
      completion_tokens: 500,
      prompt_ms: 50,
      completion_ms: 14500,
      draft_tokens: 120,
      draft_accepted: 96,
    });
  });

  it("reads cached tokens from OpenAI-style usage details", () => {
    const line = sse({
      choices: [],
      usage: { prompt_tokens: 20, completion_tokens: 1, prompt_tokens_details: { cached_tokens: 15 } },
    });
    expect(parseChatCompletionsLine(line)?.usage).toEqual({ prompt_tokens: 20, completion_tokens: 1, cached_tokens: 15 });
  });

  it("ignores malformed usage", () => {
    const withText = sse({ choices: [{ delta: { content: "x" } }], usage: "nope" });
    expect(parseChatCompletionsLine(withText)?.usage).toBeUndefined();
    expect(parseChatCompletionsLine(sse({ choices: [], usage: { prompt_tokens: "12" } }))).toBeNull();
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

  it("asks for usage in the stream", () => {
    expect(build([]).stream_options).toEqual({ include_usage: true });
  });

  // Without per-chunk timings the exact numbers only arrive in the final
  // chunk, which a cancelled stream never delivers.
  it("asks llama.cpp for timings on every chunk by default", () => {
    expect(build([]).timings_per_token).toBe(true);
  });

  it("omits the timings extension when it is turned off", () => {
    expect(build([], { timingsPerToken: false })).not.toHaveProperty("timings_per_token");
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
      {
        role: "assistant",
        content: "hi",
        reasoning_content: "think",
        reasoningTimeMs: 5,
        stats: {
          prompt: { approxTokens: false, approxTimings: true },
          generation: { tokens: 1, approxTokens: true, approxTimings: true },
        },
      },
    ]);

    expect(body.messages[0]).not.toHaveProperty("toolOk");
    expect(body.messages[0]).not.toHaveProperty("toolDurationMs");
    expect(body.messages[1]).not.toHaveProperty("reasoning_content");
    expect(body.messages[1]).not.toHaveProperty("reasoningTimeMs");
    expect(body.messages[1]).not.toHaveProperty("stats");
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

describe("stop reason normalization", () => {
  it("maps Anthropic stop reasons onto the OpenAI vocabulary", () => {
    expect(normalizeAnthropicStopReason("end_turn")).toBe("stop");
    expect(normalizeAnthropicStopReason("stop_sequence")).toBe("stop");
    expect(normalizeAnthropicStopReason("max_tokens")).toBe("length");
    expect(normalizeAnthropicStopReason("tool_use")).toBe("tool_calls");
    expect(normalizeAnthropicStopReason("refusal")).toBe("refusal"); // unknown ones pass through
    expect(normalizeAnthropicStopReason(null)).toBeUndefined();
    expect(normalizeAnthropicStopReason("")).toBeUndefined();
  });

  it("maps Responses terminal events onto the OpenAI vocabulary", () => {
    expect(normalizeResponsesStop("response.completed", { status: "completed" })).toBe("stop");
    expect(normalizeResponsesStop("response.incomplete", { incomplete_details: { reason: "max_output_tokens" } })).toBe("length");
    expect(normalizeResponsesStop("response.incomplete", { incomplete_details: { reason: "content_filter" } })).toBe("content_filter");
    expect(normalizeResponsesStop("response.incomplete", {})).toBe("incomplete");
    expect(normalizeResponsesStop("response.completed", { status: "incomplete", incomplete_details: { reason: "max_output_tokens" } })).toBe("length");
    expect(normalizeResponsesStop("response.failed", {})).toBe("error");
    expect(normalizeResponsesStop("response.output_text.delta", {})).toBeUndefined();
  });
});
