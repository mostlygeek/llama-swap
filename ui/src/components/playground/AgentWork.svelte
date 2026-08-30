<script module lang="ts">
  export interface ToolWorkItem {
    kind: "tool";
    name: string;
    label: string;
    args: string;
    content: string;
    ok?: boolean;
    durationMs?: number;
    running?: boolean;
  }

  export interface ReasoningWorkItem {
    kind: "reasoning";
    content: string;
    durationMs?: number;
    running?: boolean;
  }

  export type WorkItem = ReasoningWorkItem | ToolWorkItem;
</script>

<script lang="ts">
  import { Brain, ChevronDown, ChevronRight, LoaderCircle, Wrench } from "@lucide/svelte";
  import { formatDuration } from "../../lib/format";
  import ToolCallCard from "./ToolCallCard.svelte";

  interface Props {
    items: WorkItem[];
    running?: boolean;
  }

  let { items, running = false }: Props = $props();
  let expanded = $state(false);
  let expandedReasoning = $state<Set<number>>(new Set());
  let currentItem = $derived(items[items.length - 1]);
  let isWorking = $derived(running || items.some((item) => item.running));
  let reasoningCharacters = $derived(
    items.reduce((total, item) => total + (item.kind === "reasoning" ? item.content.length : 0), 0)
  );
  let totalDurationMs = $derived(
    items.reduce((total, item) => total + (item.durationMs ?? 0), 0)
  );
  let toolCallCount = $derived(items.filter((item) => item.kind === "tool").length);

  function toggleReasoning(idx: number) {
    const next = new Set(expandedReasoning);
    if (next.has(idx)) next.delete(idx);
    else next.add(idx);
    expandedReasoning = next;
  }
</script>

<div class="mb-3 overflow-hidden rounded-md border">
  <button
    type="button"
    class="bg-muted/50 hover:bg-muted flex w-full items-center gap-2 px-3 py-2 text-left text-sm transition-colors"
    onclick={() => (expanded = !expanded)}
    aria-expanded={expanded}
  >
    {#if expanded}
      <ChevronDown class="size-4 shrink-0" />
    {:else}
      <ChevronRight class="size-4 shrink-0" />
    {/if}
    <span class="font-medium">Work</span>
    {#if isWorking}
      <span class="text-muted-foreground flex min-w-0 items-center gap-1.5 text-xs">
        {#if currentItem?.kind === "tool" && currentItem.running}
          <Wrench class="size-3.5 shrink-0" />
          <span class="truncate">{currentItem.label || currentItem.name}</span>
        {:else if currentItem?.kind === "reasoning" && currentItem.running}
          <Brain class="size-3.5 shrink-0" />
          <span>Reasoning…</span>
        {:else}
          <span>Working…</span>
        {/if}
        <LoaderCircle class="size-3.5 shrink-0 animate-spin" />
      </span>
    {:else}
      <span class="text-muted-foreground text-xs">
        {reasoningCharacters.toLocaleString()} reasoning characters ·
        {formatDuration(totalDurationMs, { precision: 1, subSecondMs: true })} ·
        {toolCallCount} {toolCallCount === 1 ? "tool call" : "tool calls"}
      </span>
    {/if}
  </button>

  {#if expanded}
    <div class="space-y-3 px-3 py-2">
      {#each items as item, idx (idx)}
        {#if item.kind === "reasoning"}
          <div class="overflow-hidden rounded-md border">
            <button
              type="button"
              class="bg-muted/30 hover:bg-muted flex w-full items-center gap-2 px-3 py-2 text-left text-sm transition-colors"
              onclick={() => toggleReasoning(idx)}
              aria-expanded={expandedReasoning.has(idx)}
            >
              {#if expandedReasoning.has(idx)}
                <ChevronDown class="size-4 shrink-0" />
              {:else}
                <ChevronRight class="size-4 shrink-0" />
              {/if}
              <Brain class="size-4" />
              <span class="font-medium">Reasoning</span>
              {#if item.durationMs && !item.running}
                <span class="text-muted-foreground">({formatDuration(item.durationMs, { precision: 1, subSecondMs: true })})</span>
              {/if}
              {#if item.running}
                <span class="text-muted-foreground ml-auto">reasoning...</span>
              {/if}
            </button>
            {#if expandedReasoning.has(idx)}
              <div class="bg-muted/20 text-muted-foreground whitespace-pre-wrap px-3 py-2 font-mono text-sm">
                {item.content}{#if item.running}<span class="ml-0.5 inline-block h-4 w-1.5 animate-pulse bg-current"></span>{/if}
              </div>
            {/if}
          </div>
        {:else}
          <ToolCallCard
            name={item.name}
            label={item.label}
            args={item.args}
            content={item.content}
            ok={item.ok}
            durationMs={item.durationMs}
            running={item.running}
          />
        {/if}
      {/each}
    </div>
  {/if}
</div>
