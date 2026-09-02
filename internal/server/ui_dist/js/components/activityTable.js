// Activity table with toggleable/sortable columns, server-side pagination,
// in-flight requests section, capture viewer, and markdown export.
// Ported from components/ActivityTable.svelte (tanstack-table replaced by
// plain DOM: sorting forwards to the caller, column order/visibility persist).
import { el, cleanupAll, escapeHtml } from "../dom.js";
import { persistent } from "../store.js";
import {
  getActivity,
  getActivityStats,
  getCapture,
  cancelInflightRequest,
  inflightRequestEntries,
  uiConfig,
} from "../api.js";
import { formatRelativeTime, formatDuration, formatSpeed, formatFileSize } from "../util/format.js";
import { formatDrafted, buildActivityMarkdown } from "../util/activityExport.js";
import { requestHeader, sessionID, formatBytes, liveElapsedMs } from "../util/inflight.js";
import { Tooltip } from "./tooltip.js";
import { CaptureDialogController } from "./captureDialog.js";

const COLUMNS = [
  { key: "id", label: "ID", defaultVisible: true, sortable: true },
  { key: "time", label: "Time", defaultVisible: true, sortable: true },
  { key: "model", label: "Model", defaultVisible: true, sortable: true },
  { key: "req_path", label: "Path", defaultVisible: false, sortable: true },
  { key: "resp_status_code", label: "Status", defaultVisible: false, sortable: true },
  { key: "resp_content_type", label: "Content-Type", defaultVisible: false, sortable: true },
  { key: "cached", label: "Cached", defaultVisible: true, tooltip: "prompt tokens from cache", sortable: true },
  { key: "prompt", label: "Prompt", defaultVisible: true, tooltip: "new prompt tokens processed", sortable: true },
  { key: "generated", label: "Generated", defaultVisible: true, sortable: true },
  { key: "drafted", label: "Drafted", defaultVisible: false, tooltip: "speculative decoding acceptance", sortable: true },
  { key: "prompt_speed", label: "Prompt Speed", defaultVisible: true, sortable: true },
  { key: "gen_speed", label: "Gen Speed", defaultVisible: true, sortable: true },
  { key: "duration", label: "Duration", defaultVisible: true, sortable: true },
  { key: "capture", label: "Capture", defaultVisible: true, sortable: false },
  { key: "meta", label: "Meta", defaultVisible: false, sortable: false },
];

const DEFAULT_VISIBLE = COLUMNS.filter((c) => c.defaultVisible).map((c) => c.key);

const INFLIGHT_COLUMNS = [
  { key: "cancel", label: "Cancel" },
  { key: "elapsed", label: "Elapsed" },
  { key: "model", label: "Model" },
  { key: "request", label: "Request" },
  { key: "identity", label: "Address" },
  { key: "user_agent", label: "User Agent" },
  { key: "session_id", label: "Session ID" },
  { key: "bytes_received", label: "Bytes Received" },
];

// Mirrors what is on screen, minus Capture: it is a button that fetches a body
// on click, so it has no text form to export.
function exportColumns(visible) {
  return visible.filter((c) => c.key !== "capture").map((c) => ({ id: c.key, label: c.label }));
}

// visiblePages: a window of page numbers around the current page, with
// ellipsis markers (null) where pages are skipped.
function pageWindow(page, pageCount, span = 2) {
  if (pageCount <= 7) return Array.from({ length: pageCount }, (_, i) => i + 1);
  const pages = new Set([1, pageCount, page]);
  for (let i = 1; i <= span; i++) {
    pages.add(Math.min(pageCount, page - i));
    pages.add(Math.max(1, page + i));
  }
  const sorted = [...pages].filter((p) => p >= 1 && p <= pageCount).sort((a, b) => a - b);
  const out = [];
  let prev = 0;
  for (const p of sorted) {
    if (p - prev > 1) out.push(null);
    out.push(p);
    prev = p;
  }
  return out;
}

function cellHtml(m, key, loadingCaptureId) {
  switch (key) {
    case "id":
      return `<td class="activity-td">${m.id}</td>`;
    case "time":
      return `<td class="activity-td">${escapeHtml(formatRelativeTime(m.timestamp))}</td>`;
    case "model": {
      const name = escapeHtml(m.model || "");
      return `<td class="activity-td"><a class="activity-model-link" href="#/models/${encodeURIComponent(m.model || "")}">${name}</a></td>`;
    }
    case "req_path":
      return `<td class="activity-td">${escapeHtml(m.req_path || "-")}</td>`;
    case "resp_status_code": {
      const code = m.resp_status_code || "-";
      const cls = code >= 500 || code === 499 ? " activity-status-err" : code >= 400 ? " activity-status-warn" : "";
      return `<td class="activity-td${cls}">${code}</td>`;
    }
    case "resp_content_type":
      return `<td class="activity-td">${escapeHtml(m.resp_content_type || "-")}</td>`;
    case "cached":
      return `<td class="activity-td">${m.tokens.cache_tokens > 0 ? m.tokens.cache_tokens.toLocaleString() : "-"}</td>`;
    case "prompt":
      return `<td class="activity-td">${m.tokens.input_tokens.toLocaleString()}</td>`;
    case "generated":
      return `<td class="activity-td">${m.tokens.output_tokens.toLocaleString()}</td>`;
    case "drafted":
      return `<td class="activity-td">${formatDrafted(m.tokens.draft_tokens, m.tokens.draft_acc_tokens)}</td>`;
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
    case "meta": {
      const entries = Object.entries(m.metadata || {});
      if (entries.length === 0) return `<td class="activity-td muted">-</td>`;
      const text = entries.map(([k, v]) => `${k}=${v}`).join("; ");
      return `<td class="activity-td activity-meta" title="${escapeHtml(text)}">${escapeHtml(text)}</td>`;
    }
    default:
      return `<td class="activity-td"></td>`;
  }
}

function inflightCellHtml(req, key, nowMs, canceling) {
  switch (key) {
    case "cancel":
      return `<td class="activity-td"><button class="btn btn--sm activity-cancel-btn" data-cancel="${escapeHtml(req.id)}" ${
        canceling ? "disabled" : ""
      } title="Cancel request">✕</button></td>`;
    case "elapsed":
      return `<td class="activity-td mono">${(liveElapsedMs(req.elapsed_ms, req.client_received_at_ms, nowMs) / 1000).toFixed(2)}s</td>`;
    case "model":
      return `<td class="activity-td" title="${escapeHtml(req.model)}">${escapeHtml(req.model || "-")}</td>`;
    case "request":
      return `<td class="activity-td mono" title="${escapeHtml((req.method || "-") + " " + (req.req_path || "-"))}">${escapeHtml(
        (req.method || "-") + " " + (req.req_path || "-")
      )}</td>`;
    case "identity":
      return `<td class="activity-td mono">${escapeHtml(req.remote_ip || "-")}</td>`;
    case "user_agent":
      return `<td class="activity-td" title="${escapeHtml(requestHeader(req.req_headers, "User-Agent"))}">${escapeHtml(
        requestHeader(req.req_headers, "User-Agent") || "-"
      )}</td>`;
    case "session_id": {
      const session = sessionID(req.req_headers, uiConfig.get()?.activity?.session_id ?? []);
      return `<td class="activity-td mono">${escapeHtml(session || "-")}</td>`;
    }
    case "bytes_received":
      return `<td class="activity-td mono">${formatBytes(req.resp_bytes)}</td>`;
    default:
      return `<td class="activity-td"></td>`;
  }
}

// ActivityTable(options):
//   model          — pin the table to one model (model detail page); omit for all
//   showModel      — show the Model column (default true)
//   showPagination — server-side paging + filter drawer (default true)
//   storagePrefix  — localStorage key prefix (default "activity")
export function ActivityTable(options = {}) {
  const {
    model = "",
    showModel = true,
    showPagination = true,
    storagePrefix = "activity",
    emptyMessage = "No activity recorded",
  } = options;

  const columns = COLUMNS.filter((c) => showModel || c.key !== "model");
  const defaultVisible = columns.filter((c) => c.defaultVisible).map((c) => c.key);

  const visibleColumns = persistent(`${storagePrefix}-columns`, defaultVisible);
  const pageSizeStore = persistent(`${storagePrefix}-page-size`, 25);
  const inflightOpenStore = persistent(`${storagePrefix}-inflight-open`, false);
  const filterOpenStore = persistent(`${storagePrefix}-filter-open`, false);
  const filterMinId = persistent(`${storagePrefix}-filter-min-id`, "");
  const filterMaxId = persistent(`${storagePrefix}-filter-max-id`, "");

  let rows = [];
  let page = 1;
  let limit = pageSizeStore.get();
  let sort = "id";
  let order = "desc";
  let total = 0;
  let totalPages = 0;
  let loadingCaptureId = null;
  let cancelingIds = [];
  let menuOpen = false;
  let raf = 0;
  let nowMs = performance.now();

  const captureDlg = CaptureDialogController();

  const root = el(`
    <div class="activity-table-root">
      <div class="card activity-inflight" data-inflight>
        <div class="activity-inflight-head">
          <span class="activity-inflight-label">In-flight Requests</span>
          <span class="activity-inflight-count"><strong data-inflight-count>0</strong> active</span>
          <div class="activity-inflight-actions">
            <button class="activity-icon-btn" data-inflight-toggle title="Show in-flight requests"></button>
          </div>
        </div>
        <div class="activity-inflight-body" data-inflight-body style="display:none">
          <table class="activity-table activity-inflight-table">
            <thead><tr data-inflight-head></tr></thead>
            <tbody data-inflight-tbody></tbody>
          </table>
        </div>
      </div>
      <div class="card activity-table-wrap">
        <div class="activity-table-toolbar">
          <div class="activity-table-title" data-title></div>
          <div class="activity-table-tools">
            <button class="activity-icon-btn" data-export title="Export as markdown">
              <svg class="icon-4" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M21 15v4a2 2 0 01-2 2H5a2 2 0 01-2-2v-4"/><polyline points="7 10 12 15 17 10"/><line x1="12" y1="15" x2="12" y2="3"/></svg>
            </button>
            ${
              showPagination
                ? `<button class="activity-icon-btn" data-filter-toggle title="Show filters">
                <svg class="icon-4" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><polygon points="22 3 2 3 10 12.46 10 19 14 21 14 12.46 22 3"/></svg>
              </button>`
                : ""
            }
            <button class="activity-icon-btn" data-menu title="Select columns">
              <svg class="icon-4" fill="none" stroke="currentColor" viewBox="0 0 24 24"><path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M12 6V4m0 2a2 2 0 100 4m0-4a2 2 0 110 4m-6 8a2 2 0 100-4m0 4a2 2 0 110-4m0 4v2m0-6V4m6 6v10m6-2a2 2 0 100-4m0 4a2 2 0 110-4m0 4v2m0-6V4"></path></svg>
            </button>
            <div class="activity-columns-menu" data-menu-pop style="display:none"></div>
          </div>
        </div>
        <div class="activity-filter-drawer" data-filter-drawer style="display:none">
          <label>Min ID <input type="number" min="1" data-filter-min placeholder="min" /></label>
          <label>Max ID <input type="number" min="1" data-filter-max placeholder="max" /></label>
          <button class="btn btn--sm" data-filter-clear>Clear</button>
        </div>
        <table class="activity-table">
          <thead class="activity-thead"><tr data-head></tr></thead>
          <tbody class="activity-tbody" data-body></tbody>
        </table>
        <div class="activity-pagination" data-pagination style="display:none"></div>
      </div>
      <dialog class="activity-export-dialog" data-export-dialog>
        <div class="activity-export-head">
          <h3>Export</h3>
          <div>
            <button class="btn btn--sm" data-export-copy>Copy</button>
            <button class="btn btn--sm" data-export-close>Close</button>
          </div>
        </div>
        <pre class="activity-export-pre" data-export-pre></pre>
      </dialog>
    </div>
  `);

  const headRow = root.querySelector("[data-head]");
  const bodyEl = root.querySelector("[data-body]");
  const titleEl = root.querySelector("[data-title]");
  const menuBtn = root.querySelector("[data-menu]");
  const menuPop = root.querySelector("[data-menu-pop]");
  const filterBtn = root.querySelector("[data-filter-toggle]");
  const filterDrawer = root.querySelector("[data-filter-drawer]");
  const filterMin = root.querySelector("[data-filter-min]");
  const filterMax = root.querySelector("[data-filter-max]");
  const paginationEl = root.querySelector("[data-pagination]");
  const inflightToggle = root.querySelector("[data-inflight-toggle]");
  const inflightBody = root.querySelector("[data-inflight-body]");
  const inflightHead = root.querySelector("[data-inflight-head]");
  const inflightTbody = root.querySelector("[data-inflight-tbody]");
  const inflightCount = root.querySelector("[data-inflight-count]");
  const exportDialog = root.querySelector("[data-export-dialog]");
  const exportPre = root.querySelector("[data-export-pre]");

  filterMin.value = filterMinId.get();
  filterMax.value = filterMaxId.get();

  function visible() {
    const set = new Set(visibleColumns.get());
    return columns.filter((c) => set.has(c.key));
  }

  function sortIndicator(key) {
    if (key !== sort) return `<span class="activity-sort-ind"></span>`;
    return `<span class="activity-sort-ind active">${order === "asc" ? "▲" : "▼"}</span>`;
  }

  function renderHead() {
    const cols = visible();
    headRow.innerHTML = cols
      .map((c) => {
        const tip = c.tooltip ? `<span class="activity-th-tip" data-tip="${c.key}"></span>` : "";
        const label = escapeHtml(c.label);
        if (c.sortable) {
          return `<th class="activity-th activity-th-sortable" data-sort="${c.key}"><span class="activity-th-label">${label}${tip}</span>${sortIndicator(c.key)}</th>`;
        }
        return `<th class="activity-th">${label}${tip}</th>`;
      })
      .join("");
    cols.forEach((c) => {
      if (!c.tooltip) return;
      const holder = headRow.querySelector(`[data-tip="${c.key}"]`);
      if (holder) holder.appendChild(Tooltip({ content: c.tooltip }).el);
    });
  }

  function renderBody() {
    const cols = visible();
    if (rows.length === 0) {
      bodyEl.innerHTML = `<tr><td class="activity-empty" colspan="${cols.length}">${escapeHtml(emptyMessage)}</td></tr>`;
      return;
    }
    bodyEl.innerHTML = rows.map((m) => `<tr class="activity-tr">${cols.map((c) => cellHtml(m, c.key, loadingCaptureId)).join("")}</tr>`).join("");
  }

  function renderInflightHead() {
    inflightHead.innerHTML = INFLIGHT_COLUMNS.map(
      (c) => `<th class="activity-th activity-inflight-th">${escapeHtml(c.label)}</th>`
    ).join("");
  }

  function renderInflightBody() {
    const reqs = model
      ? inflightRequestEntries.get().filter((r) => r.model === model)
      : inflightRequestEntries.get();
    inflightCount.textContent = String(reqs.length);
    if (reqs.length === 0) {
      inflightTbody.innerHTML = `<tr><td class="activity-empty" colspan="${INFLIGHT_COLUMNS.length}">No in-flight requests</td></tr>`;
      return;
    }
    inflightTbody.innerHTML = reqs
      .map(
        (r) =>
          `<tr class="activity-tr">${INFLIGHT_COLUMNS.map((c) => inflightCellHtml(r, c.key, nowMs, cancelingIds.includes(r.id))).join("")}</tr>`
      )
      .join("");
  }

  function renderInflightToggle() {
    const open = inflightOpenStore.get();
    inflightBody.style.display = open ? "" : "none";
    inflightToggle.title = open ? "Hide in-flight requests" : "Show in-flight requests";
    inflightToggle.innerHTML = open
      ? `<svg class="icon-3" viewBox="0 0 16 16" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round"><path d="M3.5 3.5l9 9M12.5 3.5l-9 9"/></svg>`
      : `<svg class="icon-3-5" viewBox="0 0 16 16" fill="currentColor"><path d="M4.5 6l3.5 4 3.5-4H4.5z"/></svg>`;
  }

  function renderPagination() {
    if (!showPagination || total === 0) {
      paginationEl.style.display = "none";
      return;
    }
    paginationEl.style.display = "";
    const pageNumbers = pageWindow(page, totalPages)
      .map((p) => (p === null ? `<span class="activity-page-ellipsis">…</span>` : `<button class="activity-page-btn ${p === page ? "active" : ""}" data-page="${p}" ${p === page ? "disabled" : ""}>${p}</button>`))
      .join("");
    paginationEl.innerHTML = `
      <span class="activity-page-info">Page ${page} of ${totalPages} · ${total.toLocaleString()} total</span>
      <div class="activity-page-btns">
        <button class="activity-page-nav" data-page="1" ${page <= 1 ? "disabled" : ""} title="First page">⇤</button>
        <button class="activity-page-nav" data-page="${page - 1}" ${page <= 1 ? "disabled" : ""} title="Previous page">‹</button>
        ${pageNumbers}
        <button class="activity-page-nav" data-page="${page + 1}" ${page >= totalPages ? "disabled" : ""} title="Next page">›</button>
        <button class="activity-page-nav" data-page="${totalPages}" ${page >= totalPages ? "disabled" : ""} title="Last page">⇥</button>
      </div>
    `;
  }

  function renderTitle() {
    if (model) {
      titleEl.innerHTML = `Activity <span class="muted activity-title-count">(${total.toLocaleString()})</span>`;
    } else {
      titleEl.innerHTML = "";
    }
  }

  function renderMenu() {
    if (!menuOpen) {
      menuPop.style.display = "none";
      return;
    }
    const cur = new Set(visibleColumns.get());
    menuPop.innerHTML = `<div class="activity-columns-head">Columns</div>` + columns
      .map(
        (c) => `
        <label class="activity-columns-item">
          <input type="checkbox" data-col="${c.key}" ${cur.has(c.key) ? "checked" : ""} />
          ${escapeHtml(c.label)}
        </label>`
      )
      .join("");
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

  function renderFilterDrawer() {
    const open = filterOpenStore.get();
    filterDrawer.style.display = open ? "" : "none";
    if (filterBtn) {
      filterBtn.title = open ? "Hide filters" : "Show filters";
      filterBtn.classList.toggle("active", open);
      const active = filterMinId.get() !== "" || filterMaxId.get() !== "";
      filterBtn.classList.toggle("has-badge", active);
    }
  }

  async function refresh() {
    try {
      const params = { page, limit, sort, order };
      if (model) params.model = model;
      if (filterMinId.get()) params.minID = Number(filterMinId.get());
      if (filterMaxId.get()) params.maxID = Number(filterMaxId.get());
      const activity = await getActivity(params);
      rows = activity.data ?? [];
      total = activity.total ?? 0;
      totalPages = activity.total_pages ?? 0;
      renderBody();
      renderPagination();
      renderTitle();
    } catch (error) {
      console.error("Failed to refresh activity:", error);
    }
  }

  function setSort(nextSort) {
    if (sort === nextSort) {
      order = order === "asc" ? "desc" : "asc";
    } else {
      sort = nextSort;
      order = "desc";
    }
    page = 1;
    renderHead();
    refresh();
  }

  headRow.addEventListener("click", (e) => {
    const th = e.target.closest("[data-sort]");
    if (th) setSort(th.getAttribute("data-sort"));
  });

  paginationEl.addEventListener("click", (e) => {
    const btn = e.target.closest("[data-page]");
    if (!btn || btn.disabled) return;
    const next = Number(btn.getAttribute("data-page"));
    if (next >= 1 && next <= totalPages && next !== page) {
      page = next;
      refresh();
    }
  });

  bodyEl.addEventListener("click", async (e) => {
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

  inflightTbody.addEventListener("click", async (e) => {
    const btn = e.target.closest("[data-cancel]");
    if (!btn) return;
    const id = btn.getAttribute("data-cancel");
    if (cancelingIds.includes(id)) return;
    cancelingIds = [...cancelingIds, id];
    renderInflightBody();
    try {
      await cancelInflightRequest(id);
    } catch (err) {
      console.error(err);
    } finally {
      cancelingIds = cancelingIds.filter((c) => c !== id);
      renderInflightBody();
    }
  });

  inflightToggle.addEventListener("click", () => {
    inflightOpenStore.update((v) => !v);
    renderInflightToggle();
  });

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

  if (filterBtn) {
    filterBtn.addEventListener("click", () => {
      filterOpenStore.update((v) => !v);
      renderFilterDrawer();
    });
  }
  const applyFilters = () => {
    filterMinId.set(filterMin.value.trim());
    filterMaxId.set(filterMax.value.trim());
    page = 1;
    refresh();
    renderFilterDrawer();
  };
  filterMin.addEventListener("change", applyFilters);
  filterMax.addEventListener("change", applyFilters);
  root.querySelector("[data-filter-clear]").addEventListener("click", () => {
    filterMin.value = "";
    filterMax.value = "";
    applyFilters();
  });

  root.querySelector("[data-export]").addEventListener("click", () => {
    exportPre.textContent = buildActivityMarkdown(rows, exportColumns(visible()));
    if (!exportDialog.open) exportDialog.showModal();
  });
  root.querySelector("[data-export-close]").addEventListener("click", () => exportDialog.close());
  root.querySelector("[data-export-copy]").addEventListener("click", async (e) => {
    try {
      await navigator.clipboard.writeText(exportPre.textContent);
      e.target.textContent = "Copied!";
      setTimeout(() => (e.target.textContent = "Copy"), 1500);
    } catch {
      exportPre.focus();
      document.execCommand("selectAll", false, null);
    }
  });

  const subs = [
    visibleColumns.subscribe(() => {
      renderHead();
      renderBody();
    }),
    inflightRequestEntries.subscribe(() => {
      renderInflightBody();
      // Refresh synchronously when requests appear so the first render's
      // elapsed times are not stale; then tick with animation frames.
      if (inflightRequestEntries.get().length > 0 && !raf) {
        nowMs = performance.now();
        const tick = () => {
          nowMs = performance.now();
          raf = requestAnimationFrame(tick);
        };
        raf = requestAnimationFrame(tick);
      } else if (inflightRequestEntries.get().length === 0 && raf) {
        cancelAnimationFrame(raf);
        raf = 0;
      }
    }),
  ];

  renderHead();
  renderInflightHead();
  renderInflightToggle();
  renderFilterDrawer();
  renderPagination();
  renderTitle();
  refresh();

  return {
    el: root,
    refresh,
    destroy() {
      cleanupAll(subs);
      if (raf) cancelAnimationFrame(raf);
      captureDlg.destroy();
    },
  };
}
