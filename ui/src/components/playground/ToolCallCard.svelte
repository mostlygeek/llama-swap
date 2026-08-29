<script lang="ts">
  import { Wrench, ChevronRight, ChevronDown, Check, TriangleAlert, LoaderCircle } from "@lucide/svelte";
  import { formatDuration } from "../../lib/format";

  interface Props {
    name: string;
    /** Human-friendly display name; falls back to the raw tool name. */
    label?: string;
    /** Raw JSON arguments string, as the model emitted them. */
    args: string;
    content: string;
    ok?: boolean;
    durationMs?: number;
    running?: boolean;
  }

  let { name, label, args, content, ok, durationMs = 0, running = false }: Props = $props();

  let expanded = $state(false);

  // Pretty-printed when it parses, verbatim when it does not -- an unparseable
  // argument string is exactly what the reader needs to see.
  let prettyArgs = $derived.by(() => {
    if (!args.trim()) return "";
    try {
      return JSON.stringify(JSON.parse(args), null, 2);
    } catch {
      return args;
    }
  });

  // One-line preview for the collapsed row: the argument values alone, since
  // the keys are implied by the tool.
  let argSummary = $derived.by(() => {
    if (!args.trim()) return "";
    try {
      const parsed = JSON.parse(args);
      if (!parsed || typeof parsed !== "object") return String(parsed);
      const values = Object.values(parsed).map((v) => (typeof v === "string" ? v : JSON.stringify(v)));
      return values.join(", ");
    } catch {
      return args.length > 60 ? args.slice(0, 60) + "…" : args;
    }
  });
</script>

<div class="mb-2 ml-1">
  <button
    type="button"
    class="bg-muted/40 hover:bg-muted/70 flex w-full items-center gap-2 rounded-md border px-2 py-1.5 text-left text-xs transition-colors"
    onclick={() => (expanded = !expanded)}
    aria-expanded={expanded}
  >
    {#if expanded}
      <ChevronDown class="text-muted-foreground size-3.5 shrink-0" />
    {:else}
      <ChevronRight class="text-muted-foreground size-3.5 shrink-0" />
    {/if}

    <Wrench class="text-muted-foreground size-3.5 shrink-0" />
    <span class="font-medium">{label || name}</span>
    {#if label && label !== name}
      <span class="text-muted-foreground font-mono text-[11px]">{name}</span>
    {/if}

    {#if argSummary}
      <span class="text-muted-foreground min-w-0 flex-1 truncate">{argSummary}</span>
    {:else}
      <span class="flex-1"></span>
    {/if}

    {#if running}
      <LoaderCircle class="text-muted-foreground size-3.5 shrink-0 animate-spin" />
    {:else if ok === false}
      <TriangleAlert class="size-3.5 shrink-0 text-amber-600 dark:text-amber-400" />
    {:else}
      <Check class="size-3.5 shrink-0 text-emerald-600 dark:text-emerald-400" />
    {/if}

    {#if durationMs > 0}
      <span class="text-muted-foreground shrink-0 tabular-nums">
        {formatDuration(durationMs, { precision: 1, subSecondMs: true })}
      </span>
    {/if}
  </button>

  {#if expanded}
    <div class="mt-1 space-y-2 rounded-md border px-2 py-2">
      {#if prettyArgs}
        <div>
          <div class="text-muted-foreground mb-1 text-[11px] font-medium uppercase tracking-wide">Arguments</div>
          <pre class="bg-muted/50 max-h-40 overflow-auto rounded p-2 text-xs">{prettyArgs}</pre>
        </div>
      {/if}
      <div>
        <div class="text-muted-foreground mb-1 text-[11px] font-medium uppercase tracking-wide">
          {ok === false ? "Error" : "Result"}
        </div>
        {#if running}
          <div class="text-muted-foreground text-xs italic">Running…</div>
        {:else}
          <pre class="bg-muted/50 max-h-80 overflow-auto whitespace-pre-wrap rounded p-2 text-xs">{content}</pre>
        {/if}
      </div>
    </div>
  {/if}
</div>
