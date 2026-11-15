import { get, writable } from "svelte/store";
import { chatHistory, type createChatHistory, type PlainMessage } from "../stores/chatHistory";
import { playgroundStores } from "../stores/playgroundActivity";
import { streamChatCompletion } from "./chatApi";
import { currentStats, markCancelled, startTracking, trackChunk } from "./generationStats";

/** Requests own a chat ID and settings snapshot for their entire lifetime. */
export function createChatGeneration(history: ReturnType<typeof createChatHistory>, complete = streamChatCompletion) {
  const requests = new Map<string, AbortController>();
  const reasoning = writable<Record<string, boolean>>({});
  function setReasoning(id: string, value: boolean) {
    reasoning.update((state) => {
      const next = { ...state };
      if (value) next[id] = true;
      else delete next[id];
      return next;
    });
  }

  return {
    reasoning,
    cancel(id: string) { requests.get(id)?.abort(); },
    async generate(id: string, index: number, contextLength?: number) {
      const chat = get(history).chats.find((chat) => chat.id === id);
      if (!chat || requests.has(id) || !chat.settings.model) return;
      const settings = { ...chat.settings };
      // Advance recency once per request so simultaneous streams do not keep
      // shuffling the sidebar every time a token or stats update arrives.
      const messages = history.messagesFor(id, false);
      const controller = new AbortController();
      requests.set(id, controller);
      history.generating.update((ids) => new Set(ids).add(id));
      const conversation = chat.messages.slice(0, index + 1);
      history.messagesFor(id).set([...conversation, { role: "assistant", content: "" }]);
      const prompt = settings.systemPrompt.trim();
      const input = prompt ? [{ role: "system" as const, content: prompt }, ...conversation] : conversation;
      let reasoningStarted = 0;
      let thinking = false;
      let cancelled = false;
      function patchLast(patch: Partial<PlainMessage>) {
        messages.update((all) => all.map((msg, i) => i === all.length - 1 ? { ...msg, ...patch } : msg));
      }
      function last() { return get(messages).at(-1)!; }
      function lastText() { const content = last().content; return typeof content === "string" ? content : ""; }
      function endReasoning() {
        if (thinking) patchLast({ reasoningTimeMs: Date.now() - reasoningStarted });
        thinking = false;
        setReasoning(id, false);
      }
      const tracker = startTracking(performance.now(), contextLength);
      const ticker = setInterval(() => {
        patchLast({ stats: currentStats(tracker, performance.now(), true) });
      }, 200);
      try {
        for await (const chunk of complete(settings.model, input, controller.signal, {
          temperature: settings.temperature, endpoint: settings.endpoint, max_tokens: settings.maxTokens,
          top_p: settings.topP, top_k: settings.topK, min_p: settings.minP,
        })) {
          // Some transports can yield a buffered chunk after cancellation.
          if (controller.signal.aborted) throw new DOMException("Cancelled", "AbortError");
          trackChunk(tracker, chunk, performance.now());
          if (chunk.done) break;
          if (chunk.reasoning_content) {
            if (!thinking) { thinking = true; reasoningStarted = Date.now(); setReasoning(id, true); }
            patchLast({ reasoning_content: (last().reasoning_content ?? "") + chunk.reasoning_content });
          }
          if (chunk.content) {
            endReasoning();
            patchLast({ content: lastText() + chunk.content });
          }
          patchLast({ stats: currentStats(tracker, performance.now(), true) });
        }
        if (controller.signal.aborted) throw new DOMException("Cancelled", "AbortError");
      } catch (error) {
        if (controller.signal.aborted || (error instanceof Error && error.name === "AbortError")) {
          cancelled = true;
          markCancelled(tracker);
          patchLast({ interrupted: "cancelled" });
        } else {
          const message = error instanceof Error ? error.message : "An error occurred";
          patchLast({ content: lastText() + `\n\n**Error:** ${message}`, interrupted: "error" });
        }
      } finally {
        clearInterval(ticker);
        endReasoning();
        patchLast({ stats: currentStats(tracker, performance.now(), false) });
        requests.delete(id);
        history.generating.update((ids) => { const next = new Set(ids); next.delete(id); return next; });
        if (!cancelled) history.complete(id);
        history.flush();
      }
    },
  };
}

export const chatGeneration = createChatGeneration(chatHistory);
chatHistory.generating.subscribe((ids) => playgroundStores.chatStreaming.set(ids.size > 0));
