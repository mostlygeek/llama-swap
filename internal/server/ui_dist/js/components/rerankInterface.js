// Rerank interface: table editor + JSON editor with sort + per-row scores.
// Ported from components/playground/RerankInterface.svelte.
import { el, cleanupAll, escapeHtml } from "../dom.js";
import { models } from "../api.js";
import { playgroundStores } from "../playgroundActivity.js";
import { persistent } from "../store.js";
import { ModelSelector } from "./modelSelector.js";
import { rerank } from "../api/rerank.js";

const DEFAULT_QUERY = "How do LLM's work?";
const DEFAULT_DOCS = [
  "Large language models (LLMs) use transformer architectures to predict the next token in a sequence based on massive amounts of text data.",
  "LLMs are trained on diverse internet text, learning statistical patterns of language that allow them to generate coherent responses.",
  "During training, LLMs minimize a loss function that measures the difference between predicted and actual tokens across billions of examples.",
  "Attention mechanisms in transformers enable LLMs to weigh the importance of different words when generating output.",
  "Fine‑tuning allows a pre‑trained LLM to adapt to a specific downstream task with a smaller dataset.",
  "Neural networks consist of layers of interconnected neurons that adjust their weights during back‑propagation.",
  "The history of the Roman Empire spanned over a thousand years.",
  "Soccer is the most popular sport in many countries around the world.",
  "Quantum computing uses qubits to perform calculations that are intractable for classical computers.",
];

export function RerankInterface() {
  const selectedModel = persistent("playground-rerank-model", "");

  let query = DEFAULT_QUERY;
  let rows = [...DEFAULT_DOCS.map((d) => ({ doc: d, score: null })), { doc: "", score: null }];
  let isLoading = false;
  let error = null;
  let usage = null;
  let abortController = null;
  let sortOrder = "desc";
  let editorMode = "table";
  let jsonText = "";
  let jsonError = null;

  const root = el(`
    <div class="pg-rerank">
      <div class="pg-rerank-toolbar" data-toolbar></div>
      <div class="pg-rerank-content" data-content></div>
      <div class="pg-rerank-bottom" data-bottom></div>
    </div>
  `);

  const toolbar = root.querySelector("[data-toolbar]");
  const content = root.querySelector("[data-content]");
  const bottom = root.querySelector("[data-bottom]");

  const modelSel = ModelSelector({ value: selectedModel, placeholder: "Select a rerank model..." });
  toolbar.appendChild(modelSel.el);

  const queryInput = el(`<input type="text" class="pg-input pg-rerank-query" placeholder="Query..." data-query>`);
  queryInput.value = query;
  toolbar.appendChild(queryInput);
  queryInput.addEventListener("input", (e) => { query = e.target.value; updateSubmitState(); });
  queryInput.addEventListener("keydown", (e) => { if (e.key === "Enter" && !e.shiftKey) { e.preventDefault(); submit(); } });

  const modeToggle = el(`
    <div class="pg-rerank-mode">
      <button class="pg-rerank-mode-btn ${editorMode === "table" ? "pg-rerank-mode-active" : ""}" data-mode="table">Table</button>
      <button class="pg-rerank-mode-btn ${editorMode === "json" ? "pg-rerank-mode-active" : ""}" data-mode="json">JSON</button>
    </div>
  `);
  toolbar.appendChild(modeToggle);

  modeToggle.addEventListener("click", (e) => {
    const b = e.target.closest("[data-mode]");
    if (!b) return;
    const m = b.getAttribute("data-mode");
    if (m === "json") switchToJson();
    else switchToTable();
  });

  function switchToJson() {
    if (editorMode === "json") return;
    const docs = rows.filter((r) => r.doc.trim() !== "").map((r) => r.doc);
    jsonText = JSON.stringify({ query, documents: docs }, null, 2);
    jsonError = null;
    editorMode = "json";
    updateModeToggle();
    renderContent();
  }

  function switchToTable() {
    if (editorMode === "table") return;
    if (jsonText.trim() === "") {
      query = "";
      rows = [{ doc: "", score: null }];
      jsonError = null;
      editorMode = "table";
      updateModeToggle();
      queryInput.value = query;
      renderContent();
      return;
    }
    try {
      const parsed = JSON.parse(jsonText);
      if (typeof parsed !== "object" || parsed === null || Array.isArray(parsed)) throw new Error("Expected a JSON object");
      if (typeof parsed.query !== "string") throw new Error('"query" must be a string');
      if (!Array.isArray(parsed.documents)) throw new Error('"documents" must be an array');
      query = parsed.query;
      queryInput.value = query;
      const newRows = parsed.documents.map((d) => ({ doc: typeof d === "string" ? d : String(d), score: null }));
      if (newRows.length === 0 || newRows[newRows.length - 1].doc.trim() !== "") newRows.push({ doc: "", score: null });
      rows = newRows;
      jsonError = null;
      editorMode = "table";
      updateModeToggle();
      renderContent();
    } catch (err) {
      jsonError = err.message || "Invalid JSON";
      updateModeToggle();
      renderContent();
    }
  }

  function updateModeToggle() {
    modeToggle.querySelectorAll("[data-mode]").forEach((b) => {
      const m = b.getAttribute("data-mode");
      b.classList.toggle("pg-rerank-mode-active", m === editorMode);
    });
    queryInput.style.display = editorMode === "table" ? "" : "none";
  }

  function getDisplayRows() {
    const indexed = rows.map((row, i) => ({ row, i }));
    if (sortOrder === "none") return indexed;
    return [...indexed].sort((a, b) => {
      if (a.row.score === null && b.row.score === null) return 0;
      if (a.row.score === null) return 1;
      if (b.row.score === null) return -1;
      return sortOrder === "desc" ? b.row.score - a.row.score : a.row.score - b.row.score;
    });
  }

  function renderContent() {
    if (editorMode === "json") {
      content.innerHTML = `
        <div class="pg-rerank-json">
          <textarea class="pg-rerank-json-input" data-json spellcheck="false" placeholder='{\\n  "query": "your search query",\\n  "documents": [\\n    "document one",\\n    "document two"\\n  ]\\n}'>${escapeHtml(jsonText)}</textarea>
          ${jsonError ? `<p class="pg-rerank-json-err">${escapeHtml(jsonError)}</p>` : ""}
        </div>`;
      const ta = content.querySelector("[data-json]");
      ta.addEventListener("input", (e) => { jsonText = e.target.value; updateSubmitState(); });
      return;
    }

    // Table editor
    const display = getDisplayRows();
    const sortInd = sortOrder === "desc" ? " ↓" : sortOrder === "asc" ? " ↑" : "";
    content.innerHTML = `
      <div class="pg-rerank-table-wrap">
        <table class="pg-rerank-table">
          <colgroup><col><col class="pg-rerank-col-score"><col class="pg-rerank-col-rm"></colgroup>
          <thead>
            <tr>
              <th>Document</th>
              <th class="pg-rerank-score-th" data-sort>Score${sortInd}</th>
              <th></th>
            </tr>
          </thead>
          <tbody>
            ${display
              .map(
                ({ row, i }) => `
                  <tr>
                    <td><input type="text" class="pg-rerank-doc" data-doc-i="${i}" value="${escapeHtml(row.doc)}" placeholder="${i === rows.length - 1 ? "Add document..." : "Document text..."}"></td>
                    <td class="pg-rerank-score ${scoreColorClass(row.score)}">
                      ${isLoading && row.score === null && row.doc.trim() !== ""
                        ? `<span class="spinner spinner-sm"></span>`
                        : escapeHtml(formatScore(row.score))}
                    </td>
                    <td class="pg-rerank-rm-cell">
                      <button class="pg-rerank-rm" data-rm-i="${i}" ${rows.length <= 1 ? "disabled" : ""} tabindex="-1" aria-label="Remove row">×</button>
                    </td>
                  </tr>`
              )
              .join("")}
          </tbody>
        </table>
      </div>
    `;
  }

  content.addEventListener("click", (e) => {
    const sortBtn = e.target.closest("[data-sort]");
    if (sortBtn) {
      sortOrder = sortOrder === "none" ? "desc" : sortOrder === "desc" ? "asc" : "none";
      renderContent();
      return;
    }
    const rm = e.target.closest("[data-rm-i]");
    if (rm) {
      const i = Number(rm.getAttribute("data-rm-i"));
      if (rows.length > 1) {
        rows = rows.filter((_, idx) => idx !== i);
        renderContent();
        updateSubmitState();
      }
    }
  });
  content.addEventListener("input", (e) => {
    const doc = e.target.closest("[data-doc-i]");
    if (doc) {
      const i = Number(doc.getAttribute("data-doc-i"));
      rows[i].doc = doc.value;
      // auto-add empty trailing row when last row gets content
      if (i === rows.length - 1 && rows[i].doc.trim() !== "") {
        rows.push({ doc: "", score: null });
        renderContent();
      }
      updateSubmitState();
    }
  });

  function scoreColorClass(s) {
    if (s === null) return "pg-score-muted";
    if (s > 0) return "pg-score-pos";
    return "pg-score-neg";
  }
  function formatScore(s) { return s === null ? "—" : s.toFixed(3); }

  function canSubmit() {
    if (!selectedModel.get() || isLoading) return false;
    if (editorMode === "json") {
      try {
        const p = JSON.parse(jsonText);
        return typeof p.query === "string" && p.query.trim() !== "" &&
               Array.isArray(p.documents) && p.documents.some((d) => typeof d === "string" && d.trim() !== "");
      } catch { return false; }
    }
    return query.trim() !== "" && rows.some((r) => r.doc.trim() !== "");
  }

  function updateSubmitState() {
    const b = bottom.querySelector("[data-submit]");
    if (b) b.disabled = !canSubmit();
    const c = bottom.querySelector("[data-clear]");
    if (c) {
      const isCleared =
        query === DEFAULT_QUERY &&
        rows.every((r, i) => r.score === null && r.doc === (DEFAULT_DOCS[i] ?? "")) &&
        rows.length === DEFAULT_DOCS.length + 1 &&
        !jsonText.trim() && !error && !usage;
      c.disabled = isCleared;
    }
  }

  function renderBottom() {
    if (isLoading) {
      bottom.innerHTML = `
        <button class="btn pg-btn-cancel" data-cancel>Cancel</button>
        ${error ? `<span class="pg-rerank-msg pg-error">${escapeHtml(error)}</span>` : ""}
      `;
      return;
    }
    bottom.innerHTML = `
      <button class="btn btn--primary" data-submit ${canSubmit() ? "" : "disabled"}>Rerank</button>
      <button class="btn" data-clear disabled>Clear</button>
      ${error ? `<span class="pg-rerank-msg pg-error">${escapeHtml(error)}</span>` : ""}
      ${usage ? `<span class="pg-rerank-msg muted">${usage.total_tokens} tokens</span>` : ""}
    `;
    updateSubmitState();
  }

  bottom.addEventListener("click", (e) => {
    if (e.target.closest("[data-submit]")) submit();
    else if (e.target.closest("[data-cancel]")) abortController?.abort();
    else if (e.target.closest("[data-clear]")) doClear();
  });

  async function submit() {
    if (!canSubmit()) return;
    let submitQuery;
    let nonEmptyEntries;
    if (editorMode === "json") {
      try {
        const parsed = JSON.parse(jsonText);
        submitQuery = parsed.query;
        const docs = parsed.documents.filter((d) => d.trim() !== "");
        const newRows = docs.map((d) => ({ doc: d, score: null }));
        newRows.push({ doc: "", score: null });
        rows = newRows;
        query = submitQuery;
        queryInput.value = query;
        editorMode = "table";
        updateModeToggle();
      } catch {
        error = "Invalid JSON — fix before submitting";
        renderBottom();
        return;
      }
      nonEmptyEntries = rows.map((r, i) => ({ originalIndex: i, doc: r.doc })).filter((e) => e.doc.trim() !== "");
    } else {
      submitQuery = query;
      nonEmptyEntries = rows.map((r, i) => ({ originalIndex: i, doc: r.doc })).filter((e) => e.doc.trim() !== "");
    }

    isLoading = true;
    error = null;
    usage = null;
    rows = rows.map((r) => ({ ...r, score: null }));
    abortController = new AbortController();
    playgroundStores.rerankLoading.set(true);

    renderContent();
    renderBottom();

    try {
      const resp = await rerank(selectedModel.get(), submitQuery, nonEmptyEntries.map((e) => e.doc), abortController.signal);
      usage = resp.usage;
      const updated = rows.map((r) => ({ ...r }));
      for (const result of resp.results) {
        const entry = nonEmptyEntries[result.index];
        if (entry !== undefined) updated[entry.originalIndex].score = result.relevance_score;
      }
      rows = updated;
    } catch (err) {
      if (err.name !== "AbortError") error = err.message || "An error occurred";
    } finally {
      isLoading = false;
      abortController = null;
      playgroundStores.rerankLoading.set(false);
      renderContent();
      renderBottom();
    }
  }

  function doClear() {
    query = DEFAULT_QUERY;
    rows = [...DEFAULT_DOCS.map((d) => ({ doc: d, score: null })), { doc: "", score: null }];
    error = null;
    usage = null;
    sortOrder = "desc";
    jsonText = "";
    jsonError = null;
    queryInput.value = query;
    renderContent();
    renderBottom();
  }

  const subs = [
    models.subscribe(() => {
      const has = models.get().some((m) => !m.unlisted);
      root.classList.toggle("pg-no-models", !has);
    }),
    selectedModel.subscribe(updateSubmitState),
  ];

  renderContent();
  renderBottom();
  updateModeToggle();

  return {
    el: root,
    destroy() {
      cleanupAll([modelSel.destroy, ...subs]);
    },
  };
}
