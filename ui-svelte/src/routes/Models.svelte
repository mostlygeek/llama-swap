<script lang="ts">
  import { isNarrow } from "../stores/theme";
  import { upstreamLogs } from "../stores/api";
  import ModelsPanel from "../components/ModelsPanel.svelte";
  import LogPanel from "../components/LogPanel.svelte";
  import ResizablePanels from "../components/ResizablePanels.svelte";

  let direction = $derived<"horizontal" | "vertical">($isNarrow ? "vertical" : "horizontal");
</script>

<ResizablePanels {direction} storageKey="models-panel-group">
  {#snippet leftPanel()}
    <ModelsPanel />
  {/snippet}
  {#snippet rightPanel()}
    <LogPanel id="modelsupstream" title="Upstream Logs" logData={$upstreamLogs} />
  {/snippet}
</ResizablePanels>
