import { get } from "svelte/store";
import { describe, expect, it, vi } from "vitest";
import { createChatHistory } from "../stores/chatHistory";
import { createChatGeneration } from "./chatGeneration";
import type { StreamChunk, streamChatCompletion } from "./chatApi";

function channel() {
  const queue: (StreamChunk | Error | null)[] = [];
  let wake: (() => void) | undefined;
  return {
    send(chunk: StreamChunk | Error | null) { queue.push(chunk); wake?.(); },
    async *read(signal: AbortSignal) {
      const abort = () => { queue.push(new DOMException("Cancelled", "AbortError")); wake?.(); };
      signal.addEventListener("abort", abort);
      try {
        while (true) {
          if (!queue.length) await new Promise<void>((resolve) => { wake = resolve; });
          const next = queue.shift();
          if (next === null) return;
          if (next instanceof Error) throw next;
          if (next) yield next;
        }
      } finally { signal.removeEventListener("abort", abort); }
    },
  };
}
const settle = async () => { for (let i = 0; i < 12; i++) await Promise.resolve(); };

describe("chat generation", () => {
  it("keeps simultaneous responses, reasoning, settings, and cancellation attached to their chats", async () => {
    const history = createChatHistory();
    const a = get(history).activeId;
    history.setting("model").set("model-a");
    history.setting("systemPrompt").set("Prompt A");
    history.messages.set([{ role: "user", content: "Question A" }]);
    const channels = [channel(), channel()];
    const complete = vi.fn<typeof streamChatCompletion>()
      .mockImplementationOnce((_model, _messages, signal) => channels[0].read(signal!))
      .mockImplementationOnce((_model, _messages, signal) => channels[1].read(signal!));
    const generation = createChatGeneration(history, complete);
    const first = generation.generate(a, 0, 4096);
    history.create();
    const b = get(history).activeId;
    history.setting("model").set("model-b");
    history.setting("systemPrompt").set("Prompt B");
    history.messages.set([{ role: "user", content: "Question B" }]);
    const second = generation.generate(b, 0, 8192);
    try {
      expect(get(history.generating)).toEqual(new Set([a, b]));
      channels[0].send({ content: "", reasoning_content: "Thinking A", done: false });
      channels[1].send({ content: "Answer B", done: false });
      await settle();
      expect(get(history.messages).at(-1)?.content).toBe("Answer B");
      expect(get(generation.reasoning)[a]).toBe(true);
      expect(get(generation.reasoning)[b]).toBeUndefined();
      history.select(a);
      expect(get(history.messages).at(-1)?.reasoning_content).toBe("Thinking A");
      channels[0].send({ content: "Answer A", done: false });
      await settle();
      expect(get(generation.reasoning)[a]).toBeUndefined();
      generation.cancel(a);
      await first;
      expect(get(history.messages).at(-1)?.interrupted).toBe("cancelled");
      expect(get(history.active).unread).not.toBe(true);
      expect(get(history.generating)).toEqual(new Set([b]));
      history.select(b);
      channels[1].send({ content: " continued", done: false });
      channels[1].send(null);
      await second;
      expect(get(history.messages).at(-1)?.content).toBe("Answer B continued");
      expect(get(history.messages).at(-1)?.stats?.contextLength).toBe(8192);
      expect(get(history.messages).at(-1)?.interrupted).toBeUndefined();
      expect(get(history.active).unread).toBe(true);
      history.select(a);
      expect(get(history.messages).at(-1)?.content).toBe("Answer A");
      expect(complete.mock.calls[0][0]).toBe("model-a");
      expect(complete.mock.calls[0][1][0]).toEqual({ role: "system", content: "Prompt A" });
      expect(complete.mock.calls[1][0]).toBe("model-b");
      expect(complete.mock.calls[1][1][0]).toEqual({ role: "system", content: "Prompt B" });
      expect(get(history.generating).size).toBe(0);
    } finally {
      generation.cancel(a); generation.cancel(b);
      await Promise.all([first, second]);
      history.destroy();
    }
  });

  it("routes background errors to the original chat and rejects a second request in that chat", async () => {
    const history = createChatHistory();
    const a = get(history).activeId;
    history.setting("model").set("model-a");
    history.messages.set([{ role: "user", content: "Question A" }]);
    const stream = channel();
    const complete = vi.fn<typeof streamChatCompletion>((_model, _messages, signal) => stream.read(signal!));
    const generation = createChatGeneration(history, complete);
    const first = generation.generate(a, 0);
    try {
      await generation.generate(a, 0);
      expect(complete).toHaveBeenCalledTimes(1);
      history.create();
      stream.send(new Error("Backend failed"));
      await first;
      expect(get(history.messages)).toEqual([]);
      expect(get(history.messagesFor(a)).at(-1)?.content).toContain("Backend failed");
      expect(get(history.messagesFor(a)).at(-1)?.interrupted).toBe("error");
      expect(get(history.generating).size).toBe(0);
    } finally {
      generation.cancel(a); await first; history.destroy();
    }
  });
});
