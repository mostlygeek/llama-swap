import { untrack } from "svelte";
import { get } from "svelte/store";
import type { ChatHistory } from "../../stores/chatHistory";
import { getTextContent, type ChatMessage } from "../types";

const SAVE_INTERVAL_MS = 2000;
const TITLE_MAX = 60;

export interface ConversationOptions<S extends object> {
  history: ChatHistory<S>;
  getMessages: () => ChatMessage[];
  setMessages: (messages: ChatMessage[]) => void;
  /** Everything that should come back with a conversation (model, params...). */
  getSettings: () => S;
  /** Receives the stored settings merged over the current ones. */
  applySettings: (settings: S) => void;
  isStreaming: () => boolean;
}

function newId(): string {
  // crypto.randomUUID needs a secure context; llama-swap is often on plain http.
  return globalThis.crypto?.randomUUID?.() ?? `${Date.now().toString(36)}-${Math.random().toString(36).slice(2)}`;
}

export function conversationTitle(messages: ChatMessage[]): string {
  const first = messages.find((m) => m.role === "user");
  if (!first) return "Conversation";
  const text = getTextContent(first.content).replace(/\s+/g, " ").trim();
  if (!text) return "Image";
  return text.length > TITLE_MAX ? text.slice(0, TITLE_MAX - 1) + "…" : text;
}

/**
 * Ties a chat page to a ChatHistory: restores the last conversation on mount,
 * saves changes (throttled), and switches between conversations. Shared by
 * Chat and Help; call it once while the component initialises.
 */
export function useConversation<S extends object>(options: ConversationOptions<S>) {
  const { history } = options;
  let restoring = $state(get(history.limit) > 0 && get(history.activeId) !== null);
  // Set while switching or starting a chat, so nothing is sent into a
  // conversation that is about to be replaced.
  let switching = $state(false);
  const isBusy = () => restoring || switching;
  let id: string | null = null;
  // What was last saved or restored; the effect only writes on a difference.
  let lastJson = "";
  let lastSaveTime = 0;

  function snapshotJson(): string {
    return JSON.stringify({ m: options.getMessages(), s: options.getSettings() });
  }

  async function write() {
    const messages = $state.snapshot(options.getMessages()) as ChatMessage[];
    if (messages.length === 0 || get(history.limit) === 0) return;
    id ??= newId();
    // Saving may have been off (limit 0), which clears the active id.
    history.activeId.set(id);
    lastJson = snapshotJson();
    lastSaveTime = Date.now();
    await history.save({
      id,
      title: conversationTitle(messages),
      settings: $state.snapshot(options.getSettings()) as S,
      messages,
    });
  }

  /** Saves right now if anything changed since the last save. */
  function flush(): Promise<void> {
    if (restoring || snapshotJson() === lastJson) return Promise.resolve();
    return write();
  }

  async function restore(convId: string): Promise<boolean> {
    const conv = await history.load(convId);
    if (!conv) return false;
    id = conv.id;
    history.activeId.set(conv.id);
    options.setMessages(conv.messages);
    options.applySettings({ ...options.getSettings(), ...conv.settings });
    lastJson = snapshotJson();
    return true;
  }

  // Restore the last conversation. A limit of 0 means every visit is blank.
  const storedId = get(history.activeId);
  if (get(history.limit) > 0 && storedId) {
    void restore(storedId)
      .then((ok) => {
        if (!ok) history.activeId.set(null);
      })
      .finally(() => (restoring = false));
  } else {
    history.activeId.set(null);
  }

  // Throttled save: at most one write per interval while messages stream in.
  $effect(() => {
    const json = snapshotJson();
    return untrack(() => {
      if (restoring || json === lastJson || options.getMessages().length === 0) return;
      const wait = SAVE_INTERVAL_MS - (Date.now() - lastSaveTime);
      if (wait <= 0) {
        void write();
        return;
      }
      // Re-checked when it fires: the chat may have been detached or
      // switched meanwhile, and then there is nothing left to save.
      const timer = setTimeout(() => {
        if (snapshotJson() !== lastJson) void write();
      }, wait);
      return () => clearTimeout(timer);
    });
  });

  // Don't lose the tail of a reply to a closed tab.
  $effect(() => {
    const onHide = () => {
      if (document.visibilityState === "hidden") void flush();
    };
    document.addEventListener("visibilitychange", onHide);
    window.addEventListener("pagehide", flush);
    return () => {
      document.removeEventListener("visibilitychange", onHide);
      window.removeEventListener("pagehide", flush);
      void flush();
    };
  });

  let wasStreaming = false;
  $effect(() => {
    const streaming = options.isStreaming();
    if (wasStreaming && !streaming) untrack(() => void flush());
    wasStreaming = streaming;
  });

  return {
    /** True while restoring or switching; the page should not accept input. */
    get loading() {
      return isBusy();
    },
    flush,
    /** Starts a blank conversation; the current one is saved first. */
    async newChat() {
      switching = true;
      try {
        await flush();
        id = null;
        history.activeId.set(null);
        options.setMessages([]);
        lastJson = snapshotJson();
      } finally {
        switching = false;
      }
    },
    /** Switches to a saved conversation. False if it could not be loaded. */
    async open(convId: string): Promise<boolean> {
      if (options.isStreaming() || isBusy() || convId === id) return false;
      switching = true;
      try {
        await flush();
        return await restore(convId);
      } finally {
        switching = false;
      }
    },
    /** After the stored history was cleared: keep what is on screen, unsaved. */
    detach() {
      id = null;
      history.activeId.set(null);
      lastJson = snapshotJson();
    },
  };
}
