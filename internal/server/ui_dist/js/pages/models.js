// Models page: ModelsPanel on the left, Upstream logs on the right (ResizablePanels).
// Ported from routes/Models.svelte.
import { el, cleanupAll } from "../dom.js";
import { isNarrow } from "../theme.js";
import { upstreamLogs } from "../api.js";
import { ResizablePanels } from "../components/resizablePanels.js";
import { ModelsPanel } from "../components/modelsPanel.js";
import { LogPanel } from "../components/logPanel.js";

export function ModelsPage() {
  let panels = null;
  const root = el(`<div class="page page-models"></div>`);

  let lastDirection = null;

  function rebuild() {
    if (panels) {
      panels.destroy();
      if (panels.el.parentNode) panels.el.parentNode.removeChild(panels.el);
      panels = null;
    }
    const direction = isNarrow.get() ? "vertical" : "horizontal";
    lastDirection = direction;
    panels = ResizablePanels({
      direction,
      storageKey: "models-panel-group",
      left: () => ModelsPanel(),
      right: () => LogPanel({ id: "modelsupstream", title: "Upstream Logs", logData: upstreamLogs }),
    });
    root.appendChild(panels.el);
  }

  const sub = isNarrow.subscribe(() => {
    const direction = isNarrow.get() ? "vertical" : "horizontal";
    if (direction !== lastDirection) rebuild();
  });

  rebuild();

  return {
    el: root,
    destroy() {
      cleanupAll([sub, panels ? panels.destroy : null]);
    },
  };
}
