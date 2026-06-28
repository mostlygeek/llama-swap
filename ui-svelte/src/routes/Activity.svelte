<script lang="ts">
  import { metrics, getCapture } from "../stores/api";
  import ActivityStats from "../components/ActivityStats.svelte";
  import Tooltip from "../components/Tooltip.svelte";
  import MetadataTooltip from "../components/MetadataTooltip.svelte";
  import CaptureDialog from "../components/CaptureDialog.svelte";
  import { persistentStore } from "../stores/persistent";
  import { onMount } from "svelte";
  import type { ReqRespCapture } from "../lib/types";
  import { Columns3, GripVertical } from "@lucide/svelte";
  import * as Card from "$lib/components/ui/card/index.js";
  import { Button } from "$lib/components/ui/button/index.js";

  type ColumnKey = string;

  interface ColumnDef {
    key: ColumnKey;
    label: string;
    defaultVisible: boolean;
  }

  const columns: ColumnDef[] = [
    { key: "id", label: "ID", defaultVisible: true },
    { key: "time", label: "Time", defaultVisible: true },
    { key: "model", label: "Model", defaultVisible: true },
    { key: "req_path", label: "Path", defaultVisible: false },
    { key: "resp_status_code", label: "Status", defaultVisible: true },
    { key: "resp_content_type", label: "Content-Type", defaultVisible: false },
    { key: "cached", label: "Cached", defaultVisible: true },
    { key: "prompt", label: "Prompt", defaultVisible: true },
    { key: "generated", label: "Generated", defaultVisible: true },
    { key: "drafted", label: "Drafted", defaultVisible: false },
    { key: "prompt_speed", label: "Prompt Speed", defaultVisible: true },
    { key: "gen_speed", label: "Gen Speed", defaultVisible: true },
    { key: "duration", label: "Duration", defaultVisible: true },
    { key: "capture", label: "Capture", defaultVisible: true },
    { key: "meta", label: "Meta", defaultVisible: false },
  ];

  const defaultVisibleKeys = columns.filter((c) => c.defaultVisible).map((c) => c.key);

  const visibleColumns = persistentStore<ColumnKey[]>("activity-columns", defaultVisibleKeys);
  const columnOrder = persistentStore<ColumnKey[]>(
    "activity-column-order",
    columns.map((c) => c.key)
  );

  let columnsMenuOpen = $state(false);
  let dropdownContainer: HTMLDivElement | null = null;
  let dragKey: ColumnKey | null = $state(null);
  let dragOverKey: ColumnKey | null = $state(null);

  onMount(() => {
    function handleKeydown(e: KeyboardEvent) {
      if (e.key === "Escape" && columnsMenuOpen) {
        columnsMenuOpen = false;
      }
    }
    function handleClick(e: MouseEvent) {
      if (columnsMenuOpen && dropdownContainer && !dropdownContainer.contains(e.target as Node)) {
        columnsMenuOpen = false;
      }
    }
    document.addEventListener("keydown", handleKeydown);
    document.addEventListener("click", handleClick);
    return () => {
      document.removeEventListener("keydown", handleKeydown);
      document.removeEventListener("click", handleClick);
    };
  });

  function toggleColumn(key: ColumnKey) {
    const current = $visibleColumns;
    if (current.includes(key)) {
      if (current.length > 1) {
        visibleColumns.set(current.filter((k) => k !== key));
      }
    } else {
      visibleColumns.set([...current, key]);
    }
  }

  function isColumnVisible(key: ColumnKey): boolean {
    return $visibleColumns.includes(key);
  }

  function handleDragStart(e: DragEvent, key: ColumnKey) {
    dragKey = key;
    e.dataTransfer?.setData("text/plain", key);
    if (e.dataTransfer) {
      e.dataTransfer.effectAllowed = "move";
    }
  }

  function handleDragOver(e: DragEvent, key: ColumnKey) {
    e.preventDefault();
    if (e.dataTransfer) {
      e.dataTransfer.dropEffect = "move";
    }
    dragOverKey = key;
  }

  function handleDrop(e: DragEvent, targetKey: ColumnKey) {
    e.preventDefault();
    if (!dragKey || dragKey === targetKey) return;
    const order = [...$columnOrder];
    const fromIndex = order.indexOf(dragKey);
    let toIndex = order.indexOf(targetKey);
    if (fromIndex === -1 || toIndex === -1) return;
    order.splice(fromIndex, 1);
    if (fromIndex < toIndex) {
      toIndex -= 1;
    }
    order.splice(toIndex, 0, dragKey);
    columnOrder.set(order);
  }

  function handleDragEnd() {
    dragKey = null;
    dragOverKey = null;
  }

  let orderedColumns = $derived(
    columns.slice().sort((a, b) => {
      const aIndex = $columnOrder.indexOf(a.key);
      const bIndex = $columnOrder.indexOf(b.key);
      if (aIndex === -1 && bIndex === -1) return 0;
      if (aIndex === -1) return 1;
      if (bIndex === -1) return -1;
      return aIndex - bIndex;
    })
  );

  let activeVisibleColumns = $derived(
    columns
      .filter((c) => isColumnVisible(c.key))
      .sort((a, b) => {
        const aIndex = $columnOrder.indexOf(a.key);
        const bIndex = $columnOrder.indexOf(b.key);
        if (aIndex === -1 && bIndex === -1) return 0;
        if (aIndex === -1) return 1;
        if (bIndex === -1) return -1;
        return aIndex - bIndex;
      })
      .map((c) => c.key)
  );

  let columnLabelMap = $derived(Object.fromEntries(columns.map((c) => [c.key, c.label])));

  $effect(() => {
    const staticKeys = new Set(columns.map((c) => c.key));
    const order = $columnOrder;
    const hasStale = order.some((k) => !staticKeys.has(k));
    const missing = columns.filter((c) => !order.includes(c.key)).map((c) => c.key);
    if (hasStale || missing.length > 0) {
      const cleaned = order.filter((k) => staticKeys.has(k));
      columnOrder.set([...cleaned, ...missing]);
    }
  });

  function formatSpeed(speed: number): string {
    return speed < 0 ? "unknown" : speed.toFixed(2) + " t/s";
  }

  function formatDrafted(drafted: number, accepted: number): string {
    return drafted > 0 ? (accepted * 100 / drafted).toFixed(1) + "% (" + accepted + "/" + drafted + ")" : "-";
  }

  function formatDuration(ms: number): string {
    return (ms / 1000).toFixed(2) + "s";
  }

  function formatRelativeTime(timestamp: string): string {
    const now = new Date();
    const date = new Date(timestamp);
    const diffInSeconds = Math.floor((now.getTime() - date.getTime()) / 1000);

    // Handle future dates by returning "just now"
    if (diffInSeconds < 5) {
      return "now";
    }

    if (diffInSeconds < 60) {
      return `${diffInSeconds}s ago`;
    }

    const diffInMinutes = Math.floor(diffInSeconds / 60);
    if (diffInMinutes < 60) {
      return `${diffInMinutes}m ago`;
    }

    const diffInHours = Math.floor(diffInMinutes / 60);
    if (diffInHours < 24) {
      return `${diffInHours}h ago`;
    }

    return "a while ago";
  }

  let sortedMetrics = $derived([...$metrics].sort((a, b) => b.id - a.id));

  let selectedCapture = $state<ReqRespCapture | null>(null);
  let dialogOpen = $state(false);
  let loadingCaptureId = $state<number | null>(null);

  async function viewCapture(id: number) {
    loadingCaptureId = id;
    const capture = await getCapture(id);
    loadingCaptureId = null;
    selectedCapture = capture;
    dialogOpen = true;
  }

  function closeDialog() {
    dialogOpen = false;
    selectedCapture = null;
  }
</script>

<div class="p-2">
  <div class="mt-4 mb-4">
    <ActivityStats />
  </div>

  <Card.Root class="relative min-h-[30rem] gap-0 overflow-auto py-2">
    <div class="flex justify-end px-2" bind:this={dropdownContainer}>
      <div class="relative">
        <Button
          variant="ghost"
          size="icon-sm"
          onclick={() => (columnsMenuOpen = !columnsMenuOpen)}
          title="Select columns"
        >
          <Columns3 />
        </Button>
        {#if columnsMenuOpen}
          <div
            class="bg-popover text-popover-foreground absolute right-0 top-full z-20 mt-1 min-w-[16rem] rounded-md border py-1 shadow-md"
            role="list"
          >
            <div
              class="text-muted-foreground border-b px-3 py-2 text-xs font-medium uppercase tracking-wider"
              role="presentation"
            >
              Columns
            </div>
            {#each orderedColumns as col (col.key)}
              {@const key = col.key}
              <div
                class="hover:bg-accent flex items-center gap-2 px-3 py-1.5 text-sm transition-colors {dragOverKey ===
                  key && dragKey !== key
                  ? 'bg-primary/10 ring-primary/40 ring-1'
                  : ''} {dragKey === key ? 'opacity-40' : ''}"
                role="listitem"
                ondragover={(e) => handleDragOver(e, key)}
                ondrop={(e) => handleDrop(e, key)}
              >
                <span
                  class="text-muted-foreground flex cursor-grab select-none"
                  draggable={true}
                  role="button"
                  tabindex="-1"
                  aria-label="Drag to reorder {col.label}"
                  ondragstart={(e) => handleDragStart(e, key)}
                  ondragend={handleDragEnd}
                >
                  <GripVertical class="size-4" />
                </span>
                <label class="flex flex-1 cursor-pointer items-center gap-2">
                  <input
                    type="checkbox"
                    checked={isColumnVisible(key)}
                    onchange={() => toggleColumn(key)}
                    class="accent-primary rounded-none"
                  />
                  {col.label}
                </label>
              </div>
            {/each}
          </div>
        {/if}
      </div>
    </div>

    <table class="min-w-full">
      <thead>
        <tr class="text-muted-foreground border-b text-left text-xs uppercase tracking-wider">
          {#each activeVisibleColumns as key (key)}
            <th class="px-6 py-3 font-medium">
              {#if key === "cached"}
                Cached <Tooltip content="prompt tokens from cache" />
              {:else if key === "prompt"}
                Prompt <Tooltip content="new prompt tokens processed" />
              {:else if key === "drafted"}
                Drafted <Tooltip content="acceptance rate (accepted/drafted)" />
              {:else}
                {columnLabelMap[key] ?? key}
              {/if}
            </th>
          {/each}
        </tr>
      </thead>
      <tbody>
        {#if sortedMetrics.length === 0}
          <tr>
            <td colspan={activeVisibleColumns.length} class="text-muted-foreground px-6 py-8 text-center text-sm">
              No activity recorded
            </td>
          </tr>
        {:else}
          {#each sortedMetrics as metric (metric.id)}
            <tr class="hover:bg-muted/50 whitespace-nowrap border-b text-sm transition-colors">
              {#each activeVisibleColumns as key (key)}
                <td class="px-6 py-4">
                  {#if key === "id"}
                    {metric.id + 1}
                  {:else if key === "time"}
                    {formatRelativeTime(metric.timestamp)}
                  {:else if key === "model"}
                    {metric.model}
                  {:else if key === "req_path"}
                    {metric.req_path || "-"}
                  {:else if key === "resp_status_code"}
                    {#if metric.error_msg}
                      <span class="text-destructive cursor-help" title={metric.error_msg}>
                        {metric.resp_status_code || "-"}
                      </span>
                    {:else}
                      {metric.resp_status_code || "-"}
                    {/if}
                  {:else if key === "resp_content_type"}
                    {metric.resp_content_type || "-"}
                  {:else if key === "cached"}
                    {metric.tokens.cache_tokens > 0 ? metric.tokens.cache_tokens.toLocaleString() : "-"}
                  {:else if key === "prompt"}
                    {metric.tokens.input_tokens.toLocaleString()}
                  {:else if key === "generated"}
                    {metric.tokens.output_tokens.toLocaleString()}
                  {:else if key === "drafted"}
                    {formatDrafted(metric.tokens.draft_tokens, metric.tokens.draft_acc_tokens)}
                  {:else if key === "prompt_speed"}
                    {formatSpeed(metric.tokens.prompt_per_second)}
                  {:else if key === "gen_speed"}
                    {formatSpeed(metric.tokens.tokens_per_second)}
                  {:else if key === "duration"}
                    {formatDuration(metric.duration_ms)}
                  {:else if key === "capture"}
                    {#if metric.has_capture}
                      <Button
                        variant="outline"
                        size="xs"
                        onclick={() => viewCapture(metric.id)}
                        disabled={loadingCaptureId === metric.id}
                      >
                        {loadingCaptureId === metric.id ? "..." : "View"}
                      </Button>
                    {:else}
                      <span class="text-muted-foreground">-</span>
                    {/if}
                  {:else if key === "meta"}
                    {#if Object.keys(metric.metadata || {}).length > 0}
                      <MetadataTooltip metadata={metric.metadata}>
                        <span class="text-muted-foreground hover:text-foreground cursor-help">...</span>
                      </MetadataTooltip>
                    {:else}
                      <span class="text-muted-foreground">-</span>
                    {/if}
                  {:else}
                    -
                  {/if}
                </td>
              {/each}
            </tr>
          {/each}
        {/if}
      </tbody>
    </table>
  </Card.Root>
</div>

<CaptureDialog capture={selectedCapture} open={dialogOpen} onclose={closeDialog} />
