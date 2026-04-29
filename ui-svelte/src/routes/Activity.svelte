<script lang="ts">
  import { metrics, getCapture } from "../stores/api";
  import ActivityStats from "../components/ActivityStats.svelte";
  import Tooltip from "../components/Tooltip.svelte";
  import CaptureDialog from "../components/CaptureDialog.svelte";
  import { persistentStore } from "../stores/persistent";
  import { onMount } from "svelte";
  import type { ReqRespCapture } from "../lib/types";

  type ColumnKey =
    | "id"
    | "time"
    | "model"
    | "req_path"
    | "resp_status_code"
    | "resp_content_type"
    | "cached"
    | "prompt"
    | "generated"
    | "prompt_speed"
    | "gen_speed"
    | "duration"
    | "capture";

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
  ];

  const defaultVisibleKeys = columns.filter((c) => c.defaultVisible).map((c) => c.key);

  const visibleColumns = persistentStore<ColumnKey[]>(
    "activity-columns",
    defaultVisibleKeys
  );

  let columnsMenuOpen = $state(false);
  let dropdownContainer: HTMLDivElement | null = null;

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
    if (capture) {
      selectedCapture = capture;
      dialogOpen = true;
    }
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
          <div class="absolute right-0 top-full mt-1 bg-surface border border-gray-200 dark:border-white/10 rounded shadow-lg z-10 py-1 min-w-[16rem]">
            <div class="px-3 py-2 text-xs font-medium uppercase tracking-wider text-gray-500 dark:text-gray-400 border-b border-gray-200 dark:border-white/10">
              Columns
            </div>
            {#each columns as col (col.key)}
              <label
                class="flex items-center gap-2 px-3 py-1.5 text-sm cursor-pointer hover:bg-secondary-hover transition-colors"
              >
                <input
                  type="checkbox"
                  checked={$visibleColumns.includes(col.key)}
                  onchange={() => toggleColumn(col.key)}
                  class="rounded"
                />
                {col.label}
              </label>
            {/each}
          </div>
        {/if}
      </div>
    </div>

    <table class="min-w-full divide-y">
      <thead class="border-gray-200 dark:border-white/10">
        <tr class="text-left text-xs uppercase tracking-wider">
          {#if $visibleColumns.includes("id")}
            <th class="px-6 py-3">ID</th>
          {/if}
          {#if $visibleColumns.includes("time")}
            <th class="px-6 py-3">Time</th>
          {/if}
          {#if $visibleColumns.includes("model")}
            <th class="px-6 py-3">Model</th>
          {/if}
          {#if $visibleColumns.includes("req_path")}
            <th class="px-6 py-3">Path</th>
          {/if}
          {#if $visibleColumns.includes("resp_status_code")}
            <th class="px-6 py-3">Status</th>
          {/if}
          {#if $visibleColumns.includes("resp_content_type")}
            <th class="px-6 py-3">Content-Type</th>
          {/if}
          {#if $visibleColumns.includes("cached")}
            <th class="px-6 py-3">
              Cached <Tooltip content="prompt tokens from cache" />
            </th>
          {/if}
          {#if $visibleColumns.includes("prompt")}
            <th class="px-6 py-3">
              Prompt <Tooltip content="new prompt tokens processed" />
            </th>
          {/if}
          {#if $visibleColumns.includes("generated")}
            <th class="px-6 py-3">Generated</th>
          {/if}
          {#if $visibleColumns.includes("prompt_speed")}
            <th class="px-6 py-3">Prompt Speed</th>
          {/if}
          {#if $visibleColumns.includes("gen_speed")}
            <th class="px-6 py-3">Gen Speed</th>
          {/if}
          {#if $visibleColumns.includes("duration")}
            <th class="px-6 py-3">Duration</th>
          {/if}
          {#if $visibleColumns.includes("capture")}
            <th class="px-6 py-3">Capture</th>
          {/if}
        </tr>
      </thead>
      <tbody class="divide-y">
        {#if sortedMetrics.length === 0}
          <tr>
            <td colspan={$visibleColumns.length} class="px-6 py-8 text-center text-sm text-gray-500 dark:text-gray-400">
              No activity recorded
            </td>
          </tr>
        {:else}
          {#each sortedMetrics as metric (metric.id)}
            <tr class="whitespace-nowrap text-sm border-gray-200 dark:border-white/10">
              {#if $visibleColumns.includes("id")}
                <td class="px-4 py-4">{metric.id + 1}</td>
              {/if}
              {#if $visibleColumns.includes("time")}
                <td class="px-6 py-4">{formatRelativeTime(metric.timestamp)}</td>
              {/if}
              {#if $visibleColumns.includes("model")}
                <td class="px-6 py-4">{metric.model}</td>
              {/if}
              {#if $visibleColumns.includes("req_path")}
                <td class="px-6 py-4">{metric.req_path || "-"}</td>
              {/if}
              {#if $visibleColumns.includes("resp_status_code")}
                <td class="px-6 py-4">{metric.resp_status_code || "-"}</td>
              {/if}
              {#if $visibleColumns.includes("resp_content_type")}
                <td class="px-6 py-4">{metric.resp_content_type || "-"}</td>
              {/if}
              {#if $visibleColumns.includes("cached")}
                <td class="px-6 py-4">{metric.tokens.cache_tokens > 0 ? metric.tokens.cache_tokens.toLocaleString() : "-"}</td>
              {/if}
              {#if $visibleColumns.includes("prompt")}
                <td class="px-6 py-4">{metric.tokens.input_tokens.toLocaleString()}</td>
              {/if}
              {#if $visibleColumns.includes("generated")}
                <td class="px-6 py-4">{metric.tokens.output_tokens.toLocaleString()}</td>
              {/if}
              {#if $visibleColumns.includes("prompt_speed")}
                <td class="px-6 py-4">{formatSpeed(metric.tokens.prompt_per_second)}</td>
              {/if}
              {#if $visibleColumns.includes("gen_speed")}
                <td class="px-6 py-4">{formatSpeed(metric.tokens.tokens_per_second)}</td>
              {/if}
              {#if $visibleColumns.includes("duration")}
                <td class="px-6 py-4">{formatDuration(metric.duration_ms)}</td>
              {/if}
              {#if $visibleColumns.includes("capture")}
                <td class="px-6 py-4">
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
                </td>
              {/if}
            </tr>
          {/each}
        {/if}
      </tbody>
    </table>
  </div>
</div>

<CaptureDialog capture={selectedCapture} open={dialogOpen} onclose={closeDialog} />
