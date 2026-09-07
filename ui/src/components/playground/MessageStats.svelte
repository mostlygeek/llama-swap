<script lang="ts">
  import { Clock, Coins, Database, Gauge } from "@lucide/svelte";
  import type { PhaseStats } from "../../lib/types";
  import { formatDuration, formatSpeed } from "../../lib/format";
  import { DETAIL_TOOLTIP, PHASE_LABEL, PHASE_TOOLTIP, phaseValueTooltip, type PhaseKind } from "../../lib/statsTooltips";

  /**
   * One line of token / time / speed for a single phase of a turn: prompt
   * processing under the user message, reasoning in the Reasoning header, and
   * the generation under the reply, mirroring llama.cpp's web UI. Every value
   * carries its own tooltip saying what it measures.
   */
  interface Props {
    phase?: PhaseStats;
    kind: PhaseKind;
    /** Print the phase name in front of the numbers, for rows that hold several phases. */
    showLabel?: boolean;
    /** Show a pulsing "processing prompt…" in front of the numbers. */
    waiting?: boolean;
    /** Prompt tokens served from cache, shown after the count when known. */
    cached?: number;
    class?: string;
  }

  let { phase, kind, showLabel = false, waiting = false, cached, class: className = "" }: Props = $props();

  let tokens = $derived(phase?.tokens);
  let ms = $derived(phase?.ms);
  let speed = $derived(phase?.perSecond);
  let approxTokens = $derived(Boolean(phase?.approxTokens));
  let approxSpeed = $derived(approxTokens || Boolean(phase?.approxTimings));
  let visible = $derived(tokens !== undefined || ms !== undefined);
  let tip = $derived((metric: "tokens" | "time" | "speed") => (phase ? phaseValueTooltip(kind, metric, phase) : ""));
</script>

{#if visible}
  <span class="text-muted-foreground inline-flex flex-wrap items-center gap-x-3 gap-y-1 text-xs tabular-nums {className}" data-stats={kind}>
    {#if waiting}
      <span class="flex items-center gap-1" title={DETAIL_TOOLTIP.waiting}>
        <span class="bg-primary h-1.5 w-1.5 animate-pulse rounded-full"></span>
        processing prompt…
      </span>
    {/if}
    {#if showLabel}
      <span class="font-medium" title={PHASE_TOOLTIP[kind]}>{PHASE_LABEL[kind]}</span>
    {/if}
    {#if tokens !== undefined}
      <span class="flex items-center gap-1" title={tip("tokens")}>
        <Coins class="size-3" />
        {approxTokens ? "~" : ""}{tokens.toLocaleString()} tokens
      </span>
    {/if}
    {#if cached}
      <span class="flex items-center gap-1" title={DETAIL_TOOLTIP.cached}>
        <Database class="size-3" />
        {cached.toLocaleString()} cached
      </span>
    {/if}
    {#if ms !== undefined}
      <span class="flex items-center gap-1" title={tip("time")}>
        <Clock class="size-3" />
        {formatDuration(ms, { precision: 1, subSecondMs: true })}
      </span>
    {/if}
    {#if speed !== undefined}
      <span class="flex items-center gap-1" title={tip("speed")}>
        <Gauge class="size-3" />
        {approxSpeed ? "~" : ""}{formatSpeed(speed)}
      </span>
    {/if}
  </span>
{/if}
