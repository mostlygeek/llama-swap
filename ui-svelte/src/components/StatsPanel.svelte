<script lang="ts">
  import { inFlightRequests, metrics } from "../stores/api";
  import TokenHistogram from "./TokenHistogram.svelte";
  import { buildHistogramData } from "../lib/histogram";

  let stats = $derived.by(() => {
    const totalRequests = $metrics.length;
    if (totalRequests === 0) {
      return {
        totalRequests: 0,
        totalInputTokens: 0,
        totalOutputTokens: 0,
        inFlightRequests: $inFlightRequests,
        tokenStats: { p99: "0", p95: "0", p50: "0" },
        histogramData: null,
      };
    }

    const totalInputTokens = $metrics.reduce((sum, m) => sum + m.input_tokens, 0);
    const totalOutputTokens = $metrics.reduce((sum, m) => sum + m.output_tokens, 0);

    const histogramData = buildHistogramData($metrics);
    if (histogramData === null) {
      return {
        totalRequests,
        totalInputTokens,
        totalOutputTokens,
        inFlightRequests: $inFlightRequests,
        tokenStats: { p99: "0", p95: "0", p50: "0" },
        histogramData: null,
      };
    }

    return {
      totalRequests,
      totalInputTokens,
      totalOutputTokens,
      inFlightRequests: $inFlightRequests,
      tokenStats: {
        p99: histogramData.p99.toFixed(2),
        p95: histogramData.p95.toFixed(2),
        p50: histogramData.p50.toFixed(2),
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
          <td class="px-4 py-4 text-sm font-semibold text-gray-900 dark:text-white">
            <div class="flex flex-col gap-1">
              <span class="text-xs font-medium text-gray-500 dark:text-gray-400">Completed: {nf.format(stats.totalRequests)}</span>
              <span class="text-xs font-medium text-gray-500 dark:text-gray-400">Waiting: {nf.format(stats.inFlightRequests)}</span>
            </div>
          </td>

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
