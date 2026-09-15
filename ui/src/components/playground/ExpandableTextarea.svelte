<script lang="ts">
  import { Maximize2 } from "@lucide/svelte";
  import { Button } from "$lib/components/ui/button/index.js";
  import { Textarea } from "$lib/components/ui/textarea/index.js";
  import TextEditDialog from "./TextEditDialog.svelte";

  interface Props {
    value: string;
    ref?: HTMLTextAreaElement | null;
    placeholder?: string;
    rows?: number;
    disabled?: boolean;
    onkeydown?: (event: KeyboardEvent) => void;
  }

  let {
    value = $bindable(),
    ref = $bindable(null),
    placeholder = "",
    rows = 3,
    disabled = false,
    onkeydown,
  }: Props = $props();

  let isExpanded = $state(false);

  function saveExpanded(text: string) {
    value = text;
    isExpanded = false;
  }
</script>

<div class="group relative flex min-h-0 flex-1 items-stretch">
  <Textarea
    class="resize-none pr-10"
    bind:ref
    {placeholder}
    {rows}
    bind:value
    {onkeydown}
    {disabled}
  />
  <Button
    variant="outline"
    size="icon-sm"
    class="absolute right-2 top-2 opacity-60 transition-opacity group-hover:opacity-100 md:opacity-0"
    onclick={() => (isExpanded = true)}
    title="Expand to edit"
    type="button"
    {disabled}
  >
    <Maximize2 />
  </Button>
</div>

{#if isExpanded}
  <TextEditDialog {value} {placeholder} onsave={saveExpanded} oncancel={() => (isExpanded = false)} />
{/if}
