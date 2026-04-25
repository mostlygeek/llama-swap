<script lang="ts">
  import type { HistogramData } from "../lib/types";

  let {
    data,
    unit = "tokens/sec",
    colorClass = "text-blue-500 dark:text-blue-400",
  }: {
    data: HistogramData;
    unit?: string;
    colorClass?: string;
  } = $props();

  const height = 250;
  const padding = { top: 5, right: 45, bottom: 15, left: 45 };
  const viewBoxWidth = 1200;
  const chartWidth = viewBoxWidth - padding.left - padding.right;
  const chartHeight = height - padding.top - padding.bottom;

  let maxCount = $derived(Math.max(...data.bins));
  let barWidth = $derived(chartWidth / data.bins.length);
  let range = $derived(data.max - data.min);

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
    {#each data.bins as count, i}
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

    <!-- Percentile lines -->
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

    <!-- X-axis labels -->
    <text x={padding.left} y={height - 5} font-size="10" fill="currentColor" opacity="0.6" text-anchor="start">
      {data.min.toFixed(1)}
    </text>

    <text
      x={viewBoxWidth - padding.right}
      y={height - 5}
      font-size="10"
      fill="currentColor"
      opacity="0.6"
      text-anchor="end"
    >
      {data.max.toFixed(1)}
    </text>
  </svg>
</div>
