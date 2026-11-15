import { openDB, type IDBPDatabase } from "idb";
import { get, writable, type Readable, type Writable } from "svelte/store";
import { persistentStore } from "./persistent";
import type { ChatMessage, ContentPart } from "../lib/types";

/**
 * Saved conversations, kept in IndexedDB.
 *
 * localStorage was the first home for the chat, but it holds ~5 MB of UTF-16
 * for the whole origin and pasted images are base64 text, so a couple of
 * screenshots could fill it. IndexedDB has no such ceiling and stores images
 * as binary.
 *
 * One database serves every chat-like page (Chat, Help). Records carry a
 * `namespace`, and the limit, listing, pruning and clearing all work per
 * namespace. Image blobs are shared: a blob goes away only when no
 * conversation in any namespace refers to it.
 */

const DB_NAME = "llama-swap-chat";
const MAX_LIMIT = 100;
const IMAGE_REF = "idb:";

interface StoredConversation<S> {
  id: string;
  namespace: string;
  title: string;
  updatedAt: number;
  settings: S;
  /** Messages with data-URL images replaced by `idb:<id>` references. */
  messages: ChatMessage[];
  blobIds: string[];
}

interface StoredBlob {
  id: string;
  type: string;
  data: ArrayBuffer;
}

export interface Conversation<S> {
  id: string;
  title: string;
  updatedAt: number;
  settings: S;
  messages: ChatMessage[];
}

export interface ConversationMeta {
  id: string;
  title: string;
  updatedAt: number;
}

export interface ChatHistoryOptions {
  /** Keeps this page's conversations apart from the others'. */
  namespace: string;
  /** Conversations kept until the user changes it. */
  defaultLimit?: number;
  /** Applied to messages on load, e.g. to drop tool calls left unanswered. */
  sanitize?: (messages: ChatMessage[]) => ChatMessage[];
}

export interface ChatHistory<S extends object> {
  /** How many conversations to keep. 0 saves nothing. */
  limit: Readable<number>;
  /** The conversation on screen, if it has been saved. */
  activeId: Writable<string | null>;
  /** False once IndexedDB turned out to be unusable; history is then off. */
  available: Readable<boolean>;
  /** Set when the last save could not fit, even after pruning. */
  saveError: Readable<boolean>;
  setLimit(n: number): Promise<void>;
  list(): Promise<ConversationMeta[]>;
  load(id: string): Promise<Conversation<S> | null>;
  save(conversation: Omit<Conversation<S>, "updatedAt">): Promise<void>;
  remove(id: string): Promise<void>;
  clear(): Promise<void>;
}

export function normalizeLimit(n: unknown): number {
  const value = typeof n === "number" ? n : Number(n);
  if (!Number.isFinite(value)) return 1;
  return Math.min(MAX_LIMIT, Math.max(0, Math.floor(value)));
}

// ---- image <-> blob ------------------------------------------------------

/** Cheap 53-bit string hash. crypto.subtle is missing on plain-http origins. */
function hashString(s: string): string {
  let h1 = 0xdeadbeef;
  let h2 = 0x41c6ce57;
  for (let i = 0; i < s.length; i++) {
    const ch = s.charCodeAt(i);
    h1 = Math.imul(h1 ^ ch, 2654435761);
    h2 = Math.imul(h2 ^ ch, 1597334677);
  }
  h1 = Math.imul(h1 ^ (h1 >>> 16), 2246822507) ^ Math.imul(h2 ^ (h2 >>> 13), 3266489909);
  h2 = Math.imul(h2 ^ (h2 >>> 16), 2246822507) ^ Math.imul(h1 ^ (h1 >>> 13), 3266489909);
  return (4294967296 * (2097151 & h2) + (h1 >>> 0)).toString(16) + s.length.toString(16);
}

function dataUrlToBlob(url: string): StoredBlob | null {
  const m = /^data:([^;,]*)(;base64)?,(.*)$/s.exec(url);
  if (!m) return null;
  const bin = m[2] ? atob(m[3]) : decodeURIComponent(m[3]);
  const bytes = new Uint8Array(bin.length);
  for (let i = 0; i < bin.length; i++) bytes[i] = bin.charCodeAt(i);
  return { id: hashString(url), type: m[1] || "application/octet-stream", data: bytes.buffer };
}

function blobToDataUrl(blob: StoredBlob): string {
  const bytes = new Uint8Array(blob.data);
  let bin = "";
  for (let i = 0; i < bytes.length; i += 0x8000) {
    bin += String.fromCharCode(...bytes.subarray(i, i + 0x8000));
  }
  return `data:${blob.type};base64,${btoa(bin)}`;
}

/** Swaps data-URL images for references; returns the blobs to store. */
function externalizeImages(messages: ChatMessage[]): { messages: ChatMessage[]; blobs: StoredBlob[] } {
  const blobs = new Map<string, StoredBlob>();
  const out = messages.map((message) => {
    if (typeof message.content === "string") return message;
    const content = message.content.map((part): ContentPart => {
      if (part.type !== "image_url" || !part.image_url.url.startsWith("data:")) return part;
      const blob = dataUrlToBlob(part.image_url.url);
      if (!blob) return part;
      blobs.set(blob.id, blob);
      return { type: "image_url", image_url: { url: IMAGE_REF + blob.id } };
    });
    return { ...message, content };
  });
  return { messages: out, blobs: [...blobs.values()] };
}

function referencedBlobIds(messages: ChatMessage[]): string[] {
  const ids = new Set<string>();
  for (const message of messages) {
    if (typeof message.content === "string") continue;
    for (const part of message.content) {
      if (part.type === "image_url" && part.image_url.url.startsWith(IMAGE_REF)) {
        ids.add(part.image_url.url.slice(IMAGE_REF.length));
      }
    }
  }
  return [...ids];
}

// ---- database ------------------------------------------------------------

type Db = IDBPDatabase;

let dbPromise: Promise<Db | null> | null = null;
let persistRequested = false;

function getDb(): Promise<Db | null> {
  dbPromise ??= (async () => {
    if (typeof indexedDB === "undefined") return null;
    try {
      return await openDB(DB_NAME, 1, {
        upgrade(db) {
          const conversations = db.createObjectStore("conversations", { keyPath: "id" });
          conversations.createIndex("byNamespace", ["namespace", "updatedAt"]);
          db.createObjectStore("blobs", { keyPath: "id" });
        },
      });
    } catch (e) {
      console.error("chat history: IndexedDB is unavailable", e);
      return null;
    }
  })();
  return dbPromise;
}

/** Test hook: closes and forgets the connection so a test can start clean. */
export async function resetChatHistoryDb(): Promise<void> {
  const db = await dbPromise;
  db?.close();
  dbPromise = null;
  persistRequested = false;
}

/** Deletes blobs that no stored conversation refers to. */
async function collectGarbage(db: Db): Promise<void> {
  const tx = db.transaction(["conversations", "blobs"], "readwrite");
  const live = new Set<string>();
  for (const conv of (await tx.objectStore("conversations").getAll()) as StoredConversation<unknown>[]) {
    for (const id of conv.blobIds) live.add(id);
  }
  const blobs = tx.objectStore("blobs");
  for (const key of await blobs.getAllKeys()) {
    if (!live.has(key as string)) await blobs.delete(key);
  }
  await tx.done;
}

function isQuotaError(e: unknown): boolean {
  return e instanceof DOMException && (e.name === "QuotaExceededError" || e.code === 22);
}

// ---- factory -------------------------------------------------------------

export function createChatHistory<S extends object>(options: ChatHistoryOptions): ChatHistory<S> {
  const { namespace, sanitize } = options;
  const limit = persistentStore<number>(`${namespace}-history-limit`, options.defaultLimit ?? 1);
  limit.update(normalizeLimit);
  const activeId = persistentStore<string | null>(`${namespace}-active-id`, null);
  const available = writable(true);
  const saveError = writable(false);

  // Operations run one at a time, so a throttled save cannot interleave with
  // a prune or a clear.
  let queue: Promise<unknown> = Promise.resolve();
  function enqueue<T>(op: (db: Db) => Promise<T>, fallback: T): Promise<T> {
    const run = queue.then(async () => {
      const db = await getDb();
      if (!db) {
        available.set(false);
        return fallback;
      }
      try {
        return await op(db);
      } catch (e) {
        console.error(`chat history (${namespace})`, e);
        return fallback;
      }
    });
    queue = run.catch(() => undefined);
    return run;
  }

  /** Newest first. */
  async function namespaceKeys(db: Db): Promise<{ id: string; updatedAt: number }[]> {
    const range = IDBKeyRange.bound([namespace, 0], [namespace, Infinity]);
    const rows = (await db.getAllFromIndex("conversations", "byNamespace", range)) as StoredConversation<S>[];
    return rows.map((r) => ({ id: r.id, updatedAt: r.updatedAt })).reverse();
  }

  async function prune(db: Db, keep: number, protectId?: string): Promise<void> {
    const keys = await namespaceKeys(db);
    const doomed = keys.slice(keep).filter((k) => k.id !== protectId);
    if (doomed.length === 0) return;
    const tx = db.transaction("conversations", "readwrite");
    for (const k of doomed) tx.store.delete(k.id);
    await tx.done;
    await collectGarbage(db);
  }

  async function put(db: Db, record: StoredConversation<S>, blobs: StoredBlob[]): Promise<void> {
    const tx = db.transaction(["conversations", "blobs"], "readwrite");
    for (const blob of blobs) tx.objectStore("blobs").put(blob);
    tx.objectStore("conversations").put(record);
    await tx.done;
  }

  return {
    limit: { subscribe: limit.subscribe },
    activeId,
    available: { subscribe: available.subscribe },
    saveError: { subscribe: saveError.subscribe },

    setLimit(n) {
      const value = normalizeLimit(n);
      limit.set(value);
      if (value === 0) activeId.set(null);
      return enqueue((db) => prune(db, value), undefined);
    },

    list() {
      return enqueue(async (db) => {
        const range = IDBKeyRange.bound([namespace, 0], [namespace, Infinity]);
        const rows = (await db.getAllFromIndex("conversations", "byNamespace", range)) as StoredConversation<S>[];
        return rows
          .map(({ id, title, updatedAt }) => ({ id, title, updatedAt }))
          .reverse();
      }, [] as ConversationMeta[]);
    },

    load(id) {
      return enqueue(async (db) => {
        const rec = (await db.get("conversations", id)) as StoredConversation<S> | undefined;
        if (!rec || rec.namespace !== namespace) return null;
        const blobs = new Map<string, string>();
        for (const blobId of rec.blobIds) {
          const blob = (await db.get("blobs", blobId)) as StoredBlob | undefined;
          if (blob) blobs.set(blobId, blobToDataUrl(blob));
        }
        let messages = rec.messages.map((message): ChatMessage => {
          if (typeof message.content === "string") return message;
          const content = message.content.flatMap((part): ContentPart[] => {
            if (part.type !== "image_url" || !part.image_url.url.startsWith(IMAGE_REF)) return [part];
            const url = blobs.get(part.image_url.url.slice(IMAGE_REF.length));
            // A blob that is gone (evicted storage) leaves a hole, not a broken image.
            return url ? [{ type: "image_url", image_url: { url } }] : [];
          });
          return { ...message, content };
        });
        if (sanitize) messages = sanitize(messages);
        return { id: rec.id, title: rec.title, updatedAt: rec.updatedAt, settings: rec.settings, messages };
      }, null as Conversation<S> | null);
    },

    save(conversation) {
      if (get(limit) === 0) return Promise.resolve();
      return enqueue(async (db) => {
        const keep = get(limit);
        if (keep === 0) return;
        const { messages, blobs } = externalizeImages(conversation.messages);
        const record: StoredConversation<S> = {
          id: conversation.id,
          namespace,
          title: conversation.title,
          updatedAt: Date.now(),
          settings: conversation.settings,
          messages,
          blobIds: referencedBlobIds(messages),
        };
        const previous = (await db.get("conversations", record.id)) as StoredConversation<S> | undefined;

        // On a full disk, give up the oldest other conversations one by one.
        for (;;) {
          try {
            await put(db, record, blobs);
            break;
          } catch (e) {
            if (!isQuotaError(e)) throw e;
            const victim = (await namespaceKeys(db)).reverse().find((k) => k.id !== record.id);
            if (!victim) {
              saveError.set(true);
              return;
            }
            await db.delete("conversations", victim.id);
            await collectGarbage(db);
          }
        }
        saveError.set(false);

        await prune(db, keep, record.id);
        if (previous?.blobIds.some((id) => !record.blobIds.includes(id))) await collectGarbage(db);

        if (!persistRequested) {
          persistRequested = true;
          void navigator.storage?.persist?.().catch(() => undefined);
        }
      }, undefined);
    },

    remove(id) {
      if (get(activeId) === id) activeId.set(null);
      return enqueue(async (db) => {
        const rec = (await db.get("conversations", id)) as StoredConversation<S> | undefined;
        if (!rec || rec.namespace !== namespace) return;
        await db.delete("conversations", id);
        await collectGarbage(db);
      }, undefined);
    },

    clear() {
      activeId.set(null);
      return enqueue((db) => prune(db, 0), undefined);
    },
  };
}
