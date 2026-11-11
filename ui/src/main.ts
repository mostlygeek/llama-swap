import "./index.css";
import App from "./App.svelte";
import { mount } from "svelte";

// After llama-swap is upgraded, a tab that was left open still references the
// old hashed chunk filenames. The server answers those with a 404 (text/plain),
// the lazy route import fails and the page hangs on its spinner. Reload once to
// pick up the new index.html; the timestamp guard prevents a reload loop if the
// failure is something else (e.g. the server is down).
const RELOAD_KEY = "chunk-reload-at";
function reloadOnStaleChunk() {
  const last = Number(sessionStorage.getItem(RELOAD_KEY) ?? 0);
  if (Date.now() - last < 10_000) return;
  sessionStorage.setItem(RELOAD_KEY, String(Date.now()));
  window.location.reload();
}

window.addEventListener("vite:preloadError", (event) => {
  event.preventDefault();
  reloadOnStaleChunk();
});

window.addEventListener("unhandledrejection", (event) => {
  const msg = String((event.reason as Error | undefined)?.message ?? event.reason);
  if (/dynamically imported module|Importing a module script failed|Failed to fetch dynamically/i.test(msg)) {
    reloadOnStaleChunk();
  }
});

const app = mount(App, {
  target: document.getElementById("app")!,
});

export default app;
