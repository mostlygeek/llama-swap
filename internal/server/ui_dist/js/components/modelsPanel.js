// Models panel: local + peer model list with load/unload, profiles and
// selectors cards, capability badges, and persistent display toggles.
// Ported from routes/ModelsDash.svelte + components/ModelsPanel.svelte.
import { el, cleanupAll, escapeHtml } from "../dom.js";
import {
  models,
  playgroundModels,
  profiles,
  activeProfile,
  fetchPlaygroundModels,
  loadModel,
  unloadAllModels,
  unloadSingleModel,
} from "../api.js";
import { isNarrow } from "../theme.js";
import { persistent } from "../store.js";
import { listCapabilityBadges, capabilityBadgeClass } from "../util/capabilities.js";

function statusDotClass(m) {
  if (m.state === "ready") return "status-dot status-dot--ready";
  if (m.state === "starting" || m.state === "stopping") return "status-dot status-dot--transition";
  return "status-dot status-dot--idle";
}

function modelServerPath(modelId) {
  if (modelId === "comfyui_auto") return "/comfyui/";
  return `/upstream/${encodeURIComponent(modelId)}/`;
}

export function ModelsPanel() {
  const showUnlisted = persistent("showUnlisted", true);
  const showIdOrName = persistent("showIdorName", "id");
  const showCapTags = persistent("showCapabilityTags", true);
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

  // Merge /v1/models capabilities into the live SSE model records. The SSE
  // modelStatus entries carry state; the playground records carry
  // capabilities/context_length/aliases.
  function mergedModels() {
    const live = models.get();
    const playground = playgroundModels.get();
    const byId = new Map(playground.map((p) => [p.id, p]));
    return live.map((m) => {
      const extra = byId.get(m.id);
      return extra ? { ...m, capabilities: extra.capabilities, context_length: extra.context_length } : m;
    });
  }

  function badgeHtml(m) {
    if (!showCapTags.get()) return "";
    const badges = listCapabilityBadges(m);
    if (badges.length === 0) return "";
    return `<div class="model-cap-badges">${badges
      .map((b) => `<span class="model-cap-badge ${capabilityBadgeClass[b.key] ?? ""}">${escapeHtml(b.label)}</span>`)
      .join("")}</div>`;
  }

  function filtered() {
    const all = mergedModels();
    const filteredAll = all.filter((m) => showUnlisted.get() || !m.unlisted);
    const peers = filteredAll.filter((m) => m.peerID);
    const grouped = peers.reduce((acc, m) => {
      const k = m.peerID || "unknown";
      (acc[k] = acc[k] || []).push(m);
      return acc;
    }, {});
    // Active (loaded / loading) models float to the top so they're quick to
    // find and unload. Array.sort is stable, so the existing ordering is
    // preserved within the active and inactive groups.
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
    const unlistedTag = m.unlisted ? `<span class="model-tag model-tag--unlisted">unlisted</span>` : "";
    const rowCls =
      m.state === "ready" ? " models-row-loaded" : m.state !== "stopped" ? " models-row-transition" : "";
    const action =
      m.state === "stopped"
        ? `<button class="btn btn--sm" data-load="${escapeHtml(m.id)}">Load</button>`
        : `<button class="btn btn--sm" data-unload="${escapeHtml(m.id)}" ${m.state !== "ready" ? "disabled" : ""}>Unload</button>`;
    return `
      <tr class="models-row${rowCls}">
        <td class="models-name">
          <div class="models-name-row">
            <span class="${statusDotClass(m)}" title="${escapeHtml(m.state)}"></span>
            <a class="models-link" href="#/models/${encodeURIComponent(m.id)}">${display}</a>
            ${unlistedTag}
            <a class="models-open" href="${modelServerPath(m.id)}" target="_blank" rel="noopener" title="Open model server">↗</a>
          </div>
          ${desc}${aliases}
          ${badgeHtml(m)}
        </td>
        <td class="models-action">${action}</td>
        <td class="models-state"><span class="status status--${escapeHtml(m.state)}">${escapeHtml(m.state)}</span></td>
      </tr>
    `;
  }

  function profilesCardHtml() {
    const list = profiles.get();
    if (list.length === 0) return "";
    const active = activeProfile.get();
    const selected = list.find((p) => p.id === active);
    const mappings = Object.entries(selected?.pins ?? {}).sort(([a], [b]) =>
      a.localeCompare(b, undefined, { numeric: true })
    );

    const options = [`<option value="">(none)</option>`]
      .concat(list.map((p) => `<option value="${escapeHtml(p.id)}" ${p.id === active ? "selected" : ""}>${escapeHtml(p.id)}</option>`))
      .join("");

    let bodyHtml;
    if (!selected) {
      bodyHtml = `<div class="models-empty muted">No active profile</div>`;
    } else if (mappings.length === 0) {
      bodyHtml = `<div class="models-empty muted">No mappings</div>`;
    } else {
      bodyHtml = `<div class="profile-mappings">${mappings
        .map(
          ([modelID, target]) => `
          <div class="profile-mapping">
            <span class="profile-mapping-model">${escapeHtml(modelID)}</span>
            <span class="profile-mapping-arrow" aria-hidden="true">→</span>
            ${
              target
                ? `<span class="profile-mapping-target">${escapeHtml(target)}</span>`
                : `<span class="model-tag model-tag--unlisted">disabled</span>`
            }
          </div>`
        )
        .join("")}</div>`;
    }

    return `
      <div class="card models-profiles">
        <div class="models-section-head">
          <h3 class="models-section-title">Profiles</h3>
          ${selected ? `<span class="model-tag">${escapeHtml(active)}</span><span class="model-tag model-tag--active">Active</span>` : ""}
          <span class="muted models-section-count">${mappings.length} ${mappings.length === 1 ? "mapping" : "mappings"}</span>
        </div>
        ${selected?.description ? `<p class="muted models-section-desc">${escapeHtml(selected.description)}</p>` : ""}
        <div class="models-profile-select">
          <label for="models-profile-select">Active profile</label>
          <select id="models-profile-select" data-profile-select>${options}</select>
        </div>
        ${bodyHtml}
      </div>
    `;
  }

  function selectorsCardHtml() {
    const selectors = playgroundModels.get().filter((m) => m.playgroundType === "selector");
    if (selectors.length === 0) return "";
    return `
      <div class="card models-selectors">
        <div class="models-section-head">
          <h3 class="models-section-title">Selectors</h3>
          <span class="muted models-section-count">${selectors.length} ${selectors.length === 1 ? "selector" : "selectors"}</span>
        </div>
        ${selectors
          .map((selector) => {
            const spillover =
              selector.strategy === "spillover" && selector.spillover
                ? `<span class="model-tag">spillover ${escapeHtml(String(selector.spillover))}</span>`
                : "";
            const targets = (selector.targets ?? [])
              .map(
                (t) =>
                  `<a class="models-link models-selector-target" href="#/models/${encodeURIComponent(t)}">${escapeHtml(t)}</a>`
              )
              .join(", ");
            return `
              <div class="models-selector">
                <div class="models-selector-name">${escapeHtml(selector.name ? `${selector.id} - ${selector.name}` : selector.id)}</div>
                ${selector.description ? `<div class="muted models-selector-desc">${escapeHtml(selector.description)}</div>` : ""}
                <div class="muted models-selector-targets">targets: ${targets}</div>
                <div class="models-selector-tags">${spillover}<span class="model-tag model-tag--unlisted">${escapeHtml(selector.strategy ?? "")}</span></div>
              </div>`;
          })
          .join("")}
      </div>
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
      ${profilesCardHtml()}
      ${selectorsCardHtml()}
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
    const caps = showCapTags.get();
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
              <button class="menu-item" data-toggle-caps>${caps ? "Hide Capability Tags" : "Show Capability Tags"}</button>
              <button class="menu-item" data-unload-all ${isUnloading ? "disabled" : ""}>${btnLabel}</button>
            </div>` : ""}
        </div>
      `;
    } else {
      actionsEl.innerHTML = `
        <div class="models-actions-row">
          <button class="btn" data-toggle-display>${mode === "id" ? "ID" : "Name"}</button>
          <button class="btn" data-toggle-unlisted>unlisted</button>
          <button class="btn ${caps ? "btn--active" : ""}" data-toggle-caps title="Toggle capability tags">caps</button>
        </div>
        <button class="btn" data-unload-all ${isUnloading ? "disabled" : ""}>${btnLabel}</button>
      `;
    }
  }

  // Wire delegated handlers on actions
  actionsEl.addEventListener("click", (e) => {
    const t = e.target.closest("[data-toggle-display],[data-toggle-unlisted],[data-toggle-caps],[data-unload-all],[data-menu]");
    if (!t) return;
    if (t.hasAttribute("data-menu")) {
      menuOpen = !menuOpen;
      renderActions();
    } else if (t.hasAttribute("data-toggle-display")) {
      showIdOrName.update((v) => (v === "name" ? "id" : "name"));
    } else if (t.hasAttribute("data-toggle-unlisted")) {
      showUnlisted.update((v) => !v);
    } else if (t.hasAttribute("data-toggle-caps")) {
      showCapTags.update((v) => !v);
      renderActions();
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

  // Delegated handlers on list (load/unload per row; profile switch)
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
  listEl.addEventListener("change", async (e) => {
    const select = e.target.closest("[data-profile-select]");
    if (!select) return;
    const value = select.value || null;
    try {
      const { setActiveProfile } = await import("../api.js");
      await setActiveProfile(value);
    } catch (err) {
      console.error(err);
    }
  });

  const subs = [
    models.subscribe(renderList),
    playgroundModels.subscribe(renderList),
    profiles.subscribe(renderList),
    activeProfile.subscribe(renderList),
    showUnlisted.subscribe(renderList),
    showCapTags.subscribe(renderList),
    showIdOrName.subscribe(() => {
      renderList();
      renderActions();
    }),
    isNarrow.subscribe(() => {
      renderActions();
      root.classList.toggle("models-panel-narrow", isNarrow.get());
    }),
  ];

  // The /v1/models list carries capabilities and selectors that the SSE
  // modelStatus events do not.
  fetchPlaygroundModels().catch((err) => console.error(err));

  renderActions();
  renderList();

  return {
    el: root,
    destroy() {
      cleanupAll(subs);
    },
  };
}
