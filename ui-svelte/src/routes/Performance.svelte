<script lang="ts">
  import { onMount } from "svelte";
  import { fetchPerformance } from "../stores/api";
  import { persistentStore } from "../stores/persistent";
  import type { SysStat, GpuStat } from "../lib/types";
  import { formatBytesPerSecond } from "../lib/format";
  import { buildAdvancedGpuIO, hasAdvancedGpuIO } from "../lib/performanceGpu";
  import PerformanceChart from "../components/PerformanceChart.svelte";
  import SegmentedControl from "../components/SegmentedControl.svelte";
  import * as Card from "$lib/components/ui/card/index.js";
  import { Button } from "$lib/components/ui/button/index.js";
  import { RefreshCw } from "@lucide/svelte";

  const COLORS = [
    "#3b82f6",
    "#ef4444",
    "#10b981",
    "#f59e0b",
    "#8b5cf6",
    "#ec4899",
    "#06b6d4",
    "#84cc16",
    "#f97316",
    "#14b8a6",
    "#a855f7",
    "#e11d48",
    "#0ea5e9",
    "#eab308",
    "#d946ef",
    "#22d3ee",
  ];

  const WINDOWS = [
    { label: "5 min", ms: 5 * 60 * 1000 },
    { label: "15 min", ms: 15 * 60 * 1000 },
    { label: "1 hr", ms: 60 * 60 * 1000 },
  ] as const;

  const INTERVALS = [
    { label: "Off", ms: 0 },
    { label: "5s", ms: 5000 },
    { label: "10s", ms: 10000 },
    { label: "30s", ms: 30000 },
    { label: "60s", ms: 60000 },
  ] as const;

  let selectedWindow = persistentStore("perf-window", 0);
  let selectedInterval = persistentStore("perf-refresh-interval", 0);
  let sysData = $state<SysStat[]>([]);
  let gpuData = $state<GpuStat[]>([]);
  let refreshing = $state(false);

  let pollTimer: ReturnType<typeof setInterval> | null = null;
  let visible = $state(true);
  let mounted = $state(false);

  function cutoffTime(): number {
    return Date.now() - WINDOWS[$selectedWindow].ms;
  }

  function formatDelta(ts: string, refTime: number): string {
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

  const sysLabels = $derived.by(() => {
    const stats = filteredSysStats;
    if (stats.length === 0) return [];
    const refTime = new Date(stats[stats.length - 1].timestamp).getTime();
    return stats.map((s) => formatDelta(s.timestamp, refTime));
  });

  async function loadAll() {
    const resp = await fetchPerformance();
    if (resp) {
      sysData = resp.sys_stats ?? [];
      gpuData = resp.gpu_stats ?? [];
    }
  }

  async function loadIncremental() {
    const lastTs = sysData.length > 0 ? sysData[sysData.length - 1].timestamp : undefined;
    const resp = await fetchPerformance(lastTs);
    if (resp) {
      const newSys = resp.sys_stats ?? [];
      const newGpu = resp.gpu_stats ?? [];
      if (newSys.length > 0) {
        sysData = [...sysData, ...newSys];
      }
      if (newGpu.length > 0) {
        gpuData = [...gpuData, ...newGpu];
      }
    }
  }

  function startPolling() {
    stopPolling();
    const ms = INTERVALS[$selectedInterval].ms;
    if (ms <= 0) return;
    pollTimer = setInterval(() => {
      if (visible) {
        loadIncremental();
      }
    }, ms);
  }

  function stopPolling() {
    if (pollTimer) {
      clearInterval(pollTimer);
      pollTimer = null;
    }
  }

  function handleVisibility() {
    visible = !document.hidden;
    if (visible && mounted) {
      loadAll().then(() => startPolling());
    } else {
      stopPolling();
    }
  }

  function handleIntervalChange(i: number) {
    $selectedInterval = i;
    if (visible && mounted) {
      startPolling();
    }
  }

  async function manualRefresh() {
    refreshing = true;
    await loadIncremental();
    refreshing = false;
  }

  $effect(() => {
    return () => {
      stopPolling();
    };
  });

  onMount(() => {
    mounted = true;
    document.addEventListener("visibilitychange", handleVisibility);
    loadAll().then(() => startPolling());

    return () => {
      mounted = false;
      stopPolling();
      document.removeEventListener("visibilitychange", handleVisibility);
    };
  });

  // --- System charts (filtered by time window) ---

  const filteredSysStats = $derived(sysData.filter((s) => new Date(s.timestamp).getTime() >= cutoffTime()));

  const cpuDatasets = $derived.by(() => {
    const stats = filteredSysStats;
    if (stats.length === 0) return [];
    const coreCount = stats[0].cpu_util_per_core.length;
    const datasets = [];
    for (let i = 0; i < coreCount; i++) {
      datasets.push({
        label: `Core ${i}`,
        data: stats.map((s) => s.cpu_util_per_core[i]),
        borderColor: COLORS[i % COLORS.length],
      });
    }
    return datasets;
  });

  const memSwapDatasets = $derived.by(() => {
    const stats = filteredSysStats;
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
  });

  const latestMemSwap = $derived.by(() => {
    const stats = filteredSysStats;
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
  });

  const loadDatasets = $derived.by(() => {
    const stats = filteredSysStats;
    if (stats.length === 0) return [];
    return [
      {
        label: "1 min",
        data: stats.map((s) => s.load_avg_1),
        borderColor: "#10b981",
      },
      {
        label: "5 min",
        data: stats.map((s) => s.load_avg_5),
        borderColor: "#f59e0b",
      },
      {
        label: "15 min",
        data: stats.map((s) => s.load_avg_15),
        borderColor: "#ef4444",
      },
    ];
  });

  const netBandwidthDatasets = $derived.by(() => {
    const stats = filteredSysStats;
    if (stats.length < 2) return [];

    const ifaceNames = new Set<string>();
    for (const s of stats) {
      for (const n of s.net_io ?? []) {
        ifaceNames.add(n.name);
      }
    }

    const interfaces = [...ifaceNames].sort();
    if (interfaces.length === 0) return [];

    const datasets: { label: string; data: number[]; borderColor: string }[] = [];
    let colorIdx = 0;

    for (const iface of interfaces) {
      const recvData: number[] = [];
      const sentData: number[] = [];

      for (let i = 1; i < stats.length; i++) {
        const prev = stats[i - 1];
        const curr = stats[i];
        const prevIO = (prev.net_io ?? []).find((n) => n.name === iface);
        const currIO = (curr.net_io ?? []).find((n) => n.name === iface);

        if (!prevIO || !currIO) {
          recvData.push(0);
          sentData.push(0);
          continue;
        }

        const dtMs = new Date(curr.timestamp).getTime() - new Date(prev.timestamp).getTime();
        if (dtMs <= 0) {
          recvData.push(0);
          sentData.push(0);
          continue;
        }

        const dtSec = dtMs / 1000;
        recvData.push((((currIO.bytes_recv - prevIO.bytes_recv) / dtSec) * 8) / 1_000_000);
        sentData.push((((currIO.bytes_sent - prevIO.bytes_sent) / dtSec) * 8) / 1_000_000);
      }

      datasets.push({
        label: `${iface} in`,
        data: recvData,
        borderColor: COLORS[colorIdx % COLORS.length],
      });
      colorIdx++;
      datasets.push({
        label: `${iface} out`,
        data: sentData,
        borderColor: COLORS[colorIdx % COLORS.length],
      });
      colorIdx++;
    }

    return datasets;
  });

  const netBandwidthLabels = $derived.by(() => {
    const stats = filteredSysStats;
    if (stats.length < 2) return [];
    const refTime = new Date(stats[stats.length - 1].timestamp).getTime();
    return stats.slice(1).map((s) => formatDelta(s.timestamp, refTime));
  });

  // --- GPU charts (filtered by time window) ---

  const filteredGpuStats = $derived(gpuData.filter((g) => new Date(g.timestamp).getTime() >= cutoffTime()));

  const hasGpuData = $derived(gpuData.length > 0);

  const gpuTimestamps = $derived.by(() => {
    const seen = new Set<string>();
    const timestamps: string[] = [];
    const stats = filteredGpuStats;
    for (const g of stats) {
      if (!seen.has(g.timestamp)) {
        seen.add(g.timestamp);
        timestamps.push(g.timestamp);
      }
    }
    return timestamps;
  });

  const gpuLabels = $derived.by(() => {
    if (gpuTimestamps.length === 0) return [];
    const refTime = new Date(gpuTimestamps[gpuTimestamps.length - 1]).getTime();
    return gpuTimestamps.map((timestamp) => formatDelta(timestamp, refTime));
  });

  function gpuIdentity(gpu: GpuStat): string {
    return gpu.uuid || String(gpu.id);
  }

  function buildGpuDatasets(
    stats: GpuStat[],
    field: keyof Pick<GpuStat, "gpu_util_pct" | "mem_util_pct" | "temp_c" | "vram_temp_c" | "power_draw_w">,
  ) {
    if (stats.length === 0) return [];
    const timestamps = gpuTimestamps;
    const indexByTimestamp = new Map(timestamps.map((timestamp, index) => [timestamp, index]));
    const byGpu = new Map<string, { id: number; name: string; values: Array<number | null> }>();

    for (const g of stats) {
      const key = gpuIdentity(g);
      if (!byGpu.has(key)) {
        byGpu.set(key, { id: g.id, name: g.name, values: Array(timestamps.length).fill(null) });
      }
      const index = indexByTimestamp.get(g.timestamp);
      if (index !== undefined) byGpu.get(key)!.values[index] = g[field] as number;
    }

    const datasets = [];
    let colorIdx = 0;
    for (const entry of byGpu.values()) {
      datasets.push({
        label: entry.name || `GPU ${entry.id}`,
        data: entry.values,
        borderColor: COLORS[colorIdx % COLORS.length],
      });
      colorIdx++;
    }
    return datasets;
  }

  function buildGpuUtilDatasets(stats: GpuStat[]) {
    if (stats.length === 0) return [];
    const timestamps = gpuTimestamps;
    const indexByTimestamp = new Map(timestamps.map((timestamp, index) => [timestamp, index]));
    const fields: Array<{ key: keyof Pick<GpuStat, "gpu_util_pct" | "compute_util_pct" | "render_util_pct" | "copy_util_pct">; label: string }> = [
      { key: "gpu_util_pct", label: "GPU" },
      { key: "compute_util_pct", label: "Compute" },
      { key: "render_util_pct", label: "Render" },
      { key: "copy_util_pct", label: "Copy" },
    ];
    const byGpu = new Map<string, { id: number; name: string; values: Map<string, Array<number | null>> }>();

    for (const g of stats) {
      const identity = gpuIdentity(g);
      if (!byGpu.has(identity)) {
        byGpu.set(identity, {
          id: g.id,
          name: g.name,
          values: new Map(fields.map((field) => [field.key, Array(timestamps.length).fill(null)])),
        });
      }
      const index = indexByTimestamp.get(g.timestamp);
      if (index === undefined) continue;
      const entry = byGpu.get(identity)!;
      for (const field of fields) {
        const value = g[field.key];
        if (typeof value === "number") entry.values.get(field.key)![index] = value;
      }
    }

    const datasets = [];
    let colorIdx = 0;
    for (const entry of byGpu.values()) {
      for (const field of fields) {
        const values = entry.values.get(field.key)!;
        if (values.every((value) => value === null)) continue;
        datasets.push({
          label: `${entry.name || `GPU ${entry.id}`} ${field.label}`,
          data: values,
          borderColor: COLORS[colorIdx % COLORS.length],
        });
        colorIdx++;
      }
    }
    return datasets;
  }

  const gpuUtilDatasets = $derived(buildGpuUtilDatasets(filteredGpuStats));
  const gpuMemDatasets = $derived(buildGpuDatasets(filteredGpuStats, "mem_util_pct"));
  const gpuTempDatasets = $derived(buildGpuDatasets(filteredGpuStats, "temp_c"));
  const gpuVramTempDatasets = $derived(buildGpuDatasets(filteredGpuStats, "vram_temp_c"));
  const gpuPowerDatasets = $derived(buildGpuDatasets(filteredGpuStats, "power_draw_w"));
  const hasVramTemp = $derived(filteredGpuStats.some((g) => g.vram_temp_c > 0));
  const advancedGpuIO = $derived(buildAdvancedGpuIO(filteredGpuStats, gpuTimestamps));
  const hasAdvancedGpuIOData = $derived(hasAdvancedGpuIO(filteredGpuStats));

  function colorizeGpuDatasets(datasets: Array<{ label: string; data: Array<number | null> }>) {
    return datasets.map((dataset, index) => ({
      ...dataset,
      borderColor: COLORS[index % COLORS.length],
    }));
  }

  const memoryBandwidthDatasets = $derived(colorizeGpuDatasets(advancedGpuIO.memoryBandwidthDatasets));
  const pcieDatasets = $derived(colorizeGpuDatasets(advancedGpuIO.pcieDatasets));
  const graphicsClockDatasets = $derived(colorizeGpuDatasets(advancedGpuIO.graphicsClockDatasets));
</script>

<div class="space-y-6">
  <div class="flex items-center justify-between">
    <h2 class="text-xl font-semibold text-foreground">Performance (Experimental)</h2>
    <div class="flex items-center gap-4">
      <SegmentedControl items={WINDOWS} selected={$selectedWindow} onSelect={(i) => ($selectedWindow = i)} />
      <SegmentedControl
        items={INTERVALS}
        selected={$selectedInterval}
        onSelect={handleIntervalChange}
        label="Refresh:"
      />
      <Button variant="outline" size="icon-sm" title="Refresh" onclick={manualRefresh} disabled={refreshing}>
        <RefreshCw class={refreshing ? "animate-spin" : ""} />
      </Button>
    </div>
  </div>
  <p class="text-sm text-muted-foreground">
    This is an experimental feature. Please use <a
      class="underline hover:text-foreground"
      href="https://github.com/mostlygeek/llama-swap/discussions/771">discussion #771</a
    > for instructions and to share feedback.
  </p>

  <!-- GPU Section -->
  <section class="space-y-4">
    <h3 class="text-lg font-medium text-foreground">GPU</h3>
    {#if !hasGpuData}
      <Card.Root class="py-0">
        <Card.Content class="p-4">
          <p class="text-muted-foreground">No GPU data available</p>
        </Card.Content>
      </Card.Root>
    {:else}
      <div class="grid grid-cols-1 lg:grid-cols-2 gap-4">
        <PerformanceChart
          title="GPU Utilization (%)"
          labels={gpuLabels}
          datasets={gpuUtilDatasets}
          yMin={0}
          yMax={100}
          yLabel="%"
        />
        <PerformanceChart
          title="GPU Memory Utilization (%)"
          labels={gpuLabels}
          datasets={gpuMemDatasets}
          yMin={0}
          yMax={100}
          yLabel="%"
        />
        <PerformanceChart
          title="GPU Temperature (°C)"
          labels={gpuLabels}
          datasets={gpuTempDatasets}
          yMin={0}
          yLabel="°C"
        />
        {#if hasVramTemp}
          <PerformanceChart
            title="GPU VRAM Temperature (°C)"
            labels={gpuLabels}
            datasets={gpuVramTempDatasets}
            yMin={0}
            yLabel="°C"
          />
        {/if}
        <PerformanceChart
          title="GPU Power Draw (W)"
          labels={gpuLabels}
          datasets={gpuPowerDatasets}
          yMin={0}
          yLabel="W"
        />
      </div>
    {/if}
  </section>

  {#if hasAdvancedGpuIOData}
    <section class="space-y-4">
      <h3 class="text-lg font-medium text-foreground">Advanced GPU I/O</h3>
      <div class="grid grid-cols-1 lg:grid-cols-2 gap-4">
        {#if memoryBandwidthDatasets.length > 0}
          <div>
            <PerformanceChart
              title="Memory Bandwidth"
              labels={gpuLabels}
              datasets={memoryBandwidthDatasets}
              yMin={0}
              yLabel="B/s"
              valueFormatter={formatBytesPerSecond}
            />
            {#if advancedGpuIO.memoryBandwidthUtilization.length > 0}
              <div class="flex flex-wrap items-center justify-center gap-4 text-xs text-muted-foreground mt-1 px-4">
                {#each advancedGpuIO.memoryBandwidthUtilization as reference}
                  <span
                    >{reference.name || `GPU ${reference.id}`} bandwidth: <span class="text-foreground font-medium"
                      >{reference.value.toFixed(1)}%</span
                    ></span
                  >
                {/each}
              </div>
            {/if}
          </div>
        {/if}
        {#if pcieDatasets.length > 0}
          <PerformanceChart
            title="PCIe Throughput"
            labels={gpuLabels}
            datasets={pcieDatasets}
            yMin={0}
            yLabel="B/s"
            valueFormatter={formatBytesPerSecond}
          />
        {/if}
        {#if graphicsClockDatasets.length > 0}
          <div>
            <PerformanceChart
              title="Graphics Clock"
              labels={gpuLabels}
              datasets={graphicsClockDatasets}
              yMin={0}
              yLabel="MHz"
            />
            {#if advancedGpuIO.graphicsClockMaximums.length > 0}
              <div class="flex flex-wrap items-center justify-center gap-4 text-xs text-muted-foreground mt-1 px-4">
                {#each advancedGpuIO.graphicsClockMaximums as reference}
                  <span
                    >{reference.name || `GPU ${reference.id}`} max: <span class="text-foreground font-medium"
                      >{reference.value.toLocaleString()} MHz</span
                    ></span
                  >
                {/each}
              </div>
            {/if}
          </div>
        {/if}
      </div>
    </section>
  {/if}

  <!-- System Section -->
  <section class="space-y-4">
    <h3 class="text-lg font-medium text-foreground">System</h3>
    <div class="grid grid-cols-1 lg:grid-cols-2 gap-4">
      <PerformanceChart
        title="CPU Utilization (%)"
        labels={sysLabels}
        datasets={cpuDatasets}
        yMin={0}
        yMax={100}
        yLabel="%"
        showLegend={false}
      />
      <div>
        <PerformanceChart
          title="Memory & Swap Usage (%)"
          labels={sysLabels}
          datasets={memSwapDatasets}
          yMin={0}
          yMax={100}
          yLabel="%"
        />
        {#if latestMemSwap}
          <div class="flex items-center justify-center gap-4 text-xs text-muted-foreground mt-1 px-4">
            <span
              >Mem: <span class="text-foreground font-medium"
                >{latestMemSwap.mem_used_mb.toLocaleString()} / {latestMemSwap.mem_total_mb.toLocaleString()} MB ({latestMemSwap.mem_used_pct}%)</span
              ></span
            >
            {#if latestMemSwap.swap_used_pct !== null}
              <span
                >Swap: <span class="text-foreground font-medium"
                  >{latestMemSwap.swap_used_mb.toLocaleString()} / {latestMemSwap.swap_total_mb.toLocaleString()} MB ({latestMemSwap.swap_used_pct}%)</span
                ></span
              >
            {/if}
          </div>
        {/if}
      </div>
      <PerformanceChart title="Load Average" labels={sysLabels} datasets={loadDatasets} yMin={0} />
      {#if netBandwidthDatasets.length > 0}
        <PerformanceChart
          title="Network Bandwidth (Mbit/s)"
          labels={netBandwidthLabels}
          datasets={netBandwidthDatasets}
          yMin={0}
          yLabel="Mbit/s"
          showLegend={false}
        />
      {/if}
    </div>
  </section>
</div>
