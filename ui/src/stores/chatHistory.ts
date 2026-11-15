import { derived, get, writable, type Writable } from "svelte/store";
import type { Endpoint } from "../lib/chatApi";
import { getTextContent, type ChatMessage } from "../lib/types";
import { DOCS_AGENT_SYSTEM_PROMPT } from "../lib/prompts/docsAgent";

export type PlainMessage = ChatMessage & { role: "user" | "assistant" | "system" };
export interface ChatSettings {
  model: string;
  systemPrompt: string;
  temperature: number;
  endpoint: Endpoint;
  maxTokens: number;
  topP: number;
  topK: number;
  minP: number;
}
export interface SavedChat {
  id: string;
  title: string;
  createdAt: number;
  updatedAt: number;
  messages: PlainMessage[];
  settings: ChatSettings;
  unread?: boolean;
  pinned?: boolean;
}

/** Empty drafts stay out of history; pins precede the remaining recent chats. */
export function listSavedChats(chats: SavedChat[], search = ""): SavedChat[] {
  const query = search.trim().toLocaleLowerCase();
  return chats.filter((chat) => chat.messages.length > 0 && (!query ||
    chat.title.toLocaleLowerCase().includes(query) ||
    chat.messages.some((msg) => getTextContent(msg.content).toLocaleLowerCase().includes(query))))
    .sort((a, b) => Number(Boolean(b.pinned)) - Number(Boolean(a.pinned)) || b.updatedAt - a.updatedAt);
}
interface History { version: 1; activeId: string; chats: SavedChat[] }
const STORAGE_KEY = "playground-chat-history";
const defaults: ChatSettings = {
  model: "", systemPrompt: "", temperature: 0.7, endpoint: "v1/chat/completions",
  maxTokens: 4096, topP: 1, topK: 40, minP: 0,
};

function validSettings(value: unknown): ChatSettings {
  const settings = { ...defaults };
  if (!value || typeof value !== "object") return settings;
  for (const key of Object.keys(defaults) as (keyof ChatSettings)[]) {
    const candidate = (value as Record<string, unknown>)[key];
    if (typeof candidate !== typeof defaults[key]) continue;
    if (typeof candidate === "number" && !Number.isFinite(candidate)) continue;
    if (key === "endpoint" && !["v1/chat/completions", "v1/responses", "v1/messages"].includes(candidate as string)) continue;
    Object.assign(settings, { [key]: candidate });
  }
  return settings;
}

function plainMessages(value: unknown): PlainMessage[] {
  if (!Array.isArray(value)) return [];
  return value.filter((msg): msg is PlainMessage =>
    msg && ["user", "assistant", "system"].includes(msg.role) &&
    !msg.tool_calls?.length && (typeof msg.content === "string" ||
      (Array.isArray(msg.content) && msg.content.every((part: any) =>
        part && ((part.type === "text" && typeof part.text === "string") ||
          (part.type === "image_url" && typeof part.image_url?.url === "string"))))));
}

/** One browser-local history, shared by Chat and the application sidebar. */
export function createChatHistory(storage?: Pick<Storage, "getItem" | "setItem">) {
  const storageError = writable("");
  const generating = writable(new Set<string>());
  let viewedChatId: string | null = null;
  function read(key: string): unknown {
    try { return JSON.parse(storage?.getItem(key) ?? "null"); } catch { return null; }
  }
  function makeChat(settings: ChatSettings): SavedChat {
    const now = Date.now();
    // getRandomValues also works when the UI is served over HTTP on a LAN.
    const id = Array.from(crypto.getRandomValues(new Uint8Array(16)),
      (byte) => byte.toString(16).padStart(2, "0")).join("");
    return { id, title: "New chat", createdAt: now, updatedAt: now,
      messages: [], settings: { ...settings } };
  }
  const legacySettings = { ...defaults };
  const legacyKeys: Record<keyof ChatSettings, string> = {
    model: "selected-model", systemPrompt: "system-prompt", temperature: "temperature",
    endpoint: "endpoint", maxTokens: "max-tokens", topP: "top-p", topK: "top-k", minP: "min-p",
  };
  for (const key of Object.keys(defaults) as (keyof ChatSettings)[]) {
    const value = read(`playground-${legacyKeys[key]}`);
    if (typeof value === typeof defaults[key]) Object.assign(legacySettings, { [key]: value });
  }
  if (legacySettings.systemPrompt === DOCS_AGENT_SYSTEM_PROMPT) legacySettings.systemPrompt = "";
  const saved = read(STORAGE_KEY) as Partial<History> | null;
  let chats: SavedChat[] = [];
  if (saved?.version === 1 && Array.isArray(saved.chats)) {
    const ids = new Set<string>();
    chats = saved.chats.filter((chat) => {
      if (!chat || typeof chat.id !== "string" || ids.has(chat.id) ||
        typeof chat.title !== "string" || !Number.isFinite(chat.updatedAt) ||
        !Number.isFinite(chat.createdAt)) return false;
      ids.add(chat.id);
      return true;
    }).map((chat) => ({ ...chat, pinned: chat.pinned === true, unread: chat.unread === true, messages: plainMessages(chat.messages),
      settings: validSettings(chat.settings) }));
  }
  if (!chats.length) {
    const chat = makeChat(validSettings(legacySettings));
    chat.messages = plainMessages(read("playground-messages"));
    const first = chat.messages.find((msg) => msg.role === "user");
    if (first) chat.title = getTextContent(first.content).trim().replace(/\s+/g, " ").slice(0, 80) || "Image chat";
    chats = [chat];
  }
  const state = writable<History>({ version: 1, chats,
    activeId: chats.some((chat) => chat.id === saved?.activeId) ? saved!.activeId! : chats[0].id });
  const active = derived(state, ($state) => $state.chats.find((chat) => chat.id === $state.activeId)!);
  let timer: ReturnType<typeof setTimeout> | undefined;
  function flush() {
    clearTimeout(timer);
    timer = undefined;
    if (!storage) return;
    try {
      storage.setItem(STORAGE_KEY, JSON.stringify(get(state)));
      storageError.set("");
    } catch {
      storageError.set("Chats could not be saved in this browser. Storage may be full or unavailable. Keep this page open to retain your changes.");
    }
  }
  // Streaming updates stay in memory immediately; writes are limited to once
  // every two seconds, with an explicit flush on navigation and turn completion.
  const unsubscribe = state.subscribe(() => { if (storage) timer ??= setTimeout(flush, 2000); });
  function updateChat(id: string, update: (chat: SavedChat) => SavedChat) {
    state.update((history) => ({ ...history, chats: history.chats.map((chat) =>
      chat.id === id ? update(chat) : chat) }));
  }
  function messagesFor(id?: string, touch = true): Writable<PlainMessage[]> {
    const selected = id ? derived(state, ($state) => $state.chats.find((chat) => chat.id === id)) : active;
    const messages: Writable<PlainMessage[]> = {
      subscribe: derived(selected, (chat) => chat?.messages ?? []).subscribe,
      set(value) {
        updateChat(id ?? get(state).activeId, (chat) => ({ ...chat, messages: value,
          updatedAt: touch ? Date.now() : chat.updatedAt,
          title: chat.messages.length === 0 && chat.title === "New chat" && value.length
            ? getTextContent(value[0].content).trim().replace(/\s+/g, " ").slice(0, 80) || "Image chat"
            : chat.title }));
      },
      update(fn) { messages.set(fn(get(messages))); },
    };
    return messages;
  }
  const messages = messagesFor();
  function setting<K extends keyof ChatSettings>(key: K): Writable<ChatSettings[K]> {
    const field: Writable<ChatSettings[K]> = {
      subscribe: derived(active, (chat) => chat.settings[key]).subscribe,
      set(value) { updateChat(get(state).activeId, (chat) => ({ ...chat, settings: { ...chat.settings, [key]: value } })); },
      update(fn) { field.set(fn(get(field))); },
    };
    return field;
  }
  return {
    subscribe: state.subscribe, active, messages, messagesFor, generating, setting, storageError, flush,
    setViewed(id: string | null) {
      viewedChatId = id;
      if (id && get(state).chats.some((chat) => chat.id === id && chat.unread)) {
        updateChat(id, (chat) => ({ ...chat, unread: false }));
        flush();
      }
    },
    complete(id: string) {
      updateChat(id, (chat) => ({ ...chat, unread: viewedChatId !== id }));
    },
    create() {
      const current = get(active);
      if (!current.messages.length) return;
      const chat = makeChat(current.settings);
      state.update((history) => ({ ...history, activeId: chat.id, chats: [chat, ...history.chats] }));
      flush();
    },
    select(id: string) {
      if (!get(state).chats.some((chat) => chat.id === id)) return;
      state.update((history) => ({ ...history, activeId: id }));
      flush();
    },
    rename(id: string, title: string) {
      const trimmed = title.trim().replace(/\s+/g, " ").slice(0, 80);
      if (!trimmed) return;
      state.update((history) => ({ ...history, chats: history.chats.map((chat) =>
        chat.id === id ? { ...chat, title: trimmed } : chat) }));
      flush();
    },
    togglePin(id: string) {
      updateChat(id, (chat) => ({ ...chat, pinned: !chat.pinned }));
      flush();
    },
    remove(id: string) {
      if (get(generating).has(id)) return;
      state.update((history) => {
        const remaining = history.chats.filter((chat) => chat.id !== id);
        if (!remaining.length) remaining.push(makeChat(get(active).settings));
        return { ...history, chats: remaining, activeId: history.activeId === id
          ? [...remaining].sort((a, b) => b.updatedAt - a.updatedAt)[0].id : history.activeId };
      });
      flush();
    },
    destroy() { flush(); unsubscribe(); },
  };
}

let browserStorage: Pick<Storage, "getItem" | "setItem"> | undefined;
try { if (typeof window !== "undefined") browserStorage = window.localStorage; } catch {
  browserStorage = { getItem: () => null, setItem: () => { throw new Error("Storage unavailable"); } };
}
export const chatHistory = createChatHistory(browserStorage);
export const chatMessages = chatHistory.messages;
export const chatStorageError = chatHistory.storageError;
export const activeChat = chatHistory.active;
if (typeof window !== "undefined") {
  window.addEventListener("pagehide", chatHistory.flush);
  document.addEventListener("visibilitychange", () => {
    if (document.visibilityState === "hidden") chatHistory.flush();
  });
}
