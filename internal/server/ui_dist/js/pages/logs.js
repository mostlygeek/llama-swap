// Logs page: view-mode toggle (Both / Proxy / Upstream) + ResizablePanels or single LogPanel.
// Ported from routes/LogViewer.svelte.
import { el, cleanupAll } from "../dom.js";
import { proxyLogs, upstreamLogs } from "../api.js";
import { screenWidth } from "../theme.js";
import { persistent } from "../store.js";
import { ResizablePanels } from "../components/resizablePanels.js";
import { LogPanel } from "../components/logPanel.js";

export function LogsPage() {
  const viewMode = persistent("logviewer-view-mode", "panels");
  let active = null;
  let lastDirection = null;

  const root = el(`
    <div class="page page-logs">
      <div class="logs-toolbar">
        <button class="btn" data-mode="panels">Both</button>
        <button class="btn" data-mode="proxy">Proxy</button>
        <button class="btn" data-mode="upstream">Upstream</button>
      </div>
      <div class="logs-content" data-content></div>
    </div>
  `);

  const buttons = [...root.querySelectorAll("[data-mode]")];
  const content = root.querySelector("[data-content]");

  function updateButtonStates() {
    const cur = viewMode.get();
    buttons.forEach((b) => {
      const isActive = b.getAttribute("data-mode") === cur;
      b.classList.toggle("btn-primary-on", isActive);
    });
  }

  buttons.forEach((b) =>
    b.addEventListener("click", () => viewMode.set(b.getAttribute("data-mode")))
  );

  function teardownActive() {
    if (!active) return;
    active.destroy?.();
    if (active.el && active.el.parentNode) active.el.parentNode.removeChild(active.el);
    active = null;
  }

  function rebuild() {
    teardownActive();
    const mode = viewMode.get();
    const sw = screenWidth.get();
    const direction = sw === "xs" || sw === "sm" ? "vertical" : "horizontal";
    lastDirection = direction;

    if (mode === "panels") {
      active = ResizablePanels({
        direction,
        storageKey: "logviewer-panel-group",
        left: () => LogPanel({ id: "proxy", title: "Proxy Logs", logData: proxyLogs }),
        right: () => LogPanel({ id: "upstream", title: "Upstream Logs", logData: upstreamLogs }),
      });
    } else if (mode === "proxy") {
      active = LogPanel({ id: "proxy", title: "Proxy Logs", logData: proxyLogs });
    } else {
      active = LogPanel({ id: "upstream", title: "Upstream Logs", logData: upstreamLogs });
    }
    content.appendChild(active.el);
  }

  const subs = [
    viewMode.subscribe(() => {
      updateButtonStates();
      rebuild();
    }),
    screenWidth.subscribe(() => {
      const sw = screenWidth.get();
      const direction = sw === "xs" || sw === "sm" ? "vertical" : "horizontal";
      if (direction !== lastDirection && viewMode.get() === "panels") rebuild();
    }),
  ];

  updateButtonStates();
  rebuild();

  return {
    el: root,
    destroy() {
      cleanupAll(subs);
      teardownActive();
    },
  };
}
