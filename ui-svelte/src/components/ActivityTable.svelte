<script lang="ts">
  import type { ActivityLogEntry, InflightRequestEntry, ReqRespCapture } from "../lib/types";
  import { cancelInflightRequest, getCapture, uiConfig } from "../stores/api";
  import { persistentStore } from "../stores/persistent";
  import CaptureDialog from "./CaptureDialog.svelte";
  import {
    type ColumnDef,
    type SortingState,
    type VisibilityState,
    getCoreRowModel,
    getSortedRowModel,
  } from "@tanstack/table-core";
  import {
    FlexRender,
    createSvelteTable,
    renderComponent,
  } from "$lib/components/ui/data-table/index.js";
  import * as Table from "$lib/components/ui/table/index.js";
  import * as Card from "$lib/components/ui/card/index.js";
  import * as Select from "$lib/components/ui/select/index.js";
  import * as DropdownMenu from "$lib/components/ui/dropdown-menu/index.js";
  import { Button } from "$lib/components/ui/button/index.js";
  import {
    Columns3,
    ChevronDown,
    ChevronLeft,
    ChevronRight,
    ChevronsLeft,
    ChevronsRight,
    ArrowUp,
    ArrowDown,
    ArrowUpDown,
    CircleX,
    GripVertical,
    X,
  } from "@lucide/svelte";
  import HeaderLabel from "./activity-table/HeaderLabel.svelte";
  import ViewCaptureButton from "./activity-table/ViewCaptureButton.svelte";
  import MetaCell from "./activity-table/MetaCell.svelte";
  import ModelLink from "./activity-table/ModelLink.svelte";
  import MiddleEllipsis from "./activity-table/MiddleEllipsis.svelte";
  import { formatDuration, formatSpeed, formatRelativeTime } from "../lib/format";
  import { formatBytes, liveElapsedMs, requestHeader, sessionID } from "../lib/inflight";

  interface Props {
    metrics: ActivityLogEntry[];
    inflightRequests?: InflightRequestEntry[];
    storagePrefix: string;
    showModelColumn?: boolean;
    showPagination?: boolean;
    page?: number;
    limit?: number;
    total?: number;
    totalPages?: number;
    onPageChange?: (page: number) => void;
    onPageSizeChange?: (limit: number) => void;
    sort?: string;
    order?: "asc" | "desc";
    onSortChange?: (sort: string, order: "asc" | "desc") => void;
    title?: string;
    compact?: boolean;
    emptyMessage?: string;
    cardClass?: string;
  }

  let {
    metrics,
    inflightRequests = [],
    storagePrefix,
    showModelColumn = true,
    showPagination = false,
    page = 1,
    limit = 25,
    total = metrics.length,
    totalPages = metrics.length > 0 ? 1 : 0,
    onPageChange,
    onPageSizeChange,
    sort,
    order = "desc",
    onSortChange,
    title,
    compact = false,
    emptyMessage = "No activity recorded",
    cardClass = "",
  }: Props = $props();

  function formatDrafted(drafted: number, accepted: number): string {
    return drafted > 0
      ? ((accepted * 100) / drafted).toFixed(1) + "% (" + accepted + "/" + drafted + ")"
      : "-";
  }

  interface ColMeta {
    id: string;
    label: string;
    defaultVisible: boolean;
  }

  function buildColumnMeta(withModel: boolean): ColMeta[] {
    const cols: ColMeta[] = [
      { id: "id", label: "ID", defaultVisible: true },
      { id: "time", label: "Time", defaultVisible: true },
    ];
    if (withModel) cols.push({ id: "model", label: "Model", defaultVisible: true });
    cols.push(
      { id: "req_path", label: "Path", defaultVisible: false },
      { id: "resp_status_code", label: "Status", defaultVisible: true },
      { id: "resp_content_type", label: "Content-Type", defaultVisible: false },
      { id: "cached", label: "Cached", defaultVisible: true },
      { id: "prompt", label: "Prompt", defaultVisible: true },
      { id: "generated", label: "Generated", defaultVisible: true },
      { id: "drafted", label: "Drafted", defaultVisible: false },
      { id: "prompt_speed", label: "Prompt Speed", defaultVisible: true },
      { id: "gen_speed", label: "Gen Speed", defaultVisible: true },
      { id: "duration", label: "Duration", defaultVisible: true },
      { id: "capture", label: "Capture", defaultVisible: true },
      { id: "meta", label: "Meta", defaultVisible: false }
    );
    return cols;
  }

  let columnMeta = $derived(buildColumnMeta(showModelColumn));

  let columnLabelMap = $derived(
    Object.fromEntries(columnMeta.map((c) => [c.id, c.label])) as Record<string, string>
  );

  let defaultVisibility = $derived.by(() => {
    const v: VisibilityState = {};
    for (const c of columnMeta) v[c.id] = c.defaultVisible;
    return v;
  });

  // svelte-ignore state_referenced_locally
  const storedVisibility = persistentStore<VisibilityState>(
    `${storagePrefix}-columns`,
    {}
  );

  // svelte-ignore state_referenced_locally
  let columnVisibility = $state<VisibilityState>(
    Object.keys($storedVisibility).length > 0 ? $storedVisibility : defaultVisibility
  );

  // svelte-ignore state_referenced_locally
  const storedColumnOrder = persistentStore<string[]>(`${storagePrefix}-column-order`, []);

  let defaultColumnOrder = $derived(columnMeta.map((c) => c.id));

  // svelte-ignore state_referenced_locally
  let columnOrder = $state<string[]>(
    $storedColumnOrder.length > 0 ? $storedColumnOrder : defaultColumnOrder
  );

  // Reconcile stored order against the current column set: drop stale ids
  // and append any new ones so all columns are always represented.
  $effect(() => {
    const known = new Set(columnMeta.map((c) => c.id));
    const order = columnOrder;
    const hasStale = order.some((k) => !known.has(k));
    const missing = columnMeta.filter((c) => !order.includes(c.id)).map((c) => c.id);
    if (hasStale || missing.length > 0) {
      const cleaned = order.filter((k) => known.has(k));
      columnOrder = [...cleaned, ...missing];
      storedColumnOrder.set(columnOrder);
    }
  });

  // svelte-ignore state_referenced_locally
  const storedInflightOpen = persistentStore<boolean>(`${storagePrefix}-inflight-open`, true);

  function buildInflightColumnMeta(withModel: boolean): ColMeta[] {
    const cols: ColMeta[] = [
      { id: "cancel", label: "Cancel", defaultVisible: true },
      { id: "elapsed", label: "Elapsed", defaultVisible: true },
    ];
    if (withModel) cols.push({ id: "model", label: "Model", defaultVisible: true });
    cols.push(
      { id: "request", label: "Request", defaultVisible: true },
      { id: "identity", label: "Address", defaultVisible: true },
      { id: "user_agent", label: "User Agent", defaultVisible: true },
      { id: "session_id", label: "Session ID", defaultVisible: true },
      { id: "bytes_received", label: "Bytes Received", defaultVisible: true }
    );
    return cols;
  }

  let inflightColumnMeta = $derived(buildInflightColumnMeta(showModelColumn));
  let inflightColumnLabelMap = $derived(
    Object.fromEntries(inflightColumnMeta.map((column) => [column.id, column.label])) as Record<string, string>
  );
  let inflightDefaultVisibility = $derived.by(() => {
    const visibility: VisibilityState = {};
    for (const column of inflightColumnMeta) visibility[column.id] = column.defaultVisible;
    return visibility;
  });

  // svelte-ignore state_referenced_locally
  const storedInflightVisibility = persistentStore<VisibilityState>(
    `${storagePrefix}-inflight-columns`,
    {}
  );
  // svelte-ignore state_referenced_locally
  let inflightColumnVisibility = $state<VisibilityState>(
    Object.keys($storedInflightVisibility).length > 0
      ? $storedInflightVisibility
      : inflightDefaultVisibility
  );
  // svelte-ignore state_referenced_locally
  const storedInflightColumnOrder = persistentStore<string[]>(
    `${storagePrefix}-inflight-column-order`,
    []
  );
  let inflightDefaultColumnOrder = $derived(inflightColumnMeta.map((column) => column.id));
  // svelte-ignore state_referenced_locally
  let inflightColumnOrder = $state<string[]>(
    $storedInflightColumnOrder.length > 0
      ? $storedInflightColumnOrder
      : inflightDefaultColumnOrder
  );
  let visibleInflightColumns = $derived(
    inflightColumnOrder.filter((id) => inflightColumnVisibility[id] !== false)
  );

  $effect(() => {
    const known = new Set(inflightColumnMeta.map((column) => column.id));
    const hasStale = inflightColumnOrder.some((id) => !known.has(id));
    const missing = inflightColumnMeta
      .filter((column) => !inflightColumnOrder.includes(column.id))
      .map((column) => column.id);
    if (hasStale || missing.length > 0) {
      inflightColumnOrder = [
        ...inflightColumnOrder.filter((id) => known.has(id)),
        ...missing,
      ];
      storedInflightColumnOrder.set(inflightColumnOrder);
    }
  });

  // When onSortChange is provided the table sorts on the server: the sort
  // state is driven by the sort/order props and toggles are forwarded to the
  // parent. Otherwise it falls back to client-side sorting of the current page.
  let localSorting = $state<SortingState>([]);
  let sorting = $derived<SortingState>(
    onSortChange ? (sort ? [{ id: sort, desc: order === "desc" }] : []) : localSorting
  );
  let inflightOpen = $state($storedInflightOpen);

  let selectedCapture = $state<ReqRespCapture | null>(null);
  let dialogOpen = $state(false);
  let loadingCaptureId = $state<number | null>(null);
  let cancelingInflightIds = $state<string[]>([]);
  let inflightNowMs = $state(performance.now());

  $effect(() => {
    if (inflightRequests.length === 0) return;

    // Refresh synchronously when the first request appears so the initial
    // render does not use the timestamp from when this component mounted.
    inflightNowMs = performance.now();

    let frame = 0;
    const tick = () => {
      inflightNowMs = performance.now();
      frame = requestAnimationFrame(tick);
    };
    frame = requestAnimationFrame(tick);

    return () => cancelAnimationFrame(frame);
  });

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

  function setInflightOpen(open: boolean) {
    inflightOpen = open;
    storedInflightOpen.set(open);
  }

  async function cancelInflight(id: string) {
    if (cancelingInflightIds.includes(id)) return;
    cancelingInflightIds = [...cancelingInflightIds, id];
    try {
      await cancelInflightRequest(id);
    } finally {
      cancelingInflightIds = cancelingInflightIds.filter((current) => current !== id);
    }
  }

  function buildColumns(withModel: boolean): ColumnDef<ActivityLogEntry>[] {
    const cols: ColumnDef<ActivityLogEntry>[] = [
      {
        id: "id",
        accessorKey: "id",
        header: "ID",
        cell: ({ row }) => String(row.original.id + 1),
      },
      {
        id: "time",
        accessorKey: "timestamp",
        header: "Time",
        cell: ({ row }) => formatRelativeTime(row.original.timestamp),
      },
    ];

    if (withModel) {
      cols.push({
        id: "model",
        accessorKey: "model",
        header: "Model",
        cell: ({ row }) =>
          renderComponent(ModelLink, { model: row.original.model }),
      });
    }

    cols.push(
      {
        id: "req_path",
        accessorKey: "req_path",
        header: "Path",
        cell: ({ row }) => row.original.req_path || "-",
      },
      {
        id: "resp_status_code",
        accessorKey: "resp_status_code",
        header: "Status",
        cell: ({ row }) => String(row.original.resp_status_code || "-"),
      },
      {
        id: "resp_content_type",
        accessorKey: "resp_content_type",
        header: "Content-Type",
        cell: ({ row }) => row.original.resp_content_type || "-",
      },
      {
        id: "cached",
        accessorFn: (row) => row.tokens.cache_tokens,
        header: () => renderComponent(HeaderLabel, { label: "Cached", tooltip: "prompt tokens from cache" }),
        cell: ({ row }) =>
          row.original.tokens.cache_tokens > 0
            ? row.original.tokens.cache_tokens.toLocaleString()
            : "-",
      },
      {
        id: "prompt",
        accessorFn: (row) => row.tokens.input_tokens,
        header: () => renderComponent(HeaderLabel, { label: "Prompt", tooltip: "new prompt tokens processed" }),
        cell: ({ row }) => row.original.tokens.input_tokens.toLocaleString(),
      },
      {
        id: "generated",
        accessorFn: (row) => row.tokens.output_tokens,
        header: "Generated",
        cell: ({ row }) => row.original.tokens.output_tokens.toLocaleString(),
      },
      {
        id: "drafted",
        accessorFn: (row) => row.tokens.draft_tokens,
        header: () => renderComponent(HeaderLabel, { label: "Drafted", tooltip: "acceptance rate (accepted/drafted)" }),
        cell: ({ row }) =>
          formatDrafted(row.original.tokens.draft_tokens, row.original.tokens.draft_acc_tokens),
      },
      {
        id: "prompt_speed",
        accessorFn: (row) => row.tokens.prompt_per_second,
        header: "Prompt Speed",
        cell: ({ row }) => formatSpeed(row.original.tokens.prompt_per_second),
      },
      {
        id: "gen_speed",
        accessorFn: (row) => row.tokens.tokens_per_second,
        header: "Gen Speed",
        cell: ({ row }) => formatSpeed(row.original.tokens.tokens_per_second),
      },
      {
        id: "duration",
        accessorKey: "duration_ms",
        header: "Duration",
        cell: ({ row }) => formatDuration(row.original.duration_ms),
      },
      {
        id: "capture",
        header: "Capture",
        enableSorting: false,
        cell: ({ row }) =>
          renderComponent(ViewCaptureButton, {
            hasCapture: row.original.has_capture,
            loading: loadingCaptureId === row.original.id,
            onclick: () => viewCapture(row.original.id),
          }),
      },
      {
        id: "meta",
        header: "Meta",
        enableSorting: false,
        cell: ({ row }) =>
          renderComponent(MetaCell, { metadata: row.original.metadata }),
      }
    );
    return cols;
  }

  let columns = $derived(buildColumns(showModelColumn));

  const table = createSvelteTable({
    get data() {
      return metrics;
    },
    get columns() {
      return columns;
    },
    state: {
      get columnVisibility() {
        return columnVisibility;
      },
      get sorting() {
        return sorting;
      },
      get columnOrder() {
        return columnOrder;
      },
    },
    // svelte-ignore state_referenced_locally
    manualSorting: !!onSortChange,
    onSortingChange: (updater) => {
      const next = typeof updater === "function" ? updater(sorting) : updater;
      if (onSortChange) {
        const first = next[0];
        onSortChange(first?.id ?? "", first?.desc === false ? "asc" : "desc");
      } else {
        localSorting = next;
      }
    },
    onColumnOrderChange: (updater) => {
      columnOrder =
        typeof updater === "function" ? updater(columnOrder) : updater;
      storedColumnOrder.set(columnOrder);
    },
    onColumnVisibilityChange: (updater) => {
      columnVisibility =
        typeof updater === "function" ? updater(columnVisibility) : updater;
      storedVisibility.set(columnVisibility);
    },
    getCoreRowModel: getCoreRowModel(),
    getSortedRowModel: getSortedRowModel(),
  });

  let thClass = $derived(compact ? "px-2 py-2 h-9" : "px-3 py-3 h-12");
  let tdClass = $derived(compact ? "px-2 py-2" : "px-3 py-4");
  let inflightThClass = $derived(compact ? "h-7 px-2 py-1" : "h-8 px-3 py-1.5");
  let inflightTdClass = $derived(compact ? "px-2 py-1" : "px-3 py-1.5");
  let visibleColumnCount = $derived(table.getVisibleLeafColumns().length);
  let pageCount = $derived(Math.max(totalPages, 1));
  let visiblePages = $derived.by(() => {
    const maxButtons = 7;
    const count = pageCount;
    const current = Math.min(Math.max(page, 1), count);
    const half = Math.floor(maxButtons / 2);
    let start = Math.max(1, current - half);
    let end = Math.min(count, start + maxButtons - 1);
    start = Math.max(1, end - maxButtons + 1);
    return Array.from({ length: end - start + 1 }, (_, i) => start + i);
  });

  function setServerPage(nextPage: number) {
    const clamped = Math.min(Math.max(nextPage, 1), pageCount);
    if (clamped !== page) onPageChange?.(clamped);
  }

  function setServerPageSize(nextLimit: number) {
    onPageSizeChange?.(nextLimit);
  }

  function sortIcon(state: false | "asc" | "desc") {
    if (state === "asc") return ArrowUp;
    if (state === "desc") return ArrowDown;
    return ArrowUpDown;
  }

  let dragColId: string | null = $state(null);
  let dragOverColId: string | null = $state(null);

  function handleColDragStart(e: DragEvent, colId: string) {
    dragColId = colId;
    if (e.dataTransfer) {
      e.dataTransfer.effectAllowed = "move";
      e.dataTransfer.setData("text/plain", colId);
    }
  }

  function handleColDragOver(e: DragEvent, colId: string) {
    e.preventDefault();
    if (e.dataTransfer) e.dataTransfer.dropEffect = "move";
    if (dragOverColId !== colId) dragOverColId = colId;
  }

  function handleColDrop(e: DragEvent, targetId: string) {
    e.preventDefault();
    const sourceId = dragColId;
    dragColId = null;
    dragOverColId = null;
    if (!sourceId || sourceId === targetId) return;
    const order = [...columnOrder];
    const fromIndex = order.indexOf(sourceId);
    let toIndex = order.indexOf(targetId);
    if (fromIndex === -1 || toIndex === -1) return;
    order.splice(fromIndex, 1);
    if (fromIndex < toIndex) toIndex -= 1;
    order.splice(toIndex, 0, sourceId);
    columnOrder = order;
    storedColumnOrder.set(order);
  }

  function handleColDragEnd() {
    dragColId = null;
    dragOverColId = null;
  }

  function formatInflightElapsed(request: InflightRequestEntry, nowMs: number): string {
    return `${(liveElapsedMs(request.elapsed_ms, request.client_received_at_ms, nowMs) / 1000).toFixed(2)}s`;
  }

  function toggleInflightColumn(id: string, visible: boolean) {
    inflightColumnVisibility = { ...inflightColumnVisibility, [id]: visible };
    storedInflightVisibility.set(inflightColumnVisibility);
  }

  let inflightDragColId: string | null = $state(null);
  let inflightDragOverColId: string | null = $state(null);

  function handleInflightColDragStart(e: DragEvent, id: string) {
    inflightDragColId = id;
    if (e.dataTransfer) {
      e.dataTransfer.effectAllowed = "move";
      e.dataTransfer.setData("text/plain", id);
    }
  }

  function handleInflightColDragOver(e: DragEvent, id: string) {
    e.preventDefault();
    if (e.dataTransfer) e.dataTransfer.dropEffect = "move";
    inflightDragOverColId = id;
  }

  function handleInflightColDrop(e: DragEvent, targetId: string) {
    e.preventDefault();
    const sourceId = inflightDragColId;
    inflightDragColId = null;
    inflightDragOverColId = null;
    if (!sourceId || sourceId === targetId) return;
    const next = [...inflightColumnOrder];
    const from = next.indexOf(sourceId);
    let to = next.indexOf(targetId);
    if (from === -1 || to === -1) return;
    next.splice(from, 1);
    if (from < to) to -= 1;
    next.splice(to, 0, sourceId);
    inflightColumnOrder = next;
    storedInflightColumnOrder.set(next);
  }

  function clearInflightColumnDrag() {
    inflightDragColId = null;
    inflightDragOverColId = null;
  }
</script>

<Card.Root class="relative p-3">
  <div class="flex items-center gap-2 pr-16 text-sm">
    <span class="text-muted-foreground text-xs uppercase tracking-wider">In-flight Requests</span>
    <span>
      <span class="font-semibold">{inflightRequests.length}</span> active
    </span>
  </div>

  <div class="absolute right-2 top-2 flex items-center gap-1">
    <DropdownMenu.Root>
      <DropdownMenu.Trigger
        class="text-muted-foreground hover:bg-muted inline-flex size-6 items-center justify-center rounded-full"
        title="Select in-flight columns"
      >
        <Columns3 class="size-4" />
      </DropdownMenu.Trigger>
      <DropdownMenu.Content align="end" class="min-w-[18rem] max-h-[60vh] overflow-y-auto p-0">
        <DropdownMenu.Label class="text-muted-foreground border-b px-3 py-2 text-xs font-medium uppercase tracking-wider">
          Columns <span class="text-[10px] normal-case tracking-normal">(drag to reorder)</span>
        </DropdownMenu.Label>
        {#each inflightColumnOrder as columnId (columnId)}
          {@const isDragOver = inflightDragOverColId === columnId && inflightDragColId !== columnId}
          <DropdownMenu.CheckboxItem
            checked={inflightColumnVisibility[columnId] !== false}
            onCheckedChange={(visible) => toggleInflightColumn(columnId, !!visible)}
            closeOnSelect={false}
            draggable="true"
            ondragstart={(event) => handleInflightColDragStart(event, columnId)}
            ondragover={(event) => handleInflightColDragOver(event, columnId)}
            ondrop={(event) => handleInflightColDrop(event, columnId)}
            ondragend={clearInflightColumnDrag}
            class={isDragOver ? "bg-accent" : ""}
          >
            <GripVertical class="text-muted-foreground/50 size-4 cursor-grab active:cursor-grabbing" />
            <span class="flex-1">{inflightColumnLabelMap[columnId] ?? columnId}</span>
          </DropdownMenu.CheckboxItem>
        {/each}
      </DropdownMenu.Content>
    </DropdownMenu.Root>

    <Button
      variant="ghost"
      size="icon-xs"
      class="text-muted-foreground rounded-full"
      onclick={() => setInflightOpen(!inflightOpen)}
      title={inflightOpen ? "Hide in-flight requests" : "Show in-flight requests"}
    >
      {#if inflightOpen}
        <X />
      {:else}
        <ChevronDown />
      {/if}
    </Button>
  </div>

  {#if inflightOpen}
    <div class="mt-2 overflow-x-auto">
      <Table.Root class="min-w-full">
        <Table.Header>
          <Table.Row>
            {#each visibleInflightColumns as columnId (columnId)}
              <Table.Head class={inflightThClass}>{inflightColumnLabelMap[columnId] ?? columnId}</Table.Head>
            {/each}
          </Table.Row>
        </Table.Header>
        <Table.Body>
          {#each inflightRequests as request (request.id)}
            <Table.Row>
              {#each visibleInflightColumns as columnId (columnId)}
                <Table.Cell class={inflightTdClass}>
                  {#if columnId === "cancel"}
                    <Button
                      variant="ghost"
                      size="icon-sm"
                      class="text-muted-foreground hover:text-destructive size-6"
                      onclick={() => cancelInflight(request.id)}
                      disabled={cancelingInflightIds.includes(request.id)}
                      title="Cancel request"
                      aria-label="Cancel inflight request"
                    >
                      <CircleX class="size-4" />
                    </Button>
                  {:else if columnId === "elapsed"}
                    <span class="font-mono text-xs tabular-nums">
                      {formatInflightElapsed(request, inflightNowMs)}
                    </span>
                  {:else if columnId === "model"}
                    <MiddleEllipsis value={request.model} tailLength={10} className="max-w-[14rem]" />
                  {:else if columnId === "request"}
                    {@const requestLabel = `${request.method || "-"} ${request.req_path || "-"}`}
                    <MiddleEllipsis value={requestLabel} tailLength={14} className="max-w-[20rem] font-mono text-xs" />
                  {:else if columnId === "identity"}
                    <MiddleEllipsis value={request.remote_ip} tailLength={8} className="max-w-[12rem] font-mono text-xs" />
                  {:else if columnId === "user_agent"}
                    {@const userAgent = requestHeader(request.req_headers, "User-Agent")}
                    <MiddleEllipsis value={userAgent} tailLength={18} className="max-w-[20rem] text-xs" />
                  {:else if columnId === "session_id"}
                    {@const session = sessionID(request.req_headers, $uiConfig.activity.session_id)}
                    <MiddleEllipsis value={session} tailLength={8} className="max-w-[14rem] font-mono text-xs" />
                  {:else if columnId === "bytes_received"}
                    <span class="font-mono text-xs tabular-nums">{formatBytes(request.resp_bytes)}</span>
                  {/if}
                </Table.Cell>
              {/each}
            </Table.Row>
          {:else}
            <Table.Row>
              <Table.Cell colspan={Math.max(visibleInflightColumns.length, 1)} class="text-muted-foreground py-4 text-center text-sm">
                No in-flight requests
              </Table.Cell>
            </Table.Row>
          {/each}
        </Table.Body>
      </Table.Root>
    </div>
  {/if}
</Card.Root>

<Card.Root class="mt-3 shrink-0 gap-0 overflow-hidden py-0 {cardClass}">
  <Card.Header class="flex items-center justify-between border-b px-4 py-2">
    <div class="flex items-center gap-2">
      {#if title}
        <Card.Title class="text-sm font-semibold">
          {title}
          <span class="text-muted-foreground text-xs font-normal">({total})</span>
        </Card.Title>
      {/if}
    </div>
    <div class="flex items-center gap-2">
      {#if showPagination}
        <span class="text-muted-foreground text-xs">Rows</span>
        <Select.Root
          type="single"
          value={String(limit)}
          onValueChange={(v) => setServerPageSize(Number(v))}
        >
          <Select.Trigger size="sm" class="h-7 w-[4.5rem] text-xs">
            {limit}
          </Select.Trigger>
          <Select.Content>
            {#each [10, 25, 50, 100] as size (size)}
              <Select.Item value={String(size)}>{size}</Select.Item>
            {/each}
          </Select.Content>
        </Select.Root>
      {/if}
      <DropdownMenu.Root>
        <DropdownMenu.Trigger
          class="hover:bg-muted inline-flex size-7 items-center justify-center rounded-[min(var(--radius-md),12px)]"
          title="Select columns"
        >
          <Columns3 class="size-4" />
        </DropdownMenu.Trigger>
        <DropdownMenu.Content align="end" class="min-w-[18rem] max-h-[60vh] overflow-y-auto p-0">
          <DropdownMenu.Label class="text-muted-foreground border-b px-3 py-2 text-xs font-medium uppercase tracking-wider">
            Columns <span class="text-[10px] normal-case tracking-normal">(drag to reorder)</span>
          </DropdownMenu.Label>
          {#each table.getAllColumns() as column (column.id)}
            {#if column.getCanHide()}
              {@const isDragOver = dragOverColId === column.id && dragColId !== column.id}
              <DropdownMenu.CheckboxItem
                checked={column.getIsVisible()}
                onCheckedChange={(v) => column.toggleVisibility(!!v)}
                closeOnSelect={false}
                draggable="true"
                ondragstart={(e) => handleColDragStart(e, column.id)}
                ondragover={(e) => handleColDragOver(e, column.id)}
                ondrop={(e) => handleColDrop(e, column.id)}
                ondragend={handleColDragEnd}
                class={isDragOver ? "bg-accent" : ""}
              >
                <GripVertical class="text-muted-foreground/50 size-4 cursor-grab active:cursor-grabbing" />
                <span class="flex-1">{columnLabelMap[column.id] ?? column.id}</span>
              </DropdownMenu.CheckboxItem>
            {/if}
          {/each}
        </DropdownMenu.Content>
      </DropdownMenu.Root>
    </div>
  </Card.Header>
  <Card.Content class="overflow-x-auto p-0">
    <Table.Root class="min-w-full">
      <Table.Header>
        {#each table.getHeaderGroups() as headerGroup (headerGroup.id)}
          <Table.Row>
            {#each headerGroup.headers as header (header.id)}
              <Table.Head class={thClass} colspan={header.colSpan}>
                {#if !header.isPlaceholder}
                  {#if header.column.getCanSort()}
                    {@const sorted = header.column.getIsSorted()}
                    {@const Icon = sortIcon(sorted)}
                    <button
                      type="button"
                      class="text-muted-foreground hover:text-foreground -mx-1 inline-flex items-center gap-1 text-left"
                      onclick={() => header.column.toggleSorting(sorted === "asc")}
                    >
                      <FlexRender content={header.column.columnDef.header} context={header.getContext()} />
                      <Icon class={`size-3 ${sorted ? "text-foreground" : "opacity-50"}`} />
                    </button>
                  {:else}
                    <FlexRender content={header.column.columnDef.header} context={header.getContext()} />
                  {/if}
                {/if}
              </Table.Head>
            {/each}
          </Table.Row>
        {/each}
      </Table.Header>
      <Table.Body>
        {#each table.getRowModel().rows as row (row.id)}
          <Table.Row>
            {#each row.getVisibleCells() as cell (cell.id)}
              <Table.Cell class={tdClass}>
                <FlexRender content={cell.column.columnDef.cell} context={cell.getContext()} />
              </Table.Cell>
            {/each}
          </Table.Row>
        {/each}
        {#if table.getRowModel().rows.length === 0}
          <Table.Row>
            <Table.Cell colspan={visibleColumnCount} class="text-muted-foreground py-6 text-center text-sm">
              {emptyMessage}
            </Table.Cell>
          </Table.Row>
        {/if}
      </Table.Body>
    </Table.Root>

    {#if showPagination && total > 0}
      <div class="flex items-center justify-between gap-2 border-t px-4 py-2 text-sm">
        <span class="text-muted-foreground text-xs">
          Page {page} of {pageCount} · {total} total
        </span>
        <div class="flex items-center gap-1">
          <Button
            variant="ghost"
            size="icon-sm"
            onclick={() => setServerPage(1)}
            disabled={page <= 1}
            title="First page"
          >
            <ChevronsLeft />
          </Button>
          <Button
            variant="ghost"
            size="icon-sm"
            onclick={() => setServerPage(page - 1)}
            disabled={page <= 1}
            title="Previous page"
          >
            <ChevronLeft />
          </Button>
          {#each visiblePages as pageNumber (pageNumber)}
            <Button
              variant={pageNumber === page ? "secondary" : "ghost"}
              size="sm"
              class="h-7 min-w-7 px-2 text-xs"
              onclick={() => setServerPage(pageNumber)}
              disabled={pageNumber === page}
            >
              {pageNumber}
            </Button>
          {/each}
          <Button
            variant="ghost"
            size="icon-sm"
            onclick={() => setServerPage(page + 1)}
            disabled={page >= pageCount}
            title="Next page"
          >
            <ChevronRight />
          </Button>
          <Button
            variant="ghost"
            size="icon-sm"
            onclick={() => setServerPage(pageCount)}
            disabled={page >= pageCount}
            title="Last page"
          >
            <ChevronsRight />
          </Button>
        </div>
      </div>
    {/if}
  </Card.Content>
</Card.Root>

<CaptureDialog capture={selectedCapture} open={dialogOpen} onclose={closeDialog} />
