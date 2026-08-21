<script lang="ts">
  import {
    activeFilterCount,
    emptyActivityFilters,
    type ActivityFilters,
  } from "../../lib/activityFilters";
  import { Input } from "$lib/components/ui/input/index.js";
  import { Label } from "$lib/components/ui/label/index.js";
  import { Button } from "$lib/components/ui/button/index.js";
  import * as Select from "$lib/components/ui/select/index.js";

  const PAGE_SIZES = [10, 25, 50, 100, 250, 500];

  interface Props {
    filters: ActivityFilters;
    onchange: (filters: ActivityFilters) => void;
    showRows?: boolean;
    limit?: number;
    onLimitChange?: (limit: number) => void;
  }

  let { filters, onchange, showRows = false, limit = 25, onLimitChange }: Props = $props();

  // Committed on change rather than input so a partially typed number does not
  // trigger a fetch per keystroke.
  function update(patch: Partial<ActivityFilters>) {
    onchange({ ...filters, ...patch });
  }

  let canClear = $derived(activeFilterCount(filters) > 0);
</script>

<div class="bg-muted/30 border-b px-4 py-3">
  <div class="flex flex-wrap items-end gap-3">
    <div class="flex flex-col gap-1">
      <Label for="filter-min-id" class="text-muted-foreground text-xs">ID from</Label>
      <Input
        id="filter-min-id"
        type="number"
        min="1"
        step="1"
        placeholder="min"
        class="h-8 w-[6.5rem] text-xs"
        value={filters.minID}
        onchange={(event) => update({ minID: event.currentTarget.value })}
      />
    </div>

    <div class="flex flex-col gap-1">
      <Label for="filter-max-id" class="text-muted-foreground text-xs">ID to</Label>
      <Input
        id="filter-max-id"
        type="number"
        min="1"
        step="1"
        placeholder="max"
        class="h-8 w-[6.5rem] text-xs"
        value={filters.maxID}
        onchange={(event) => update({ maxID: event.currentTarget.value })}
      />
    </div>

    <Button
      variant="ghost"
      size="sm"
      class="h-8 text-xs"
      disabled={!canClear}
      onclick={() => onchange(emptyActivityFilters())}
    >
      Clear filters
    </Button>

    {#if showRows && onLimitChange}
      <!-- Rows is a view setting rather than a filter: it sits apart on the
           right, is left alone by "Clear filters", and is not counted by the
           header's active-filter badge. -->
      <div class="ml-auto flex flex-col gap-1">
        <span class="text-muted-foreground text-xs">Rows</span>
        <Select.Root
          type="single"
          value={String(limit)}
          onValueChange={(value) => onLimitChange(Number(value))}
        >
          <Select.Trigger size="sm" class="h-8 w-[5.5rem] text-xs">
            {limit}
          </Select.Trigger>
          <Select.Content>
            {#each PAGE_SIZES as size (size)}
              <Select.Item value={String(size)}>{size}</Select.Item>
            {/each}
          </Select.Content>
        </Select.Root>
      </div>
    {/if}
  </div>
</div>
