<script lang="ts">
  import { proxyLogs, upstreamLogs } from "../stores/api";
  import { screenWidth } from "../stores/theme";
  import { persistentStore } from "../stores/persistent";
  import LogPanel from "../components/LogPanel.svelte";
  import ResizablePanels from "../components/ResizablePanels.svelte";

  type ViewMode = "proxy" | "upstream" | "panels";

  const viewModeStore = persistentStore<ViewMode>("logviewer-view-mode", "panels");

  let direction = $derived<"horizontal" | "vertical">(
    $screenWidth === "xs" || $screenWidth === "sm" ? "vertical" : "horizontal"
  );

  function cycleViewMode(): void {
    const modes: ViewMode[] = ["panels", "proxy", "upstream"];
    const currentIndex = modes.indexOf($viewModeStore);
    const nextIndex = (currentIndex + 1) % modes.length;
    viewModeStore.set(modes[nextIndex]);
  }

  function getViewModeIcon(mode: ViewMode): string {
    switch (mode) {
      case "proxy":
        return "P";
      case "upstream":
        return "U";
      case "panels":
        return "⊞";
    }
  }

  function getViewModeLabel(mode: ViewMode): string {
    switch (mode) {
      case "proxy":
        return "Proxy";
      case "upstream":
        return "Upstream";
      case "panels":
        return "Panels";
    }
  }
</script>

<div class="flex flex-col h-full w-full gap-2">
  <div class="flex items-center gap-2">
    <button
      onclick={cycleViewMode}
      class="btn flex items-center gap-2 text-sm"
      title="Toggle view mode"
      aria-label="Toggle view mode: {getViewModeLabel($viewModeStore)}"
    >
      <span class="font-mono font-bold">{getViewModeIcon($viewModeStore)}</span>
      <span>{getViewModeLabel($viewModeStore)}</span>
    </button>
  </div>

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
