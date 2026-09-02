// Expandable textarea with fullscreen edit dialog. Ported from
// components/playground/ExpandableTextarea.svelte. The textarea always renders
// in-place; the expanded overlay mounts to document.body when opened.
import { el, escapeHtml } from "../dom.js";

const ICON_EXPAND = `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" class="icon-4"><polyline points="15 3 21 3 21 9"></polyline><polyline points="9 21 3 21 3 15"></polyline><line x1="21" y1="3" x2="14" y2="10"></line><line x1="3" y1="21" x2="10" y2="14"></line></svg>`;
const ICON_CLOSE = `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" class="icon-5"><line x1="18" y1="6" x2="6" y2="18"></line><line x1="6" y1="6" x2="18" y2="18"></line></svg>`;

export function ExpandableTextarea({ value, placeholder = "", rows = 3, disabled = false, onkeydown }) {
  const root = el(`
    <div class="eta-wrap">
      <textarea class="eta-input" rows="${rows}" placeholder="${escapeHtml(placeholder)}"></textarea>
      <button class="eta-expand-btn" type="button" title="Expand to edit">${ICON_EXPAND}</button>
    </div>
  `);

  const ta = root.querySelector(".eta-input");
  const expandBtn = root.querySelector(".eta-expand-btn");

  ta.value = value.get();
  ta.disabled = !!disabled;
  expandBtn.disabled = !!disabled;

  ta.addEventListener("input", () => value.set(ta.value));
  if (onkeydown) ta.addEventListener("keydown", onkeydown);

  value.subscribe((v) => {
    if (ta.value !== v) ta.value = v;
  });

  let overlay = null;
  let expandedTa = null;
  let expandedValue = "";

  function closeExpanded() {
    if (overlay) {
      overlay.remove();
      overlay = null;
      expandedTa = null;
    }
  }
  function saveExpanded() {
    value.set(expandedValue);
    closeExpanded();
  }
  function openExpanded() {
    expandedValue = value.get();
    overlay = el(`
      <div class="eta-overlay">
        <div class="eta-dialog">
          <div class="eta-dialog-head">
            <h3>Edit Text</h3>
            <button class="eta-dialog-close" type="button" title="Close">${ICON_CLOSE}</button>
          </div>
          <div class="eta-dialog-body"><textarea class="eta-dialog-input"></textarea></div>
          <div class="eta-dialog-foot">
            <button class="btn" type="button" data-cancel>Cancel</button>
            <button class="btn btn--primary" type="button" data-save>Done</button>
          </div>
        </div>
      </div>
    `);
    expandedTa = overlay.querySelector(".eta-dialog-input");
    expandedTa.value = expandedValue;
    expandedTa.placeholder = placeholder;
    expandedTa.addEventListener("input", () => { expandedValue = expandedTa.value; });
    expandedTa.addEventListener("keydown", (e) => {
      if (e.key === "Escape") closeExpanded();
    });
    overlay.querySelector(".eta-dialog-close").addEventListener("click", closeExpanded);
    overlay.querySelector("[data-cancel]").addEventListener("click", closeExpanded);
    overlay.querySelector("[data-save]").addEventListener("click", saveExpanded);
    document.body.appendChild(overlay);
    // focus + caret to end
    requestAnimationFrame(() => {
      expandedTa.focus();
      const len = expandedValue.length;
      try { expandedTa.setSelectionRange(len, len); } catch { /* ignore */ }
    });
  }

  expandBtn.addEventListener("click", openExpanded);

  return {
    el: root,
    setDisabled(d) {
      disabled = d;
      ta.disabled = !!d;
      expandBtn.disabled = !!d;
    },
    destroy() {
      closeExpanded();
    },
  };
}
