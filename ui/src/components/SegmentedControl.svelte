<script lang="ts">
  import { Button } from "$lib/components/ui/button/index.js";

  interface Props {
    items: readonly { readonly label: string }[];
    selected: number;
    onSelect: (index: number) => void;
    /** Optional leading label rendered before the buttons. */
    label?: string;
  }

  let { items, selected, onSelect, label }: Props = $props();
</script>

<div class="flex items-center gap-1">
  {#if label}
    <span class="text-muted-foreground mr-1 text-xs">{label}</span>
  {/if}
  {#each items as item, i (i)}
    <Button variant={selected === i ? "default" : "outline"} size="sm" onclick={() => onSelect(i)}>
      {item.label}
    </Button>
  {/each}
</div>
