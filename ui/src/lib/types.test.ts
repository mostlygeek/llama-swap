import { describe, it, expect } from "vitest";
import { isToolCallOnlyTurn } from "./types";
import type { ChatMessage, ToolCall } from "./types";

const call: ToolCall = {
  id: "call_1",
  type: "function",
  function: { name: "docs__search_docs", arguments: '{"query":"ttl"}' },
};

describe("isToolCallOnlyTurn", () => {
  it("hides an assistant turn that only requested tools", () => {
    const message: ChatMessage = { role: "assistant", content: "", tool_calls: [call] };
    expect(isToolCallOnlyTurn(message)).toBe(true);
  });

  it("keeps a tool-calling turn that reasoned first", () => {
    const message: ChatMessage = {
      role: "assistant",
      content: "",
      reasoning_content: "The user asked about ttl, so search the docs.",
      tool_calls: [call],
    };
    expect(isToolCallOnlyTurn(message)).toBe(false);
  });

  it("keeps a tool-calling turn that also wrote text", () => {
    const message: ChatMessage = { role: "assistant", content: "Let me look.", tool_calls: [call] };
    expect(isToolCallOnlyTurn(message)).toBe(false);
  });

  it("keeps turns with no tool calls, empty or not", () => {
    expect(isToolCallOnlyTurn({ role: "assistant", content: "" })).toBe(false);
    expect(isToolCallOnlyTurn({ role: "assistant", content: "", tool_calls: [] })).toBe(false);
    expect(isToolCallOnlyTurn({ role: "assistant", content: "Set ttl to 300." })).toBe(false);
  });

  it("keeps non-assistant roles", () => {
    const message: ChatMessage = { role: "tool", tool_call_id: "call_1", content: "" };
    expect(isToolCallOnlyTurn(message)).toBe(false);
  });

  it("reads text out of multimodal content", () => {
    const message: ChatMessage = {
      role: "assistant",
      content: [{ type: "text", text: "Looking it up." }],
      tool_calls: [call],
    };
    expect(isToolCallOnlyTurn(message)).toBe(false);
  });
});
