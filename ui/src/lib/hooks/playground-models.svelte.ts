import { fromStore } from "svelte/store";
import { fetchPlaygroundModels, models } from "../../stores/api";

const MODEL_REFRESH_DEBOUNCE_MS = 200;

/**
 * Keeps the playground model list in step with the server's for as long as the
 * calling component lives. Call it during setup.
 *
 * The Playground and Help both need that list, and this ran in the Playground
 * for both of them while Help was one of its tabs. Now that Help is its own
 * page it asks for the list itself, rather than working only because the
 * Playground happens to be mounted too. Two callers still make one request:
 * fetchPlaygroundModels shares an in-flight fetch and coalesces the refresh
 * behind it.
 */
export function refreshPlaygroundModels(): void {
  const serverModels = fromStore(models);
  let initialized = false;

  $effect(() => {
    void serverModels.current;

    // The first pass is the initial load; there is nothing to debounce yet.
    if (!initialized) {
      initialized = true;
      void fetchPlaygroundModels();
      return;
    }

    const timeout = window.setTimeout(() => {
      void fetchPlaygroundModels();
    }, MODEL_REFRESH_DEBOUNCE_MS);
    return () => window.clearTimeout(timeout);
  });
}
