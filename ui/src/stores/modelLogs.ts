import { writable, type Readable } from "svelte/store";

const LOG_LENGTH_LIMIT = 1024 * 100; /* 100KB of log data */

/**
 * Stream a model's log tail by opening a long-lived fetch to
 * GET /logs/stream/{modelId} and accumulating text into a store. The
 * returned store is Readable: callers never write to it. The stream is
 * closed automatically when the last subscriber unsubscribes.
 */
export function streamModelLog(modelId: string): Readable<string> {
  const store = writable<string>("");
  let controller: AbortController | null = null;
  let started = false;

  async function run() {
    controller = new AbortController();
    try {
      const res = await fetch(`/logs/stream/${encodeURIComponent(modelId)}`, {
        method: "GET",
        signal: controller.signal,
      });
      if (!res.ok || !res.body) {
        store.set(`Failed to load logs (HTTP ${res.status})\n`);
        return;
      }
      const reader = res.body.getReader();
      const decoder = new TextDecoder();
      let acc = "";
      // eslint-disable-next-line no-constant-condition
      while (true) {
        const { value, done } = await reader.read();
        if (done) break;
        acc += decoder.decode(value, { stream: true });
        if (acc.length > LOG_LENGTH_LIMIT) {
          acc = acc.slice(-LOG_LENGTH_LIMIT);
        }
        store.set(acc);
      }
    } catch (err) {
      if (err instanceof DOMException && err.name === "AbortError") return;
      console.error(`Failed to stream logs for ${modelId}:`, err);
      store.set(`Failed to load logs: ${String(err)}\n`);
    }
  }

  return {
    subscribe(sub: (v: string) => void) {
      const unsub = store.subscribe(sub);
      if (!started) {
        started = true;
        void run();
      }
      return () => {
        unsub();
        controller?.abort();
        controller = null;
        started = false;
        store.set("");
      };
    },
  };
}
