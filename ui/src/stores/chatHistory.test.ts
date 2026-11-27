import "fake-indexeddb/auto";
import { beforeEach, describe, expect, it } from "vitest";
import { IDBFactory } from "fake-indexeddb";
import { openDB } from "idb";
import { createChatHistory, normalizeLimit, resetChatHistoryDb } from "./chatHistory";
import type { ChatMessage } from "../lib/types";

// persistentStore reads localStorage; node has none.
const storage = new Map<string, string>();
Object.defineProperty(globalThis, "window", { value: globalThis, configurable: true });
Object.defineProperty(globalThis, "localStorage", {
  configurable: true,
  value: {
    getItem: (k: string) => storage.get(k) ?? null,
    setItem: (k: string, v: string) => void storage.set(k, v),
    removeItem: (k: string) => void storage.delete(k),
  },
});

interface S {
  model: string;
}

const PNG = "data:image/png;base64,iVBORw0KGgo=";

function conv(id: string, text = id, messages?: ChatMessage[]) {
  return {
    id,
    title: text,
    settings: { model: "m-" + id },
    messages: messages ?? [{ role: "user" as const, content: text }],
  };
}

function open(namespace = "chat", limit = 3) {
  storage.set(`${namespace}-history-limit`, String(limit));
  return createChatHistory<S>({ namespace });
}

async function tick() {
  await new Promise((r) => setTimeout(r, 2));
}

async function blobCount(): Promise<number> {
  const db = await openDB("llama-swap-chat");
  const n = await db.count("blobs");
  db.close();
  return n;
}

const withImage: ChatMessage[] = [{ role: "user", content: [{ type: "image_url", image_url: { url: PNG } }] }];

describe("ChatHistory", () => {
  beforeEach(async () => {
    await resetChatHistoryDb();
    globalThis.indexedDB = new IDBFactory();
    storage.clear();
  });

  it("normalizes the limit", () => {
    expect(normalizeLimit(-3)).toBe(0);
    expect(normalizeLimit(2.9)).toBe(2);
    expect(normalizeLimit(9999)).toBe(100);
    expect(normalizeLimit("x")).toBe(1);
  });

  it("round-trips messages and settings, newest first", async () => {
    const h = open();
    await h.save(conv("a"));
    await tick();
    await h.save(conv("b"));
    expect((await h.list()).map((c) => c.id)).toEqual(["b", "a"]);
    const loaded = await h.load("a");
    expect(loaded?.settings).toEqual({ model: "m-a" });
    expect(loaded?.messages).toEqual([{ role: "user", content: "a" }]);
  });

  it("keeps only `limit` conversations", async () => {
    const h = open("chat", 2);
    for (const id of ["a", "b", "c"]) {
      await h.save(conv(id));
      await tick();
    }
    expect((await h.list()).map((c) => c.id)).toEqual(["c", "b"]);
  });

  it("saves nothing when the limit is 0", async () => {
    const h = open("chat", 0);
    await h.save(conv("a"));
    expect(await h.list()).toEqual([]);
  });

  it("lowering the limit prunes, and 0 clears", async () => {
    const h = open("chat", 3);
    for (const id of ["a", "b", "c"]) {
      await h.save(conv(id));
      await tick();
    }
    await h.setLimit(1);
    expect((await h.list()).map((c) => c.id)).toEqual(["c"]);
    await h.setLimit(0);
    expect(await h.list()).toEqual([]);
  });

  it("keeps namespaces apart", async () => {
    const chat = open("chat");
    const docs = open("docs");
    await chat.save(conv("a"));
    await docs.save(conv("d"));
    expect((await chat.list()).map((c) => c.id)).toEqual(["a"]);
    expect(await docs.load("a")).toBeNull();
    await chat.clear();
    expect(await chat.list()).toEqual([]);
    expect((await docs.list()).map((c) => c.id)).toEqual(["d"]);
  });

  it("stores images as blobs and restores them", async () => {
    const h = open();
    await h.save(conv("a", "a", withImage));
    expect(await blobCount()).toBe(1);
    expect((await h.load("a"))?.messages[0].content).toEqual(withImage[0].content);
  });

  it("keeps a shared blob until its last conversation is gone", async () => {
    const chat = open("chat");
    const docs = open("docs");
    await chat.save(conv("a", "a", withImage));
    await docs.save(conv("d", "d", withImage));
    await chat.clear();
    expect(await blobCount()).toBe(1);
    expect((await docs.load("d"))?.messages[0].content).toEqual(withImage[0].content);
    await docs.remove("d");
    expect(await blobCount()).toBe(0);
  });

  it("drops an orphaned blob when a save removes the image", async () => {
    const h = open();
    await h.save(conv("a", "a", withImage));
    await h.save(conv("a", "a", [{ role: "user", content: "no image" }]));
    expect(await blobCount()).toBe(0);
  });

  it("applies sanitize on load", async () => {
    storage.set("docs-history-limit", "1");
    const h = createChatHistory<S>({
      namespace: "docs",
      sanitize: (m) => m.filter((x) => x.role !== "tool"),
    });
    await h.save(conv("a", "a", [{ role: "user", content: "q" }, { role: "tool", content: "x" }]));
    expect((await h.load("a"))?.messages).toHaveLength(1);
  });

  it("clear detaches the active id", async () => {
    const h = open();
    await h.save(conv("a"));
    h.activeId.set("a");
    await h.clear();
    let id: string | null = "x";
    h.activeId.subscribe((v) => (id = v))();
    expect(id).toBeNull();
  });
});

describe("ChatHistory legacy keys", () => {
  it("deletes the old localStorage keys without reading them", () => {
    storage.set("playground-messages", "[...]");
    storage.set("other", "keep");
    createChatHistory<S>({ namespace: "chat", legacyKeys: ["playground-messages"] });
    expect(storage.has("playground-messages")).toBe(false);
    expect(storage.get("other")).toBe("keep");
  });
});
