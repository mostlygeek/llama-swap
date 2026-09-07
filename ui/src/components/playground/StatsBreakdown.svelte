<script lang="ts">
  import type { GenerationStats, PhaseStats } from "../../lib/types";
  import { formatDuration, formatSpeed } from "../../lib/format";
  import { DETAIL_TOOLTIP, PHASE_LABEL, PHASE_TOOLTIP, phaseValueTooltip, type PhaseKind } from "../../lib/statsTooltips";

  /**
   * The expert view of a turn's stats: one row per phase with tokens, time,
   * throughput and time per token, then the details a benchmark cares about
   * (cache hits, draft acceptance, time to first token, context use, stop
   * reason). Backend-reported values are exact; browser measurements and the
   * reasoning/response split carry a ~.
   */
  interface Props {
    stats: GenerationStats;
  }

  let { stats }: Props = $props();
  let contextLength = $derived(stats.contextLength);

  interface Row {
    kind: PhaseKind;
    phase: PhaseStats;
    /** Tokens the throughput was measured over, when not all of them. */
    processed?: number;
  }

  let rows = $derived.by((): Row[] => {
    const out: Row[] = [];
    const cached = stats.cachedTokens ?? 0;
    out.push({
      kind: "prompt",
      phase: stats.prompt,
      processed: stats.prompt.tokens !== undefined && cached > 0 ? stats.prompt.tokens - cached : undefined,
    });
    if (stats.reasoning && stats.answer) {
      out.push({ kind: "reasoning", phase: stats.reasoning });
      out.push({ kind: "response", phase: stats.answer });
    }
    out.push({ kind: "generation", phase: stats.generation });
    return out;
  });

  let contextUsed = $derived(
    stats.prompt.tokens !== undefined && stats.generation.tokens !== undefined
      ? stats.prompt.tokens + stats.generation.tokens
      : undefined
  );
  let hasDraft = $derived((stats.draftTokens ?? 0) > 0);
  let draftText = $derived.by(() => {
    if (!hasDraft) return "";
    let text = `${stats.draftTokens!.toLocaleString()} proposed`;
    if (stats.draftAccepted !== undefined) {
      const rate = ((stats.draftAccepted / stats.draftTokens!) * 100).toFixed(0);
      text += `, ${stats.draftAccepted.toLocaleString()} accepted (${rate}%)`;
    }
    return text;
  });
  let approxAnywhere = $derived(
    [stats.prompt, stats.generation, stats.reasoning, stats.answer].some((p) => p && (p.approxTokens || p.approxTimings))
  );

  const tokens = (n: number | undefined, approx: boolean) =>
    n === undefined ? "–" : `${approx ? "~" : ""}${n.toLocaleString()}`;
  const time = (ms: number | undefined) => (ms === undefined ? "–" : formatDuration(ms, { precision: 1, subSecondMs: true }));
  // The ~ belongs to a value; a phase the backend said nothing about is just
  // a dash.
  const speed = (row: Row) => {
    if (row.phase.perSecond === undefined) return "–";
    const approx = row.phase.approxTokens || row.phase.approxTimings;
    return `${approx ? "~" : ""}${formatSpeed(row.phase.perSecond)}`;
  };
  const perToken = (row: Row) => {
    const n = row.processed ?? row.phase.tokens;
    if (n === undefined || n <= 0 || row.phase.ms === undefined) return "–";
    return `${(row.phase.ms / n).toFixed(1)} ms`;
  };
  const pct = (part: number, whole: number) => (whole > 0 ? `${((part / whole) * 100).toFixed(1)}%` : "");

  let stopReason = $derived.by(() => {
    switch (stats.finishReason) {
      case undefined:
        return undefined;
      case "stop":
        return "end of turn";
      case "length":
        return "max_tokens reached";
      case "tool_calls":
        return "tool call";
      case "cancelled":
        return "cancelled by you";
      default:
        return stats.finishReason;
    }
  });
</script>

<div class="text-muted-foreground mt-1 text-xs tabular-nums">
  <table class="ml-auto border-separate border-spacing-x-3 border-spacing-y-0">
    <thead>
      <tr class="text-right">
        <th class="text-left font-normal"></th>
        <th class="font-medium" title={DETAIL_TOOLTIP.columnTokens}>tokens</th>
        <th class="font-medium" title={DETAIL_TOOLTIP.columnTime}>time</th>
        <th class="font-medium" title={DETAIL_TOOLTIP.columnSpeed}>speed</th>
        <th class="font-medium" title={DETAIL_TOOLTIP.columnPerToken}>per token</th>
      </tr>
    </thead>
    <tbody>
      {#each rows as row (row.kind)}
        <tr class="text-right">
          <td class="text-left font-medium" title={PHASE_TOOLTIP[row.kind]}>{PHASE_LABEL[row.kind]}</td>
          <td title={phaseValueTooltip(row.kind, "tokens", row.phase)}>
            {tokens(row.phase.tokens, row.phase.approxTokens)}
            {#if row.processed !== undefined}
              <span class="opacity-70" title="Prompt tokens that were not in the cache and had to be processed.">({row.processed.toLocaleString()} new)</span>
            {/if}
          </td>
          <td title={phaseValueTooltip(row.kind, "time", row.phase)}>{time(row.phase.ms)}</td>
          <td title={phaseValueTooltip(row.kind, "speed", row.phase)}>{speed(row)}</td>
          <td title={phaseValueTooltip(row.kind, "perToken", row.phase)}>{perToken(row)}</td>
        </tr>
      {/each}
    </tbody>
  </table>
  <dl class="mt-1 flex flex-wrap justify-end gap-x-4 gap-y-0.5 pr-1">
    {#if stats.cachedTokens && stats.prompt.tokens}
      <div title={DETAIL_TOOLTIP.cached}><dt class="inline font-medium">Cache</dt> <dd class="inline">{stats.cachedTokens.toLocaleString()} tokens reused ({pct(stats.cachedTokens, stats.prompt.tokens)})</dd></div>
    {/if}
    {#if hasDraft}
      <div title={DETAIL_TOOLTIP.draft}>
        <dt class="inline font-medium">Draft</dt>
        <dd class="inline">{draftText}</dd>
      </div>
    {/if}
    {#if stats.firstTokenMs !== undefined}
      <div title={DETAIL_TOOLTIP.firstToken}><dt class="inline font-medium">First token</dt> <dd class="inline">{time(stats.firstTokenMs)}</dd></div>
    {/if}
    {#if stats.wallMs !== undefined}
      <div title={DETAIL_TOOLTIP.wall}><dt class="inline font-medium">Wall</dt> <dd class="inline">{time(stats.wallMs)}</dd></div>
    {/if}
    {#if contextUsed !== undefined}
      <div title={DETAIL_TOOLTIP.context}>
        <dt class="inline font-medium">Context</dt>
        <dd class="inline">
          {#if contextLength}
            {contextUsed.toLocaleString()} / {contextLength.toLocaleString()} ({pct(contextUsed, contextLength)})
          {:else}
            {contextUsed.toLocaleString()}
          {/if}
        </dd>
      </div>
    {/if}
    {#if stopReason}
      <div title={DETAIL_TOOLTIP.stop}><dt class="inline font-medium">Stop</dt> <dd class="inline">{stopReason}</dd></div>
    {/if}
  </dl>
  {#if approxAnywhere}
    <p class="mt-0.5 pr-1 text-right opacity-70" title="Hover any value marked ~ to see how it was estimated.">~ estimated in the browser from the stream</p>
  {/if}
</div>
