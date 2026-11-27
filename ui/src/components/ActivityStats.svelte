<script lang="ts">
  import type { ActivityStatsData } from "../lib/types";
  import { persistentStore } from "../stores/persistent";
  import TokenHistogram from "./TokenHistogram.svelte";
  import { ChevronDown, X } from "@lucide/svelte";
  import * as Card from "$lib/components/ui/card/index.js";
  import { Button } from "$lib/components/ui/button/index.js";

  interface Props {
    stats: ActivityStatsData | null;
  }

  let { stats }: Props = $props();

  const nf = new Intl.NumberFormat();
  // smaller screens get larger units (e.g. 309.7M) so the numbers don't collide
  const compact = new Intl.NumberFormat(undefined, { notation: "compact", maximumFractionDigits: 1 });
  const items = $derived([
    { label: "Requests", value: stats?.total_requests ?? 0, unit: "completed" },
    { label: "Cached", value: stats?.total_cache_tokens ?? 0, unit: "tokens" },
    { label: "Processed", value: stats?.total_input_tokens ?? 0, unit: "tokens" },
    { label: "Generated", value: stats?.total_output_tokens ?? 0, unit: "tokens" },
  ]);
  const histogramCollapsed = persistentStore<boolean>("activity-histogram-collapsed", false);
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
        {#if stats?.prompt_histogram}
          <TokenHistogram
            data={stats.prompt_histogram}
            unit="prompt tokens/sec"
            colorClass="text-amber-500 dark:text-amber-400"
          />
        {:else}
          <div class="text-muted-foreground py-6 text-center text-sm">No prompt speed data yet</div>
        {/if}
      </div>
      <div class="w-full min-w-0 sm:w-1/2">
        <div class="text-muted-foreground mb-1 text-sm font-medium">Token Generation</div>
        {#if stats?.gen_histogram}
          <TokenHistogram data={stats.gen_histogram} unit="tokens/sec" />
        {:else}
          <div class="text-muted-foreground py-6 text-center text-sm">No generation speed data yet</div>
        {/if}
      </div>
    </div>
  {/if}
  <div class="grid grid-cols-2 gap-x-6 gap-y-3 text-sm sm:grid-cols-4">
    {#each items as item}
      <div class="min-w-0">
        <div class="text-muted-foreground text-xs uppercase tracking-wider">{item.label}</div>
        <div class="text-sm">
          <span class="font-semibold lg:hidden" title={nf.format(item.value)}>{compact.format(item.value)}</span>
          <span class="hidden font-semibold lg:inline">{nf.format(item.value)}</span>
          {item.unit}
        </div>
      </div>
    {/each}
  </div>
</Card.Root>
