<script lang="ts">
  import { isNarrow } from "../stores/theme";
  import ModelsPanel from "../components/ModelsPanel.svelte";
  import ModelsRightPanel from "../components/ModelsRightPanel.svelte";
  import ResizablePanels from "../components/ResizablePanels.svelte";

  let direction = $derived<"horizontal" | "vertical">($isNarrow ? "vertical" : "horizontal");
</script>

<ResizablePanels {direction} storageKey="models-panel-group">
  {#snippet leftPanel()}
    <ModelsPanel />
  {/snippet}
  {#snippet rightPanel()}
    <ModelsRightPanel showStats={direction === "horizontal"} />
  {/snippet}
</ResizablePanels>
