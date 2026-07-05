<script lang="ts">
  import { untrack } from "svelte";
  import type { ActivityLogEntry } from "../../lib/types";
  import { activityRevision, getActivity, inflightRequestEntries } from "../../stores/api";
  import { connectionState } from "../../stores/theme";
  import { persistentStore } from "../../stores/persistent";
  import ActivityTable from "../ActivityTable.svelte";

  interface Props {
    modelId: string;
  }

  let { modelId }: Props = $props();

  const storedPageSize = persistentStore<number>("model-detail-activity-page-size", 25);

  let modelMetrics = $state<ActivityLogEntry[]>([]);
  let page = $state(1);
  let limit = $state($storedPageSize);
  let sort = $state("id");
  let order = $state<"asc" | "desc">("desc");
  let total = $state(0);
  let totalPages = $state(0);
  let requestID = 0;

  let modelInflightRequests = $derived(
    $inflightRequestEntries.filter((request) => request.model === modelId)
  );

  async function refreshActivity() {
    const id = ++requestID;
    try {
      const activity = await getActivity({ model: modelId, page, limit, sort, order });
      if (id !== requestID) return;
      modelMetrics = activity.data;
      total = activity.total;
      totalPages = activity.total_pages;
    } catch (error) {
      console.error("Failed to refresh model activity:", error);
    }
  }

  function setPage(nextPage: number) {
    page = nextPage;
  }

  function setPageSize(nextLimit: number) {
    limit = nextLimit;
    page = 1;
    storedPageSize.set(nextLimit);
  }

  function setSort(nextSort: string, nextOrder: "asc" | "desc") {
    sort = nextSort;
    order = nextOrder;
    page = 1;
  }

  $effect(() => {
    if ($connectionState !== "connected") return;
    $activityRevision;
    modelId;
    page;
    limit;
    sort;
    order;
    untrack(() => {
      refreshActivity();
    });
  });
</script>

<ActivityTable
  metrics={modelMetrics}
  inflightRequests={modelInflightRequests}
  storagePrefix="model-detail"
  showModelColumn={false}
  showPagination={true}
  {page}
  {limit}
  {total}
  totalPages={totalPages}
  onPageChange={setPage}
  onPageSizeChange={setPageSize}
  {sort}
  {order}
  onSortChange={setSort}
  compact={true}
  title="Recent Activity"
  emptyMessage="No activity recorded for this model"
/>
