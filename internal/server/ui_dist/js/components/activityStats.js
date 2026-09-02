// Aggregate stats + dual token histograms for the Activity page.
// Ported from components/ActivityStats.svelte.
import { el, cleanupAll } from "../dom.js";
import { metrics, inFlightRequests } from "../api.js";
import { persistent } from "../store.js";
import { calculateHistogramData } from "../util/histogram.js";
import { TokenHistogram } from "./tokenHistogram.js";

const nf = new Intl.NumberFormat();

export function ActivityStats() {
  const collapsed = persistent("activity-histogram-collapsed", false);

  const root = el(`
    <div class="card activity-stats">
      <button class="activity-collapse" data-collapse title=""></button>
      <div class="activity-histograms" data-histograms></div>
      <div class="activity-grid">
        <div class="activity-grid-label">Requests</div>
        <div class="activity-grid-label">Cached</div>
        <div class="activity-grid-label">Processed</div>
        <div class="activity-grid-label">Generated</div>
        <div class="activity-grid-value"><span class="num" data-req></span> completed, <span class="num" data-wait></span> waiting</div>
        <div class="activity-grid-value"><span class="num" data-cached></span> tokens</div>
        <div class="activity-grid-value"><span class="num" data-input></span> tokens</div>
        <div class="activity-grid-value"><span class="num" data-output></span> tokens</div>
      </div>
    </div>
  `);

  const collapseBtn = root.querySelector("[data-collapse]");
  const histContainer = root.querySelector("[data-histograms]");
  const reqEl = root.querySelector("[data-req]");
  const waitEl = root.querySelector("[data-wait]");
  const cachedEl = root.querySelector("[data-cached]");
  const inputEl = root.querySelector("[data-input]");
  const outputEl = root.querySelector("[data-output]");

  let promptHist = null;
  let genHist = null;

  function renderHistograms() {
    histContainer.innerHTML = "";
    if (collapsed.get()) return;

    const m = metrics.get();
    const promptData = m
      .filter((x) => x.tokens.prompt_per_second > 0)
      .map((x) => x.tokens.prompt_per_second);
    const genData = m
      .filter((x) => x.tokens.tokens_per_second > 0)
      .map((x) => x.tokens.tokens_per_second);
    const promptHd = promptData.length ? calculateHistogramData(promptData) : null;
    const genHd = genData.length ? calculateHistogramData(genData) : null;

    const left = document.createElement("div");
    left.className = "activity-hist-left";
    left.innerHTML = `<div class="activity-hist-label">Prompt Processing</div>`;
    if (promptHd) {
      promptHist = TokenHistogram({
        data: promptHd,
        unit: "prompt tokens/sec",
        colorClass: "hist-amber",
      });
      left.appendChild(promptHist.el);
    } else {
      left.insertAdjacentHTML("beforeend", `<div class="activity-hist-empty">No prompt speed data yet</div>`);
    }
    histContainer.appendChild(left);

    const right = document.createElement("div");
    right.className = "activity-hist-right";
    right.innerHTML = `<div class="activity-hist-label">Token Generation</div>`;
    if (genHd) {
      genHist = TokenHistogram({ data: genHd, unit: "tokens/sec" });
      right.appendChild(genHist.el);
    } else {
      right.insertAdjacentHTML("beforeend", `<div class="activity-hist-empty">No generation speed data yet</div>`);
    }
    histContainer.appendChild(right);
  }

  function renderStats() {
    const m = metrics.get();
    const totalReq = m.length;
    const totalIn = m.reduce((s, x) => s + x.tokens.input_tokens, 0);
    const totalOut = m.reduce((s, x) => s + x.tokens.output_tokens, 0);
    const totalCache = m.reduce((s, x) => s + Math.max(0, x.tokens.cache_tokens), 0);
    reqEl.textContent = nf.format(totalReq);
    waitEl.textContent = nf.format(inFlightRequests.get());
    cachedEl.textContent = nf.format(totalCache);
    inputEl.textContent = nf.format(totalIn);
    outputEl.textContent = nf.format(totalOut);
  }

  function renderCollapseBtn() {
    const isCollapsed = collapsed.get();
    collapseBtn.title = isCollapsed ? "Show histograms" : "Hide histograms";
    collapseBtn.innerHTML = isCollapsed
      ? `<svg class="icon-3-5" viewBox="0 0 16 16" fill="currentColor"><path d="M4.5 6l3.5 4 3.5-4H4.5z"/></svg>`
      : `<svg class="icon-3" viewBox="0 0 16 16" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round"><path d="M3.5 3.5l9 9M12.5 3.5l-9 9"/></svg>`;
  }

  collapseBtn.addEventListener("click", () => collapsed.update((v) => !v));

  const subs = [
    metrics.subscribe(() => {
      renderStats();
      renderHistograms();
    }),
    inFlightRequests.subscribe(renderStats),
    collapsed.subscribe(() => {
      renderCollapseBtn();
      renderHistograms();
    }),
  ];

  renderStats();
  renderHistograms();
  renderCollapseBtn();

  return {
    el: root,
    destroy() {
      cleanupAll(subs);
    },
  };
}
