<script lang="ts">
  import { proxyLogs, upstreamLogs } from "../stores/api";
  import { screenWidth } from "../stores/theme";
  import { persistentStore } from "../stores/persistent";
  import LogPanel from "../components/LogPanel.svelte";
  import ResizablePanels from "../components/ResizablePanels.svelte";
  import * as ToggleGroup from "$lib/components/ui/toggle-group/index.js";

  type ViewMode = "proxy" | "upstream" | "panels";

  const viewModeStore = persistentStore<ViewMode>("logviewer-view-mode", "panels");

  let direction = $derived<"horizontal" | "vertical">(
    $screenWidth === "xs" || $screenWidth === "sm" ? "vertical" : "horizontal",
  );
</script>

<div class="flex flex-col h-full w-full gap-2">
  <ToggleGroup.Root
    type="single"
    variant="outline"
    value={$viewModeStore}
    onValueChange={(v) => v && viewModeStore.set(v as ViewMode)}
    class="justify-start"
  >
    <ToggleGroup.Item value="panels">Both</ToggleGroup.Item>
    <ToggleGroup.Item value="proxy">Proxy</ToggleGroup.Item>
    <ToggleGroup.Item value="upstream">Upstream</ToggleGroup.Item>
  </ToggleGroup.Root>

  <div class="flex-1 w-full overflow-hidden">
    {#if $viewModeStore === "panels"}
      <ResizablePanels {direction} storageKey="logviewer-panel-group">
        {#snippet leftPanel()}
          <LogPanel id="proxy" title="Proxy Logs" logData={$proxyLogs} />
        {/snippet}
        {#snippet rightPanel()}
          <LogPanel id="upstream" title="Upstream Logs" logData={$upstreamLogs} />
        {/snippet}
      </ResizablePanels>
    {:else if $viewModeStore === "proxy"}
      <LogPanel id="proxy" title="Proxy Logs" logData={$proxyLogs} />
    {:else}
      <LogPanel id="upstream" title="Upstream Logs" logData={$upstreamLogs} />
    {/if}
  </div>
</div>
