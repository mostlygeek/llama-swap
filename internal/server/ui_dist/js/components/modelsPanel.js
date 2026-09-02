// Models panel: list + load/unload, with persistent show-id/name + show-unlisted
// toggles. Ported from components/ModelsPanel.svelte.
import { el, cleanupAll, escapeHtml } from "../dom.js";
import { models, loadModel, unloadAllModels, unloadSingleModel } from "../api.js";
import { isNarrow } from "../theme.js";
import { persistent } from "../store.js";

export function ModelsPanel() {
  const showUnlisted = persistent("showUnlisted", true);
  const showIdOrName = persistent("showIdorName", "id");
  let isUnloading = false;
  let menuOpen = false;

  const root = el(`
    <div class="card models-panel">
      <div class="models-head">
        <div class="models-head-row">
          <h2 class="models-title">Models</h2>
          <div class="models-actions" data-actions></div>
        </div>
      </div>
      <div class="models-body" data-list></div>
    </div>
  `);

  const actionsEl = root.querySelector("[data-actions]");
  const listEl = root.querySelector("[data-list]");

  function getDisplay(m) {
    return showIdOrName.get() === "id" ? m.id : m.name || m.id;
  }

  function filtered() {
    const all = models.get();
    const filteredAll = all.filter((m) => showUnlisted.get() || !m.unlisted);
    const peers = filteredAll.filter((m) => m.peerID);
    const grouped = peers.reduce((acc, m) => {
      const k = m.peerID || "unknown";
      (acc[k] = acc[k] || []).push(m);
      return acc;
    }, {});
    // Active (loaded / loading) models float to the top so they're quick to find
    // and unload. Array.sort is stable, so the existing name ordering is preserved
    // within the active and inactive groups.
    const regularModels = filteredAll
      .filter((m) => !m.peerID)
      .sort((a, b) => (a.state === "stopped" ? 1 : 0) - (b.state === "stopped" ? 1 : 0));
    return {
      regularModels,
      peerModelsByPeerId: grouped,
    };
  }

  function rowHtml(m) {
    const display = escapeHtml(getDisplay(m));
    const desc = m.description ? `<p class="model-desc"><em>${escapeHtml(m.description)}</em></p>` : "";
    const aliases =
      m.aliases && m.aliases.length
        ? `<p class="model-aliases">Aliases: ${escapeHtml(m.aliases.join(", "))}</p>`
        : "";
    const unlistedCls = m.unlisted ? " model-unlisted" : "";
    const rowCls =
      m.state === "ready" ? " models-row-loaded" : m.state !== "stopped" ? " models-row-transition" : "";
    const action =
      m.state === "stopped"
        ? `<button class="btn btn--sm" data-load="${escapeHtml(m.id)}">Load</button>`
        : `<button class="btn btn--sm" data-unload="${escapeHtml(m.id)}" ${m.state !== "ready" ? "disabled" : ""}>Unload</button>`;
    return `
      <tr class="models-row${rowCls}">
        <td class="models-name${unlistedCls}">
          <a class="models-link" href="/upstream/${encodeURIComponent(m.id)}/" target="_blank" rel="noopener">${display}</a>
          ${desc}${aliases}
        </td>
        <td class="models-action">${action}</td>
        <td class="models-state"><span class="status status--${escapeHtml(m.state)}">${escapeHtml(m.state)}</span></td>
      </tr>
    `;
  }

  function renderList() {
    const f = filtered();
    const regularHtml = f.regularModels.map(rowHtml).join("");
    const peerEntries = Object.entries(f.peerModelsByPeerId).sort(([a], [b]) => a.localeCompare(b));
    const peerHtml = peerEntries.length
      ? `<h3 class="peer-heading">Peer Models</h3>` +
        peerEntries
          .map(
            ([peerId, peerModels]) => `
              <div class="peer-group">
                <table class="models-table">
                  <thead><tr><th>${escapeHtml(peerId)}</th></tr></thead>
                  <tbody>
                    ${peerModels
                      .map(
                        (m) => `<tr><td class="peer-model ${m.unlisted ? "model-unlisted" : ""}">${escapeHtml(m.id)}</td></tr>`
                      )
                      .join("")}
                  </tbody>
                </table>
              </div>`
          )
          .join("")
      : "";

    if (!regularHtml && !peerHtml) {
      listEl.innerHTML = `<div class="models-empty muted">No models configured.</div>`;
      return;
    }
    listEl.innerHTML = `
      <table class="models-table">
        <thead class="models-thead">
          <tr>
            <th>${showIdOrName.get() === "id" ? "Model ID" : "Name"}</th>
            <th></th>
            <th>State</th>
          </tr>
        </thead>
        <tbody>${regularHtml}</tbody>
      </table>
      ${peerHtml}
    `;
  }

  async function handleUnloadAll() {
    if (isUnloading) return;
    isUnloading = true;
    renderActions();
    try {
      await unloadAllModels();
    } catch (e) {
      console.error(e);
    } finally {
      setTimeout(() => {
        isUnloading = false;
        renderActions();
      }, 1000);
    }
  }

  function renderActions() {
    const narrow = isNarrow.get();
    const mode = showIdOrName.get();
    const seeUnlisted = showUnlisted.get();
    const btnLabel = isUnloading ? "Unloading..." : "Unload All";
    if (narrow) {
      actionsEl.innerHTML = `
        <div class="menu-wrap">
          <button class="btn menu-btn" data-menu aria-label="Toggle menu">
            <svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24" fill="currentColor" class="icon-5"><path fill-rule="evenodd" d="M3 6.75A.75.75 0 0 1 3.75 6h16.5a.75.75 0 0 1 0 1.5H3.75A.75.75 0 0 1 3 6.75ZM3 12a.75.75 0 0 1 .75-.75h16.5a.75.75 0 0 1 0 1.5H3.75A.75.75 0 0 1 3 12Zm0 5.25a.75.75 0 0 1 .75-.75h16.5a.75.75 0 0 1 0 1.5H3.75a.75.75 0 0 1-.75-.75Z" clip-rule="evenodd"/></svg>
          </button>
          ${menuOpen ? `
            <div class="menu-pop">
              <button class="menu-item" data-toggle-display>${mode === "id" ? "Show Name" : "Show ID"}</button>
              <button class="menu-item" data-toggle-unlisted>${seeUnlisted ? "Hide Unlisted" : "Show Unlisted"}</button>
              <button class="menu-item" data-unload-all ${isUnloading ? "disabled" : ""}>${btnLabel}</button>
            </div>` : ""}
        </div>
      `;
    } else {
      actionsEl.innerHTML = `
        <div class="models-actions-row">
          <button class="btn" data-toggle-display>${mode === "id" ? "ID" : "Name"}</button>
          <button class="btn" data-toggle-unlisted>unlisted</button>
        </div>
        <button class="btn" data-unload-all ${isUnloading ? "disabled" : ""}>${btnLabel}</button>
      `;
    }
  }

  // Wire delegated handlers on actions
  actionsEl.addEventListener("click", (e) => {
    const t = e.target.closest("[data-toggle-display],[data-toggle-unlisted],[data-unload-all],[data-menu]");
    if (!t) return;
    if (t.hasAttribute("data-menu")) {
      menuOpen = !menuOpen;
      renderActions();
    } else if (t.hasAttribute("data-toggle-display")) {
      showIdOrName.update((v) => (v === "name" ? "id" : "name"));
    } else if (t.hasAttribute("data-toggle-unlisted")) {
      showUnlisted.update((v) => !v);
    } else if (t.hasAttribute("data-unload-all")) {
      handleUnloadAll();
    }
  });

  // Outside-click closes narrow menu
  document.addEventListener("click", (e) => {
    if (!menuOpen) return;
    if (!actionsEl.contains(e.target)) {
      menuOpen = false;
      renderActions();
    }
  });

  // Delegated handlers on list (load/unload per row)
  listEl.addEventListener("click", (e) => {
    const loadBtn = e.target.closest("[data-load]");
    const unloadBtn = e.target.closest("[data-unload]");
    if (loadBtn) {
      const id = loadBtn.getAttribute("data-load");
      loadModel(id).catch((err) => console.error(err));
    } else if (unloadBtn) {
      const id = unloadBtn.getAttribute("data-unload");
      unloadSingleModel(id).catch((err) => console.error(err));
    }
  });

  const subs = [
    models.subscribe(renderList),
    showUnlisted.subscribe(renderList),
    showIdOrName.subscribe(() => {
      renderList();
      renderActions();
    }),
    isNarrow.subscribe(() => {
      renderActions();
      root.classList.toggle("models-panel-narrow", isNarrow.get());
    }),
  ];

  renderActions();
  renderList();

  return {
    el: root,
    destroy() {
      cleanupAll(subs);
    },
  };
}
