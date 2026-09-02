// Inline help tooltip, ported from components/Tooltip.svelte.
import { el } from "../dom.js";

export function Tooltip({ content }) {
  const root = el(`
    <div class="tooltip-wrap">
      <span class="tooltip-icon">&#9432;</span>
      <div class="tooltip-body">${content}</div>
    </div>
  `);
  return { el: root, destroy() {} };
}
