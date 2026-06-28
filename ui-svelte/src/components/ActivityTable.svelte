<script lang="ts">
  import type { ActivityLogEntry, ReqRespCapture } from "../lib/types";
  import { getCapture } from "../stores/api";
  import { persistentStore } from "../stores/persistent";
  import { onMount } from "svelte";
  import Tooltip from "./Tooltip.svelte";
  import MetadataTooltip from "./MetadataTooltip.svelte";
  import CaptureDialog from "./CaptureDialog.svelte";
  import { Columns3, GripVertical } from "@lucide/svelte";
  import * as Card from "$lib/components/ui/card/index.js";
  import * as Select from "$lib/components/ui/select/index.js";
  import { Button } from "$lib/components/ui/button/index.js";

  type ColumnKey = string;

  interface ColumnDef {
    key: ColumnKey;
    label: string;
    defaultVisible: boolean;
  }

  interface Props {
    metrics: ActivityLogEntry[];
    storagePrefix: string;
    showModelColumn?: boolean;
    showPagination?: boolean;
    title?: string;
    compact?: boolean;
    emptyMessage?: string;
    cardClass?: string;
  }

  let {
    metrics,
    storagePrefix,
    showModelColumn = true,
    showPagination = false,
    title,
    compact = false,
    emptyMessage = "No activity recorded",
    cardClass = "",
  }: Props = $props();

  function buildColumns(withModel: boolean): ColumnDef[] {
    const cols: ColumnDef[] = [
      { key: "id", label: "ID", defaultVisible: true },
      { key: "time", label: "Time", defaultVisible: true },
    ];
    if (withModel) cols.push({ key: "model", label: "Model", defaultVisible: true });
    cols.push(
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
      { key: "meta", label: "Meta", defaultVisible: false }
    );
    return cols;
  }

  // svelte-ignore state_referenced_locally
  const columns: ColumnDef[] = buildColumns(showModelColumn);
  const defaultVisibleKeys = columns.filter((c) => c.defaultVisible).map((c) => c.key);

  // svelte-ignore state_referenced_locally
  const visibleColumns = persistentStore<ColumnKey[]>(`${storagePrefix}-columns`, defaultVisibleKeys);
  // svelte-ignore state_referenced_locally
  const columnOrder = persistentStore<ColumnKey[]>(
    `${storagePrefix}-column-order`,
    columns.map((c) => c.key)
  );
  // svelte-ignore state_referenced_locally
  const pageSizeStore = persistentStore<number>(`${storagePrefix}-page-size`, 10);

  let page = $state(0);
  let totalPages = $derived(Math.max(1, Math.ceil(metrics.length / $pageSizeStore)));
  let pageMetrics = $derived(metrics.slice(page * $pageSizeStore, (page + 1) * $pageSizeStore));
  let displayMetrics = $derived(showPagination ? pageMetrics : metrics);

  // Reset page when data source or page size changes
  $effect(() => {
    metrics;
    $pageSizeStore;
    page = 0;
  });

  let columnsMenuOpen = $state(false);
  let dropdownContainer: HTMLDivElement | null = $state(null);
  let dragKey: ColumnKey | null = $state(null);
  let dragOverKey: ColumnKey | null = $state(null);

  onMount(() => {
    function handleKeydown(e: KeyboardEvent) {
      if (e.key === "Escape" && columnsMenuOpen) columnsMenuOpen = false;
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
      if (current.length > 1) visibleColumns.set(current.filter((k) => k !== key));
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
    if (e.dataTransfer) e.dataTransfer.effectAllowed = "move";
  }

  function handleDragOver(e: DragEvent, key: ColumnKey) {
    e.preventDefault();
    if (e.dataTransfer) e.dataTransfer.dropEffect = "move";
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
    if (fromIndex < toIndex) toIndex -= 1;
    order.splice(toIndex, 0, dragKey);
    columnOrder.set(order);
  }

  function handleDragEnd() {
    dragKey = null;
    dragOverKey = null;
  }

  function orderByColumnOrder<T extends { key: ColumnKey }>(cols: T[]): T[] {
    return cols.slice().sort((a, b) => {
      const aIndex = $columnOrder.indexOf(a.key);
      const bIndex = $columnOrder.indexOf(b.key);
      if (aIndex === -1 && bIndex === -1) return 0;
      if (aIndex === -1) return 1;
      if (bIndex === -1) return -1;
      return aIndex - bIndex;
    });
  }

  let orderedColumns = $derived(orderByColumnOrder(columns));

  let activeVisibleColumns = $derived(
    orderByColumnOrder(columns.filter((c) => isColumnVisible(c.key))).map((c) => c.key)
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

  function formatDuration(ms: number): string {
    return (ms / 1000).toFixed(2) + "s";
  }

  function formatRelativeTime(timestamp: string): string {
    const now = new Date();
    const date = new Date(timestamp);
    const diffInSeconds = Math.floor((now.getTime() - date.getTime()) / 1000);
    if (diffInSeconds < 5) return "now";
    if (diffInSeconds < 60) return `${diffInSeconds}s ago`;
    const diffInMinutes = Math.floor(diffInSeconds / 60);
    if (diffInMinutes < 60) return `${diffInMinutes}m ago`;
    const diffInHours = Math.floor(diffInMinutes / 60);
    if (diffInHours < 24) return `${diffInHours}h ago`;
    return "a while ago";
  }

  function formatDrafted(drafted: number, accepted: number): string {
    return drafted > 0
      ? ((accepted * 100) / drafted).toFixed(1) + "% (" + accepted + "/" + drafted + ")"
      : "-";
  }

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

  let thClass = $derived(compact ? "px-4 py-2 font-medium" : "px-6 py-3 font-medium");
  let tdClass = $derived(compact ? "px-4 py-2" : "px-6 py-4");
  let rowClass = $derived(
    compact
      ? "hover:bg-muted/50 whitespace-nowrap border-b"
      : "hover:bg-muted/50 whitespace-nowrap border-b text-sm transition-colors"
  );
</script>

<Card.Root class="shrink-0 gap-0 overflow-hidden py-0 {cardClass}">
  <Card.Header class="flex items-center justify-between border-b px-4 py-2">
    <div class="flex items-center gap-2">
      {#if title}
        <Card.Title class="text-sm font-semibold">
          {title}
          <span class="text-muted-foreground text-xs font-normal">({metrics.length})</span>
        </Card.Title>
      {/if}
    </div>
    <div class="flex items-center gap-2">
      {#if showPagination}
        <span class="text-muted-foreground text-xs">Per page</span>
        <Select.Root
          type="single"
          value={String($pageSizeStore)}
          onValueChange={(v) => pageSizeStore.set(Number(v))}
        >
          <Select.Trigger class="h-7 w-16 text-xs">{$pageSizeStore}</Select.Trigger>
          <Select.Content>
            {#each [5, 10, 25, 50] as size (size)}
              <Select.Item value={String(size)}>{size}</Select.Item>
            {/each}
          </Select.Content>
        </Select.Root>
      {/if}
      <div bind:this={dropdownContainer}>
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
    </div>
  </Card.Header>
  <Card.Content class="overflow-x-auto p-0">
    <table class="min-w-full text-sm">
      <thead class="text-muted-foreground border-b text-left text-xs uppercase tracking-wider">
        <tr>
          {#each activeVisibleColumns as key (key)}
            <th class={thClass}>
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
        {#if displayMetrics.length === 0}
          <tr>
            <td colspan={activeVisibleColumns.length} class="text-muted-foreground px-4 py-6 text-center text-sm">
              {emptyMessage}
            </td>
          </tr>
        {:else}
          {#each displayMetrics as metric (metric.id)}
            <tr class={rowClass}>
              {#each activeVisibleColumns as key (key)}
                <td class={tdClass}>
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

    {#if showPagination && metrics.length > 0}
      <div class="flex items-center justify-between gap-2 border-t px-4 py-2 text-sm">
        <span class="text-muted-foreground text-xs">
          Page {page + 1} of {totalPages} · {metrics.length} total
        </span>
        <div class="flex gap-1">
          <Button variant="outline" size="sm" onclick={() => (page = 0)} disabled={page === 0}>
            First
          </Button>
          <Button
            variant="outline"
            size="sm"
            onclick={() => (page = Math.max(0, page - 1))}
            disabled={page === 0}
          >
            Prev
          </Button>
          <Button
            variant="outline"
            size="sm"
            onclick={() => (page = Math.min(totalPages - 1, page + 1))}
            disabled={page >= totalPages - 1}
          >
            Next
          </Button>
          <Button
            variant="outline"
            size="sm"
            onclick={() => (page = totalPages - 1)}
            disabled={page >= totalPages - 1}
          >
            Last
          </Button>
        </div>
      </div>
    {/if}
  </Card.Content>
</Card.Root>

<CaptureDialog capture={selectedCapture} open={dialogOpen} onclose={closeDialog} />
