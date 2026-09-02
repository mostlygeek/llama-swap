// Activity page: stats summary + table with toggleable columns + capture dialog.
// Ported from routes/Activity.svelte.
import { el, cleanupAll, escapeHtml } from "../dom.js";
import { metrics, getCapture } from "../api.js";
import { persistent } from "../store.js";
import { ActivityStats } from "../components/activityStats.js";
import { Tooltip } from "../components/tooltip.js";
import { CaptureDialogController } from "../components/captureDialog.js";

const COLUMNS = [
  { key: "id", label: "ID", defaultVisible: true },
  { key: "time", label: "Time", defaultVisible: true },
  { key: "model", label: "Model", defaultVisible: true },
  { key: "req_path", label: "Path", defaultVisible: false },
  { key: "resp_status_code", label: "Status", defaultVisible: false },
  { key: "resp_content_type", label: "Content-Type", defaultVisible: false },
  { key: "cached", label: "Cached", defaultVisible: true, tooltip: "prompt tokens from cache" },
  { key: "prompt", label: "Prompt", defaultVisible: true, tooltip: "new prompt tokens processed" },
  { key: "generated", label: "Generated", defaultVisible: true },
  { key: "prompt_speed", label: "Prompt Speed", defaultVisible: true },
  { key: "gen_speed", label: "Gen Speed", defaultVisible: true },
  { key: "duration", label: "Duration", defaultVisible: true },
  { key: "capture", label: "Capture", defaultVisible: true },
];

const DEFAULT_VISIBLE = COLUMNS.filter((c) => c.defaultVisible).map((c) => c.key);

function formatSpeed(s) {
  return s < 0 ? "unknown" : s.toFixed(2) + " t/s";
}
function formatDuration(ms) {
  return (ms / 1000).toFixed(2) + "s";
}
function formatRelativeTime(timestamp) {
  const now = new Date();
  const date = new Date(timestamp);
  const diff = Math.floor((now.getTime() - date.getTime()) / 1000);
  if (diff < 5) return "now";
  if (diff < 60) return `${diff}s ago`;
  const mins = Math.floor(diff / 60);
  if (mins < 60) return `${mins}m ago`;
  const hours = Math.floor(mins / 60);
  if (hours < 24) return `${hours}h ago`;
  return "a while ago";
}

export function ActivityPage() {
  const visibleColumns = persistent("activity-columns", DEFAULT_VISIBLE);
  let menuOpen = false;
  let loadingCaptureId = null;

  const captureDlg = CaptureDialogController();

  const root = el(`
    <div class="page page-activity">
      <div class="activity-stats-wrap" data-stats></div>
      <div class="card activity-table-wrap">
        <div class="activity-columns-wrap" data-columns-wrap>
          <div class="activity-columns-btn-wrap">
            <button class="activity-columns-btn" data-menu title="Select columns">
              <svg class="icon-4" fill="none" stroke="currentColor" viewBox="0 0 24 24">
                <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M12 6V4m0 2a2 2 0 100 4m0-4a2 2 0 110 4m-6 8a2 2 0 100-4m0 4a2 2 0 110-4m0 4v2m0-6V4m6 6v10m6-2a2 2 0 100-4m0 4a2 2 0 110-4m0 4v2m0-6V4"></path>
              </svg>
            </button>
            <div class="activity-columns-menu" data-menu-pop style="display:none">
              <div class="activity-columns-head">Columns</div>
            </div>
          </div>
        </div>
        <table class="activity-table">
          <thead class="activity-thead"><tr data-head></tr></thead>
          <tbody class="activity-tbody" data-body></tbody>
        </table>
      </div>
    </div>
  `);

  // Stats
  const stats = ActivityStats();
  root.querySelector("[data-stats]").appendChild(stats.el);

  const headRow = root.querySelector("[data-head]");
  const body = root.querySelector("[data-body]");
  const menuBtn = root.querySelector("[data-menu]");
  const menuPop = root.querySelector("[data-menu-pop]");

  function visible() {
    const set = new Set(visibleColumns.get());
    return COLUMNS.filter((c) => set.has(c.key));
  }

  function renderHead() {
    const cols = visible();
    headRow.innerHTML = cols
      .map((c) => {
        const tip = c.tooltip ? `<span class="activity-th-tip" data-tip="${c.key}"></span>` : "";
        return `<th class="activity-th">${escapeHtml(c.label)}${tip}</th>`;
      })
      .join("");
    // Attach tooltips after innerHTML rebuild
    cols.forEach((c) => {
      if (!c.tooltip) return;
      const holder = headRow.querySelector(`[data-tip="${c.key}"]`);
      if (holder) {
        const tip = Tooltip({ content: c.tooltip });
        holder.appendChild(tip.el);
      }
    });
  }

  function renderBody() {
    const cols = visible();
    const sorted = [...metrics.get()].sort((a, b) => b.id - a.id);
    if (sorted.length === 0) {
      body.innerHTML = `<tr><td class="activity-empty" colspan="${cols.length}">No activity recorded</td></tr>`;
      return;
    }
    body.innerHTML = sorted
      .map((m) => {
        const cells = cols
          .map((c) => {
            switch (c.key) {
              case "id":
                return `<td class="activity-td">${m.id + 1}</td>`;
              case "time":
                return `<td class="activity-td">${escapeHtml(formatRelativeTime(m.timestamp))}</td>`;
              case "model":
                return `<td class="activity-td">${escapeHtml(m.model || "")}</td>`;
              case "req_path":
                return `<td class="activity-td">${escapeHtml(m.req_path || "-")}</td>`;
              case "resp_status_code":
                return `<td class="activity-td">${escapeHtml(String(m.resp_status_code || "-"))}</td>`;
              case "resp_content_type":
                return `<td class="activity-td">${escapeHtml(m.resp_content_type || "-")}</td>`;
              case "cached":
                return `<td class="activity-td">${
                  m.tokens.cache_tokens > 0 ? m.tokens.cache_tokens.toLocaleString() : "-"
                }</td>`;
              case "prompt":
                return `<td class="activity-td">${m.tokens.input_tokens.toLocaleString()}</td>`;
              case "generated":
                return `<td class="activity-td">${m.tokens.output_tokens.toLocaleString()}</td>`;
              case "prompt_speed":
                return `<td class="activity-td">${formatSpeed(m.tokens.prompt_per_second)}</td>`;
              case "gen_speed":
                return `<td class="activity-td">${formatSpeed(m.tokens.tokens_per_second)}</td>`;
              case "duration":
                return `<td class="activity-td">${formatDuration(m.duration_ms)}</td>`;
              case "capture":
                return m.has_capture
                  ? `<td class="activity-td"><button class="btn btn--sm" data-view="${m.id}" ${
                      loadingCaptureId === m.id ? "disabled" : ""
                    }>${loadingCaptureId === m.id ? "..." : "View"}</button></td>`
                  : `<td class="activity-td muted">-</td>`;
              default:
                return `<td class="activity-td"></td>`;
            }
          })
          .join("");
        return `<tr class="activity-tr">${cells}</tr>`;
      })
      .join("");
  }

  function renderMenu() {
    if (!menuOpen) {
      menuPop.style.display = "none";
      return;
    }
    const cur = new Set(visibleColumns.get());
    const items = COLUMNS.map(
      (c) => `
        <label class="activity-columns-item">
          <input type="checkbox" data-col="${c.key}" ${cur.has(c.key) ? "checked" : ""} />
          ${escapeHtml(c.label)}
        </label>`
    ).join("");
    menuPop.innerHTML = `<div class="activity-columns-head">Columns</div>${items}`;
    menuPop.style.display = "";
    menuPop.querySelectorAll("[data-col]").forEach((cb) =>
      cb.addEventListener("change", () => {
        const key = cb.getAttribute("data-col");
        const list = visibleColumns.get();
        if (list.includes(key)) {
          if (list.length > 1) visibleColumns.set(list.filter((k) => k !== key));
        } else {
          visibleColumns.set([...list, key]);
        }
      })
    );
  }

  menuBtn.addEventListener("click", (e) => {
    e.stopPropagation();
    menuOpen = !menuOpen;
    renderMenu();
  });
  document.addEventListener("click", (e) => {
    if (!menuOpen) return;
    if (!menuPop.contains(e.target) && e.target !== menuBtn && !menuBtn.contains(e.target)) {
      menuOpen = false;
      renderMenu();
    }
  });
  document.addEventListener("keydown", (e) => {
    if (e.key === "Escape" && menuOpen) {
      menuOpen = false;
      renderMenu();
    }
  });

  body.addEventListener("click", async (e) => {
    const btn = e.target.closest("[data-view]");
    if (!btn) return;
    const id = Number(btn.getAttribute("data-view"));
    loadingCaptureId = id;
    renderBody();
    try {
      const cap = await getCapture(id);
      if (cap) captureDlg.open(cap);
    } finally {
      loadingCaptureId = null;
      renderBody();
    }
  });

  const subs = [metrics.subscribe(renderBody), visibleColumns.subscribe(() => {
    renderHead();
    renderBody();
  })];

  renderHead();
  renderBody();

  return {
    el: root,
    destroy() {
      cleanupAll(subs);
      stats.destroy();
      captureDlg.destroy();
    },
  };
}
