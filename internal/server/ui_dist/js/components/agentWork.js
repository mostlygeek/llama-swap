// Agent work items: collapsible reasoning blocks and tool-call cards rendered
// inside an assistant response. Ported from components/playground/AgentWork.svelte.
import { el, escapeHtml } from "../dom.js";
import { friendlyToolName } from "../agent/agentTools.js";

function formatDuration(ms) {
  if (ms < 1000) return `${Math.round(ms)}ms`;
  return `${(ms / 1000).toFixed(1)}s`;
}

// One work item: { kind: "reasoning", content, durationMs, running }
//          or   { kind: "tool", name, label, args, content, ok, durationMs, running }
export function AgentWork({ workItems = [], onCollapse } = {}) {
  const root = el(`<div class="agent-work"></div>`);

  function render() {
    if (workItems.length === 0) {
      root.innerHTML = "";
      return;
    }
    root.innerHTML = workItems
      .map((item, i) => {
        if (item.kind === "reasoning") {
          const meta = item.running
            ? "thinking…"
            : `${item.content.length} chars${item.durationMs > 0 ? `, ${formatDuration(item.durationMs)}` : ""}`;
          return `
            <details class="agent-work-item agent-reasoning" ${item.running ? "open" : ""}>
              <summary class="agent-work-summary">
                <span class="agent-chevron" aria-hidden="true">▸</span>
                <span class="agent-work-label">Reasoning</span>
                <span class="muted agent-work-meta">${escapeHtml(meta)}</span>
              </summary>
              <div class="agent-work-body">${escapeHtml(item.content)}</div>
            </details>`;
        }
        const statusCls = item.ok === false ? "agent-tool--err" : item.ok === true ? "agent-tool--ok" : "agent-tool--running";
        const statusText = item.running ? "running…" : item.ok === false ? "failed" : item.ok === true ? formatDuration(item.durationMs) : "";
        return `
          <details class="agent-work-item agent-tool ${statusCls}" ${item.running ? "open" : ""}>
            <summary class="agent-work-summary">
              <span class="agent-chevron" aria-hidden="true">▸</span>
              <span class="agent-tool-icon" aria-hidden="true">🛠</span>
              <span class="agent-work-label">${escapeHtml(item.label || item.name)}</span>
              <span class="muted agent-work-meta">${escapeHtml(statusText)}</span>
            </summary>
            <div class="agent-work-body">
              ${item.args ? `<pre class="agent-tool-args">${escapeHtml(item.args)}</pre>` : ""}
              <div class="agent-tool-result">${escapeHtml(item.content || "")}</div>
            </div>
          </details>`;
      })
      .join("");
  }

  render();

  return {
    el: root,
    setItems(next) {
      workItems = next;
      render();
    },
    destroy() {},
  };
}

export { friendlyToolName };
