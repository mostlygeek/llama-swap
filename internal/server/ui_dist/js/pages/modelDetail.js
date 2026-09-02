// Model detail page: header with load/open controls + Activity / Logs / Details
// tabs. Ported from routes/ModelDetail.svelte.
import { el, cleanupAll, escapeHtml } from "../dom.js";
import { models, playgroundModels, loadModel, unloadSingleModel } from "../api.js";
import { observable } from "../store.js";
import { LogPanel } from "../components/logPanel.js";
import { ActivityTable } from "../components/activityTable.js";
import { capabilityLabels } from "../util/capabilities.js";

const LOG_LENGTH_LIMIT = 1024 * 100; // 100KB

function modelServerPath(modelId) {
  if (modelId === "comfyui_auto") return "/comfyui/";
  return `/upstream/${encodeURIComponent(modelId)}/`;
}

function statusDotClass(m) {
  if (!m) return "status-dot status-dot--idle";
  if (m.state === "ready") return "status-dot status-dot--ready";
  if (m.state === "starting" || m.state === "stopping") return "status-dot status-dot--transition";
  return "status-dot status-dot--idle";
}

// Stream a model's log tail by opening a long-lived fetch to
// GET /logs/stream/{modelId} and accumulating text into an observable.
function streamModelLog(modelId, store) {
  const controller = new AbortController();
  (async () => {
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
      for (;;) {
        const { value, done } = await reader.read();
        if (done) break;
        acc += decoder.decode(value, { stream: true });
        if (acc.length > LOG_LENGTH_LIMIT) {
          acc = acc.slice(-LOG_LENGTH_LIMIT);
        }
        store.set(acc);
      }
    } catch (err) {
      if (err.name !== "AbortError") {
        store.set((store.get() || "") + `\n[log stream error: ${err.message}]\n`);
      }
    }
  })();
  return () => controller.abort();
}

// ModelDetailPage(routeParam) — routeParam is a function returning the current
// :id path segment (so the component can react to hash changes) or a string.
export function ModelDetailPage(getModelId) {
  const root = el(`
    <div class="page page-model-detail">
      <div class="card model-detail-head" data-head></div>
      <div class="model-detail-tabs" role="tablist">
        <button class="model-detail-tab active" role="tab" data-tab="activity">Activity</button>
        <button class="model-detail-tab" role="tab" data-tab="logs">Logs</button>
        <button class="model-detail-tab" role="tab" data-tab="details">Details</button>
      </div>
      <div class="model-detail-content">
        <div class="model-detail-pane" data-pane="activity"></div>
        <div class="model-detail-pane" data-pane="logs" style="display:none"></div>
        <div class="model-detail-pane" data-pane="details" style="display:none"></div>
      </div>
    </div>
  `);

  const headEl = root.querySelector("[data-head]");
  const tabs = [...root.querySelectorAll("[data-tab]")];
  const panes = {
    activity: root.querySelector('[data-pane="activity"]'),
    logs: root.querySelector('[data-pane="logs"]'),
    details: root.querySelector('[data-pane="details"]'),
  };

  const modelLogData = observable("");
  let stopLogStream = null;
  let currentModelId = null;
  let currentTab = "activity";
  let activityTable = null;
  let logPanel = null;

  function resolveModel(id) {
    const live = models.get();
    return (
      live.find((m) => m.id === id) ??
      // fall back to an alias match so links to alias targets resolve
      live.find((m) => (m.aliases ?? []).includes(id))
    );
  }

  function renderHead() {
    const id = currentModelId ?? "";
    const m = resolveModel(id);
    if (!m) {
      headEl.innerHTML = `
        <p class="muted">Model “${escapeHtml(id)}” not found.</p>
        <a class="models-link" href="#/">Back to Playground</a>
      `;
      return;
    }
    const desc = m.description ? `<p class="muted model-detail-desc"><em>${escapeHtml(m.description)}</em></p>` : "";
    const aliases =
      m.aliases && m.aliases.length
        ? `<p class="muted model-detail-aliases">Aliases: ${escapeHtml(m.aliases.join(", "))}</p>`
        : "";
    const action =
      m.state === "stopped"
        ? `<button class="btn btn--sm" data-load="${escapeHtml(m.id)}">Load</button>`
        : `<button class="btn btn--sm" data-unload="${escapeHtml(m.id)}" ${m.state !== "ready" ? "disabled" : ""}>Unload</button>`;
    headEl.innerHTML = `
      <div class="model-detail-head-row">
        <span class="${statusDotClass(m)}" title="${escapeHtml(m.state)}"></span>
        <h2 class="model-detail-title">${escapeHtml(m.name || m.id)}</h2>
        <span class="muted model-detail-id">(${escapeHtml(m.id)})</span>
        <span class="status status--${escapeHtml(m.state)}">${escapeHtml(m.state)}</span>
        <div class="model-detail-head-actions">
          ${
            !m.peerID
              ? `<a class="models-open" href="${modelServerPath(m.id)}" target="_blank" rel="noopener" title="Open model server">↗</a>${action}`
              : ""
          }
        </div>
      </div>
      ${desc}${aliases}
    `;
  }

  function renderDetails() {
    const id = currentModelId ?? "";
    const m = resolveModel(id);
    // Capabilities come from the /v1/models records; merge them in.
    const playground = playgroundModels.get().find((p) => p.id === (m?.id ?? id));
    const caps = Object.entries(playground?.capabilities ?? {}).filter(([, v]) => v);
    const ctx = playground?.context_length ?? 0;

    panes.details.innerHTML = `
      <div class="card model-detail-details">
        <div class="models-section-head"><h3 class="models-section-title">Capabilities</h3></div>
        <div class="model-detail-caps">
          ${
            caps.length === 0 && !ctx
              ? `<span class="muted">No capabilities reported.</span>`
              : `<div class="model-cap-badges">${caps
                  .map(([key]) => `<span class="model-cap-badge">${escapeHtml(capabilityLabels[key] ?? key)}</span>`)
                  .join("")}${
                  ctx > 0 ? `<span class="model-cap-badge cap-context">${escapeHtml(String(ctx))} context</span>` : ""
                }</div>`
          }
        </div>
      </div>
    `;
  }

  function setTab(tab) {
    currentTab = tab;
    tabs.forEach((t) => t.classList.toggle("active", t.getAttribute("data-tab") === tab));
    Object.entries(panes).forEach(([name, pane]) => {
      pane.style.display = name === tab ? "" : "none";
    });
    if (tab === "details") renderDetails();
  }

  tabs.forEach((t) => t.addEventListener("click", () => setTab(t.getAttribute("data-tab"))));

  headEl.addEventListener("click", (e) => {
    const loadBtn = e.target.closest("[data-load]");
    const unloadBtn = e.target.closest("[data-unload]");
    if (loadBtn) {
      loadModel(loadBtn.getAttribute("data-load")).catch((err) => console.error(err));
    } else if (unloadBtn) {
      unloadSingleModel(unloadBtn.getAttribute("data-unload")).catch((err) => console.error(err));
    }
  });

  function mountFor(modelId) {
    if (modelId === currentModelId) return;
    currentModelId = modelId;

    // Rebuild the activity table for the new model.
    if (activityTable) {
      activityTable.destroy();
      if (activityTable.el.parentNode) activityTable.el.parentNode.removeChild(activityTable.el);
    }
    activityTable = ActivityTable({
      model: modelId,
      showModel: false,
      showPagination: true,
      storagePrefix: "model-detail-activity",
      emptyMessage: "No activity recorded for this model",
    });
    panes.activity.appendChild(activityTable.el);

    // Restart the model log stream.
    if (stopLogStream) stopLogStream();
    if (logPanel) {
      logPanel.destroy();
      if (logPanel.el.parentNode) logPanel.el.parentNode.removeChild(logPanel.el);
    }
    modelLogData.set("");
    logPanel = LogPanel({ id: `model-${modelId}`, title: "Model Logs", logData: modelLogData });
    panes.logs.appendChild(logPanel.el);
    stopLogStream = streamModelLog(modelId, modelLogData);

    renderHead();
    renderDetails();
  }

  // React to route param changes (e.g. clicking a selector target while on the
  // detail page) and to live model status updates.
  const subs = [
    models.subscribe(() => {
      renderHead();
      if (currentTab === "details") renderDetails();
      mountFor(typeof getModelId === "function" ? getModelId() : getModelId);
    }),
    playgroundModels.subscribe(() => {
      if (currentTab === "details") renderDetails();
    }),
  ];

  mountFor(typeof getModelId === "function" ? getModelId() : getModelId);
  setTab("activity");

  return {
    el: root,
    destroy() {
      cleanupAll(subs);
      if (stopLogStream) stopLogStream();
      if (activityTable) activityTable.destroy();
      if (logPanel) logPanel.destroy();
    },
  };
}
