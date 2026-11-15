import { get, writable } from "svelte/store";
import { describe, expect, it } from "vitest";
import { createChatHistory } from "../stores/chatHistory";
import type { PlaygroundTab } from "../stores/playground";
import { trackViewedChat } from "./chatViewed";

describe("viewed chat tracking", () => {
  it("tracks the chat view independently of whether the mobile drawer is mounted", () => {
    const history = createChatHistory();
    const route = writable("/playground");
    const tab = writable<PlaygroundTab>("chat");
    const visible = writable(true);
    const stop = trackViewedChat(history, route, tab, visible);
    const id = get(history).activeId;
    try {
      // No sidebar or drawer is mounted: the visible chat is still read.
      history.complete(id);
      expect(get(history.active).unread).toBe(false);
      route.set("/models");
      history.complete(id);
      expect(get(history.active).unread).toBe(true);
      route.set("/playground");
      expect(get(history.active).unread).toBe(false);
      visible.set(false);
      history.complete(id);
      expect(get(history.active).unread).toBe(true);
      visible.set(true);
      expect(get(history.active).unread).toBe(false);
      tab.set("images");
      history.complete(id);
      expect(get(history.active).unread).toBe(true);
      tab.set("chat");
      expect(get(history.active).unread).toBe(false);
    } finally { stop(); history.destroy(); }
  });
});
