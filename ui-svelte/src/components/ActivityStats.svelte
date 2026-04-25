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

    const promptPerSecond = $metrics.filter((m) => m.prompt_per_second > 0).map((m) => m.prompt_per_second);

    const tokensPerSecond = $metrics.filter((m) => m.tokens_per_second > 0).map((m) => m.tokens_per_second);

    const promptHistogramData =
      promptPerSecond.length > 0
        ? calculateHistogramData(promptPerSecond, { minBins: 20, maxBins: 80, binScaling: 3 })
        : null;

    const genHistogramData =
      tokensPerSecond.length > 0
        ? calculateHistogramData(tokensPerSecond, { minBins: 20, maxBins: 80, binScaling: 3 })
        : null;

    return {
      totalRequests,
      totalInputTokens,
      totalOutputTokens,
      inFlightRequests: $inFlightRequests,
      promptHistogramData,
      genHistogramData,
    };
  });
</script>

<div class="card">
  <button
    class="flex items-center gap-1 px-2 text-xs font-medium text-gray-500 dark:text-gray-400 hover:text-gray-700 dark:hover:text-gray-200 transition-colors"
    onclick={() => ($histogramCollapsed = !$histogramCollapsed)}
  >
    <h2 class="flex">
      <svg
        class="w-8 h-8 transition-transform"
        style="transform: rotate({$histogramCollapsed ? -90 : 0}deg)"
        viewBox="0 0 16 16"
        fill="currentColor"
      >
        <path d="M4.5 6l3.5 4 3.5-4H4.5z" />
      </svg>
      Distribution
    </h2>
  </button>
  {#if !$histogramCollapsed}
    <div class="flex gap-1 px-2 pb-2">
      <div class="flex-1 min-w-0">
        <div class="text-sm font-medium text-gray-500 dark:text-gray-400 mb-1">Prompt Processing</div>
        {#if stats.promptHistogramData}
          <TokenHistogram
            data={stats.promptHistogramData}
            unit="prompt tokens/sec"
            colorClass="text-amber-500 dark:text-amber-400"
          />
        {:else}
          <div class="py-6 text-center text-sm text-gray-500 dark:text-gray-400">No prompt speed data yet</div>
        {/if}
      </div>
      <div class="flex-1 min-w-0">
        <div class="text-sm font-medium text-gray-500 dark:text-gray-400 mb-1">Token Generation</div>
        {#if stats.genHistogramData}
          <TokenHistogram data={stats.genHistogramData} unit="tokens/sec" />
        {:else}
          <div class="py-6 text-center text-sm text-gray-500 dark:text-gray-400">No generation speed data yet</div>
        {/if}
      </div>
    </div>
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
  {/if}
</div>
