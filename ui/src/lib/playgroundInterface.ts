import { get, writable, type Writable } from "svelte/store";
import { persistentStore } from "../stores/persistent";

export interface PlaygroundInterface {
  /** Persisted selected-model id for this interface. */
  selectedModel: Writable<string>;
  /** True while a request is in flight. Shared with the global activity store. */
  busy: Writable<boolean>;
  /** Last error message, or null. Cleared at the start of each run. */
  error: Writable<string | null>;
  /** Run an async task with abort-controller setup, AbortError swallowing and busy/error handling. */
  run: (task: (signal: AbortSignal) => Promise<void>) => Promise<void>;
  /** Abort the in-flight task, if any. */
  cancel: () => void;
}

/**
 * Shared state + lifecycle for a playground interface (model selection,
 * busy/error tracking, abort handling). `busy` is the interface's entry in the
 * global playground activity store so the sidebar indicator stays in sync.
 */
export function createPlaygroundInterface(
  storageKey: string,
  busy: Writable<boolean>
): PlaygroundInterface {
  const selectedModel = persistentStore<string>(storageKey, "");
  const error = writable<string | null>(null);
  let abort: AbortController | null = null;

  async function run(task: (signal: AbortSignal) => Promise<void>): Promise<void> {
    if (get(busy)) return;
    busy.set(true);
    error.set(null);
    abort = new AbortController();
    try {
      await task(abort.signal);
    } catch (err) {
      if (!(err instanceof Error && err.name === "AbortError")) {
        error.set(err instanceof Error ? err.message : "An error occurred");
      }
    } finally {
      busy.set(false);
      abort = null;
    }
  }

  function cancel(): void {
    abort?.abort();
  }

  return { selectedModel, busy, error, run, cancel };
}
