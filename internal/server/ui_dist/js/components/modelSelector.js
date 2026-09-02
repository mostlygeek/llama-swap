// Model picker dropdown, ported from components/playground/ModelSelector.svelte.
// Renders Local models + aliases + peer groups. Hidden when no models are available.
import { el, cleanupAll } from "../dom.js";
import { models } from "../api.js";
import { groupModels } from "../util/modelUtils.js";

export function ModelSelector({ value, placeholder = "Select a model...", disabled = false }) {
  const root = el(`<select class="pg-model-select" data-sel></select>`);
  let isUpdatingFromState = false;

  function render() {
    const grouped = groupModels(models.get());
    const hasModels = grouped.local.length > 0 || Object.keys(grouped.peersByProvider).length > 0;
    if (!hasModels) {
      root.style.display = "none";
      root.innerHTML = "";
      return;
    }
    root.style.display = "";
    const parts = [`<option value="">${placeholder}</option>`];
    if (grouped.local.length > 0) {
      parts.push(`<optgroup label="Local">`);
      for (const m of grouped.local) {
        parts.push(`<option value="${escapeAttr(m.id)}">${escapeText(m.id)}</option>`);
        if (m.aliases) {
          for (const a of m.aliases) {
            parts.push(`<option value="${escapeAttr(a)}">  ↳ ${escapeText(a)}</option>`);
          }
        }
      }
      parts.push(`</optgroup>`);
    }
    for (const [peerId, peerModels] of Object.entries(grouped.peersByProvider).sort(([a], [b]) =>
      a.localeCompare(b)
    )) {
      parts.push(`<optgroup label="Peer: ${escapeText(peerId)}">`);
      for (const m of peerModels) {
        parts.push(`<option value="${escapeAttr(m.id)}">${escapeText(m.id)}</option>`);
      }
      parts.push(`</optgroup>`);
    }
    isUpdatingFromState = true;
    root.innerHTML = parts.join("");
    root.value = value.get();
    root.disabled = !!disabled;
    isUpdatingFromState = false;
  }

  function escapeAttr(s) {
    return String(s).replace(/&/g, "&amp;").replace(/"/g, "&quot;").replace(/</g, "&lt;");
  }
  function escapeText(s) {
    return String(s).replace(/&/g, "&amp;").replace(/</g, "&lt;").replace(/>/g, "&gt;");
  }

  root.addEventListener("change", () => {
    if (!isUpdatingFromState) value.set(root.value);
  });

  const subs = [
    models.subscribe(render),
    value.subscribe(() => {
      if (root.value !== value.get()) root.value = value.get();
    }),
  ];

  render();

  return {
    el: root,
    setDisabled(d) { disabled = d; root.disabled = !!d; },
    destroy() { cleanupAll(subs); },
  };
}
