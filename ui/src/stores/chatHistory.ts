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

type HistoryStorage = Pick<Storage, "getItem" | "setItem" | "removeItem" | "key" | "length">;
const CHAT_PREFIX = `${STORAGE_KEY}:`;
const chatFields = ["title", "messages", "updatedAt", "unread", "pinned"] as const;
type DirtyChat = { fields: Set<typeof chatFields[number]>; settings: Set<keyof ChatSettings> };

function normalizeChat(value: unknown): SavedChat | null {
  if (!value || typeof value !== "object") return null;
  const chat = value as SavedChat;
  if (typeof chat.id !== "string" || typeof chat.title !== "string" ||
    !Number.isFinite(chat.createdAt) || !Number.isFinite(chat.updatedAt)) return null;
  return { ...chat, pinned: chat.pinned === true, unread: chat.unread === true,
    messages: plainMessages(chat.messages), settings: validSettings(chat.settings) };
}

/** Each chat has its own atomic storage record; idle tabs never rewrite a snapshot. */
export function createChatHistory(storage?: HistoryStorage) {
  const storageError = writable("");
  const generating = writable(new Set<string>());
  const dirty = new Map<string, DirtyChat>();
  const deleted = new Set<string>();
  let viewedChatId: string | null = null;
  let activeDirty = false;
  let timer: ReturnType<typeof setTimeout> | undefined;
  function read(key: string): unknown {
    try { return JSON.parse(storage?.getItem(key) ?? "null"); } catch { return null; }
  }
  function makeChat(settings: ChatSettings): SavedChat {
    const now = Date.now();
    const id = Array.from(crypto.getRandomValues(new Uint8Array(16)),
      (byte) => byte.toString(16).padStart(2, "0")).join("");
    return { id, title: "New chat", createdAt: now, updatedAt: now,
      messages: [], settings: { ...settings } };
  }
  function records(): Map<string, SavedChat | null> {
    const result = new Map<string, SavedChat | null>();
    if (!storage) return result;
    for (let i = 0; i < storage.length; i++) {
      const key = storage.key(i);
      if (!key?.startsWith(CHAT_PREFIX)) continue;
      const id = key.slice(CHAT_PREFIX.length);
      const value = read(key);
      if (value === null && storage.getItem(key) === "null") result.set(id, null);
      else {
        const chat = normalizeChat(value);
        if (chat?.id === id) result.set(id, chat);
      }
    }
    return result;
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
  const saved = read(STORAGE_KEY) as { version?: number; activeId?: string; chats?: unknown[] } | null;
  const retire = new Set<string>();
  let chats: SavedChat[] = [];
  if (saved?.version === 1 && Array.isArray(saved.chats)) {
    chats = saved.chats.map(normalizeChat).filter((chat): chat is SavedChat => chat !== null);
    retire.add(STORAGE_KEY);
  }
  const legacyMessages = read("playground-messages");
  if (Array.isArray(legacyMessages)) {
    if (!chats.length && saved?.version !== 2) {
      const chat = makeChat(validSettings(legacySettings));
      chat.id = "legacy"; // Concurrent first-load migrations identify the same conversation.
      chat.messages = plainMessages(legacyMessages);
      const first = chat.messages.find((msg) => msg.role === "user");
      if (first) chat.title = getTextContent(first.content).trim().replace(/\s+/g, " ").slice(0, 80) || "Image chat";
      chats = [chat];
    }
    retire.add("playground-messages");
  }
  // An unmodified migrated chat must not overwrite a record another tab has
  // already migrated and edited (or deleted).
  for (const chat of chats) dirty.set(chat.id, { fields: new Set(), settings: new Set() });
  const loaded = records();
  const byId = new Map(chats.map((chat) => [chat.id, chat]));
  for (const [id, chat] of loaded) {
    if (chat) byId.set(id, chat);
    else byId.delete(id);
  }
  chats = [...byId.values()];
  if (!chats.length) {
    const chat = makeChat(validSettings(legacySettings));
    chats = [chat];
    dirty.set(chat.id, { fields: new Set(), settings: new Set() });
  }
  const state = writable<History>({ version: 1, chats,
    activeId: chats.some((chat) => chat.id === saved?.activeId) ? saved!.activeId! : chats[0].id });
  const active = derived(state, ($state) => $state.chats.find((chat) => chat.id === $state.activeId)!);

  function scheduleSave() { if (storage) timer ??= setTimeout(flush, 2000); }
  function merged(remote: SavedChat, local: SavedChat): SavedChat {
    const changes = dirty.get(local.id);
    if (!changes) return remote;
    const result = { ...remote, settings: { ...remote.settings } };
    for (const field of changes.fields) Object.assign(result, { [field]: local[field] });
    for (const field of changes.settings) Object.assign(result.settings, { [field]: local.settings[field] });
    return result;
  }
  function sync() {
    if (!storage) return;
    const remote = records();
    state.update((history) => {
      const local = new Map(history.chats.map((chat) => [chat.id, chat]));
      for (const [id, chat] of remote) {
        if (deleted.has(id)) continue;
        if (chat) local.set(id, local.has(id) ? merged(chat, local.get(id)!) : chat);
        else if (!get(generating).has(id)) local.delete(id);
      }
      for (const id of deleted) local.delete(id);
      if (!local.size) {
        const chat = makeChat(get(active).settings);
        local.set(chat.id, chat);
      }
      const next = [...local.values()];
      return { ...history, chats: next, activeId: local.has(history.activeId) ? history.activeId : next[0].id };
    });
  }
  function flush() {
    clearTimeout(timer);
    timer = undefined;
    if (!storage) return;
    try {
      const writes = new Map<string, string>();
      const history = get(state);
      for (const [id] of dirty) {
        const key = CHAT_PREFIX + id;
        if (storage.getItem(key) === "null" || deleted.has(id)) continue;
        const local = history.chats.find((chat) => chat.id === id);
        if (!local) continue;
        const remote = normalizeChat(read(key));
        writes.set(key, JSON.stringify(remote ? merged(remote, local) : local));
      }
      // Keep tombstones so a stale writer cannot resurrect a deleted chat.
      for (const id of deleted) writes.set(CHAT_PREFIX + id, "null");
      const retiring = [...retire].filter((key) => storage.getItem(key) !== null);
      if (activeDirty || retiring.length || (writes.size && (read(STORAGE_KEY) as { version?: number })?.version !== 2)) {
        writes.set(STORAGE_KEY, JSON.stringify({ version: 2, activeId: history.activeId }));
      }
      const originals = new Map([...new Set([...writes.keys(), ...retiring])]
        .map((key) => [key, storage.getItem(key)]));
      const write = () => { for (const [key, value] of writes) storage.setItem(key, value); };
      const restore = (keys: Iterable<string>) => {
        // Release new records before restoring previous values near the quota.
        for (const key of keys) if (originals.get(key) === null) storage.removeItem(key);
        for (const key of keys) {
          const value = originals.get(key);
          if (value != null) storage.setItem(key, value);
        }
      };
      try {
        write();
      } catch (error) {
        if (!retiring.length || !(error instanceof DOMException) || error.name !== "QuotaExceededError") throw error;
        restore(writes.keys());
        // localStorage has no rename operation. Release the old copy only for
        // this synchronous retry, and restore it if the replacement cannot fit.
        for (const key of retiring) storage.removeItem(key);
        try { write(); } catch (retryError) {
          restore(writes.keys());
          restore(retiring);
          throw retryError;
        }
      }
      for (const key of retiring) if (!writes.has(key)) storage.removeItem(key);
      dirty.clear();
      deleted.clear();
      retire.clear();
      activeDirty = false;
      storageError.set("");
      sync();
    } catch {
      storageError.set("Chats could not be saved in this browser. Storage may be full or unavailable. Keep this page open to retain your changes.");
    }
  }
  function updateChat(id: string, update: (chat: SavedChat) => SavedChat) {
    state.update((history) => ({ ...history, chats: history.chats.map((chat) => {
      if (chat.id !== id) return chat;
      const next = update(chat);
      const changes = dirty.get(id) ?? { fields: new Set(), settings: new Set() };
      for (const field of chatFields) if (chat[field] !== next[field]) changes.fields.add(field);
      for (const field of Object.keys(defaults) as (keyof ChatSettings)[]) {
        if (chat.settings[field] !== next.settings[field]) changes.settings.add(field);
      }
      dirty.set(id, changes);
      return next;
    }) }));
    scheduleSave();
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
  if (dirty.size || retire.size) scheduleSave();
  return {
    subscribe: state.subscribe, active, messages, messagesFor, generating, setting, storageError, flush, sync,
    setViewed(id: string | null) {
      viewedChatId = id;
      if (id && get(state).chats.some((chat) => chat.id === id && chat.unread)) {
        updateChat(id, (chat) => ({ ...chat, unread: false }));
        flush();
      }
    },
    complete(id: string) { updateChat(id, (chat) => ({ ...chat, unread: viewedChatId !== id })); },
    create() {
      const current = get(active);
      if (!current.messages.length) return;
      const chat = makeChat(current.settings);
      dirty.set(chat.id, { fields: new Set(), settings: new Set() });
      state.update((history) => ({ ...history, activeId: chat.id, chats: [chat, ...history.chats] }));
      activeDirty = true;
      flush();
    },
    select(id: string) {
      if (!get(state).chats.some((chat) => chat.id === id)) return;
      state.update((history) => ({ ...history, activeId: id }));
      activeDirty = true;
      flush();
    },
    rename(id: string, title: string) {
      const trimmed = title.trim().replace(/\s+/g, " ").slice(0, 80);
      if (!trimmed) return;
      updateChat(id, (chat) => ({ ...chat, title: trimmed }));
      flush();
    },
    togglePin(id: string) { updateChat(id, (chat) => ({ ...chat, pinned: !chat.pinned })); flush(); },
    remove(id: string) {
      if (get(generating).has(id)) return;
      deleted.add(id);
      dirty.delete(id);
      state.update((history) => {
        const remaining = history.chats.filter((chat) => chat.id !== id);
        if (!remaining.length) {
          const chat = makeChat(get(active).settings);
          remaining.push(chat);
          dirty.set(chat.id, { fields: new Set(), settings: new Set() });
        }
        return { ...history, chats: remaining, activeId: history.activeId === id
          ? [...remaining].sort((a, b) => b.updatedAt - a.updatedAt)[0].id : history.activeId };
      });
      activeDirty = true;
      flush();
    },
    destroy() { flush(); clearTimeout(timer); },
  };
}

let browserStorage: HistoryStorage | undefined;
try { if (typeof window !== "undefined") browserStorage = window.localStorage; } catch {
  browserStorage = { getItem: () => null, setItem: () => { throw new Error("Storage unavailable"); },
    removeItem: () => {}, key: () => null, length: 0 };
}
export const chatHistory = createChatHistory(browserStorage);
export const chatMessages = chatHistory.messages;
export const chatStorageError = chatHistory.storageError;
export const activeChat = chatHistory.active;
if (typeof window !== "undefined") {
  window.addEventListener("storage", (event) => {
    if (event.storageArea === browserStorage && (event.key === null || event.key === STORAGE_KEY || event.key.startsWith(CHAT_PREFIX))) {
      chatHistory.sync();
    }
  });
  window.addEventListener("pagehide", chatHistory.flush);
  document.addEventListener("visibilitychange", () => {
    if (document.visibilityState === "hidden") chatHistory.flush();
  });
}
