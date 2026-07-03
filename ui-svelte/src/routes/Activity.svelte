<script lang="ts">
  import { inflightRequestEntries, metrics } from "../stores/api";
  import ActivityStats from "../components/ActivityStats.svelte";
  import ActivityTable from "../components/ActivityTable.svelte";

  let sortedMetrics = $derived([...$metrics].sort((a, b) => b.id - a.id));
</script>

<div class="p-2">
  <div class="mt-4 mb-4">
    <ActivityStats />
  </div>

  <ActivityTable
    metrics={sortedMetrics}
    inflightRequests={$inflightRequestEntries}
    storagePrefix="activity"
    showModelColumn={true}
    showPagination={true}
    cardClass="min-h-[30rem] overflow-auto"
    emptyMessage="No activity recorded"
  />
</div>
