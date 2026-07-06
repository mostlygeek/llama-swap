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
  let refreshTimer: ReturnType<typeof setTimeout> | null = null;
  let lastRefresh = 0;

  let modelInflightRequests = $derived(
    $inflightRequestEntries.filter((request) => request.model === modelId)
  );

  async function refreshActivity() {
    if (refreshTimer !== null) {
      clearTimeout(refreshTimer);
      refreshTimer = null;
    }
    lastRefresh = Date.now();
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

  // scheduleRefresh throttles SSE-driven refreshes to one per second; a
  // user-driven refreshActivity cancels any pending timer.
  function scheduleRefresh() {
    if (refreshTimer !== null) return;
    const wait = Math.max(0, 1000 - (Date.now() - lastRefresh));
    refreshTimer = setTimeout(() => {
      refreshTimer = null;
      refreshActivity();
    }, wait);
  }

  // Refresh immediately on connect and when the user changes the model or
  // paging/sorting.
  $effect(() => {
    if ($connectionState !== "connected") return;
    modelId;
    page;
    limit;
    sort;
    order;
    untrack(() => {
      refreshActivity();
    });
  });

  // New activity only changes what page 1 shows; skip SSE-driven refreshes
  // while browsing older pages. seenRevision keeps the effect's initial run
  // (and re-runs from other deps) from scheduling a redundant fetch.
  let seenRevision = $activityRevision;
  $effect(() => {
    if ($connectionState !== "connected") return;
    const revision = $activityRevision;
    untrack(() => {
      if (revision === seenRevision) return;
      seenRevision = revision;
      if (page === 1) scheduleRefresh();
    });
  });

  $effect(() => {
    return () => {
      if (refreshTimer !== null) clearTimeout(refreshTimer);
    };
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
