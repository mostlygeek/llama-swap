<script lang="ts">
  import { metrics, getCapture } from "../stores/api";
  import ActivityStats from "../components/ActivityStats.svelte";
  import Tooltip from "../components/Tooltip.svelte";
  import MetadataTooltip from "../components/MetadataTooltip.svelte";
  import CaptureDialog from "../components/CaptureDialog.svelte";
  import { persistentStore } from "../stores/persistent";
  import { onMount } from "svelte";
  import type { ReqRespCapture } from "../lib/types";

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
    { key: "resp_status_code", label: "Status", defaultVisible: false },
    { key: "resp_content_type", label: "Content-Type", defaultVisible: false },
    { key: "cached", label: "Cached", defaultVisible: true },
    { key: "prompt", label: "Prompt", defaultVisible: true },
    { key: "generated", label: "Generated", defaultVisible: true },
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
    const toIndex = order.indexOf(targetKey);
    if (fromIndex === -1 || toIndex === -1) return;
    order.splice(fromIndex, 1);
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

  <div class="card overflow-auto relative min-h-[30rem]">
    <div class="flex justify-end px-4" bind:this={dropdownContainer}>
      <div class="relative">
        <button
          class="w-8 h-8 flex items-center justify-center rounded hover:bg-secondary-hover transition-colors"
          onclick={() => (columnsMenuOpen = !columnsMenuOpen)}
          title="Select columns"
        >
          <svg class="w-4 h-4" fill="none" stroke="currentColor" viewBox="0 0 24 24">
            <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M12 6V4m0 2a2 2 0 100 4m0-4a2 2 0 110 4m-6 8a2 2 0 100-4m0 4a2 2 0 110-4m0 4v2m0-6V4m6 6v10m6-2a2 2 0 100-4m0 4a2 2 0 110-4m0 4v2m0-6V4"></path>
          </svg>
        </button>
        {#if columnsMenuOpen}
          <div class="absolute right-0 top-full mt-1 bg-surface border border-gray-200 dark:border-white/10 rounded shadow-lg z-10 py-1 min-w-[16rem]" role="list">
            <div class="px-3 py-2 text-xs font-medium uppercase tracking-wider text-gray-500 dark:text-gray-400 border-b border-gray-200 dark:border-white/10" role="presentation">
              Columns
            </div>
            {#each orderedColumns as col (col.key)}
              {@const key = col.key}
              <div
                class="flex items-center gap-2 px-3 py-1.5 text-sm hover:bg-secondary-hover transition-colors {dragOverKey === key && dragKey !== key ? 'bg-primary/10 ring-1 ring-primary/40' : ''} {dragKey === key ? 'opacity-40' : ''}"
                role="listitem"
                ondragover={(e) => handleDragOver(e, key)}
                ondrop={(e) => handleDrop(e, key)}
              >
                <span
                  class="text-txtsecondary select-none cursor-grab"
                  draggable={true}
                  role="button"
                  tabindex="-1"
                  aria-label="Drag to reorder {col.label}"
                  ondragstart={(e) => handleDragStart(e, key)}
                  ondragend={handleDragEnd}
                >⋮⋮</span>
                <label class="flex items-center gap-2 flex-1 cursor-pointer">
                  <input
                    type="checkbox"
                    checked={isColumnVisible(key)}
                    onchange={() => toggleColumn(key)}
                    class="rounded"
                  />
                  {col.label}
                </label>
              </div>
            {/each}
          </div>
        {/if}
      </div>
    </div>

    <table class="min-w-full divide-y">
      <thead class="border-gray-200 dark:border-white/10">
        <tr class="text-left text-xs uppercase tracking-wider">
          {#each activeVisibleColumns as key (key)}
            <th class="px-6 py-3">
              {#if key === "cached"}
                Cached <Tooltip content="prompt tokens from cache" />
              {:else if key === "prompt"}
                Prompt <Tooltip content="new prompt tokens processed" />
              {:else}
                {columnLabelMap[key] ?? key}
              {/if}
            </th>
          {/each}
        </tr>
      </thead>
      <tbody class="divide-y">
        {#if sortedMetrics.length === 0}
          <tr>
            <td colspan={activeVisibleColumns.length} class="px-6 py-8 text-center text-sm text-gray-500 dark:text-gray-400">
              No activity recorded
            </td>
          </tr>
        {:else}
          {#each sortedMetrics as metric (metric.id)}
            <tr class="whitespace-nowrap text-sm border-gray-200 dark:border-white/10">
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
                    {metric.resp_status_code || "-"}
                  {:else if key === "resp_content_type"}
                    {metric.resp_content_type || "-"}
                  {:else if key === "cached"}
                    {metric.tokens.cache_tokens > 0 ? metric.tokens.cache_tokens.toLocaleString() : "-"}
                  {:else if key === "prompt"}
                    {metric.tokens.input_tokens.toLocaleString()}
                  {:else if key === "generated"}
                    {metric.tokens.output_tokens.toLocaleString()}
                  {:else if key === "prompt_speed"}
                    {formatSpeed(metric.tokens.prompt_per_second)}
                  {:else if key === "gen_speed"}
                    {formatSpeed(metric.tokens.tokens_per_second)}
                  {:else if key === "duration"}
                    {formatDuration(metric.duration_ms)}
                  {:else if key === "capture"}
                    {#if metric.has_capture}
                      <button
                        onclick={() => viewCapture(metric.id)}
                        disabled={loadingCaptureId === metric.id}
                        class="btn btn--sm"
                      >
                        {loadingCaptureId === metric.id ? "..." : "View"}
                      </button>
                    {:else}
                      <span class="text-txtsecondary">-</span>
                    {/if}
                  {:else if key === "meta"}
                    {#if Object.keys(metric.metadata || {}).length > 0}
                      <MetadataTooltip metadata={metric.metadata}>
                        <span class="cursor-help text-txtsecondary hover:text-txtmain">...</span>
                      </MetadataTooltip>
                    {:else}
                      <span class="text-txtsecondary">-</span>
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
  </div>
</div>

<CaptureDialog capture={selectedCapture} open={dialogOpen} onclose={closeDialog} />
