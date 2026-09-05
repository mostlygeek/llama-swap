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
  import { Tabs, TabsContent, TabsList, TabsTrigger } from "$lib/components/ui/tabs/index.js";

  const MASKED_TOKEN = "******";

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
  let configCopied = $state(false);
  let configCopyTimer: ReturnType<typeof setTimeout> | null = null;

  function peerConfigYaml(address: string, models: string[]): string {
    const modelLines =
      models.length > 0 ? models.map((model) => `      - ${model}`).join("\n") : "      - REPLACE_WITH_MODEL_ID";
    // tailcatKey is commented out: without it, connecting uses an ephemeral
    // client identity, so nobody's private key ends up in a shared snippet.
    // The commented line shows how to generate a stable one if this server
    // allowlists callers.
    return `peers:\n  friend:\n    proxy: tailcat://${address}\n    # generate with: tailcat genkey --client --key=/path/to/client.private.json\n    # tailcatKey: /path/to/client.private.json\n    models:\n${modelLines}\n`;
  }

  async function copyPeerConfig() {
    const yaml = peerConfigYaml($tailcatStatus.address, $tailcatStatus.models);
    if (!(await copyText(yaml))) return;
    configCopied = true;
    if (configCopyTimer !== null) clearTimeout(configCopyTimer);
    configCopyTimer = setTimeout(() => { configCopied = false; }, 1800);
  }

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
    if (configCopyTimer !== null) clearTimeout(configCopyTimer);
  });
</script>

<div class="space-y-4 p-2">
  <Card.Root>
    <Card.Content>
      {#if $tailcatStatus.enabled && $tailcatStatus.address}
        <Tabs value="token">
          <TabsList variant="line">
            <TabsTrigger value="token">Token</TabsTrigger>
            <TabsTrigger value="peer-config">Peer Config</TabsTrigger>
          </TabsList>

          <TabsContent value="token" class="mt-4 space-y-2">
            <p class="text-muted-foreground text-sm">
              Treat this case-sensitive token like a capability: anyone holding it can attempt to connect.
            </p>
            <div class="relative">
              <code class="bg-muted block overflow-x-auto rounded-md px-3 py-2 pr-10 text-xs">{MASKED_TOKEN}</code>
              <Button
                variant="secondary"
                size="icon-sm"
                onclick={copyAddress}
                aria-label="Copy Tailcat connection token"
                class="absolute top-1 right-1"
              >
                {#if copied}<Check class="size-4 text-green-600" />{:else}<Copy class="size-4" />{/if}
              </Button>
            </div>
          </TabsContent>

          <TabsContent value="peer-config" class="mt-4 space-y-2">
            <p class="text-muted-foreground text-sm">
              Copy a ready-to-paste <code>peers</code> entry, including the exposed models, for a friend to add to
              their own config so they can route requests through this server.
            </p>
            <div class="relative">
              <code class="bg-muted block overflow-x-auto rounded-md px-3 py-2 pr-10 text-xs whitespace-pre"
                >{peerConfigYaml(MASKED_TOKEN, $tailcatStatus.models)}</code
              >
              <Button
                variant="secondary"
                size="icon-sm"
                onclick={copyPeerConfig}
                aria-label="Copy peer configuration"
                class="absolute top-1 right-1"
              >
                {#if configCopied}<Check class="size-4 text-green-600" />{:else}<Copy class="size-4" />{/if}
              </Button>
            </div>
          </TabsContent>
        </Tabs>
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
