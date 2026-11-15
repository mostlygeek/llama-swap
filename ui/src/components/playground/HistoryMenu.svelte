<script lang="ts">
  import { History } from "@lucide/svelte";
  import { Button } from "$lib/components/ui/button/index.js";
  import * as DropdownMenu from "$lib/components/ui/dropdown-menu/index.js";
  import type { ChatHistory, ConversationMeta } from "../../stores/chatHistory";
  import { formatRelativeTime } from "../../lib/relativeTime";

  /** Previous conversations of one chat page, newest first. */
  let {
    history,
    disabled = false,
    onSelect,
    onCleared,
  }: {
    history: ChatHistory<any>;
    disabled?: boolean;
    onSelect: (id: string) => void;
    /** Called after everything was erased. */
    onCleared?: () => void;
  } = $props();

  // The history instance is created once per page and never swapped.
  // svelte-ignore state_referenced_locally
  const limit = history.limit;
  // svelte-ignore state_referenced_locally
  const activeId = history.activeId;
  // svelte-ignore state_referenced_locally
  const available = history.available;

  let items = $state<ConversationMeta[]>([]);
  let open = $state(false);

  $effect(() => {
    if (open) void history.list().then((list) => (items = list));
  });

  // With 0 or 1 kept there is nothing to pick from: 1 is just "the last
  // conversation", which is already on screen.
  const show = $derived($limit >= 2 && $available);

  async function clear() {
    if (!confirm("Erase all saved conversations?")) return;
    await history.clear();
    items = [];
    onCleared?.();
  }
</script>

{#if show}
<DropdownMenu.Root bind:open>
  <DropdownMenu.Trigger>
    {#snippet child({ props })}
      <Button {...props} variant="outline" class="shrink-0" {disabled} title="History">
        <History />
        <span class="hidden sm:inline">History</span>
      </Button>
    {/snippet}
  </DropdownMenu.Trigger>
  <DropdownMenu.Content align="start" class="max-h-80 w-72 overflow-y-auto">
    {#if items.length === 0}
      <div class="text-muted-foreground px-2 py-1.5 text-sm">No saved history</div>
    {:else}
      {#each items as item (item.id)}
        <DropdownMenu.Item onSelect={() => onSelect(item.id)} class="flex-col items-start gap-0">
          <span class="w-full truncate {item.id === $activeId ? 'font-semibold' : ''}">{item.title}</span>
          <span class="text-muted-foreground text-xs">
            {formatRelativeTime(item.updatedAt)}{item.id === $activeId ? " · current" : ""}
          </span>
        </DropdownMenu.Item>
      {/each}
    {/if}
    <DropdownMenu.Separator />
    <DropdownMenu.Item variant="destructive" disabled={items.length === 0} onSelect={clear}>
      Clear history
    </DropdownMenu.Item>
  </DropdownMenu.Content>
</DropdownMenu.Root>
{/if}
