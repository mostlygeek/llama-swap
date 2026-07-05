<script lang="ts">
  import { untrack } from "svelte";
  import type { ActivityLogEntry, ActivityStatsData } from "../lib/types";
  import { activityRevision, getActivity, getActivityStats, inflightRequestEntries } from "../stores/api";
  import { connectionState } from "../stores/theme";
  import { persistentStore } from "../stores/persistent";
  import ActivityStats from "../components/ActivityStats.svelte";
  import ActivityTable from "../components/ActivityTable.svelte";

  const storedPageSize = persistentStore<number>("activity-page-size", 25);

  let rows = $state<ActivityLogEntry[]>([]);
  let stats = $state<ActivityStatsData | null>(null);
  let page = $state(1);
  let limit = $state($storedPageSize);
  let sort = $state("id");
  let order = $state<"asc" | "desc">("desc");
  let total = $state(0);
  let totalPages = $state(0);
  let requestID = 0;

  async function refreshActivity() {
    const id = ++requestID;
    try {
      const [activity, activityStats] = await Promise.all([
        getActivity({ page, limit, sort, order }),
        getActivityStats(),
      ]);
      if (id !== requestID) return;
      rows = activity.data;
      total = activity.total;
      totalPages = activity.total_pages;
      stats = activityStats;
    } catch (error) {
      console.error("Failed to refresh activity:", error);
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
    page;
    limit;
    sort;
    order;
    untrack(() => {
      refreshActivity();
    });
  });
</script>

<div class="p-2">
  <div class="mt-4 mb-4">
    <ActivityStats {stats} />
  </div>

  <ActivityTable
    metrics={rows}
    inflightRequests={$inflightRequestEntries}
    storagePrefix="activity"
    showModelColumn={true}
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
    cardClass="min-h-[30rem] overflow-auto"
    emptyMessage="No activity recorded"
  />
</div>
