<script lang="ts">
  import { metrics } from "../stores/api";
  import { persistentStore } from "../stores/persistent";
  import { calculateHistogramData } from "../lib/histogram";
  import TokenHistogram from "./TokenHistogram.svelte";
  import { ChevronDown, X } from "@lucide/svelte";
  import * as Card from "$lib/components/ui/card/index.js";
  import { Button } from "$lib/components/ui/button/index.js";

  const nf = new Intl.NumberFormat();
  const histogramCollapsed = persistentStore<boolean>("activity-histogram-collapsed", false);

  let stats = $derived.by(() => {
    const totalRequests = $metrics.length;
    const totalInputTokens = $metrics.reduce((sum, m) => sum + m.tokens.input_tokens, 0);
    const totalOutputTokens = $metrics.reduce((sum, m) => sum + m.tokens.output_tokens, 0);
    const totalCacheTokens = $metrics.reduce((sum, m) => sum + Math.max(0, m.tokens.cache_tokens), 0);

    const promptPerSecond = $metrics.filter((m) => m.tokens.prompt_per_second > 0).map((m) => m.tokens.prompt_per_second);

    const tokensPerSecond = $metrics.filter((m) => m.tokens.tokens_per_second > 0).map((m) => m.tokens.tokens_per_second);

    const promptHistogramData =
      promptPerSecond.length > 0 ? calculateHistogramData(promptPerSecond) : null;

    const genHistogramData =
      tokensPerSecond.length > 0 ? calculateHistogramData(tokensPerSecond) : null;

    return {
      totalRequests,
      totalInputTokens,
      totalOutputTokens,
      totalCacheTokens,
      promptHistogramData,
      genHistogramData,
    };
  });
</script>

<Card.Root class="relative p-3">
  <Button
    variant="ghost"
    size="icon-xs"
    class="text-muted-foreground absolute right-2 top-2 rounded-full"
    onclick={() => ($histogramCollapsed = !$histogramCollapsed)}
    title={$histogramCollapsed ? "Show histograms" : "Hide histograms"}
  >
    {#if $histogramCollapsed}
      <ChevronDown />
    {:else}
      <X />
    {/if}
  </Button>
  {#if !$histogramCollapsed}
    <div class="mb-3 flex flex-col gap-6 sm:flex-row">
      <div class="w-full min-w-0 sm:w-1/2">
        <div class="text-muted-foreground mb-1 text-sm font-medium">Prompt Processing</div>
        {#if stats.promptHistogramData}
          <TokenHistogram
            data={stats.promptHistogramData}
            unit="prompt tokens/sec"
            colorClass="text-amber-500 dark:text-amber-400"
          />
        {:else}
          <div class="text-muted-foreground py-6 text-center text-sm">No prompt speed data yet</div>
        {/if}
      </div>
      <div class="w-full min-w-0 sm:w-1/2">
        <div class="text-muted-foreground mb-1 text-sm font-medium">Token Generation</div>
        {#if stats.genHistogramData}
          <TokenHistogram data={stats.genHistogramData} unit="tokens/sec" />
        {:else}
          <div class="text-muted-foreground py-6 text-center text-sm">No generation speed data yet</div>
        {/if}
      </div>
    </div>
  {/if}
  <div class="grid grid-cols-4 gap-x-6 gap-y-1 text-sm">
    <div class="text-muted-foreground text-xs uppercase tracking-wider">Requests</div>
    <div class="text-muted-foreground text-xs uppercase tracking-wider">Cached</div>
    <div class="text-muted-foreground text-xs uppercase tracking-wider">Processed</div>
    <div class="text-muted-foreground text-xs uppercase tracking-wider">Generated</div>
    <div class="text-sm">
      <span class="font-semibold">{nf.format(stats.totalRequests)}</span> completed
    </div>
    <div class="text-sm">
      <span class="font-semibold">{nf.format(stats.totalCacheTokens)}</span> tokens
    </div>
    <div class="text-sm">
      <span class="font-semibold">{nf.format(stats.totalInputTokens)}</span> tokens
    </div>
    <div class="text-sm">
      <span class="font-semibold">{nf.format(stats.totalOutputTokens)}</span> tokens
    </div>
  </div>
</Card.Root>
