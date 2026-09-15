<script lang="ts">
  import type { Snippet } from "svelte";
  import { ArrowUp, Maximize2, Square } from "@lucide/svelte";
  import { Button } from "$lib/components/ui/button/index.js";
  import TextEditDialog from "./TextEditDialog.svelte";

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

  function saveExpanded(text: string) {
    value = text;
    isExpanded = false;
  }
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
      onclick={() => (isExpanded = true)}
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
  <TextEditDialog {value} {placeholder} onsave={saveExpanded} oncancel={() => (isExpanded = false)} />
{/if}
