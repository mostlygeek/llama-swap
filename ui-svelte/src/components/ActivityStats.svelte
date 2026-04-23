<script lang="ts">
  import { inFlightRequests, metrics } from "../stores/api";
  import { persistentStore } from "../stores/persistent";
  import { calculateHistogramData } from "../lib/histogram";
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
    {#if stats.histogramData}
      <TokenHistogram data={stats.histogramData} />
    {:else}
      <div class="px-4 py-6 text-center text-sm text-gray-500 dark:text-gray-400">
        No token speed data yet
      </div>
    {/if}
  {/if}
  <div class="grid grid-cols-3 gap-x-6 gap-y-1 px-4 pb-3 text-sm">
    <div class="text-xs uppercase tracking-wider text-gray-500 dark:text-gray-400">Requests</div>
    <div class="text-xs uppercase tracking-wider text-gray-500 dark:text-gray-400">Processed</div>
    <div class="text-xs uppercase tracking-wider text-gray-500 dark:text-gray-400">Generated</div>
    <div class="text-sm text-gray-700 dark:text-gray-300">
      <span class="font-semibold">{nf.format(stats.totalRequests)}</span> completed,
      <span class="font-semibold">{nf.format(stats.inFlightRequests)}</span> waiting
    </div>
    <div class="text-sm text-gray-700 dark:text-gray-300">
      <span class="font-semibold">{nf.format(stats.totalInputTokens)}</span> tokens
    </div>
    <div class="text-sm text-gray-700 dark:text-gray-300">
      <span class="font-semibold">{nf.format(stats.totalOutputTokens)}</span> tokens
    </div>
  </div>
</div>
