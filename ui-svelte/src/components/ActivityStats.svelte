<script lang="ts">
  import { inFlightRequests, metrics } from "../stores/api";
  import { persistentStore } from "../stores/persistent";
  import { calculateHistogramData } from "../lib/histogram";
  import type { HistogramData } from "../lib/types";
  import TokenHistogram from "./TokenHistogram.svelte";

  const nf = new Intl.NumberFormat();
  const histogramCollapsed = persistentStore<boolean>("activity-histogram-collapsed", false);

  let stats = $derived.by(() => {
    const totalRequests = $metrics.length;
    const totalInputTokens = $metrics.reduce((sum, m) => sum + m.input_tokens, 0);
    const totalOutputTokens = $metrics.reduce((sum, m) => sum + m.output_tokens, 0);

    const tokensPerSecond = $metrics
      .filter((m) => m.tokens_per_second > 0)
      .map((m) => m.tokens_per_second);

    const histogramData = tokensPerSecond.length > 0
      ? calculateHistogramData(tokensPerSecond, { minBins: 20, maxBins: 80, binScaling: 3 })
      : null;

    return {
      totalRequests,
      totalInputTokens,
      totalOutputTokens,
      inFlightRequests: $inFlightRequests,
      histogramData,
    };
  });
</script>

<div class="card">
  {#if stats.histogramData}
    <button
      class="flex items-center gap-1 px-4 pt-3 text-xs font-medium text-gray-500 dark:text-gray-400 hover:text-gray-700 dark:hover:text-gray-200 transition-colors"
      onclick={() => $histogramCollapsed = !$histogramCollapsed}
    >
      <svg
        class="w-3 h-3 transition-transform"
        style="transform: rotate({$histogramCollapsed ? -90 : 0}deg)"
        viewBox="0 0 16 16"
        fill="currentColor"
      >
        <path d="M4.5 6l3.5 4 3.5-4H4.5z" />
      </svg>
      Tokens/sec Distribution
    </button>
    {#if !$histogramCollapsed}
      <TokenHistogram data={stats.histogramData} />
    {/if}
  {/if}
  <div class="flex flex-wrap items-center gap-x-6 gap-y-1 px-4 pb-3 text-sm text-gray-700 dark:text-gray-300">
    <span>
      Requests: <span class="font-semibold">{nf.format(stats.totalRequests)}</span> completed,
      <span class="font-semibold">{nf.format(stats.inFlightRequests)}</span> waiting
    </span>
    <span>
      Processed: <span class="font-semibold">{nf.format(stats.totalInputTokens)}</span> tokens
    </span>
    <span>
      Generated: <span class="font-semibold">{nf.format(stats.totalOutputTokens)}</span> tokens
    </span>
  </div>
</div>
