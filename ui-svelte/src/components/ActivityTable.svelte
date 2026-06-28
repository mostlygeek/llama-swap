<script lang="ts">
  import type { ActivityLogEntry, ReqRespCapture } from "../lib/types";
  import { getCapture } from "../stores/api";
  import { persistentStore } from "../stores/persistent";
  import CaptureDialog from "./CaptureDialog.svelte";
  import {
    type ColumnDef,
    type PaginationState,
    type VisibilityState,
    getCoreRowModel,
    getPaginationRowModel,
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
    ChevronLeft,
    ChevronRight,
    ChevronsLeft,
    ChevronsRight,
  } from "@lucide/svelte";
  import HeaderLabel from "./activity-table/HeaderLabel.svelte";
  import ViewCaptureButton from "./activity-table/ViewCaptureButton.svelte";
  import MetaCell from "./activity-table/MetaCell.svelte";

  interface Props {
    metrics: ActivityLogEntry[];
    storagePrefix: string;
    showModelColumn?: boolean;
    showPagination?: boolean;
    title?: string;
    compact?: boolean;
    emptyMessage?: string;
    cardClass?: string;
  }

  let {
    metrics,
    storagePrefix,
    showModelColumn = true,
    showPagination = false,
    title,
    compact = false,
    emptyMessage = "No activity recorded",
    cardClass = "",
  }: Props = $props();

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
  const storedPageSize = persistentStore<number>(`${storagePrefix}-page-size`, 10);

  // svelte-ignore state_referenced_locally
  let pagination = $state<PaginationState>({
    pageIndex: 0,
    pageSize: showPagination ? $storedPageSize : 1,
  });

  // When not paginating, show every row; reset page on data/pageSize change.
  $effect(() => {
    if (!showPagination) {
      pagination.pageSize = metrics.length || 1;
    }
  });

  $effect(() => {
    metrics;
    pagination.pageSize;
    pagination.pageIndex = 0;
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

  function buildColumns(withModel: boolean): ColumnDef<ActivityLogEntry>[] {
    const cols: ColumnDef<ActivityLogEntry>[] = [
      {
        id: "id",
        header: "ID",
        cell: ({ row }) => String(row.original.id + 1),
      },
      {
        id: "time",
        header: "Time",
        cell: ({ row }) => formatRelativeTime(row.original.timestamp),
      },
    ];

    if (withModel) {
      cols.push({
        id: "model",
        header: "Model",
        cell: ({ row }) => row.original.model ?? "-",
      });
    }

    cols.push(
      {
        id: "req_path",
        header: "Path",
        cell: ({ row }) => row.original.req_path || "-",
      },
      {
        id: "resp_status_code",
        header: "Status",
        cell: ({ row }) => String(row.original.resp_status_code || "-"),
      },
      {
        id: "resp_content_type",
        header: "Content-Type",
        cell: ({ row }) => row.original.resp_content_type || "-",
      },
      {
        id: "cached",
        header: () => renderComponent(HeaderLabel, { label: "Cached", tooltip: "prompt tokens from cache" }),
        cell: ({ row }) =>
          row.original.tokens.cache_tokens > 0
            ? row.original.tokens.cache_tokens.toLocaleString()
            : "-",
      },
      {
        id: "prompt",
        header: () => renderComponent(HeaderLabel, { label: "Prompt", tooltip: "new prompt tokens processed" }),
        cell: ({ row }) => row.original.tokens.input_tokens.toLocaleString(),
      },
      {
        id: "generated",
        header: "Generated",
        cell: ({ row }) => row.original.tokens.output_tokens.toLocaleString(),
      },
      {
        id: "drafted",
        header: () => renderComponent(HeaderLabel, { label: "Drafted", tooltip: "acceptance rate (accepted/drafted)" }),
        cell: ({ row }) =>
          formatDrafted(row.original.tokens.draft_tokens, row.original.tokens.draft_acc_tokens),
      },
      {
        id: "prompt_speed",
        header: "Prompt Speed",
        cell: ({ row }) => formatSpeed(row.original.tokens.prompt_per_second),
      },
      {
        id: "gen_speed",
        header: "Gen Speed",
        cell: ({ row }) => formatSpeed(row.original.tokens.tokens_per_second),
      },
      {
        id: "duration",
        header: "Duration",
        cell: ({ row }) => formatDuration(row.original.duration_ms),
      },
      {
        id: "capture",
        header: "Capture",
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
      get pagination() {
        return pagination;
      },
      get columnVisibility() {
        return columnVisibility;
      },
    },
    onPaginationChange: (updater) => {
      pagination =
        typeof updater === "function" ? updater(pagination) : updater;
      if (showPagination) storedPageSize.set(pagination.pageSize);
    },
    onColumnVisibilityChange: (updater) => {
      columnVisibility =
        typeof updater === "function" ? updater(columnVisibility) : updater;
      storedVisibility.set(columnVisibility);
    },
    getCoreRowModel: getCoreRowModel(),
    getPaginationRowModel: getPaginationRowModel(),
  });

  let thClass = $derived(compact ? "px-4 py-2 h-9" : "px-6 py-3 h-12");
  let tdClass = $derived(compact ? "px-4 py-2" : "px-6 py-4");
</script>

<Card.Root class="shrink-0 gap-0 overflow-hidden py-0 {cardClass}">
  <Card.Header class="flex items-center justify-between border-b px-4 py-2">
    <div class="flex items-center gap-2">
      {#if title}
        <Card.Title class="text-sm font-semibold">
          {title}
          <span class="text-muted-foreground text-xs font-normal">({metrics.length})</span>
        </Card.Title>
      {/if}
    </div>
    <div class="flex items-center gap-2">
      {#if showPagination}
        <span class="text-muted-foreground text-xs">Rows</span>
        <Select.Root
          type="single"
          value={String(pagination.pageSize)}
          onValueChange={(v) => table.setPageSize(Number(v))}
        >
          <Select.Trigger size="sm" class="h-7 w-[4.5rem] text-xs">
            {pagination.pageSize}
          </Select.Trigger>
          <Select.Content>
            {#each [5, 10, 25, 50] as size (size)}
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
        <DropdownMenu.Content align="end" class="min-w-[16rem] p-0">
          <DropdownMenu.Label class="text-muted-foreground border-b px-3 py-2 text-xs font-medium uppercase tracking-wider">
            Columns
          </DropdownMenu.Label>
          {#each table.getAllColumns() as column (column.id)}
            {#if column.getCanHide()}
              <DropdownMenu.CheckboxItem
                checked={column.getIsVisible()}
                onCheckedChange={(v) => column.toggleVisibility(!!v)}
              >
                {columnLabelMap[column.id] ?? column.id}
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
                  <FlexRender content={header.column.columnDef.header} context={header.getContext()} />
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
        {:else}
          <Table.Row>
            <Table.Cell colspan={columns.length} class="text-muted-foreground py-6 text-center text-sm">
              {emptyMessage}
            </Table.Cell>
          </Table.Row>
        {/each}
      </Table.Body>
    </Table.Root>

    {#if showPagination && metrics.length > 0}
      <div class="flex items-center justify-between gap-2 border-t px-4 py-2 text-sm">
        <span class="text-muted-foreground text-xs">
          Page {pagination.pageIndex + 1} of {table.getPageCount()} · {metrics.length} total
        </span>
        <div class="flex items-center gap-1">
          <Button
            variant="ghost"
            size="icon-sm"
            onclick={() => table.setPageIndex(0)}
            disabled={!table.getCanPreviousPage()}
            title="First page"
          >
            <ChevronsLeft />
          </Button>
          <Button
            variant="ghost"
            size="icon-sm"
            onclick={() => table.previousPage()}
            disabled={!table.getCanPreviousPage()}
            title="Previous page"
          >
            <ChevronLeft />
          </Button>
          <Button
            variant="ghost"
            size="icon-sm"
            onclick={() => table.nextPage()}
            disabled={!table.getCanNextPage()}
            title="Next page"
          >
            <ChevronRight />
          </Button>
          <Button
            variant="ghost"
            size="icon-sm"
            onclick={() => table.setPageIndex(table.getPageCount() - 1)}
            disabled={!table.getCanNextPage()}
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
