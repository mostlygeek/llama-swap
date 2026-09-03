<script lang="ts">
  import { untrack } from "svelte";
  import { Check, Copy } from "@lucide/svelte";
  import type { ActivityLogEntry } from "../lib/types";
  import { activityRevision, getActivity, tailcatStatus } from "../stores/api";
  import { connectionState } from "../stores/theme";
  import { persistentStore } from "../stores/persistent";
  import { copyText } from "../lib/clipboard";
  import ActivityTable from "../components/ActivityTable.svelte";
  import { Button } from "$lib/components/ui/button/index.js";
  import * as Card from "$lib/components/ui/card/index.js";

  const storedPageSize = persistentStore<number>("tailcat-activity-page-size", 25);
  let rows = $state<ActivityLogEntry[]>([]);
  let page = $state(1);
  let limit = $state($storedPageSize);
  let sort = $state("id");
  let order = $state<"asc" | "desc">("desc");
  let total = $state(0);
  let totalPages = $state(0);
  let requestID = 0;
  let copied = $state(false);
  let copyTimer: ReturnType<typeof setTimeout> | null = null;

  async function refreshActivity() {
    const id = ++requestID;
    try {
      const activity = await getActivity({ page, limit, sort, order, srcPrefix: "tc:" });
      if (id !== requestID) return;
      rows = activity.data;
      total = activity.total;
      totalPages = activity.total_pages;
    } catch (error) {
      console.error("Failed to refresh Tailcat activity:", error);
    }
  }

  function setPage(next: number) { page = next; }
  function setPageSize(next: number) {
    limit = next;
    page = 1;
    storedPageSize.set(next);
  }
  function setSort(nextSort: string, nextOrder: "asc" | "desc") {
    sort = nextSort;
    order = nextOrder;
    page = 1;
  }

  async function copyAddress() {
    if (!$tailcatStatus.address || !(await copyText($tailcatStatus.address))) return;
    copied = true;
    if (copyTimer !== null) clearTimeout(copyTimer);
    copyTimer = setTimeout(() => { copied = false; }, 1800);
  }

  $effect(() => {
    if ($connectionState !== "connected") return;
    page; limit; sort; order;
    untrack(() => void refreshActivity());
  });

  let seenRevision = $activityRevision;
  $effect(() => {
    const revision = $activityRevision;
    untrack(() => {
      if ($connectionState !== "connected" || revision === seenRevision) return;
      seenRevision = revision;
      if (page === 1) void refreshActivity();
    });
  });

  $effect(() => () => {
    if (copyTimer !== null) clearTimeout(copyTimer);
  });
</script>

<div class="space-y-4 p-2">
  <Card.Root>
    <Card.Header>
      <Card.Title>Tailcat connection token</Card.Title>
      <Card.Description>
        Treat this case-sensitive token like a capability: anyone holding it can attempt to connect.
      </Card.Description>
    </Card.Header>
    <Card.Content>
      {#if $tailcatStatus.enabled && $tailcatStatus.address}
        <div class="flex items-center gap-2">
          <code class="bg-muted min-w-0 flex-1 overflow-x-auto rounded-md px-3 py-2 text-xs">{$tailcatStatus.address}</code>
          <Button variant="outline" size="sm" onclick={copyAddress} aria-label="Copy Tailcat connection token">
            {#if copied}<Check class="size-4" /> Copied{:else}<Copy class="size-4" /> Copy{/if}
          </Button>
        </div>
      {:else}
        <p class="text-muted-foreground text-sm">Tailcat is not currently available.</p>
      {/if}
    </Card.Content>
  </Card.Root>

  <ActivityTable
    metrics={rows}
    storagePrefix="tailcat-activity"
    showModelColumn={true}
    showSourceColumn={true}
    showInflight={false}
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
    title="Tailcat activity"
    emptyMessage="No Tailcat activity recorded"
    cardClass="min-h-[30rem] overflow-auto"
  />
</div>
