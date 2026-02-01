<script lang="ts">
  import { untrack } from "svelte";
  import { Maximize2, X } from "lucide-svelte";

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

<div class="flex-1 relative group flex items-stretch min-h-0">
  <textarea
    class="w-full px-3 py-2 pr-10 rounded border border-gray-200 dark:border-white/10 bg-surface focus:outline-none focus:ring-2 focus:ring-inset focus:ring-primary resize-none"
    {placeholder}
    {rows}
    bind:value
    {onkeydown}
    {disabled}
  ></textarea>
  <button
    class="absolute top-2 right-2 p-1.5 rounded-lg opacity-60 md:opacity-0 group-hover:opacity-100 transition-opacity bg-surface/90 hover:bg-surface border border-gray-200 dark:border-white/10 shadow-sm"
    onclick={openExpanded}
    title="Expand to edit"
    type="button"
    {disabled}
  >
    <Maximize2 class="w-4 h-4" />
  </button>
</div>

{#if isExpanded}
  <div class="fixed inset-0 z-50 flex items-center justify-center bg-black/50 p-4">
    <div class="w-full max-w-4xl h-[80vh] flex flex-col bg-surface rounded-lg shadow-xl border border-gray-200 dark:border-white/10">
      <!-- Header -->
      <div class="flex justify-between items-center p-4 border-b border-gray-200 dark:border-white/10">
        <h3 class="font-medium">Edit Text</h3>
        <button
          class="p-1.5 rounded-lg hover:bg-gray-100 dark:hover:bg-white/10"
          onclick={closeExpanded}
          title="Close"
          type="button"
        >
          <X class="w-5 h-5" />
        </button>
      </div>

      <!-- Textarea -->
      <div class="flex-1 p-4">
        <textarea
          bind:this={expandedTextarea}
          class="w-full h-full px-4 py-3 rounded border border-gray-200 dark:border-white/10 bg-card focus:outline-none focus:ring-2 focus:ring-primary resize-none"
          placeholder={placeholder}
          bind:value={expandedValue}
          onkeydown={handleKeyDown}
        ></textarea>
      </div>

      <!-- Footer -->
      <div class="flex justify-end gap-2 p-4 border-t border-gray-200 dark:border-white/10">
        <button
          class="btn"
          onclick={closeExpanded}
          type="button"
        >
          Cancel
        </button>
        <button
          class="btn bg-primary text-btn-primary-text hover:opacity-90"
          onclick={saveExpanded}
          type="button"
        >
          Done
        </button>
      </div>
    </div>
  </div>
{/if}
