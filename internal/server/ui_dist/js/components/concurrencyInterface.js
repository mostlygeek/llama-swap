// Load Test tab: fire several streaming chat completions at llama-swap at the
// same time to see how it handles parallel loading and concurrent inference.
// Ported from components/playground/ConcurrencyInterface.svelte.
import { el, cleanupAll, escapeHtml } from "../dom.js";
import { playgroundModels } from "../api.js";
import { persistent } from "../store.js";
import { streamChatCompletion } from "../api/chat.js";
import { playgroundStores } from "../playgroundActivity.js";

const LOAD_MARKER = "━━━━━";
const DEFAULT_PROMPT = "Write a few sentences about the history of computing.";
const DEFAULT_MAX_TOKENS = 256;

function newId() {
  if (typeof crypto !== "undefined" && "randomUUID" in crypto) {
    return crypto.randomUUID();
  }
  return `${Date.now()}-${Math.random().toString(36).slice(2)}`;
}

function emptyRun() {
  return {
    status: "waiting",
    loadingText: "",
    reasoningContent: "",
    content: "",
    loadingDone: false,
    waitingMs: 0,
    loadingMs: 0,
    reasoningMs: 0,
    contentMs: 0,
    phase: "waiting",
    elapsedMs: 0,
  };
}

// Detect and split the llama-swap loading block (wrapped in ━━━━━ markers,
// delivered as reasoning_content) from the model's own reasoning tokens.
function ingestReasoning(prev, chunk) {
  if (prev.loadingDone) {
    return {
      loadingText: prev.loadingText,
      reasoningContent: prev.reasoningContent + chunk,
      loadingDone: true,
      nowPhase: "reasoning",
    };
  }

  const combined = prev.loadingText + chunk;
  // Not enough to decide whether this is a loading marker
  if (combined.length < LOAD_MARKER.length) {
    if (LOAD_MARKER.startsWith(combined)) {
      return { loadingText: combined, reasoningContent: prev.reasoningContent, loadingDone: false, nowPhase: "loading" };
    }
    return {
      loadingText: "",
      reasoningContent: prev.reasoningContent + combined,
      loadingDone: true,
      nowPhase: "reasoning",
    };
  }

  if (!combined.startsWith(LOAD_MARKER)) {
    return {
      loadingText: "",
      reasoningContent: prev.reasoningContent + combined,
      loadingDone: true,
      nowPhase: "reasoning",
    };
  }

  // We're inside a loading block — look for the closing marker
  const closingIdx = combined.indexOf(LOAD_MARKER, LOAD_MARKER.length);
  if (closingIdx < 0) {
    return { loadingText: combined, reasoningContent: prev.reasoningContent, loadingDone: false, nowPhase: "loading" };
  }
  const newlineIdx = combined.indexOf("\n", closingIdx);
  const sliceEnd = newlineIdx >= 0 ? newlineIdx + 1 : combined.length;
  const loadingPart = combined.substring(0, sliceEnd);
  // Strip the trailing " \n" the loader sends after the closing marker
  const remainder = combined.substring(sliceEnd).replace(/^[ \t]*\n?/, "");
  return {
    loadingText: loadingPart,
    reasoningContent: prev.reasoningContent + remainder,
    loadingDone: true,
    nowPhase: remainder ? "reasoning" : "waiting",
  };
}

function niceStepMs(maxMs) {
  if (maxMs <= 500) return 100;
  if (maxMs <= 2000) return 500;
  if (maxMs <= 5000) return 1000;
  if (maxMs <= 20000) return 5000;
  if (maxMs <= 60000) return 10000;
  return 30000;
}

function formatTickMs(ms) {
  if (ms < 1000) return `${ms}`;
  return `${(ms / 1000).toFixed(ms % 1000 === 0 ? 0 : 1)}s`;
}

function formatElapsed(ms) {
  if (ms < 1000) return `${Math.round(ms)}ms`;
  return `${(ms / 1000).toFixed(2)}s`;
}

export function ConcurrencyInterface() {
  const promptStore = persistent("concurrency-prompt", DEFAULT_PROMPT);
  const maxTokensStore = persistent("concurrency-max-tokens", DEFAULT_MAX_TOKENS);
  const testListStore = persistent("concurrency-test-list", []);
  const timelineCollapsedStore = persistent("concurrency-timeline-collapsed", false);

  let runs = {};
  let isRunning = false;
  let abortController = null;
  let renderQueued = false;

  const root = el(`
    <div class="conc">
      <div class="conc-left">
        <div class="conc-run-controls">
          <button class="btn btn--sm conc-go" data-go title="Run concurrent requests"><span aria-hidden="true">▶</span> Go</button>
          <button class="btn btn--sm btn--danger conc-stop" data-stop style="display:none"><span class="conc-stop-square" aria-hidden="true"></span> Stop</button>
          <button class="btn btn--sm" data-clear>Clear (<span data-count>0</span>)</button>
        </div>
        <div class="conc-models">
          <div class="conc-models-label muted">Models <span class="conc-models-hint">— click to queue (add the same model more than once to test parallel requests)</span></div>
          <div class="conc-models-list" data-models></div>
        </div>
        <div class="conc-settings">
          <div class="conc-settings-row">
            <label for="concurrency-prompt" class="muted">Prompt</label>
            <button class="btn btn--sm conc-reset" data-reset>reset defaults</button>
          </div>
          <textarea id="concurrency-prompt" class="conc-prompt" rows="3" data-prompt></textarea>
          <label for="concurrency-max-tokens" class="muted">max_tokens</label>
          <input id="concurrency-max-tokens" class="conc-max-tokens" type="number" min="1" data-max-tokens />
        </div>
      </div>
      <div class="conc-right" data-right></div>
    </div>
  `);

  const goBtn = root.querySelector("[data-go]");
  const stopBtn = root.querySelector("[data-stop]");
  const clearBtn = root.querySelector("[data-clear]");
  const countEl = root.querySelector("[data-count]");
  const modelsEl = root.querySelector("[data-models]");
  const rightEl = root.querySelector("[data-right]");
  const promptEl = root.querySelector("[data-prompt]");
  const maxTokensEl = root.querySelector("[data-max-tokens]");
  const resetBtn = root.querySelector("[data-reset]");

  promptEl.value = promptStore.get();
  maxTokensEl.value = String(maxTokensStore.get());

  promptEl.addEventListener("change", () => promptStore.set(promptEl.value));
  maxTokensEl.addEventListener("change", () => maxTokensStore.set(Number(maxTokensEl.value) || DEFAULT_MAX_TOKENS));
  resetBtn.addEventListener("click", () => {
    promptStore.set(DEFAULT_PROMPT);
    maxTokensStore.set(DEFAULT_MAX_TOKENS);
    promptEl.value = DEFAULT_PROMPT;
    maxTokensEl.value = String(DEFAULT_MAX_TOKENS);
  });

  function queueRender() {
    if (renderQueued) return;
    renderQueued = true;
    requestAnimationFrame(() => {
      renderQueued = false;
      renderRight();
    });
  }

  function renderModels() {
    const playground = playgroundModels.get();
    const available = playground.filter((m) => m.playgroundType !== "selector" && m.playgroundType !== "peer");
    const peers = playground.filter((m) => m.playgroundType === "peer");
    const selectors = playground.filter((m) => m.playgroundType === "selector");
    const sections = [
      { label: "Selectors", ids: selectors.map((m) => m.id) },
      { label: "Models", ids: available.map((m) => m.id) },
      { label: "Peers", ids: peers.map((m) => m.id) },
    ].filter((section) => section.ids.length > 0);

    if (sections.length === 0) {
      modelsEl.innerHTML = `<div class="muted conc-models-empty">No models configured.</div>`;
      return;
    }
    modelsEl.innerHTML = sections
      .map(
        (section) => `
        <div class="conc-models-section">
          <div class="conc-models-section-label">${escapeHtml(section.label)}</div>
          ${section.ids
            .map(
              (id) => `
            <button type="button" class="conc-model-item" data-add="${escapeHtml(id)}" ${isRunning ? "disabled" : ""} title="Add ${escapeHtml(id)}">
              <span class="conc-add-plus" aria-hidden="true">+</span>
              <span class="conc-model-name">${escapeHtml(id)}</span>
            </button>`
            )
            .join("")}
        </div>`
      )
      .join("");
  }

  modelsEl.addEventListener("click", (e) => {
    const btn = e.target.closest("[data-add]");
    if (!btn || isRunning) return;
    testListStore.update((list) => [...list, { id: newId(), model: btn.getAttribute("data-add") }]);
  });

  clearBtn.addEventListener("click", () => {
    if (isRunning) return;
    testListStore.set([]);
    runs = {};
    renderRight();
  });

  function barClass(run, phase, doneClass) {
    if (run.status === "error" && run.phase === phase) return "conc-bar--error";
    return doneClass;
  }

  function renderTimeline(list) {
    const maxMs = Math.max(100, ...Object.values(runs).map((r) => r.elapsedMs));
    const step = niceStepMs(maxMs);
    const ticks = [];
    for (let t = 0; t <= maxMs; t += step) ticks.push(t);

    const ticksHtml = ticks
      .map(
        (t) =>
          `<div class="conc-tick" style="left:${(t / maxMs) * 100}%"><span class="conc-tick-label">${formatTickMs(t)}</span></div>`
      )
      .join("");

    const barsHtml = list
      .map((entry, i) => {
        const run = runs[entry.id];
        const pct = (v) => (run ? (v / maxMs) * 100 : 0);
        const waitingPct = pct(run?.waitingMs ?? 0);
        const loadingPct = pct(run?.loadingMs ?? 0);
        const reasoningPct = pct(run?.reasoningMs ?? 0);
        const contentPct = pct(run?.contentMs ?? 0);
        return `
          <div class="conc-bar-row">
            <div class="conc-bar-label" title="${escapeHtml(entry.model)}"><span class="conc-bar-index">${i + 1}.</span>${escapeHtml(entry.model)}</div>
            <div class="conc-bar-track">
              ${ticks.map((t) => `<div class="conc-tick-bg" style="left:${(t / maxMs) * 100}%"></div>`).join("")}
              ${run?.waitingMs > 0 ? `<div class="conc-bar ${barClass(run, "waiting", "conc-bar--waiting")}" style="left:0;width:${waitingPct}%" title="waiting ${formatElapsed(run.waitingMs)}"></div>` : ""}
              ${run?.loadingMs > 0 ? `<div class="conc-bar ${barClass(run, "loading", "conc-bar--loading")}" style="left:${waitingPct}%;width:${loadingPct}%" title="loading ${formatElapsed(run.loadingMs)}"></div>` : ""}
              ${run?.reasoningMs > 0 ? `<div class="conc-bar ${barClass(run, "reasoning", "conc-bar--reasoning")}" style="left:${waitingPct + loadingPct}%;width:${reasoningPct}%" title="reasoning ${formatElapsed(run.reasoningMs)}"></div>` : ""}
              ${run?.contentMs > 0 ? `<div class="conc-bar ${barClass(run, "content", run.status === "done" ? "conc-bar--done" : "conc-bar--content")}" style="left:${waitingPct + loadingPct + reasoningPct}%;width:${contentPct}%" title="content ${formatElapsed(run.contentMs)}"></div>` : ""}
            </div>
            <div class="conc-bar-elapsed">${run ? formatElapsed(run.elapsedMs) : "—"}</div>
          </div>`;
      })
      .join("");

    const collapsed = timelineCollapsedStore.get();
    return `
      <div class="card conc-timeline">
        <button class="conc-timeline-toggle" data-timeline-toggle aria-expanded="${!collapsed}">
          <svg class="icon-3-5 ${collapsed ? "conc-chev-collapsed" : ""}" viewBox="0 0 16 16" fill="none" stroke="currentColor"><path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M4 6l4 4 4-4"></path></svg>
          <span>Timeline</span>
          ${
            !collapsed
              ? `<span class="conc-legend">
                  <span class="conc-legend-item"><span class="conc-swatch conc-bar--waiting"></span>waiting</span>
                  <span class="conc-legend-item"><span class="conc-swatch conc-bar--loading"></span>loading</span>
                  <span class="conc-legend-item"><span class="conc-swatch conc-bar--reasoning"></span>reasoning</span>
                  <span class="conc-legend-item"><span class="conc-swatch conc-bar--content"></span>streaming</span>
                  <span class="conc-legend-item"><span class="conc-swatch conc-bar--done"></span>done</span>
                  <span class="conc-legend-item"><span class="conc-swatch conc-bar--error"></span>error</span>
                </span>`
              : ""
          }
          <span class="conc-timeline-max muted">max ${formatElapsed(maxMs)} · ${list.length} request${list.length === 1 ? "" : "s"}</span>
        </button>
        ${
          !collapsed
            ? `<div class="conc-timeline-body">
                <div class="conc-ticks-row"><div class="conc-bar-label"></div><div class="conc-ticks-track">${ticksHtml}</div><div class="conc-bar-elapsed"></div></div>
                <div class="conc-bars">${barsHtml}</div>
              </div>`
            : ""
        }
      </div>
    `;
  }

  function renderRight() {
    const list = testListStore.get();
    countEl.textContent = String(list.length);
    goBtn.disabled = isRunning || list.length === 0 || promptStore.get().trim() === "";
    goBtn.style.display = isRunning ? "none" : "";
    stopBtn.style.display = isRunning ? "" : "none";
    clearBtn.disabled = isRunning || list.length === 0;
    renderModels();

    if (list.length === 0) {
      rightEl.innerHTML = `
        <div class="conc-empty">
          <div class="conc-empty-inner">
            <h4 class="conc-empty-title">Load Test</h4>
            <p>Fire several streaming chat completions at llama-swap at the same time to see how it handles parallel
            loading and concurrent inference. Each request streams into its own panel with a live timer and status.</p>
            <ol class="conc-empty-steps">
              <li>Click models on the left to queue them — repeat a model to hit it with parallel requests.</li>
              <li>Tweak the prompt and <code>max_tokens</code> if you want.</li>
              <li>Press <strong>Go</strong> to launch them concurrently.</li>
            </ol>
            <p class="muted conc-empty-tip">Tip: drag a result card's header to reorder, or hit × to drop it.</p>
          </div>
        </div>`;
      return;
    }

    rightEl.innerHTML = `
      ${renderTimeline(list)}
      <div class="conc-results" role="list">
        ${list
          .map((entry, i) => {
            const run = runs[entry.id];
            const status = run?.status ?? "waiting";
            return `
            <div class="card conc-result" role="listitem" data-entry="${escapeHtml(entry.id)}" data-index="${i}">
              <div class="conc-result-head" draggable="${!isRunning}" data-drag-handle title="${isRunning ? "" : "Drag to reorder"}">
                <span class="conc-drag-grip muted" aria-hidden="true">⋮⋮</span>
                <span class="conc-result-index muted">${i + 1}.</span>
                <span class="conc-result-model" title="${escapeHtml(entry.model)}">${escapeHtml(entry.model)}</span>
                <span class="conc-result-elapsed muted">${run ? formatElapsed(run.elapsedMs) : "—"}</span>
                <span class="conc-status conc-status--${status}">${status}</span>
                <button class="conc-result-remove" data-remove="${escapeHtml(entry.id)}" ${isRunning ? "disabled" : ""} aria-label="Remove">✕</button>
              </div>
              <div class="conc-result-body">
                ${run?.loadingText ? `<div class="conc-loading-text muted">${escapeHtml(run.loadingText.trim())}</div>` : ""}
                ${run?.reasoningContent ? `<div class="conc-reasoning">${escapeHtml(run.reasoningContent)}</div>` : ""}
                ${run?.content ? `<div class="conc-content ${run.reasoningContent ? "conc-content--spaced" : ""}">${escapeHtml(run.content)}</div>` : ""}
                ${run?.status === "error" && run?.error ? `<div class="conc-error">[error] ${escapeHtml(run.error)}</div>` : ""}
              </div>
            </div>`;
          })
          .join("")}
      </div>`;
  }

  rightEl.addEventListener("click", (e) => {
    const remove = e.target.closest("[data-remove]");
    if (remove && !isRunning) {
      const id = remove.getAttribute("data-remove");
      testListStore.update((list) => list.filter((x) => x.id !== id));
      const next = { ...runs };
      delete next[id];
      runs = next;
      return;
    }
    const timeline = e.target.closest("[data-timeline-toggle]");
    if (timeline) {
      timelineCollapsedStore.update((v) => !v);
    }
  });

  // Drag to reorder result cards.
  let dragIndex = null;
  rightEl.addEventListener("dragstart", (e) => {
    const card = e.target.closest("[data-entry]");
    if (!card || isRunning) return;
    dragIndex = Number(card.getAttribute("data-index"));
    e.dataTransfer.effectAllowed = "move";
    e.dataTransfer.setData("text/plain", String(dragIndex));
    card.classList.add("conc-dragging");
  });
  rightEl.addEventListener("dragend", (e) => {
    const card = e.target.closest("[data-entry]");
    if (card) card.classList.remove("conc-dragging");
    rightEl.querySelectorAll(".conc-result").forEach((c) => c.classList.remove("conc-drop-target"));
    dragIndex = null;
  });
  rightEl.addEventListener("dragover", (e) => {
    if (isRunning || dragIndex === null) return;
    const card = e.target.closest("[data-entry]");
    if (!card) return;
    e.preventDefault();
    e.dataTransfer.dropEffect = "move";
    rightEl.querySelectorAll(".conc-result").forEach((c) => c.classList.remove("conc-drop-target"));
    card.classList.add("conc-drop-target");
  });
  rightEl.addEventListener("drop", (e) => {
    if (isRunning || dragIndex === null) return;
    const card = e.target.closest("[data-entry]");
    if (!card) return;
    e.preventDefault();
    const to = Number(card.getAttribute("data-index"));
    const from = dragIndex;
    dragIndex = null;
    if (from === to) return;
    testListStore.update((list) => {
      const next = [...list];
      const [moved] = next.splice(from, 1);
      next.splice(to, 0, moved);
      return next;
    });
  });

  async function runOne(entry, signal) {
    const start = performance.now();
    let phaseStart = start;
    runs[entry.id] = { ...emptyRun(), status: "streaming" };

    const accrue = (prev, now) => {
      const delta = now - phaseStart;
      const base = {
        waitingMs: prev.waitingMs,
        loadingMs: prev.loadingMs,
        reasoningMs: prev.reasoningMs,
        contentMs: prev.contentMs,
      };
      if (prev.phase === "waiting") return { ...base, waitingMs: base.waitingMs + delta };
      if (prev.phase === "loading") return { ...base, loadingMs: base.loadingMs + delta };
      if (prev.phase === "reasoning") return { ...base, reasoningMs: base.reasoningMs + delta };
      if (prev.phase === "content") return { ...base, contentMs: base.contentMs + delta };
      return base;
    };

    const ticker = window.setInterval(() => {
      const prev = runs[entry.id];
      if (!prev || prev.status !== "streaming") return;
      const now = performance.now();
      const accrued = accrue(prev, now);
      phaseStart = now;
      runs[entry.id] = { ...prev, ...accrued, elapsedMs: now - start };
      queueRender();
    }, 50);

    try {
      const stream = streamChatCompletion(entry.model, [{ role: "user", content: promptStore.get() }], signal, {
        endpoint: "v1/chat/completions",
        max_tokens: maxTokensStore.get(),
      });
      for await (const chunk of stream) {
        if (chunk.done) break;
        const prev = runs[entry.id];
        if (!prev) break;
        const now = performance.now();
        const accrued = accrue(prev, now);
        phaseStart = now;

        let nextPhase = prev.phase;
        let loadingText = prev.loadingText;
        let reasoningContent = prev.reasoningContent;
        let loadingDone = prev.loadingDone;

        if (chunk.reasoning_content) {
          const parsed = ingestReasoning(prev, chunk.reasoning_content);
          loadingText = parsed.loadingText;
          reasoningContent = parsed.reasoningContent;
          loadingDone = parsed.loadingDone;
          nextPhase = parsed.nowPhase;
        }
        if (chunk.content) nextPhase = "content";

        runs[entry.id] = {
          ...prev,
          ...accrued,
          loadingText,
          reasoningContent,
          content: prev.content + (chunk.content ?? ""),
          loadingDone,
          phase: nextPhase,
          elapsedMs: now - start,
        };
        queueRender();
      }
      const prev = runs[entry.id];
      if (prev) {
        const now = performance.now();
        const accrued = accrue(prev, now);
        runs[entry.id] = { ...prev, ...accrued, status: "done", elapsedMs: now - start };
      }
    } catch (err) {
      const prev = runs[entry.id] ?? emptyRun();
      const now = performance.now();
      const accrued = accrue(prev, now);
      const aborted = err instanceof Error && err.name === "AbortError";
      runs[entry.id] = {
        ...prev,
        ...accrued,
        status: "error",
        elapsedMs: now - start,
        error: aborted ? "aborted" : err instanceof Error ? err.message : String(err),
      };
    } finally {
      window.clearInterval(ticker);
      queueRender();
    }
  }

  async function run() {
    if (isRunning) return;
    const entries = testListStore.get();
    if (entries.length === 0 || promptStore.get().trim() === "") return;
    const initial = {};
    for (const e of entries) initial[e.id] = emptyRun();
    runs = initial;
    isRunning = true;
    playgroundStores.concurrencyRunning.set(true);
    abortController = new AbortController();
    renderRight();
    try {
      await Promise.allSettled(entries.map((e) => runOne(e, abortController.signal)));
    } finally {
      isRunning = false;
      abortController = null;
      playgroundStores.concurrencyRunning.set(false);
      renderRight();
    }
  }

  goBtn.addEventListener("click", run);
  stopBtn.addEventListener("click", () => abortController?.abort());

  const subs = [
    playgroundModels.subscribe(renderModels),
    testListStore.subscribe(renderRight),
    timelineCollapsedStore.subscribe(renderRight),
  ];

  renderRight();

  return {
    el: root,
    destroy() {
      abortController?.abort();
      cleanupAll(subs);
    },
  };
}
