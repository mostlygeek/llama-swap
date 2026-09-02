// Performance page: GPU + system metrics dashboard using chart.js.
// Ported from routes/Performance.svelte.
import { el, cleanupAll } from "../dom.js";
import { fetchPerformance } from "../api.js";
import { persistent } from "../store.js";
import { PerformanceChart } from "../components/performanceChart.js";

const COLORS = [
  "#3b82f6", "#ef4444", "#10b981", "#f59e0b", "#8b5cf6", "#ec4899",
  "#06b6d4", "#84cc16", "#f97316", "#14b8a6", "#a855f7", "#e11d48",
  "#0ea5e9", "#eab308", "#d946ef", "#22d3ee",
];

const WINDOWS = [
  { label: "5 min", ms: 5 * 60 * 1000 },
  { label: "15 min", ms: 15 * 60 * 1000 },
  { label: "1 hr", ms: 60 * 60 * 1000 },
];

const INTERVALS = [
  { label: "Off", ms: 0 },
  { label: "5s", ms: 5000 },
  { label: "10s", ms: 10000 },
  { label: "30s", ms: 30000 },
  { label: "60s", ms: 60000 },
];

function formatDelta(ts, refTime) {
  const diffMs = new Date(ts).getTime() - refTime;
  const diffSec = Math.round(diffMs / 1000);
  const absSec = Math.abs(diffSec);
  const sign = diffSec <= 0 ? "-" : "+";
  if (absSec < 60) return `${sign}${absSec}s`;
  const min = Math.floor(absSec / 60);
  const sec = absSec % 60;
  if (sec === 0) return `${sign}${min}m`;
  return `${sign}${min}:${sec.toString().padStart(2, "0")}`;
}

function sysLabelsFor(stats) {
  if (stats.length === 0) return [];
  const refTime = new Date(stats[stats.length - 1].timestamp).getTime();
  return stats.map((s) => formatDelta(s.timestamp, refTime));
}

function cpuDatasets(stats) {
  if (stats.length === 0) return [];
  const coreCount = stats[0].cpu_util_per_core.length;
  const out = [];
  for (let i = 0; i < coreCount; i++) {
    out.push({
      label: `Core ${i}`,
      data: stats.map((s) => s.cpu_util_per_core[i]),
      borderColor: COLORS[i % COLORS.length],
    });
  }
  return out;
}

function memSwapDatasets(stats) {
  if (stats.length === 0) return [];
  return [
    {
      label: "Memory Used %",
      data: stats.map((s) => (s.mem_used_mb / s.mem_total_mb) * 100),
      borderColor: "#3b82f6",
    },
    {
      label: "Swap Used %",
      data: stats.map((s) => (s.swap_total_mb > 0 ? (s.swap_used_mb / s.swap_total_mb) * 100 : 0)),
      borderColor: "#8b5cf6",
    },
  ];
}

function latestMemSwap(stats) {
  if (stats.length === 0) return null;
  const s = stats[stats.length - 1];
  return {
    mem_total_mb: s.mem_total_mb,
    mem_used_mb: s.mem_used_mb,
    mem_used_pct: ((s.mem_used_mb / s.mem_total_mb) * 100).toFixed(1),
    swap_total_mb: s.swap_total_mb,
    swap_used_mb: s.swap_used_mb,
    swap_used_pct: s.swap_total_mb > 0 ? ((s.swap_used_mb / s.swap_total_mb) * 100).toFixed(1) : null,
  };
}

function loadDatasets(stats) {
  if (stats.length === 0) return [];
  return [
    { label: "1 min", data: stats.map((s) => s.load_avg_1), borderColor: "#10b981" },
    { label: "5 min", data: stats.map((s) => s.load_avg_5), borderColor: "#f59e0b" },
    { label: "15 min", data: stats.map((s) => s.load_avg_15), borderColor: "#ef4444" },
  ];
}

function netBandwidthDatasets(stats) {
  if (stats.length < 2) return { datasets: [], labels: [] };

  const ifaceNames = new Set();
  for (const s of stats) for (const n of s.net_io || []) ifaceNames.add(n.name);
  const interfaces = [...ifaceNames].sort();
  if (interfaces.length === 0) return { datasets: [], labels: [] };

  const datasets = [];
  let colorIdx = 0;
  for (const iface of interfaces) {
    const recv = [];
    const sent = [];
    for (let i = 1; i < stats.length; i++) {
      const prevIO = (stats[i - 1].net_io || []).find((n) => n.name === iface);
      const currIO = (stats[i].net_io || []).find((n) => n.name === iface);
      if (!prevIO || !currIO) { recv.push(0); sent.push(0); continue; }
      const dtMs = new Date(stats[i].timestamp).getTime() - new Date(stats[i - 1].timestamp).getTime();
      if (dtMs <= 0) { recv.push(0); sent.push(0); continue; }
      const dtSec = dtMs / 1000;
      recv.push((((currIO.bytes_recv - prevIO.bytes_recv) / dtSec) * 8) / 1_000_000);
      sent.push((((currIO.bytes_sent - prevIO.bytes_sent) / dtSec) * 8) / 1_000_000);
    }
    datasets.push({ label: `${iface} in`, data: recv, borderColor: COLORS[colorIdx % COLORS.length] });
    colorIdx++;
    datasets.push({ label: `${iface} out`, data: sent, borderColor: COLORS[colorIdx % COLORS.length] });
    colorIdx++;
  }
  const refTime = new Date(stats[stats.length - 1].timestamp).getTime();
  const labels = stats.slice(1).map((s) => formatDelta(s.timestamp, refTime));
  return { datasets, labels };
}

function gpuLabelsFor(stats) {
  const seen = new Set();
  const labels = [];
  if (stats.length === 0) return [];
  const refTime = new Date(stats[stats.length - 1].timestamp).getTime();
  for (const g of stats) {
    const label = formatDelta(g.timestamp, refTime);
    if (!seen.has(label)) { seen.add(label); labels.push(label); }
  }
  return labels;
}

function buildGpuDatasets(stats, field) {
  if (stats.length === 0) return [];
  const byId = new Map();
  for (const g of stats) {
    if (!byId.has(g.id)) byId.set(g.id, { name: g.name, values: [] });
    byId.get(g.id).values.push(g[field]);
  }
  const out = [];
  let colorIdx = 0;
  for (const [id, entry] of byId) {
    out.push({ label: entry.name || `GPU ${id}`, data: entry.values, borderColor: COLORS[colorIdx % COLORS.length] });
    colorIdx++;
  }
  return out;
}

export function PerformancePage() {
  const selectedWindow = persistent("perf-window", 0);
  const selectedInterval = persistent("perf-refresh-interval", 0);

  let sysData = [];
  let gpuData = [];
  let refreshing = false;
  let pollTimer = null;
  let visible = true;

  const root = el(`
    <div class="page page-performance">
      <div class="perf-toolbar">
        <h2 class="perf-title">Performance (Experimental)</h2>
        <div class="perf-toolbar-actions">
          <div class="perf-btn-group" data-window-buttons></div>
          <div class="perf-btn-group">
            <span class="perf-refresh-label">Refresh:</span>
            <div class="perf-btn-group" data-interval-buttons></div>
          </div>
          <button class="btn btn--sm perf-refresh-btn" data-refresh title="Refresh">
            <svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24" fill="none" stroke="currentColor"
                 stroke-width="2" stroke-linecap="round" stroke-linejoin="round" class="icon-4 perf-refresh-icon">
              <path d="M21 12a9 9 0 0 0-9-9 9.75 9.75 0 0 0-6.74 2.74L3 8" />
              <path d="M3 3v5h5" />
              <path d="M3 12a9 9 0 0 0 9 9 9.75 9.75 0 0 0 6.74-2.74L21 16" />
              <path d="M16 16h5v5" />
            </svg>
          </button>
        </div>
      </div>
      <p class="perf-note">
        This is an experimental feature. Please use
        <a class="perf-note-link" href="https://github.com/mostlygeek/llama-swap/discussions/771">discussion #711</a>
        for instructions and to share feedback.
      </p>
      <section class="perf-section">
        <h3 class="perf-section-heading">GPU</h3>
        <div class="perf-empty card" data-gpu-empty style="display:none">No GPU data available</div>
        <div class="perf-grid" data-gpu-grid></div>
      </section>
      <section class="perf-section">
        <h3 class="perf-section-heading">System</h3>
        <div class="perf-grid" data-sys-grid></div>
      </section>
    </div>
  `);

  // Wire window/interval button groups
  const winBtns = root.querySelector("[data-window-buttons]");
  const intBtns = root.querySelector("[data-interval-buttons]");
  const refreshBtn = root.querySelector("[data-refresh]");
  const refreshIcon = refreshBtn.querySelector(".perf-refresh-icon");

  function renderWinBtns() {
    winBtns.innerHTML = WINDOWS.map(
      (w, i) => `<button class="btn btn--sm ${selectedWindow.get() === i ? "btn-primary-on" : ""}" data-w="${i}">${w.label}</button>`
    ).join("");
  }
  function renderIntBtns() {
    intBtns.innerHTML = INTERVALS.map(
      (iv, i) => `<button class="btn btn--sm ${selectedInterval.get() === i ? "btn-primary-on" : ""}" data-i="${i}">${iv.label}</button>`
    ).join("");
  }
  renderWinBtns();
  renderIntBtns();

  winBtns.addEventListener("click", (e) => {
    const b = e.target.closest("[data-w]");
    if (!b) return;
    selectedWindow.set(Number(b.getAttribute("data-w")));
    renderWinBtns();
    redrawAllCharts();
  });
  intBtns.addEventListener("click", (e) => {
    const b = e.target.closest("[data-i]");
    if (!b) return;
    selectedInterval.set(Number(b.getAttribute("data-i")));
    renderIntBtns();
    startPolling();
  });
  refreshBtn.addEventListener("click", async () => {
    if (refreshing) return;
    refreshing = true;
    refreshIcon.classList.add("animate-spin");
    refreshBtn.disabled = true;
    try {
      await loadIncremental();
    } finally {
      refreshing = false;
      refreshIcon.classList.remove("animate-spin");
      refreshBtn.disabled = false;
    }
  });

  // ---- Data fetching ----
  async function loadAll() {
    const resp = await fetchPerformance();
    if (resp) {
      sysData = resp.sys_stats || [];
      gpuData = resp.gpu_stats || [];
    }
    redrawAllCharts();
  }

  async function loadIncremental() {
    const lastTs = sysData.length > 0 ? sysData[sysData.length - 1].timestamp : undefined;
    const resp = await fetchPerformance(lastTs);
    if (resp) {
      const newSys = resp.sys_stats || [];
      const newGpu = resp.gpu_stats || [];
      if (newSys.length > 0) sysData = [...sysData, ...newSys];
      if (newGpu.length > 0) gpuData = [...gpuData, ...newGpu];
    }
    redrawAllCharts();
  }

  function startPolling() {
    stopPolling();
    const ms = INTERVALS[selectedInterval.get()].ms;
    if (ms <= 0) return;
    pollTimer = setInterval(() => { if (visible) loadIncremental(); }, ms);
  }
  function stopPolling() {
    if (pollTimer) { clearInterval(pollTimer); pollTimer = null; }
  }

  function handleVisibility() {
    visible = !document.hidden;
    if (visible) {
      loadAll().then(() => startPolling());
    } else {
      stopPolling();
    }
  }
  document.addEventListener("visibilitychange", handleVisibility);

  // ---- Charts ----
  let charts = []; // [{instance, update}]
  const gpuGrid = root.querySelector("[data-gpu-grid]");
  const sysGrid = root.querySelector("[data-sys-grid]");
  const gpuEmpty = root.querySelector("[data-gpu-empty]");

  // Card placeholders that we will reuse across renders (avoid full recreate on every tick)
  let gpuUtil, gpuMem, gpuTemp, gpuVramTemp, gpuPower;
  let sysCpu, sysMemSwap, sysLoad, sysNet;
  let memSwapInfoEl = null;

  function ensureSysCharts() {
    if (sysCpu) return;
    sysCpu = PerformanceChart({
      title: "CPU Utilization (%)", labels: [], datasets: [],
      yMin: 0, yMax: 100, yLabel: "%", showLegend: false,
    });
    sysGrid.appendChild(sysCpu.el);

    const memWrap = document.createElement("div");
    const memCard = PerformanceChart({
      title: "Memory & Swap Usage (%)", labels: [], datasets: [],
      yMin: 0, yMax: 100, yLabel: "%",
    });
    memSwapInfoEl = document.createElement("div");
    memSwapInfoEl.className = "perf-mem-info";
    memWrap.appendChild(memCard.el);
    memWrap.appendChild(memSwapInfoEl);
    sysMemSwap = memCard;
    sysGrid.appendChild(memWrap);

    sysLoad = PerformanceChart({
      title: "Load Average", labels: [], datasets: [], yMin: 0,
    });
    sysGrid.appendChild(sysLoad.el);
  }

  function ensureGpuCharts(hasVramTemp) {
    if (gpuUtil) return;
    gpuUtil = PerformanceChart({ title: "GPU Utilization (%)", labels: [], datasets: [], yMin: 0, yMax: 100, yLabel: "%" });
    gpuMem = PerformanceChart({ title: "GPU Memory Utilization (%)", labels: [], datasets: [], yMin: 0, yMax: 100, yLabel: "%" });
    gpuTemp = PerformanceChart({ title: "GPU Temperature (°C)", labels: [], datasets: [], yMin: 0, yLabel: "°C" });
    gpuPower = PerformanceChart({ title: "GPU Power Draw (W)", labels: [], datasets: [], yMin: 0, yLabel: "W" });
    gpuGrid.appendChild(gpuUtil.el);
    gpuGrid.appendChild(gpuMem.el);
    gpuGrid.appendChild(gpuTemp.el);
    gpuGrid.appendChild(gpuPower.el);
    if (hasVramTemp) {
      gpuVramTemp = PerformanceChart({ title: "GPU VRAM Temperature (°C)", labels: [], datasets: [], yMin: 0, yLabel: "°C" });
      gpuGrid.appendChild(gpuVramTemp.el);
    }
  }

  function ensureNetChart() {
    if (sysNet) return;
    sysNet = PerformanceChart({
      title: "Network Bandwidth (Mbit/s)", labels: [], datasets: [],
      yMin: 0, yLabel: "Mbit/s", showLegend: false,
    });
    sysGrid.appendChild(sysNet.el);
  }

  function maybeRemoveNetChart() {
    if (!sysNet) return;
    sysNet.destroy();
    if (sysNet.el.parentNode) sysNet.el.parentNode.removeChild(sysNet.el);
    sysNet = null;
  }

  function maybeRemoveGpuVram() {
    if (!gpuVramTemp) return;
    gpuVramTemp.destroy();
    if (gpuVramTemp.el.parentNode) gpuVramTemp.el.parentNode.removeChild(gpuVramTemp.el);
    gpuVramTemp = null;
  }

  function redrawAllCharts() {
    const cutoff = Date.now() - WINDOWS[selectedWindow.get()].ms;
    const filteredSys = sysData.filter((s) => new Date(s.timestamp).getTime() >= cutoff);
    const filteredGpu = gpuData.filter((g) => new Date(g.timestamp).getTime() >= cutoff);
    const hasGpu = gpuData.length > 0;

    // GPU section
    gpuEmpty.style.display = hasGpu ? "none" : "";
    gpuGrid.style.display = hasGpu ? "" : "none";
    if (hasGpu) {
      const hasVramTemp = filteredGpu.some((g) => g.vram_temp_c > 0);
      ensureGpuCharts(hasVramTemp);
      const gLabels = gpuLabelsFor(filteredGpu);
      gpuUtil.update({ labels: gLabels, datasets: buildGpuDatasets(filteredGpu, "gpu_util_pct") });
      gpuMem.update({ labels: gLabels, datasets: buildGpuDatasets(filteredGpu, "mem_util_pct") });
      gpuTemp.update({ labels: gLabels, datasets: buildGpuDatasets(filteredGpu, "temp_c") });
      gpuPower.update({ labels: gLabels, datasets: buildGpuDatasets(filteredGpu, "power_draw_w") });
      if (hasVramTemp) {
        if (!gpuVramTemp) {
          gpuVramTemp = PerformanceChart({ title: "GPU VRAM Temperature (°C)", labels: [], datasets: [], yMin: 0, yLabel: "°C" });
          gpuGrid.appendChild(gpuVramTemp.el);
        }
        gpuVramTemp.update({ labels: gLabels, datasets: buildGpuDatasets(filteredGpu, "vram_temp_c") });
      } else {
        maybeRemoveGpuVram();
      }
    }

    // System section
    ensureSysCharts();
    const sLabels = sysLabelsFor(filteredSys);
    sysCpu.update({ labels: sLabels, datasets: cpuDatasets(filteredSys) });
    sysMemSwap.update({ labels: sLabels, datasets: memSwapDatasets(filteredSys) });

    const ms = latestMemSwap(filteredSys);
    if (ms) {
      let html = `<span>Mem: <span class="perf-mem-num">${ms.mem_used_mb.toLocaleString()} / ${ms.mem_total_mb.toLocaleString()} MB (${ms.mem_used_pct}%)</span></span>`;
      if (ms.swap_used_pct !== null) {
        html += `<span>Swap: <span class="perf-mem-num">${ms.swap_used_mb.toLocaleString()} / ${ms.swap_total_mb.toLocaleString()} MB (${ms.swap_used_pct}%)</span></span>`;
      }
      memSwapInfoEl.innerHTML = html;
    } else {
      memSwapInfoEl.innerHTML = "";
    }

    sysLoad.update({ labels: sLabels, datasets: loadDatasets(filteredSys) });

    const net = netBandwidthDatasets(filteredSys);
    if (net.datasets.length > 0) {
      ensureNetChart();
      sysNet.update({ labels: net.labels, datasets: net.datasets });
    } else {
      maybeRemoveNetChart();
    }
  }

  // Initial load
  loadAll().then(() => startPolling());

  return {
    el: root,
    destroy() {
      stopPolling();
      document.removeEventListener("visibilitychange", handleVisibility);
      const allCharts = [sysCpu, sysMemSwap, sysLoad, sysNet, gpuUtil, gpuMem, gpuTemp, gpuVramTemp, gpuPower].filter(Boolean);
      cleanupAll(allCharts.map((c) => c.destroy));
    },
  };
}
