// SVG token histogram, ported from components/TokenHistogram.svelte.
import { el } from "../dom.js";

const HEIGHT = 250;
const PADDING = { top: 30, right: 20, bottom: 40, left: 75 };
const VIEWBOX_W = 1200;
const CHART_W = VIEWBOX_W - PADDING.left - PADDING.right;
const CHART_H = HEIGHT - PADDING.top - PADDING.bottom;

function fmt(n) {
  return Number.isFinite(n) ? n.toFixed(1) : "";
}

export function TokenHistogram({ data, unit = "tokens/sec", colorClass = "hist-blue" }) {
  const maxCount = Math.max(...data.bins);
  const barWidth = data.bins.length > 0 ? CHART_W / data.bins.length : 0;
  const range = data.max - data.min;
  const getX = (value) => PADDING.left + ((value - data.min) / range) * CHART_W;

  const barHtml = data.bins
    .map((count, i) => {
      const barH = maxCount > 0 ? (count / maxCount) * CHART_H : 0;
      const x = PADDING.left + i * barWidth;
      const y = HEIGHT - PADDING.bottom - barH;
      const binStart = data.min + i * data.binSize;
      const binEnd = binStart + data.binSize;
      const w = Math.max(barWidth - 1, 1);
      return `<rect x="${x}" y="${y}" width="${w}" height="${barH}" class="hist-bar ${colorClass}"><title>${fmt(binStart)} - ${fmt(binEnd)} ${unit}\nCount: ${count}</title></rect>`;
    })
    .join("");

  const ticks = [0, 0.5, 1]
    .map((f) => {
      const tc = Math.round(maxCount * f);
      const ty = HEIGHT - PADDING.bottom - f * CHART_H;
      return `<line x1="${PADDING.left - 8}" y1="${ty}" x2="${PADDING.left}" y2="${ty}" class="hist-tick" /><text x="${PADDING.left - 10}" y="${ty + 10}" class="hist-label" text-anchor="end">${tc}</text>`;
    })
    .join("");

  // Skip percentile lines when range is 0 (single-value input)
  const percentiles =
    range > 0
      ? `<line x1="${getX(data.p50)}" y1="${PADDING.top}" x2="${getX(data.p50)}" y2="${HEIGHT - PADDING.bottom}" class="hist-pct hist-pct-gray" />
         <line x1="${getX(data.p95)}" y1="${PADDING.top}" x2="${getX(data.p95)}" y2="${HEIGHT - PADDING.bottom}" class="hist-pct hist-pct-orange" />
         <line x1="${getX(data.p99)}" y1="${PADDING.top}" x2="${getX(data.p99)}" y2="${HEIGHT - PADDING.bottom}" class="hist-pct hist-pct-green" />`
      : "";

  const svg = el(`
    <div class="hist-wrap">
      <svg viewBox="0 0 ${VIEWBOX_W} ${HEIGHT}" preserveAspectRatio="xMidYMid meet">
        <line x1="${PADDING.left}" y1="${PADDING.top}" x2="${PADDING.left}" y2="${HEIGHT - PADDING.bottom}" class="hist-axis" />
        ${ticks}
        <line x1="${PADDING.left}" y1="${HEIGHT - PADDING.bottom}" x2="${VIEWBOX_W - PADDING.right}" y2="${HEIGHT - PADDING.bottom}" class="hist-axis" />
        ${barHtml}
        ${percentiles}
        <text x="${PADDING.left}" y="${HEIGHT - 8}" class="hist-label" text-anchor="start">${fmt(data.min)}</text>
        <text x="${VIEWBOX_W - PADDING.right}" y="${HEIGHT - 8}" class="hist-label" text-anchor="end">${fmt(data.max)}</text>
      </svg>
    </div>
  `);
  return { el: svg, destroy() {} };
}
