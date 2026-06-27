<script lang="ts">
  import { untrack } from "svelte";
  import { Maximize2, X } from "@lucide/svelte";
  import { Button } from "$lib/components/ui/button/index.js";
  import { Textarea } from "$lib/components/ui/textarea/index.js";

  interface Props {
    value: string;
    placeholder?: string;
    rows?: number;
    disabled?: boolean;
    onkeydown?: (event: KeyboardEvent) => void;
  }

  let {
    value = $bindable(),
    placeholder = "",
    rows = 3,
    disabled = false,
    onkeydown,
  }: Props = $props();

  let isExpanded = $state(false);
  let expandedValue = $state("");
  let expandedTextarea: HTMLTextAreaElement | undefined = $state();

  function openExpanded() {
    expandedValue = value;
    isExpanded = true;
  }

  function closeExpanded() {
    isExpanded = false;
  }

  function saveExpanded() {
    value = expandedValue;
    isExpanded = false;
  }

  function handleKeyDown(event: KeyboardEvent) {
    if (event.key === "Escape") {
      closeExpanded();
    }
  }

  // Focus the textarea when expanded view opens
  $effect(() => {
    if (isExpanded && expandedTextarea) {
      expandedTextarea.focus();
      const len = untrack(() => expandedValue.length);
      expandedTextarea.setSelectionRange(len, len);
    }
  });
</script>

<div class="group relative flex min-h-0 flex-1 items-stretch">
  <Textarea
    class="resize-none pr-10"
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
    onclick={openExpanded}
    title="Expand to edit"
    type="button"
    {disabled}
  >
    <Maximize2 />
  </Button>
</div>

{#if isExpanded}
  <div class="fixed inset-0 z-50 flex items-center justify-center bg-black/50 p-4">
    <div class="bg-card flex h-[80vh] w-full max-w-4xl flex-col rounded-lg border shadow-xl">
      <!-- Header -->
      <div class="flex items-center justify-between border-b p-4">
        <h3 class="pb-0 font-medium">Edit Text</h3>
        <Button variant="ghost" size="icon-sm" onclick={closeExpanded} title="Close" type="button">
          <X />
        </Button>
      </div>

      <!-- Textarea -->
      <div class="flex-1 p-4">
        <Textarea
          bind:ref={expandedTextarea}
          class="h-full resize-none"
          {placeholder}
          bind:value={expandedValue}
          onkeydown={handleKeyDown}
        />
      </div>

      <!-- Footer -->
      <div class="flex justify-end gap-2 border-t p-4">
        <Button variant="outline" onclick={closeExpanded} type="button">Cancel</Button>
        <Button onclick={saveExpanded} type="button">Done</Button>
      </div>
    </div>
  </div>
{/if}
