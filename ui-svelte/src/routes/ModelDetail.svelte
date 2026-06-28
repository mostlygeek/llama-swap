<script lang="ts">
  import { params } from "svelte-spa-router";
  import { models, metrics, getCapture, loadModel, unloadSingleModel } from "../stores/api";
  import { streamModelLog } from "../stores/modelLogs";
  import { persistentStore } from "../stores/persistent";
  import { onMount } from "svelte";
  import LogPanel from "../components/LogPanel.svelte";
  import CaptureDialog from "../components/CaptureDialog.svelte";
  import Tooltip from "../components/Tooltip.svelte";
  import MetadataTooltip from "../components/MetadataTooltip.svelte";
  import type { Model, ReqRespCapture } from "../lib/types";
  import * as Card from "$lib/components/ui/card/index.js";
  import * as Select from "$lib/components/ui/select/index.js";
  import * as Tabs from "$lib/components/ui/tabs/index.js";
  import { Button } from "$lib/components/ui/button/index.js";
  import { Play, PowerOff, Loader2, ExternalLink, Columns3, GripVertical } from "@lucide/svelte";

  let modelId = $derived($params?.id ?? "");

  let model = $derived<Model | undefined>($models.find((m) => m.id === modelId));

  const pageSizeStore = persistentStore<number>("model-detail-page-size", 10);
  let page = $state(0);

  let modelMetrics = $derived(
    [...$metrics].filter((m) => m.model === modelId).sort((a, b) => b.id - a.id)
  );

  let totalPages = $derived(Math.max(1, Math.ceil(modelMetrics.length / $pageSizeStore)));
  let pageMetrics = $derived(modelMetrics.slice(page * $pageSizeStore, (page + 1) * $pageSizeStore));

  // Reset page when id or pageSize changes
  $effect(() => {
    modelId;
    $pageSizeStore;
    page = 0;
  });

  let logData = $state("");
  $effect(() => {
    const id = modelId;
    if (!id) {
      logData = "";
      return;
    }
    const store = streamModelLog(id);
    const unsub = store.subscribe((v) => (logData = v));
    return () => unsub();
  });

  function formatSpeed(speed: number): string {
    return speed < 0 ? "unknown" : speed.toFixed(2) + " t/s";
  }

  function formatDuration(ms: number): string {
    return (ms / 1000).toFixed(2) + "s";
  }

  function formatRelativeTime(timestamp: string): string {
    const now = new Date();
    const date = new Date(timestamp);
    const diffInSeconds = Math.floor((now.getTime() - date.getTime()) / 1000);
    if (diffInSeconds < 5) return "now";
    if (diffInSeconds < 60) return `${diffInSeconds}s ago`;
    const diffInMinutes = Math.floor(diffInSeconds / 60);
    if (diffInMinutes < 60) return `${diffInMinutes}m ago`;
    const diffInHours = Math.floor(diffInMinutes / 60);
    if (diffInHours < 24) return `${diffInHours}h ago`;
    return "a while ago";
  }

  function formatDrafted(drafted: number, accepted: number): string {
    return drafted > 0 ? ((accepted * 100) / drafted).toFixed(1) + "% (" + accepted + "/" + drafted + ")" : "-";
  }

  // --- Column customization (ported from Activity.svelte) ---
  type ColumnKey = string;

  interface ColumnDef {
    key: ColumnKey;
    label: string;
    defaultVisible: boolean;
  }

  const columns: ColumnDef[] = [
    { key: "id", label: "ID", defaultVisible: true },
    { key: "time", label: "Time", defaultVisible: true },
    { key: "req_path", label: "Path", defaultVisible: false },
    { key: "resp_status_code", label: "Status", defaultVisible: true },
    { key: "resp_content_type", label: "Content-Type", defaultVisible: false },
    { key: "cached", label: "Cached", defaultVisible: true },
    { key: "prompt", label: "Prompt", defaultVisible: true },
    { key: "generated", label: "Generated", defaultVisible: true },
    { key: "drafted", label: "Drafted", defaultVisible: false },
    { key: "prompt_speed", label: "Prompt Speed", defaultVisible: true },
    { key: "gen_speed", label: "Gen Speed", defaultVisible: true },
    { key: "duration", label: "Duration", defaultVisible: true },
    { key: "capture", label: "Capture", defaultVisible: true },
    { key: "meta", label: "Meta", defaultVisible: false },
  ];

  const defaultVisibleKeys = columns.filter((c) => c.defaultVisible).map((c) => c.key);

  const visibleColumns = persistentStore<ColumnKey[]>("model-detail-columns", defaultVisibleKeys);
  const columnOrder = persistentStore<ColumnKey[]>(
    "model-detail-column-order",
    columns.map((c) => c.key)
  );

  let columnsMenuOpen = $state(false);
  let dropdownContainer: HTMLDivElement | null = $state(null);
  let dragKey: ColumnKey | null = $state(null);
  let dragOverKey: ColumnKey | null = $state(null);

  onMount(() => {
    function handleKeydown(e: KeyboardEvent) {
      if (e.key === "Escape" && columnsMenuOpen) {
        columnsMenuOpen = false;
      }
    }
    function handleClick(e: MouseEvent) {
      if (columnsMenuOpen && dropdownContainer && !dropdownContainer.contains(e.target as Node)) {
        columnsMenuOpen = false;
      }
    }
    document.addEventListener("keydown", handleKeydown);
    document.addEventListener("click", handleClick);
    return () => {
      document.removeEventListener("keydown", handleKeydown);
      document.removeEventListener("click", handleClick);
    };
  });

  function toggleColumn(key: ColumnKey) {
    const current = $visibleColumns;
    if (current.includes(key)) {
      if (current.length > 1) {
        visibleColumns.set(current.filter((k) => k !== key));
      }
    } else {
      visibleColumns.set([...current, key]);
    }
  }

  function isColumnVisible(key: ColumnKey): boolean {
    return $visibleColumns.includes(key);
  }

  function handleDragStart(e: DragEvent, key: ColumnKey) {
    dragKey = key;
    e.dataTransfer?.setData("text/plain", key);
    if (e.dataTransfer) {
      e.dataTransfer.effectAllowed = "move";
    }
  }

  function handleDragOver(e: DragEvent, key: ColumnKey) {
    e.preventDefault();
    if (e.dataTransfer) {
      e.dataTransfer.dropEffect = "move";
    }
    dragOverKey = key;
  }

  function handleDrop(e: DragEvent, targetKey: ColumnKey) {
    e.preventDefault();
    if (!dragKey || dragKey === targetKey) return;
    const order = [...$columnOrder];
    const fromIndex = order.indexOf(dragKey);
    let toIndex = order.indexOf(targetKey);
    if (fromIndex === -1 || toIndex === -1) return;
    order.splice(fromIndex, 1);
    if (fromIndex < toIndex) {
      toIndex -= 1;
    }
    order.splice(toIndex, 0, dragKey);
    columnOrder.set(order);
  }

  function handleDragEnd() {
    dragKey = null;
    dragOverKey = null;
  }

  let orderedColumns = $derived(
    columns.slice().sort((a, b) => {
      const aIndex = $columnOrder.indexOf(a.key);
      const bIndex = $columnOrder.indexOf(b.key);
      if (aIndex === -1 && bIndex === -1) return 0;
      if (aIndex === -1) return 1;
      if (bIndex === -1) return -1;
      return aIndex - bIndex;
    })
  );

  let activeVisibleColumns = $derived(
    columns
      .filter((c) => isColumnVisible(c.key))
      .sort((a, b) => {
        const aIndex = $columnOrder.indexOf(a.key);
        const bIndex = $columnOrder.indexOf(b.key);
        if (aIndex === -1 && bIndex === -1) return 0;
        if (aIndex === -1) return 1;
        if (bIndex === -1) return -1;
        return aIndex - bIndex;
      })
      .map((c) => c.key)
  );

  let columnLabelMap = $derived(Object.fromEntries(columns.map((c) => [c.key, c.label])));

  $effect(() => {
    const staticKeys = new Set(columns.map((c) => c.key));
    const order = $columnOrder;
    const hasStale = order.some((k) => !staticKeys.has(k));
    const missing = columns.filter((c) => !order.includes(c.key)).map((c) => c.key);
    if (hasStale || missing.length > 0) {
      const cleaned = order.filter((k) => staticKeys.has(k));
      columnOrder.set([...cleaned, ...missing]);
    }
  });

  const capabilityLabels: Record<string, string> = {
    vision: "Vision",
    audio_transcriptions: "Transcription",
    audio_speech: "Speech",
    image_generation: "Image Gen",
    image_to_image: "Img→Img",
    function_calling: "Function Calling",
    reranker: "Reranker",
  };

  let capabilities = $derived.by(() => {
    const caps = model?.capabilities ?? {};
    const entries = Object.entries(caps).filter(([, v]) => v);
    return entries;
  });

  let selectedCapture = $state<ReqRespCapture | null>(null);
  let dialogOpen = $state(false);
  let loadingCaptureId = $state<number | null>(null);

  async function viewCapture(id: number) {
    loadingCaptureId = id;
    const capture = await getCapture(id);
    loadingCaptureId = null;
    selectedCapture = capture;
    dialogOpen = true;
  }

  function closeDialog() {
    dialogOpen = false;
    selectedCapture = null;
  }

  function statusDotColor(m: Model | undefined): string {
    if (!m) return "bg-muted-foreground/40";
    if (m.state === "ready") return "bg-success";
    if (m.state === "starting" || m.state === "stopping") return "bg-warning";
    return "bg-muted-foreground/40";
  }

  // Load / unload orchestration (ported from AppSidebar.svelte)
  let pendingLoads = $state<Record<string, boolean>>({});
  const loadControllers = new Map<string, AbortController>();

  async function handleLoadModel(id: string): Promise<void> {
    if (pendingLoads[id]) return;
    const controller = new AbortController();
    loadControllers.set(id, controller);
    pendingLoads[id] = true;
    try {
      await loadModel(id, controller.signal);
    } catch (e) {
      console.error(e);
    } finally {
      loadControllers.delete(id);
      delete pendingLoads[id];
    }
  }

  function cancelLoad(id: string): void {
    loadControllers.get(id)?.abort();
  }

  function onToggleLoad(e: MouseEvent, m: Model): void {
    e.preventDefault();
    e.stopPropagation();
    if (m.state === "stopped" && pendingLoads[m.id]) {
      cancelLoad(m.id);
    } else if (m.state === "stopped") {
      handleLoadModel(m.id);
    } else if (m.state === "ready") {
      unloadSingleModel(m.id);
    }
  }
</script>

<div class="flex h-full flex-col gap-4 overflow-y-auto p-2">
  {#if !model}
    <Card.Root class="shrink-0 p-6">
      <p class="text-muted-foreground">Model “{modelId}” not found.</p>
      <a href="/" class="text-primary hover:underline">Back to Playground</a>
    </Card.Root>
   {:else}
    <Card.Root class="shrink-0 gap-0 overflow-hidden rounded-none py-0">
      <Card.Header class="shrink-0 gap-2 border-b px-4 py-3">
        <div class="flex items-center gap-2">
          <span class={`size-2.5 shrink-0 rounded-full ${statusDotColor(model)}`}></span>
          <Card.Title class="text-lg">{model.name || model.id}</Card.Title>
          <span class="text-muted-foreground text-sm">({model.id})</span>
          <span class="text-muted-foreground text-xs uppercase tracking-wide">{model.state}</span>
          <div class="ml-auto flex items-center gap-2">
            <a
              href={`/upstream/${encodeURIComponent(modelId)}/`}
              target="_blank"
              rel="noopener noreferrer"
              class="text-muted-foreground hover:text-foreground"
              title="Open model server"
              aria-label="Open model server"
            >
              <ExternalLink class="size-4" />
            </a>
            <button
              type="button"
              class="flex size-5 shrink-0 items-center justify-center rounded-sm text-muted-foreground hover:bg-accent hover:text-accent-foreground disabled:opacity-50"
              title={model.state === "ready" ? "Unload" : pendingLoads[model.id] ? "Cancel" : "Load"}
              aria-label={model.state === "ready" ? "Unload model" : "Load model"}
              disabled={model.state === "starting" || model.state === "stopping"}
              onclick={(e) => onToggleLoad(e, model)}
            >
              {#if pendingLoads[model.id] && model.state === "stopped"}
                <Loader2 class="size-3.5 animate-spin" />
              {:else if model.state === "ready"}
                <PowerOff class="size-3.5" />
              {:else if model.state === "starting" || model.state === "stopping"}
                <Loader2 class="size-3.5 animate-spin" />
              {:else}
                <Play class="size-3.5" />
              {/if}
            </button>
          </div>
        </div>
        {#if model.description}
          <p class="text-muted-foreground text-sm"><em>{model.description}</em></p>
        {/if}
        {#if model.aliases && model.aliases.length > 0}
          <p class="text-muted-foreground text-xs">Aliases: {model.aliases.join(", ")}</p>
        {/if}
      </Card.Header>
    </Card.Root>

    <Tabs.Root value="activity" class="min-h-0 flex-1">
      <Tabs.List>
        <Tabs.Trigger value="activity">Activity</Tabs.Trigger>
        <Tabs.Trigger value="logs">Logs</Tabs.Trigger>
        <Tabs.Trigger value="details">Details</Tabs.Trigger>
      </Tabs.List>

      <!-- Activity -->
      <Tabs.Content value="activity">
        <Card.Root class="shrink-0 gap-0 overflow-hidden py-0">
          <Card.Header class="flex items-center justify-between border-b px-4 py-2">
            <Card.Title class="text-sm font-semibold">
              Recent Activity
              <span class="text-muted-foreground text-xs font-normal">({modelMetrics.length})</span>
            </Card.Title>
            <div class="flex items-center gap-2">
              <span class="text-muted-foreground text-xs">Per page</span>
              <Select.Root
                type="single"
                value={String($pageSizeStore)}
                onValueChange={(v) => pageSizeStore.set(Number(v))}
              >
                <Select.Trigger class="h-7 w-16 text-xs">{$pageSizeStore}</Select.Trigger>
                <Select.Content>
                  {#each [5, 10, 25, 50] as size (size)}
                    <Select.Item value={String(size)}>{size}</Select.Item>
                  {/each}
                </Select.Content>
              </Select.Root>
              <div bind:this={dropdownContainer}>
                <div class="relative">
                  <Button
                    variant="ghost"
                    size="icon-sm"
                    onclick={() => (columnsMenuOpen = !columnsMenuOpen)}
                    title="Select columns"
                  >
                    <Columns3 />
                  </Button>
                  {#if columnsMenuOpen}
                    <div
                      class="bg-popover text-popover-foreground absolute right-0 top-full z-20 mt-1 min-w-[16rem] rounded-md border py-1 shadow-md"
                      role="list"
                    >
                      <div
                        class="text-muted-foreground border-b px-3 py-2 text-xs font-medium uppercase tracking-wider"
                        role="presentation"
                      >
                        Columns
                      </div>
                      {#each orderedColumns as col (col.key)}
                        {@const key = col.key}
                        <div
                          class="hover:bg-accent flex items-center gap-2 px-3 py-1.5 text-sm transition-colors {dragOverKey ===
                            key && dragKey !== key
                            ? 'bg-primary/10 ring-primary/40 ring-1'
                            : ''} {dragKey === key ? 'opacity-40' : ''}"
                          role="listitem"
                          ondragover={(e) => handleDragOver(e, key)}
                          ondrop={(e) => handleDrop(e, key)}
                        >
                          <span
                            class="text-muted-foreground flex cursor-grab select-none"
                            draggable={true}
                            role="button"
                            tabindex="-1"
                            aria-label="Drag to reorder {col.label}"
                            ondragstart={(e) => handleDragStart(e, key)}
                            ondragend={handleDragEnd}
                          >
                            <GripVertical class="size-4" />
                          </span>
                          <label class="flex flex-1 cursor-pointer items-center gap-2">
                            <input
                              type="checkbox"
                              checked={isColumnVisible(key)}
                              onchange={() => toggleColumn(key)}
                              class="accent-primary rounded"
                            />
                            {col.label}
                          </label>
                        </div>
                      {/each}
                    </div>
                  {/if}
                </div>
              </div>
            </div>
          </Card.Header>
          <Card.Content class="overflow-x-auto p-0">
            <table class="min-w-full text-sm">
              <thead class="text-muted-foreground border-b text-left text-xs uppercase tracking-wider">
                <tr>
                  {#each activeVisibleColumns as key (key)}
                    <th class="px-4 py-2 font-medium">
                      {#if key === "cached"}
                        Cached <Tooltip content="prompt tokens from cache" />
                      {:else if key === "prompt"}
                        Prompt <Tooltip content="new prompt tokens processed" />
                      {:else if key === "drafted"}
                        Drafted <Tooltip content="acceptance rate (accepted/drafted)" />
                      {:else}
                        {columnLabelMap[key] ?? key}
                      {/if}
                    </th>
                  {/each}
                </tr>
              </thead>
              <tbody>
                {#if pageMetrics.length === 0}
                  <tr>
                    <td colspan={activeVisibleColumns.length} class="text-muted-foreground px-4 py-6 text-center text-sm">
                      No activity recorded for this model
                    </td>
                  </tr>
                {:else}
                  {#each pageMetrics as metric (metric.id)}
                    <tr class="hover:bg-muted/50 whitespace-nowrap border-b">
                      {#each activeVisibleColumns as key (key)}
                        <td class="px-4 py-2">
                          {#if key === "id"}
                            {metric.id + 1}
                          {:else if key === "time"}
                            {formatRelativeTime(metric.timestamp)}
                          {:else if key === "req_path"}
                            {metric.req_path || "-"}
                          {:else if key === "resp_status_code"}
                            {#if metric.error_msg}
                              <span class="text-destructive cursor-help" title={metric.error_msg}>
                                {metric.resp_status_code || "-"}
                              </span>
                            {:else}
                              {metric.resp_status_code || "-"}
                            {/if}
                          {:else if key === "resp_content_type"}
                            {metric.resp_content_type || "-"}
                          {:else if key === "cached"}
                            {metric.tokens.cache_tokens > 0 ? metric.tokens.cache_tokens.toLocaleString() : "-"}
                          {:else if key === "prompt"}
                            {metric.tokens.input_tokens.toLocaleString()}
                          {:else if key === "generated"}
                            {metric.tokens.output_tokens.toLocaleString()}
                          {:else if key === "drafted"}
                            {formatDrafted(metric.tokens.draft_tokens, metric.tokens.draft_acc_tokens)}
                          {:else if key === "prompt_speed"}
                            {formatSpeed(metric.tokens.prompt_per_second)}
                          {:else if key === "gen_speed"}
                            {formatSpeed(metric.tokens.tokens_per_second)}
                          {:else if key === "duration"}
                            {formatDuration(metric.duration_ms)}
                          {:else if key === "capture"}
                            {#if metric.has_capture}
                              <Button
                                variant="outline"
                                size="xs"
                                onclick={() => viewCapture(metric.id)}
                                disabled={loadingCaptureId === metric.id}
                              >
                                {loadingCaptureId === metric.id ? "..." : "View"}
                              </Button>
                            {:else}
                              <span class="text-muted-foreground">-</span>
                            {/if}
                          {:else if key === "meta"}
                            {#if Object.keys(metric.metadata || {}).length > 0}
                              <MetadataTooltip metadata={metric.metadata}>
                                <span class="text-muted-foreground hover:text-foreground cursor-help">...</span>
                              </MetadataTooltip>
                            {:else}
                              <span class="text-muted-foreground">-</span>
                            {/if}
                          {:else}
                            -
                          {/if}
                        </td>
                      {/each}
                    </tr>
                  {/each}
                {/if}
              </tbody>
            </table>

            {#if modelMetrics.length > 0}
              <div class="flex items-center justify-between gap-2 border-t px-4 py-2 text-sm">
                <span class="text-muted-foreground text-xs">
                  Page {page + 1} of {totalPages} · {modelMetrics.length} total
                </span>
                <div class="flex gap-1">
                  <Button variant="outline" size="sm" onclick={() => (page = 0)} disabled={page === 0}>
                    First
                  </Button>
                  <Button
                    variant="outline"
                    size="sm"
                    onclick={() => (page = Math.max(0, page - 1))}
                    disabled={page === 0}
                  >
                    Prev
                  </Button>
                  <Button
                    variant="outline"
                    size="sm"
                    onclick={() => (page = Math.min(totalPages - 1, page + 1))}
                    disabled={page >= totalPages - 1}
                  >
                    Next
                  </Button>
                  <Button
                    variant="outline"
                    size="sm"
                    onclick={() => (page = totalPages - 1)}
                    disabled={page >= totalPages - 1}
                  >
                    Last
                  </Button>
                </div>
              </div>
            {/if}
          </Card.Content>
        </Card.Root>
      </Tabs.Content>

      <!-- Logs -->
      <Tabs.Content value="logs" class="h-80">
        <LogPanel id={`model-${modelId}`} title="Model Logs" logData={logData} />
      </Tabs.Content>

      <!-- Details -->
      <Tabs.Content value="details">
        <Card.Root class="shrink-0 gap-0 overflow-hidden py-0">
          <Card.Header class="border-b px-4 py-2">
            <Card.Title class="text-sm font-semibold">Capabilities</Card.Title>
          </Card.Header>
          <Card.Content class="p-3">
            {#if capabilities.length === 0}
              <span class="text-muted-foreground text-sm">No capabilities reported.</span>
            {:else}
              <div class="flex flex-wrap gap-1.5">
                {#each capabilities as [key] (key)}
                  <span class="bg-muted text-muted-foreground rounded-md px-2 py-0.5 text-xs font-medium">
                    {capabilityLabels[key] ?? key}
                  </span>
                {/each}
              </div>
            {/if}
          </Card.Content>
        </Card.Root>
      </Tabs.Content>
    </Tabs.Root>
  {/if}
</div>

<CaptureDialog capture={selectedCapture} open={dialogOpen} onclose={closeDialog} />
