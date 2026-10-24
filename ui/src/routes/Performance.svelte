<script lang="ts">
  import { onMount } from "svelte";
  import { apiUrl } from "../lib/apiPath";
  import { persistentStore } from "../stores/persistent";
  import {
    PerfHistory,
    chartAxis,
    onAxis,
    hasGpuField,
    lastValue,
    type GpuField,
    type PerfMeta,
    type PerfState,
    type PerfSysMessage,
    type PerfGpuMessage,
  } from "../lib/perfStream";
  import PerformanceChart from "../components/PerformanceChart.svelte";
  import SegmentedControl from "../components/SegmentedControl.svelte";
  import * as Card from "$lib/components/ui/card/index.js";

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
    { label: "5 min", seconds: 5 * 60 },
    { label: "15 min", seconds: 15 * 60 },
    { label: "1 hr", seconds: 60 * 60 },
  ] as const;

  let selectedWindow = persistentStore("perf-window", 0);

  // PerfHistory is a plain object; bumping revision re-runs the charts.
  const history = new PerfHistory();
  let revision = $state(0);
  let connected = $state(false);
  let source: EventSource | null = null;

  function connect() {
    if (source) return;
    const es = new EventSource(apiUrl("/api/events/performance"));
    source = es;
    // the server resends the last hour on every (re)connect
    es.onopen = () => {
      history.reset();
      connected = true;
      revision++;
    };
    es.onerror = () => {
      connected = false;
    };
    const on = <T,>(name: string, apply: (data: T) => void) => {
      es.addEventListener(name, (e) => {
        apply(JSON.parse((e as MessageEvent).data) as T);
        revision++;
      });
    };
    on<PerfMeta>("meta", (m) => history.setMeta(m));
    on<PerfState>("state", (s) => history.setState(s));
    on<PerfSysMessage>("sys", (m) => history.addSys(m));
    on<PerfGpuMessage>("gpu", (m) => history.addGpu(m));
  }

  // An open stream counts as a viewer, which lets poll: active read. Closing
  // it while the tab is hidden lets idle GPUs sleep.
  function disconnect() {
    source?.close();
    source = null;
    connected = false;
  }

  function handleVisibility() {
    if (document.hidden) disconnect();
    else connect();
  }

  onMount(() => {
    connect();
    document.addEventListener("visibilitychange", handleVisibility);
    return () => {
      document.removeEventListener("visibilitychange", handleVisibility);
      disconnect();
    };
  });

  const windowSeconds = $derived(WINDOWS[$selectedWindow].seconds);
  const view = $derived.by(() => {
    void revision;
    return {
      meta: history.meta,
      state: history.state,
      sys: history.sys,
      gpu: history.gpu,
    };
  });

  // --- System charts ---

  const sysAxis = $derived(chartAxis(view.sys.t, view.meta.every.system ?? 5, windowSeconds));

  const cpuDatasets = $derived(
    view.sys.cpu.map((series, i) => ({
      label: `Core ${i}`,
      data: onAxis(series, sysAxis),
      borderColor: COLORS[i % COLORS.length],
    })),
  );

  const memSwapDatasets = $derived.by(() => {
    const s = view.sys;
    const pct = (used: number[], total: number[]) => used.map((u, i) => (total[i] > 0 ? (u / total[i]) * 100 : null));
    return [
      { label: "Memory Used %", data: onAxis(pct(s.memUsed, s.memTotal), sysAxis), borderColor: "#3b82f6" },
      { label: "Swap Used %", data: onAxis(pct(s.swapUsed, s.swapTotal), sysAxis), borderColor: "#8b5cf6" },
    ];
  });

  const latestMemSwap = $derived.by(() => {
    const s = view.sys;
    const i = s.t.length - 1;
    if (i < 0) return null;
    return {
      memUsed: s.memUsed[i],
      memTotal: s.memTotal[i],
      memPct: s.memTotal[i] > 0 ? ((s.memUsed[i] / s.memTotal[i]) * 100).toFixed(1) : "0",
      swapUsed: s.swapUsed[i],
      swapTotal: s.swapTotal[i],
      swapPct: s.swapTotal[i] > 0 ? ((s.swapUsed[i] / s.swapTotal[i]) * 100).toFixed(1) : null,
    };
  });

  const loadDatasets = $derived([
    { label: "1 min", data: onAxis(view.sys.load1, sysAxis), borderColor: "#10b981" },
    { label: "5 min", data: onAxis(view.sys.load5, sysAxis), borderColor: "#f59e0b" },
    { label: "15 min", data: onAxis(view.sys.load15, sysAxis), borderColor: "#ef4444" },
  ]);

  // Mbit/s between consecutive readings; null across a paused gap.
  function rates(t: number[], counter: number[], every: number): (number | null)[] {
    return counter.map((v, i) => {
      if (i === 0) return null;
      const dt = t[i] - t[i - 1];
      if (dt <= 0 || dt > 2 * every || v < counter[i - 1]) return null;
      return (((v - counter[i - 1]) / dt) * 8) / 1_000_000;
    });
  }

  const netDatasets = $derived.by(() => {
    const s = view.sys;
    const every = view.meta.every.system ?? 5;
    const datasets: { label: string; data: (number | null)[]; borderColor: string }[] = [];
    let color = 0;
    for (const iface of Object.keys(s.net).sort()) {
      datasets.push({
        label: `${iface} in`,
        data: onAxis(rates(s.t, s.net[iface].recv, every), sysAxis),
        borderColor: COLORS[color++ % COLORS.length],
      });
      datasets.push({
        label: `${iface} out`,
        data: onAxis(rates(s.t, s.net[iface].sent, every), sysAxis),
        borderColor: COLORS[color++ % COLORS.length],
      });
    }
    return datasets;
  });

  // --- GPU charts ---

  const gpuAxis = $derived(chartAxis(view.gpu.t, view.meta.every.gpu ?? 5, windowSeconds));

  // Two identical cards get their index added so the legend tells them apart.
  const gpuNames = $derived.by(() => {
    const names = view.meta.gpus.map((g) => g.name || g.key);
    return names.map((n, i) => (names.filter((m) => m === n).length > 1 ? `${n} #${i}` : n));
  });

  function gpuDatasets(field: GpuField, scale = 1) {
    void revision;
    return view.gpu.gpus.flatMap((series, i) =>
      series[field].some((v) => v !== null)
        ? [
            {
              label: gpuNames[i] ?? `GPU ${i}`,
              data: onAxis(
                series[field].map((v) => (v === null ? null : v * scale)),
                gpuAxis,
              ),
              borderColor: COLORS[i % COLORS.length],
            },
          ]
        : [],
    );
  }

  const MB_TO_GIB = 1 / 1024;

  // VRAM is a solid line and shared memory (GTT, unified) a dashed one.
  const gpuMemDatasets = $derived.by(() => {
    void revision;
    return view.gpu.gpus.flatMap((series, i) => {
      const out = [];
      const color = COLORS[i % COLORS.length];
      const name = gpuNames[i] ?? `GPU ${i}`;
      if (series.vramUsed.some((v) => v !== null)) {
        out.push({
          label: `${name} VRAM`,
          data: onAxis(series.vramUsed.map((v) => (v === null ? null : v * MB_TO_GIB)), gpuAxis),
          borderColor: color,
        });
      }
      if (series.sharedUsed.some((v) => v !== null)) {
        out.push({
          label: `${name} shared`,
          data: onAxis(series.sharedUsed.map((v) => (v === null ? null : v * MB_TO_GIB)), gpuAxis),
          borderColor: color,
          borderDash: [6, 4],
        });
      }
      return out;
    });
  });

  function gib(mb: number | null): string {
    return mb === null ? "?" : (mb / 1024).toFixed(1);
  }

  const gpuMemSummary = $derived.by(() => {
    void revision;
    return view.gpu.gpus.flatMap((series, i) => {
      const parts = [];
      const vramUsed = lastValue(series.vramUsed);
      if (vramUsed !== null) parts.push(`VRAM ${gib(vramUsed)} / ${gib(lastValue(series.vramTotal))} GiB`);
      const sharedUsed = lastValue(series.sharedUsed);
      if (sharedUsed !== null) parts.push(`shared ${gib(sharedUsed)} / ${gib(lastValue(series.sharedTotal))} GiB`);
      return parts.length ? [{ name: gpuNames[i] ?? `GPU ${i}`, text: parts.join(", ") }] : [];
    });
  });

  const gpuUtilDatasets = $derived(gpuDatasets("util"));
  const gpuTempDatasets = $derived(gpuDatasets("tempC"));
  const gpuVramTempDatasets = $derived(gpuDatasets("vramTempC"));
  const gpuPowerDatasets = $derived(gpuDatasets("powerW"));
  const gpuFanDatasets = $derived(gpuDatasets("fanPct"));

  const has = $derived.by(() => {
    void revision;
    return {
      util: hasGpuField(history, "util"),
      mem: hasGpuField(history, "vramUsed") || hasGpuField(history, "sharedUsed"),
      temp: hasGpuField(history, "tempC"),
      vramTemp: hasGpuField(history, "vramTempC"),
      power: hasGpuField(history, "powerW"),
      fan: hasGpuField(history, "fanPct"),
    };
  });
  const hasGpuData = $derived(view.gpu.t.length > 0 && view.meta.gpus.length > 0);
  const suspendedNames = $derived((view.state.suspended ?? []).map((i) => gpuNames[i] ?? `GPU ${i}`));
</script>

<div class="space-y-6">
  <div class="flex items-center justify-between">
    <h2 class="text-xl font-semibold text-foreground">Performance (Experimental)</h2>
    <div class="flex items-center gap-4">
      {#if !connected}
        <span class="text-xs text-muted-foreground">Connecting…</span>
      {/if}
      <SegmentedControl items={WINDOWS} selected={$selectedWindow} onSelect={(i) => ($selectedWindow = i)} />
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
    <div class="flex flex-wrap items-center gap-2">
      <h3 class="text-lg font-medium text-foreground">GPU</h3>
      {#if view.state.gpu === "paused"}
        <span class="rounded bg-muted px-2 py-0.5 text-xs text-muted-foreground">
          Paused: GPUs are read only while a model is loaded
        </span>
      {/if}
      {#each suspendedNames as name (name)}
        <span class="rounded bg-muted px-2 py-0.5 text-xs text-muted-foreground">{name}: suspended</span>
      {/each}
    </div>
    {#if view.state.gpu === "disabled"}
      <Card.Root class="py-0">
        <Card.Content class="p-4">
          <p class="text-muted-foreground">GPU monitoring is disabled (performance.gpu.disabled)</p>
        </Card.Content>
      </Card.Root>
    {:else if !hasGpuData}
      <Card.Root class="py-0">
        <Card.Content class="p-4">
          <p class="text-muted-foreground">No GPU data yet</p>
        </Card.Content>
      </Card.Root>
    {:else}
      <div class="grid grid-cols-1 lg:grid-cols-2 gap-4">
        {#if has.util}
          <PerformanceChart
            title="GPU Utilization (%)"
            labels={gpuAxis.labels}
            datasets={gpuUtilDatasets}
            yMin={0}
            yMax={100}
            yLabel="%"
          />
        {/if}
        {#if has.mem}
          <div>
            <PerformanceChart
              title="GPU Memory Used (GiB)"
              labels={gpuAxis.labels}
              datasets={gpuMemDatasets}
              yMin={0}
              yLabel="GiB"
            />
            <div class="flex flex-wrap items-center justify-center gap-4 text-xs text-muted-foreground mt-1 px-4">
              {#each gpuMemSummary as row (row.name)}
                <span>{row.name}: <span class="text-foreground font-medium">{row.text}</span></span>
              {/each}
            </div>
          </div>
        {/if}
        {#if has.temp}
          <PerformanceChart
            title="GPU Temperature (°C)"
            labels={gpuAxis.labels}
            datasets={gpuTempDatasets}
            yMin={0}
            yLabel="°C"
          />
        {/if}
        {#if has.vramTemp}
          <PerformanceChart
            title="GPU VRAM Temperature (°C)"
            labels={gpuAxis.labels}
            datasets={gpuVramTempDatasets}
            yMin={0}
            yLabel="°C"
          />
        {/if}
        {#if has.power}
          <PerformanceChart
            title="GPU Power Draw (W)"
            labels={gpuAxis.labels}
            datasets={gpuPowerDatasets}
            yMin={0}
            yLabel="W"
          />
        {/if}
        {#if has.fan}
          <PerformanceChart
            title="GPU Fan Speed (%)"
            labels={gpuAxis.labels}
            datasets={gpuFanDatasets}
            yMin={0}
            yMax={100}
            yLabel="%"
          />
        {/if}
      </div>
    {/if}
  </section>

  <!-- System Section -->
  <section class="space-y-4">
    <div class="flex flex-wrap items-center gap-2">
      <h3 class="text-lg font-medium text-foreground">System</h3>
      {#if view.state.system === "paused"}
        <span class="rounded bg-muted px-2 py-0.5 text-xs text-muted-foreground">
          Paused: system stats are read only while a model is loaded
        </span>
      {/if}
    </div>
    {#if view.state.system === "disabled"}
      <Card.Root class="py-0">
        <Card.Content class="p-4">
          <p class="text-muted-foreground">System monitoring is disabled (performance.system.disabled)</p>
        </Card.Content>
      </Card.Root>
    {:else}
      <div class="grid grid-cols-1 lg:grid-cols-2 gap-4">
        <PerformanceChart
          title="CPU Utilization (%)"
          labels={sysAxis.labels}
          datasets={cpuDatasets}
          yMin={0}
          yMax={100}
          yLabel="%"
          showLegend={false}
        />
        <div>
          <PerformanceChart
            title="Memory & Swap Usage (%)"
            labels={sysAxis.labels}
            datasets={memSwapDatasets}
            yMin={0}
            yMax={100}
            yLabel="%"
          />
          {#if latestMemSwap}
            <div class="flex items-center justify-center gap-4 text-xs text-muted-foreground mt-1 px-4">
              <span
                >Mem: <span class="text-foreground font-medium"
                  >{latestMemSwap.memUsed.toLocaleString()} / {latestMemSwap.memTotal.toLocaleString()} MB ({latestMemSwap.memPct}%)</span
                ></span
              >
              {#if latestMemSwap.swapPct !== null}
                <span
                  >Swap: <span class="text-foreground font-medium"
                    >{latestMemSwap.swapUsed.toLocaleString()} / {latestMemSwap.swapTotal.toLocaleString()} MB ({latestMemSwap.swapPct}%)</span
                  ></span
                >
              {/if}
            </div>
          {/if}
        </div>
        <PerformanceChart title="Load Average" labels={sysAxis.labels} datasets={loadDatasets} yMin={0} />
        {#if netDatasets.length > 0}
          <PerformanceChart
            title="Network Bandwidth (Mbit/s)"
            labels={sysAxis.labels}
            datasets={netDatasets}
            yMin={0}
            yLabel="Mbit/s"
            showLegend={false}
          />
        {/if}
      </div>
    {/if}
  </section>
</div>
