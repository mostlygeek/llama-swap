<script lang="ts">
  import { connectLogStreams, httpLogs, proxyLogs, upstreamLogs } from "../stores/logs";
  import { screenWidth } from "../stores/theme";
  import { persistentStore } from "../stores/persistent";
  import LogPanel from "../components/LogPanel.svelte";
  import ResizablePanels from "../components/ResizablePanels.svelte";
  import { Tabs, TabsList, TabsTrigger, TabsContent } from "$lib/components/ui/tabs/index.js";
  import type { LogSource } from "../lib/types";

  type ViewMode = "proxy" | "upstream" | "http" | "panels";

  const viewModeStore = persistentStore<ViewMode>("logviewer-view-mode", "panels");

  // Only the streams the current tab shows are connected. The connection is
  // closed when the tab changes or the page is left.
  const streamsFor: Record<ViewMode, LogSource[]> = {
    panels: ["proxy", "upstream"],
    proxy: ["proxy"],
    upstream: ["upstream"],
    http: ["http"],
  };

  $effect(() => {
    return connectLogStreams(streamsFor[$viewModeStore] ?? streamsFor.panels);
  });

  let direction = $derived<"horizontal" | "vertical">(
    $screenWidth === "xs" || $screenWidth === "sm" ? "vertical" : "horizontal",
  );
</script>

<div class="flex flex-col h-full w-full gap-2">
  <Tabs
    value={$viewModeStore}
    onValueChange={(v) => v && viewModeStore.set(v as ViewMode)}
    class="flex flex-1 w-full flex-col gap-2 overflow-hidden"
  >
    <TabsList variant="line">
      <TabsTrigger value="panels">Both</TabsTrigger>
      <TabsTrigger value="proxy">Proxy</TabsTrigger>
      <TabsTrigger value="upstream">Upstream</TabsTrigger>
      <TabsTrigger value="http">HTTP</TabsTrigger>
    </TabsList>

    <div class="flex-1 w-full overflow-hidden">
      <TabsContent value="panels" class="h-full">
        <ResizablePanels {direction} storageKey="logviewer-panel-group">
          {#snippet leftPanel()}
            <LogPanel id="proxy" title="Proxy Logs" logData={$proxyLogs} />
          {/snippet}
          {#snippet rightPanel()}
            <LogPanel id="upstream" title="Upstream Logs" logData={$upstreamLogs} />
          {/snippet}
        </ResizablePanels>
      </TabsContent>

      <TabsContent value="proxy" class="h-full">
        <LogPanel id="proxy" title="Proxy Logs" logData={$proxyLogs} />
      </TabsContent>

      <TabsContent value="upstream" class="h-full">
        <LogPanel id="upstream" title="Upstream Logs" logData={$upstreamLogs} />
      </TabsContent>

      <TabsContent value="http" class="h-full">
        <LogPanel id="http" title="HTTP Logs" logData={$httpLogs} />
      </TabsContent>
    </div>
  </Tabs>
</div>
