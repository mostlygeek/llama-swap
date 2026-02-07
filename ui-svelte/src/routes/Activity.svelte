<script lang="ts">
  import { metrics, getCapture } from "../stores/api";
  import Tooltip from "../components/Tooltip.svelte";
  import CaptureDialog from "../components/CaptureDialog.svelte";
  import type { ReqRespCapture } from "../lib/types";

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
  <h1 class="text-2xl font-bold">Activity</h1>

  {#if $metrics.length === 0}
    <div class="text-center py-8">
      <p class="text-gray-600">No metrics data available</p>
    </div>
  {:else}
    <div class="card overflow-auto">
      <table class="min-w-full divide-y">
        <thead class="border-gray-200 dark:border-white/10">
          <tr class="text-left text-xs uppercase tracking-wider">
            <th class="px-6 py-3">ID</th>
            <th class="px-6 py-3">Time</th>
            <th class="px-6 py-3">Model</th>
            <th class="px-6 py-3">
              Cached <Tooltip content="prompt tokens from cache" />
            </th>
            <th class="px-6 py-3">
              Prompt <Tooltip content="new prompt tokens processed" />
            </th>
            <th class="px-6 py-3">Generated</th>
            <th class="px-6 py-3">Prompt Processing</th>
            <th class="px-6 py-3">Generation Speed</th>
            <th class="px-6 py-3">Duration</th>
            <th class="px-6 py-3">Capture</th>
          </tr>
        </thead>
        <tbody class="divide-y">
          {#each sortedMetrics as metric (metric.id)}
            <tr class="whitespace-nowrap text-sm border-gray-200 dark:border-white/10">
              <td class="px-4 py-4">{metric.id + 1}</td>
              <td class="px-6 py-4">{formatRelativeTime(metric.timestamp)}</td>
              <td class="px-6 py-4">{metric.model}</td>
              <td class="px-6 py-4">{metric.cache_tokens > 0 ? metric.cache_tokens.toLocaleString() : "-"}</td>
              <td class="px-6 py-4">{metric.input_tokens.toLocaleString()}</td>
              <td class="px-6 py-4">{metric.output_tokens.toLocaleString()}</td>
              <td class="px-6 py-4">{formatSpeed(metric.prompt_per_second)}</td>
              <td class="px-6 py-4">{formatSpeed(metric.tokens_per_second)}</td>
              <td class="px-6 py-4">{formatDuration(metric.duration_ms)}</td>
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
            </tr>
          {/each}
        </tbody>
      </table>
    </div>
  {/if}
</div>

<CaptureDialog capture={selectedCapture} open={dialogOpen} onclose={closeDialog} />
