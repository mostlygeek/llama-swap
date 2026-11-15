import { get } from "svelte/store";
import { afterEach, describe, expect, it, vi } from "vitest";
import { createChatHistory, listSavedChats } from "./chatHistory";

const histories: ReturnType<typeof createChatHistory>[] = [];
function setup(initial: Record<string, unknown> = {}) {
  const data = new Map(Object.entries(initial).map(([key, value]) => [key, JSON.stringify(value)]));
  const storage = { getItem: (key: string) => data.get(key) ?? null,
    setItem: vi.fn((key: string, value: string) => { data.set(key, value); }) };
  const history = createChatHistory(storage);
  histories.push(history);
  return { history, storage, data };
}
afterEach(() => {
  histories.splice(0).forEach((history) => history.destroy());
  vi.useRealTimers();
});

describe("chat history", () => {
  it("hides empty drafts until their first message is sent", () => {
    const { history } = setup();
    expect(listSavedChats(get(history).chats)).toEqual([]);
    history.messages.set([{ role: "user", content: "First message" }]);
    expect(listSavedChats(get(history).chats).map((chat) => chat.title)).toEqual(["First message"]);
    history.create();
    expect(listSavedChats(get(history).chats)).toHaveLength(1);
    expect(listSavedChats(get(history).chats, "New chat")).toEqual([]);
  });

  it("persists pins, puts them first, and restores recency order when unpinned", () => {
    const { history, data } = setup();
    const firstId = get(history).activeId;
    history.messages.set([{ role: "user", content: "Older chat" }]);
    history.create();
    const secondId = get(history).activeId;
    history.messages.set([{ role: "user", content: "Newer chat" }]);
    history.togglePin(firstId);
    expect(listSavedChats(get(history).chats)[0].id).toBe(firstId);
    expect(listSavedChats(get(history).chats, "older")[0].pinned).toBe(true);
    const restored = setup(Object.fromEntries([...data].map(([key, value]) => [key, JSON.parse(value)]))).history;
    expect(listSavedChats(get(restored).chats)[0].pinned).toBe(true);
    restored.togglePin(firstId);
    const chats = get(restored).chats.map((chat) => ({ ...chat, updatedAt: chat.id === secondId ? 2 : 1 }));
    expect(listSavedChats(chats).map((chat) => chat.id)).toEqual([secondId, firstId]);
  });
  it("persists unread completions and clears them only when the chat is viewed", () => {
    const { history, data } = setup();
    const firstId = get(history).activeId;
    history.messages.set([{ role: "user", content: "First chat" }]);
    history.create();
    history.setViewed(get(history).activeId);
    history.complete(firstId);
    history.flush();
    expect(get(history).chats.find((chat) => chat.id === firstId)?.unread).toBe(true);
    const restored = setup(Object.fromEntries([...data].map(([key, value]) => [key, JSON.parse(value)]))).history;
    restored.select(firstId);
    expect(get(restored.active).unread).toBe(true);
    restored.setViewed(firstId);
    expect(get(restored.active).unread).toBe(false);
    restored.complete(firstId);
    expect(get(restored.active).unread).toBe(false);
    restored.setViewed(null); // Another page, playground tab, or hidden browser tab.
    restored.complete(firstId);
    expect(get(restored.active).unread).toBe(true);
  });
  it("migrates the existing conversation and preferences, dropping tool turns", () => {
    const { history } = setup({
      "playground-messages": [null, { role: "user", content: "Existing conversation" },
        { role: "tool", content: "result" }, { role: "assistant", content: "", tool_calls: [{}] },
        { role: "assistant", content: "Answer", reasoning_content: "Thinking" }],
      "playground-selected-model": "local-model", "playground-temperature": 0.3,
    });
    expect(get(history.active).title).toBe("Existing conversation");
    expect(get(history.messages)).toHaveLength(2);
    expect(get(history.setting("model"))).toBe("local-model");
    expect(get(history.setting("temperature"))).toBe(0.3);
  });

  it("starts separate chats, restores settings and persists the selected conversation", () => {
    const { history, data } = setup();
    const firstId = get(history).activeId;
    history.setting("model").set("model-a");
    history.setting("systemPrompt").set("First prompt");
    history.messages.set([{ role: "user", content: "First chat" }]);
    history.create();
    const secondId = get(history).activeId;
    history.create(); // Reuse the empty chat.
    expect(get(history).chats).toHaveLength(2);
    history.setting("model").set("model-b");
    history.setting("systemPrompt").set("Second prompt");
    history.messages.set([{ role: "user", content: "Second chat" }]);
    history.select(firstId);
    expect(get(history.setting("model"))).toBe("model-a");
    expect(get(history.setting("systemPrompt"))).toBe("First prompt");
    expect(get(history.messages)[0].content).toBe("First chat");
    const restored = setup(Object.fromEntries([...data].map(([key, value]) => [key, JSON.parse(value)]))).history;
    expect(get(restored).activeId).toBe(firstId);
    restored.select(secondId);
    expect(get(restored.setting("model"))).toBe("model-b");
    expect(get(restored.messages)[0].content).toBe("Second chat");
  });

  it("keeps multimodal messages, reasoning, and generation stats intact", () => {
    const { history, data } = setup();
    const messages = [{ role: "user" as const, content: [{ type: "image_url" as const,
      image_url: { url: "data:image/png;base64,abc" } }] },
      { role: "assistant" as const, content: "An image", reasoning_content: "Look closely", reasoningTimeMs: 12,
        stats: { prompt: { tokens: 10, approxTokens: false, approxTimings: false },
          generation: { tokens: 3, approxTokens: false, approxTimings: true }, wallMs: 120 },
        interrupted: "cancelled" as const }];
    history.messages.set(messages);
    history.flush();
    const restored = setup(Object.fromEntries([...data].map(([key, value]) => [key, JSON.parse(value)]))).history;
    expect(get(restored.messages)).toEqual(messages);
    expect(get(restored.active).title).toBe("Image chat");
  });

  it("renames and deletes chats without resurrecting the legacy conversation", () => {
    const { history, data } = setup({ "playground-messages": [{ role: "user", content: "Old chat" }] });
    const oldId = get(history).activeId;
    history.rename(oldId, "  Renamed  ");
    expect(get(history.active).title).toBe("Renamed");
    history.remove(oldId);
    expect(get(history.messages)).toEqual([]);
    expect(get(history).chats).toHaveLength(1);
    const restored = setup(Object.fromEntries([...data].map(([key, value]) => [key, JSON.parse(value)]))).history;
    expect(get(restored.messages)).toEqual([]);
  });

  it("allows switching and creation during generation, protecting only the running chat from deletion", () => {
    const { history } = setup();
    history.messages.set([{ role: "user", content: "First" }]);
    const firstId = get(history).activeId;
    history.create();
    const secondId = get(history).activeId;
    history.generating.set(new Set([firstId]));
    history.select(firstId);
    expect(get(history).activeId).toBe(firstId);
    history.create();
    expect(get(history).chats).toHaveLength(3);
    history.remove(firstId);
    expect(get(history).chats.some((chat) => chat.id === firstId)).toBe(true);
    history.remove(secondId);
    expect(get(history).chats.some((chat) => chat.id === secondId)).toBe(false);
  });

  it("recovers from malformed saved messages, settings, and active IDs", () => {
    const { history } = setup({ "playground-chat-history": { version: 1, activeId: "missing", chats: [
      null, { id: "valid", title: "Valid", createdAt: 1, updatedAt: 2,
        settings: { model: null, systemPrompt: 42, temperature: "bad", endpoint: "bad" },
        messages: [null, { role: "user", content: [null] }, { role: "bogus", content: "bad" },
          { role: "user", content: "Still valid" }] },
    ] } });
    expect(get(history).activeId).toBe("valid");
    expect(get(history.messages)).toEqual([{ role: "user", content: "Still valid" }]);
    expect(get(history.setting("systemPrompt"))).toBe("");
    expect(get(history.setting("endpoint"))).toBe("v1/chat/completions");
  });

  it("throttles writes and reports quota failures while retaining chats in memory", () => {
    vi.useFakeTimers();
    const { history, storage } = setup();
    storage.setItem.mockImplementation(() => { throw new Error("Quota exceeded"); });
    for (let i = 0; i < 10; i++) history.messages.set([{ role: "assistant", content: `chunk ${i}` }]);
    expect(storage.setItem).not.toHaveBeenCalled();
    vi.advanceTimersByTime(2000);
    expect(storage.setItem).toHaveBeenCalledTimes(1);
    expect(get(history.storageError)).toContain("could not be saved");
    expect(get(history.messages)[0].content).toBe("chunk 9");
    storage.setItem.mockImplementation(() => {});
    history.flush();
    expect(get(history.storageError)).toBe("");
  });
});
