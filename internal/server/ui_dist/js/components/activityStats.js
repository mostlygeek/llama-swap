// Aggregate stats + dual token histograms for the Activity page. Ported from
// components/ActivityStats.svelte. The stats come from the server
// (/api/metrics/stats) rather than client-side aggregation over SSE metrics.
import { el, cleanupAll } from "../dom.js";
import { persistent } from "../store.js";
import { TokenHistogram } from "./tokenHistogram.js";

const nf = new Intl.NumberFormat();

// setStats(stats) is driven by the page after each /api/metrics/stats fetch;
// stats has the store's ActivityStats JSON shape: total_requests,
// total_cache_tokens, total_input_tokens, total_output_tokens,
// prompt_histogram, gen_histogram (HistogramData | null).
export function ActivityStats() {
  const collapsed = persistent("activity-histogram-collapsed", false);
  let stats = null;

  const root = el(`
    <div class="card activity-stats">
      <button class="activity-collapse" data-collapse title=""></button>
      <div class="activity-histograms" data-histograms></div>
      <div class="activity-grid">
        <div class="activity-grid-label">Requests</div>
        <div class="activity-grid-label">Cached</div>
        <div class="activity-grid-label">Processed</div>
        <div class="activity-grid-label">Generated</div>
        <div class="activity-grid-value"><span class="num" data-req></span> completed</div>
        <div class="activity-grid-value"><span class="num" data-cached></span> tokens</div>
        <div class="activity-grid-value"><span class="num" data-input></span> tokens</div>
        <div class="activity-grid-value"><span class="num" data-output></span> tokens</div>
      </div>
    </div>
  `);

  const collapseBtn = root.querySelector("[data-collapse]");
  const histContainer = root.querySelector("[data-histograms]");
  const reqEl = root.querySelector("[data-req]");
  const cachedEl = root.querySelector("[data-cached]");
  const inputEl = root.querySelector("[data-input]");
  const outputEl = root.querySelector("[data-output]");

  function renderHistograms() {
    histContainer.innerHTML = "";
    if (collapsed.get()) return;

    const left = document.createElement("div");
    left.className = "activity-hist-left";
    left.innerHTML = `<div class="activity-hist-label">Prompt Processing</div>`;
    if (stats?.prompt_histogram) {
      left.appendChild(
        TokenHistogram({ data: stats.prompt_histogram, unit: "prompt tokens/sec", colorClass: "hist-amber" }).el
      );
    } else {
      left.insertAdjacentHTML("beforeend", `<div class="activity-hist-empty">No prompt speed data yet</div>`);
    }
    histContainer.appendChild(left);

    const right = document.createElement("div");
    right.className = "activity-hist-right";
    right.innerHTML = `<div class="activity-hist-label">Token Generation</div>`;
    if (stats?.gen_histogram) {
      right.appendChild(TokenHistogram({ data: stats.gen_histogram, unit: "tokens/sec" }).el);
    } else {
      right.insertAdjacentHTML("beforeend", `<div class="activity-hist-empty">No generation speed data yet</div>`);
    }
    histContainer.appendChild(right);
  }

  function renderStats() {
    reqEl.textContent = nf.format(stats?.total_requests ?? 0);
    cachedEl.textContent = nf.format(stats?.total_cache_tokens ?? 0);
    inputEl.textContent = nf.format(stats?.total_input_tokens ?? 0);
    outputEl.textContent = nf.format(stats?.total_output_tokens ?? 0);
  }

  function renderCollapseBtn() {
    const isCollapsed = collapsed.get();
    collapseBtn.title = isCollapsed ? "Show histograms" : "Hide histograms";
    collapseBtn.innerHTML = isCollapsed
      ? `<svg class="icon-3-5" viewBox="0 0 16 16" fill="currentColor"><path d="M4.5 6l3.5 4 3.5-4H4.5z"/></svg>`
      : `<svg class="icon-3" viewBox="0 0 16 16" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round"><path d="M3.5 3.5l9 9M12.5 3.5l-9 9"/></svg>`;
  }

  collapseBtn.addEventListener("click", () => collapsed.update((v) => !v));

  collapsed.subscribe(() => {
    renderCollapseBtn();
    renderHistograms();
  });

  renderStats();
  renderHistograms();
  renderCollapseBtn();

  return {
    el: root,
    setStats(next) {
      stats = next;
      renderStats();
      renderHistograms();
    },
    destroy() {},
  };
}
