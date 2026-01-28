<script lang="ts">
  import { isNarrow } from "../stores/theme";
  import { upstreamLogs } from "../stores/api";
  import ModelsPanel from "../components/ModelsPanel.svelte";
  import StatsPanel from "../components/StatsPanel.svelte";
  import LogPanel from "../components/LogPanel.svelte";
  import ResizablePanels from "../components/ResizablePanels.svelte";

  let direction = $derived<"horizontal" | "vertical">($isNarrow ? "vertical" : "horizontal");
</script>

<ResizablePanels {direction} storageKey="models-panel-group">
  {#snippet leftPanel()}
    <ModelsPanel />
  {/snippet}
  {#snippet rightPanel()}
    <div class="flex flex-col h-full space-y-4">
      {#if direction === "horizontal"}
        <StatsPanel />
      {/if}
      <div class="flex-1 min-h-0">
        <LogPanel id="modelsupstream" title="Upstream Logs" logData={$upstreamLogs} />
      </div>
    </div>
  {/snippet}
</ResizablePanels>
