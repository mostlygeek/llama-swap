<script lang="ts">
  import { untrack, type Snippet } from "svelte";
  import { ArrowUp, Maximize2, Square, X } from "@lucide/svelte";
  import { Button } from "$lib/components/ui/button/index.js";
  import { Textarea } from "$lib/components/ui/textarea/index.js";

  /**
   * The message box at the bottom of a chat. One rounded surface holds the
   * text, an optional attachment strip above it, and the buttons on either
   * side, so on a phone the whole thing is a single line tall until the text
   * needs more. The textarea grows with its content (with a JS fallback for
   * browsers without CSS field-sizing) up to a cap, then scrolls.
   */
  interface Props {
    value: string;
    ref?: HTMLTextAreaElement | null;
    placeholder?: string;
    disabled?: boolean;
    /** A reply is in flight: the send button becomes a stop button. */
    streaming?: boolean;
    /** Nothing to send yet (empty text and no attachments). */
    canSend?: boolean;
    onsend: () => void;
    onstop?: () => void;
    onkeydown?: (event: KeyboardEvent) => void;
    /** Buttons before the text, e.g. an attach button. */
    leading?: Snippet;
    /** Content above the text, e.g. attachment previews and errors. */
    attachments?: Snippet;
  }

  let {
    value = $bindable(),
    ref = $bindable(null),
    placeholder = "",
    disabled = false,
    streaming = false,
    canSend = true,
    onsend,
    onstop,
    onkeydown,
    leading,
    attachments,
  }: Props = $props();

  // Auto-grow. CSS field-sizing handles this where supported; the height
  // measurement covers the rest (Safari) and also shrinks the box back after
  // the value is cleared programmatically.
  $effect(() => {
    void value;
    const el = ref;
    if (!el) return;
    el.style.height = "auto";
    el.style.height = `${el.scrollHeight}px`;
  });

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

  function handleExpandedKeyDown(event: KeyboardEvent) {
    if (event.key === "Escape") {
      closeExpanded();
    }
  }

  $effect(() => {
    if (isExpanded && expandedTextarea) {
      expandedTextarea.focus();
      const len = untrack(() => expandedValue.length);
      expandedTextarea.setSelectionRange(len, len);
    }
  });
</script>

<div
  class="bg-card focus-within:border-ring focus-within:ring-ring/50 rounded-2xl border shadow-xs transition-[color,box-shadow] focus-within:ring-3"
  class:opacity-60={disabled && !streaming}
>
  {#if attachments}
    {@render attachments()}
  {/if}
  <div class="flex items-end gap-1 p-1.5">
    {#if leading}
      {@render leading()}
    {/if}
    <textarea
      bind:this={ref}
      bind:value
      class="placeholder:text-muted-foreground max-h-48 min-h-9 flex-1 resize-none bg-transparent px-2 py-2 text-base leading-5 outline-none disabled:cursor-not-allowed md:text-sm"
      rows="1"
      {placeholder}
      {disabled}
      {onkeydown}
      enterkeyhint="send"
    ></textarea>
    <Button
      variant="ghost"
      size="icon-lg"
      class="text-muted-foreground hidden shrink-0 rounded-full sm:inline-flex"
      onclick={openExpanded}
      title="Expand to edit"
      {disabled}
    >
      <Maximize2 />
    </Button>
    {#if streaming}
      <Button
        variant="destructive"
        size="icon-lg"
        class="shrink-0 rounded-full"
        onclick={onstop}
        title="Stop generating"
        aria-label="Stop generating"
      >
        <Square class="size-3.5 fill-current" />
      </Button>
    {:else}
      <Button
        size="icon-lg"
        class="shrink-0 rounded-full"
        onclick={onsend}
        disabled={disabled || !canSend}
        title="Send message"
        aria-label="Send message"
      >
        <ArrowUp class="size-5" />
      </Button>
    {/if}
  </div>
</div>

{#if isExpanded}
  <div class="fixed inset-0 z-50 flex items-center justify-center bg-black/50 p-4">
    <div class="bg-card flex h-[80vh] w-full max-w-4xl flex-col rounded-lg border shadow-xl">
      <div class="flex items-center justify-between border-b p-4">
        <h3 class="pb-0 font-medium">Edit Text</h3>
        <Button variant="ghost" size="icon-sm" onclick={closeExpanded} title="Close">
          <X />
        </Button>
      </div>
      <div class="flex-1 p-4">
        <Textarea
          bind:ref={expandedTextarea}
          class="h-full resize-none"
          {placeholder}
          bind:value={expandedValue}
          onkeydown={handleExpandedKeyDown}
        />
      </div>
      <div class="flex justify-end gap-2 border-t p-4">
        <Button variant="outline" onclick={closeExpanded}>Cancel</Button>
        <Button onclick={saveExpanded}>Done</Button>
      </div>
    </div>
  </div>
{/if}
