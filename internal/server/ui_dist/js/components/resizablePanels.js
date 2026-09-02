// Resizable two-pane splitter, ported from components/ResizablePanels.svelte.
// State (panel size %) persists per storageKey.
import { el, cleanupAll } from "../dom.js";

export function ResizablePanels({ direction, storageKey, left, right, defaultSize = 50, minSize = 5 }) {
  let leftSize = defaultSize;
  const storageName = `panel-size-${storageKey}`;

  try {
    const saved = localStorage.getItem(storageName);
    if (saved) {
      const parsed = parseFloat(saved);
      if (!isNaN(parsed) && parsed >= minSize && parsed <= 100 - minSize) leftSize = parsed;
    }
  } catch {
    /* ignore */
  }

  const horizontal = direction === "horizontal";
  const root = el(`
    <div class="resizable-panels ${horizontal ? "rp-horizontal" : "rp-vertical"}">
      <div class="rp-left" data-side="left"></div>
      <div class="rp-handle" role="separator" tabindex="0" aria-label="Resize panels"
           aria-orientation="${direction}" aria-valuenow="${Math.round(leftSize)}"
           aria-valuemin="${minSize}" aria-valuemax="${100 - minSize}"></div>
      <div class="rp-right" data-side="right"></div>
    </div>
  `);

  const leftEl = root.querySelector(".rp-left");
  const rightEl = root.querySelector(".rp-right");
  const handle = root.querySelector(".rp-handle");

  const leftInst = left();
  const rightInst = right();
  leftEl.appendChild(leftInst.el);
  rightEl.appendChild(rightInst.el);

  function applySize() {
    if (horizontal) {
      leftEl.style.cssText = `width: ${leftSize}%; min-width: ${minSize}%`;
      rightEl.style.cssText = `width: ${100 - leftSize}%; min-width: ${minSize}%`;
    } else {
      leftEl.style.cssText = `height: ${leftSize}%; min-height: ${minSize}%`;
      rightEl.style.cssText = `height: ${100 - leftSize}%; min-height: ${minSize}%`;
    }
    handle.setAttribute("aria-valuenow", Math.round(leftSize));
  }
  applySize();

  function saveSize() {
    try {
      localStorage.setItem(storageName, String(leftSize));
    } catch {
      /* ignore */
    }
  }

  function updateSize(clientX, clientY) {
    const rect = root.getBoundingClientRect();
    const newSize = horizontal
      ? ((clientX - rect.left) / rect.width) * 100
      : ((clientY - rect.top) / rect.height) * 100;
    leftSize = Math.max(minSize, Math.min(100 - minSize, newSize));
    applySize();
  }

  function onMove(e) {
    if (horizontal) updateSize(e.clientX, e.clientY);
    else updateSize(e.clientX, e.clientY);
  }
  function onTouchMove(e) {
    if (e.touches.length === 0) return;
    updateSize(e.touches[0].clientX, e.touches[0].clientY);
  }
  function onUp() {
    saveSize();
    document.removeEventListener("mousemove", onMove);
    document.removeEventListener("mouseup", onUp);
    document.removeEventListener("touchmove", onTouchMove);
    document.removeEventListener("touchend", onUp);
    handle.classList.remove("dragging");
  }
  function onDown(e) {
    e.preventDefault();
    document.addEventListener("mousemove", onMove);
    document.addEventListener("mouseup", onUp);
    handle.classList.add("dragging");
  }
  function onTouchStart(e) {
    document.addEventListener("touchmove", onTouchMove);
    document.addEventListener("touchend", onUp);
    handle.classList.add("dragging");
    if (e.touches.length > 0) updateSize(e.touches[0].clientX, e.touches[0].clientY);
  }
  function onKey(e) {
    const step = 2;
    const key = e.key;
    const arrows = horizontal ? ["ArrowLeft", "ArrowRight"] : ["ArrowUp", "ArrowDown"];
    if (!arrows.includes(key)) return;
    e.preventDefault();
    const delta = key === "ArrowLeft" || key === "ArrowUp" ? -step : step;
    leftSize = Math.max(minSize, Math.min(100 - minSize, leftSize + delta));
    applySize();
    saveSize();
  }

  handle.addEventListener("mousedown", onDown);
  handle.addEventListener("touchstart", onTouchStart);
  handle.addEventListener("keydown", onKey);

  return {
    el: root,
    destroy() {
      handle.removeEventListener("mousedown", onDown);
      handle.removeEventListener("touchstart", onTouchStart);
      handle.removeEventListener("keydown", onKey);
      cleanupAll([leftInst.destroy, rightInst.destroy]);
    },
  };
}
