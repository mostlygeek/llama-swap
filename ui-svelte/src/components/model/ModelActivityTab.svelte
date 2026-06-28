<script lang="ts">
  import { metrics } from "../../stores/api";
  import ActivityTable from "../ActivityTable.svelte";

  interface Props {
    modelId: string;
  }

  let { modelId }: Props = $props();

  let modelMetrics = $derived(
    [...$metrics].filter((m) => m.model === modelId).sort((a, b) => b.id - a.id)
  );
</script>

<ActivityTable
  metrics={modelMetrics}
  storagePrefix="model-detail"
  showModelColumn={false}
  showPagination={true}
  compact={true}
  title="Recent Activity"
  emptyMessage="No activity recorded for this model"
/>
