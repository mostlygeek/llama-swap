<script lang="ts">
  import { inflightRequestEntries, metrics } from "../../stores/api";
  import ActivityTable from "../ActivityTable.svelte";

  interface Props {
    modelId: string;
  }

  let { modelId }: Props = $props();

  let modelMetrics = $derived(
    [...$metrics].filter((m) => m.model === modelId).sort((a, b) => b.id - a.id)
  );
  let modelInflightRequests = $derived(
    $inflightRequestEntries.filter((request) => request.model === modelId)
  );
</script>

<ActivityTable
  metrics={modelMetrics}
  inflightRequests={modelInflightRequests}
  storagePrefix="model-detail"
  showModelColumn={false}
  showPagination={true}
  compact={true}
  title="Recent Activity"
  emptyMessage="No activity recorded for this model"
/>
