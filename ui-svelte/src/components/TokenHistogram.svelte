<script lang="ts">
  import type { HistogramData, StackedHistogramData } from "../lib/types";
  import { stackedHistogramColors } from "../lib/histogram";

  let {
    data,
    unit = "tokens/sec",
    colorClass = "text-blue-500 dark:text-blue-400",
    stacked = true,
  }: {
    data: HistogramData | StackedHistogramData;
    unit?: string;
    colorClass?: string;
    stacked?: boolean;
  } = $props();

  const stackedHeight = 300;
  const flatHeight = 250;
  const padding = $derived({ top: 30, right: 20, bottom: stacked ? 80 : 40, left: 75 });
  const viewBoxWidth = 1200;
  const height = $derived(stacked ? stackedHeight : flatHeight);
  const chartWidth = $derived(viewBoxWidth - padding.left - padding.right);
  const chartHeight = $derived(height - padding.top - padding.bottom);

  // Detect if data is stacked by checking bins[0]
  let isStacked = $derived(
    stacked && data.bins.length > 0 && typeof data.bins[0] === "object" && "segments" in data.bins[0]
  );

  let maxCount = $derived(
    isStacked
      ? Math.max(...(data as StackedHistogramData).bins.map((b) => b.totalCount))
      : Math.max(...(data as HistogramData).bins)
  );
  let barWidth = $derived(chartWidth / data.bins.length);
  let range = $derived(data.max - data.min);

  let legendItems = $derived.by(() => {
    if (!isStacked) return [];
    const sd = data as StackedHistogramData;
    return sd.models.map((model, i) => ({
      model,
      color: stackedHistogramColors[i % stackedHistogramColors.length],
    }));
  });

  function getModelColor(model: string): string {
    if (!isStacked) return "";
    const sd = data as StackedHistogramData;
    const idx = sd.models.indexOf(model);
    return idx >= 0
      ? stackedHistogramColors[idx % stackedHistogramColors.length]
      : "#888888";
  }

  function getXPosition(value: number): number {
    return padding.left + ((value - data.min) / range) * chartWidth;
  }
</script>

<div class="mt-2 w-full">
  <svg viewBox="0 0 {viewBoxWidth} {height}" class="w-full h-auto" preserveAspectRatio="xMidYMid meet">
    <!-- Y-axis -->
    <line
      x1={padding.left}
      y1={padding.top}
      x2={padding.left}
      y2={height - padding.bottom}
      stroke="currentColor"
      stroke-width="1"
      opacity="0.3"
    />

    <!-- Y-axis ticks and labels -->
    {#each [0, 0.5, 1] as fraction}
      {@const tickCount = Math.round(maxCount * fraction)}
      {@const tickY = height - padding.bottom - fraction * chartHeight}
      <line
        x1={padding.left - 8}
        y1={tickY}
        x2={padding.left}
        y2={tickY}
        stroke="currentColor"
        stroke-width="1"
        opacity="0.4"
      />
      <text x={padding.left - 10} y={tickY + 10} font-size="26" fill="currentColor" opacity="0.8" text-anchor="end">
        {tickCount}
      </text>
    {/each}

    <!-- X-axis -->
    <line
      x1={padding.left}
      y1={height - padding.bottom}
      x2={viewBoxWidth - padding.right}
      y2={height - padding.bottom}
      stroke="currentColor"
      stroke-width="1"
      opacity="0.3"
    />

    <!-- Histogram bars -->
    {#if isStacked}
      {#each (data as StackedHistogramData).bins as bin, i}
        {@const barHeight = maxCount > 0 ? (bin.totalCount / maxCount) * chartHeight : 0}
        {@const x = padding.left + i * barWidth}
        <g>
          {#each bin.segments as seg, j}
            {@const segFraction = bin.totalCount > 0 ? seg.count / bin.totalCount : 0}
            {@const segHeight = segFraction * barHeight}
            {@const stackedBelow = bin.segments.slice(0, j).reduce((acc, s) => {
              const frac = bin.totalCount > 0 ? s.count / bin.totalCount : 0;
              return acc + frac * barHeight;
            }, 0)}
            {@const segY = height - padding.bottom - stackedBelow - segHeight}
            {@const segColor = getModelColor(seg.model)}
            <rect
              {x}
              y={segY}
              width={Math.max(barWidth - 1, 1)}
              height={segHeight}
              fill={segColor}
              opacity="0.8"
              class="hover:opacity-100 transition-opacity cursor-pointer"
            />
          {/each}
          <title>{`${bin.binStart.toFixed(1)} - ${bin.binEnd.toFixed(1)} ${unit}\nTotal: ${bin.totalCount}\n${bin.segments.map(s => s.model + ': ' + s.count).join('\n')}`}</title>
        </g>
      {/each}
    {:else}
      {#each (data as HistogramData).bins as count, i}
        {@const barHeight = maxCount > 0 ? (count / maxCount) * chartHeight : 0}
        {@const x = padding.left + i * barWidth}
        {@const y = height - padding.bottom - barHeight}
        {@const binStart = data.min + i * data.binSize}
        {@const binEnd = binStart + data.binSize}
        <g>
          <rect
            {x}
            {y}
            width={Math.max(barWidth - 1, 1)}
            height={barHeight}
            fill="currentColor"
            opacity="0.6"
            class="{colorClass} hover:opacity-90 transition-opacity cursor-pointer"
          />
          <title>{`${binStart.toFixed(1)} - ${binEnd.toFixed(1)} ${unit}\nCount: ${count}`}</title>
        </g>
      {/each}
    {/if}

    <!-- Percentile lines -->
    {#if range > 0}
      <line
        x1={getXPosition(data.p50)}
        y1={padding.top}
        x2={getXPosition(data.p50)}
        y2={height - padding.bottom}
        stroke="currentColor"
        stroke-width="2"
        stroke-dasharray="4 2"
        opacity="0.7"
        class="text-gray-600 dark:text-gray-400"
      />

      <line
        x1={getXPosition(data.p95)}
        y1={padding.top}
        x2={getXPosition(data.p95)}
        y2={height - padding.bottom}
        stroke="currentColor"
        stroke-width="2"
        stroke-dasharray="4 2"
        opacity="0.7"
        class="text-orange-500 dark:text-orange-400"
      />

      <line
        x1={getXPosition(data.p99)}
        y1={padding.top}
        x2={getXPosition(data.p99)}
        y2={height - padding.bottom}
        stroke="currentColor"
        stroke-width="2"
        stroke-dasharray="4 2"
        opacity="0.7"
        class="text-green-500 dark:text-green-400"
      />
    {/if}

    <!-- X-axis labels -->
    <text x={padding.left} y={height - padding.bottom + 20} font-size="26" fill="currentColor" opacity="0.8" text-anchor="start">
      {data.min.toFixed(1)}
    </text>

    <text
      x={viewBoxWidth - padding.right}
      y={height - padding.bottom + 20}
      font-size="26"
      fill="currentColor"
      opacity="0.8"
      text-anchor="end"
    >
      {data.max.toFixed(1)}
    </text>

    <!-- Legend (stacked only) -->
    {#if isStacked && legendItems.length > 0}
      {@const legendY = height - 25}
      {@const legendStartX = padding.left}
      {@const legendItemWidth = Math.min(140, (chartWidth - (legendItems.length - 1) * 10) / legendItems.length)}
      {#each legendItems as item, i}
        {@const lx = legendStartX + i * (legendItemWidth + 10)}
        <rect
          x={lx}
          y={legendY}
          width="18"
          height="18"
          fill={item.color}
          rx="3"
        />
        <text
          x={lx + 23}
          y={legendY + 14}
          font-size="24"
          fill="currentColor"
          opacity="0.85"
        >
          {item.model.length > 12 ? item.model.slice(0, 11) + "…" : item.model}
        </text>
      {/each}
    {/if}
  </svg>
</div>
