<script lang="ts">
  import { inFlightRequests, metrics } from "../stores/api";
  import { calculateHistogramData } from "../lib/histogram";
  import type { HistogramData } from "../lib/types";
  import TokenHistogram from "./TokenHistogram.svelte";

  const nf = new Intl.NumberFormat();

  let stats = $derived.by(() => {
    const totalRequests = $metrics.length;
    const totalInputTokens = $metrics.reduce((sum, m) => sum + m.input_tokens, 0);
    const totalOutputTokens = $metrics.reduce((sum, m) => sum + m.output_tokens, 0);

    const tokensPerSecond = $metrics
      .filter((m) => m.tokens_per_second > 0)
      .map((m) => m.tokens_per_second);

    const histogramData = tokensPerSecond.length > 0 ? calculateHistogramData(tokensPerSecond) : null;

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
    <TokenHistogram data={stats.histogramData} />
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
