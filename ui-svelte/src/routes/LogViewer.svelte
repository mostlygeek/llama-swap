<script lang="ts">
  import { proxyLogs, upstreamLogs } from "../stores/api";
  import { screenWidth } from "../stores/theme";
  import LogPanel from "../components/LogPanel.svelte";
  import ResizablePanels from "../components/ResizablePanels.svelte";

  let direction = $derived<"horizontal" | "vertical">($screenWidth === "xs" || $screenWidth === "sm" ? "vertical" : "horizontal");
</script>

<ResizablePanels {direction} storageKey="logviewer-panel-group">
  {#snippet leftPanel()}
    <LogPanel id="proxy" title="Proxy Logs" logData={$proxyLogs} />
  {/snippet}
  {#snippet rightPanel()}
    <LogPanel id="upstream" title="Upstream Logs" logData={$upstreamLogs} />
  {/snippet}
</ResizablePanels>
