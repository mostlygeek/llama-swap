<script lang="ts">
  import { upstreamLogs } from "../stores/api";
  import StatsPanel from "./StatsPanel.svelte";
  import LogPanel from "./LogPanel.svelte";

  interface Props {
    showStats: boolean;
  }

  let { showStats }: Props = $props();
</script>

<div class="flex flex-col h-full space-y-4">
  {#if showStats}
    <StatsPanel />
  {/if}
  <div class="flex-1 min-h-0">
    <LogPanel id="modelsupstream" title="Upstream Logs" logData={$upstreamLogs} />
  </div>
</div>
