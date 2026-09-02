// chart.js line-chart wrapper. Ported from components/PerformanceChart.svelte.
// Uses the vendored Chart.js UMD (window.Chart) loaded as a classic <script>.
import { el, cleanupAll } from "../dom.js";
import { isDarkMode } from "../theme.js";

function chartColors(dark) {
  return {
    grid: dark ? "rgba(255,255,255,0.08)" : "rgba(0,0,0,0.08)",
    tick: dark ? "#9ca3af" : "#6b7280",
    legend: dark ? "#d1d5db" : "#374151",
    tooltipBg: dark ? "#1f2937" : "#ffffff",
    tooltipText: dark ? "#f3f4f6" : "#111827",
    tooltipBorder: dark ? "#374151" : "#e5e7eb",
  };
}

function buildOptions({ title, yMin, yMax, yLabel, showLegend, dark }) {
  const c = chartColors(dark);
  return {
    responsive: true,
    maintainAspectRatio: false,
    animation: false,
    interaction: { mode: "index", intersect: false },
    plugins: {
      legend: {
        display: showLegend,
        position: "top",
        labels: { color: c.legend, usePointStyle: true, pointStyle: "circle", padding: 12, font: { size: 11 } },
      },
      title: { display: true, text: title, color: c.legend, font: { size: 14, weight: "bold" } },
      tooltip: {
        backgroundColor: c.tooltipBg,
        titleColor: c.tooltipText,
        bodyColor: c.tooltipText,
        borderColor: c.tooltipBorder,
        borderWidth: 1,
      },
    },
    scales: {
      x: {
        bounds: "data",
        offset: false,
        ticks: { color: c.tick, maxRotation: 0, font: { size: 10 }, maxTicksLimit: 10 },
        grid: { color: c.grid },
      },
      y: {
        min: yMin,
        max: yMax,
        ticks: { color: c.tick, font: { size: 10 } },
        grid: { color: c.grid },
        title: yLabel ? { display: true, text: yLabel, color: c.tick } : undefined,
      },
    },
  };
}

function buildDatasets(datasets) {
  return datasets.map((ds) => ({
    label: ds.label,
    data: [...ds.data],
    borderColor: ds.borderColor,
    backgroundColor: ds.borderColor + "20",
    borderWidth: 1.5,
    pointRadius: 0,
    tension: 0.4,
    fill: false,
  }));
}

export function PerformanceChart({ title, labels, datasets, yMin, yMax, yLabel, showLegend = true }) {
  if (typeof window.Chart === "undefined") {
    throw new Error("window.Chart is undefined; vendor/chart.umd.js must load before this module");
  }
  const root = el(`<div class="card perf-chart-wrap"><canvas></canvas></div>`);
  const canvas = root.querySelector("canvas");

  let chart = new window.Chart(canvas, {
    type: "line",
    data: { labels: [...labels], datasets: buildDatasets(datasets) },
    options: buildOptions({ title, yMin, yMax, yLabel, showLegend, dark: isDarkMode.get() }),
  });

  const subs = [
    isDarkMode.subscribe((dark) => {
      chart.options = buildOptions({ title, yMin, yMax, yLabel, showLegend, dark });
      chart.update("none");
    }),
    // No data subscription: parent calls update() imperatively to avoid recreating charts on every tick.
  ];

  return {
    el: root,
    update({ labels: newLabels, datasets: newDatasets }) {
      chart.data.labels = [...newLabels];
      chart.data.datasets = buildDatasets(newDatasets);
      chart.update("none");
    },
    destroy() {
      cleanupAll(subs);
      chart.destroy();
      chart = null;
    },
  };
}
