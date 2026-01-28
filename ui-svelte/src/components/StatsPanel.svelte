<script lang="ts">
  import { metrics } from "../stores/api";
  import TokenHistogram from "./TokenHistogram.svelte";

  interface HistogramData {
    bins: number[];
    min: number;
    max: number;
    binSize: number;
    p99: number;
    p95: number;
    p50: number;
  }

  let stats = $derived.by(() => {
    const totalRequests = $metrics.length;
    if (totalRequests === 0) {
      return { totalRequests: 0, totalInputTokens: 0, totalOutputTokens: 0, tokenStats: { p99: "0", p95: "0", p50: "0" }, histogramData: null };
    }

    const totalInputTokens = $metrics.reduce((sum, m) => sum + m.input_tokens, 0);
    const totalOutputTokens = $metrics.reduce((sum, m) => sum + m.output_tokens, 0);

    // Calculate token statistics using output_tokens and duration_ms
    const validMetrics = $metrics.filter((m) => m.duration_ms > 0 && m.output_tokens > 0);
    if (validMetrics.length === 0) {
      return { totalRequests, totalInputTokens, totalOutputTokens, tokenStats: { p99: "0", p95: "0", p50: "0" }, histogramData: null };
    }

    // Calculate tokens/second for each valid metric
    const tokensPerSecond = validMetrics.map((m) => m.output_tokens / (m.duration_ms / 1000));

    // Sort for percentile calculation
    const sortedTokensPerSecond = [...tokensPerSecond].sort((a, b) => a - b);

    const p99 = sortedTokensPerSecond[Math.floor(sortedTokensPerSecond.length * 0.99)];
    const p95 = sortedTokensPerSecond[Math.floor(sortedTokensPerSecond.length * 0.95)];
    const p50 = sortedTokensPerSecond[Math.floor(sortedTokensPerSecond.length * 0.5)];

    // Create histogram data
    const min = Math.min(...tokensPerSecond);
    const max = Math.max(...tokensPerSecond);
    const binCount = Math.min(30, Math.max(10, Math.floor(tokensPerSecond.length / 5)));
    const binSize = (max - min) / binCount;

    const bins = Array(binCount).fill(0);
    tokensPerSecond.forEach((value) => {
      const binIndex = Math.min(Math.floor((value - min) / binSize), binCount - 1);
      bins[binIndex]++;
    });

    const histogramData: HistogramData = {
      bins,
      min,
      max,
      binSize,
      p99,
      p95,
      p50,
    };

    return {
      totalRequests,
      totalInputTokens,
      totalOutputTokens,
      tokenStats: {
        p99: p99.toFixed(2),
        p95: p95.toFixed(2),
        p50: p50.toFixed(2),
      },
      histogramData,
    };
  });

  const nf = new Intl.NumberFormat();
</script>

<div class="card">
  <div class="rounded-lg overflow-hidden border border-card-border-inner">
    <table class="min-w-full divide-y divide-card-border-inner">
      <thead class="bg-secondary">
        <tr>
          <th class="px-4 py-3 text-left text-xs font-semibold uppercase tracking-wider text-txtmain">Requests</th>
          <th class="px-4 py-3 text-left text-xs font-semibold uppercase tracking-wider text-txtmain border-l border-card-border-inner">
            Processed
          </th>
          <th class="px-4 py-3 text-left text-xs font-semibold uppercase tracking-wider text-txtmain border-l border-card-border-inner">
            Generated
          </th>
          <th class="px-4 py-3 text-left text-xs font-semibold uppercase tracking-wider text-txtmain border-l border-card-border-inner">
            Token Stats (tokens/sec)
          </th>
        </tr>
      </thead>

      <tbody class="bg-surface divide-y divide-card-border-inner">
        <tr class="hover:bg-secondary">
          <td class="px-4 py-4 text-sm font-semibold text-gray-900 dark:text-white">{stats.totalRequests}</td>

          <td class="px-4 py-4 text-sm text-gray-700 dark:text-gray-300 border-l border-gray-200 dark:border-white/10">
            <div class="flex items-center gap-2">
              <span class="text-sm font-medium">{nf.format(stats.totalInputTokens)}</span>
              <span class="text-xs text-gray-500 dark:text-gray-400">tokens</span>
            </div>
          </td>

          <td class="px-4 py-4 text-sm text-gray-700 dark:text-gray-300 border-l border-gray-200 dark:border-white/10">
            <div class="flex items-center gap-2">
              <span class="text-sm font-medium">{nf.format(stats.totalOutputTokens)}</span>
              <span class="text-xs text-gray-500 dark:text-gray-400">tokens</span>
            </div>
          </td>

          <td class="px-4 py-4 border-l border-gray-200 dark:border-white/10">
            <div class="space-y-3">
              <div class="grid grid-cols-3 gap-2 items-center">
                <div class="text-center">
                  <div class="text-xs text-gray-500 dark:text-gray-400">P50</div>
                  <div class="mt-1 inline-block rounded-full bg-gray-100 dark:bg-white/5 px-3 py-1 text-sm font-semibold text-gray-800 dark:text-white">
                    {stats.tokenStats.p50}
                  </div>
                </div>

                <div class="text-center">
                  <div class="text-xs text-gray-500 dark:text-gray-400">P95</div>
                  <div class="mt-1 inline-block rounded-full bg-gray-100 dark:bg-white/5 px-3 py-1 text-sm font-semibold text-gray-800 dark:text-white">
                    {stats.tokenStats.p95}
                  </div>
                </div>

                <div class="text-center">
                  <div class="text-xs text-gray-500 dark:text-gray-400">P99</div>
                  <div class="mt-1 inline-block rounded-full bg-gray-100 dark:bg-white/5 px-3 py-1 text-sm font-semibold text-gray-800 dark:text-white">
                    {stats.tokenStats.p99}
                  </div>
                </div>
              </div>
              {#if stats.histogramData}
                <TokenHistogram data={stats.histogramData} />
              {/if}
            </div>
          </td>
        </tr>
      </tbody>
    </table>
  </div>
</div>
