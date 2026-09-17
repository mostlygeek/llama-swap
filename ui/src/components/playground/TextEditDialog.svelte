<script lang="ts">
  import { untrack } from "svelte";
  import { X } from "@lucide/svelte";
  import { Button } from "$lib/components/ui/button/index.js";
  import { Textarea } from "$lib/components/ui/textarea/index.js";

  /**
   * A modal text editor. On a phone it is a full-screen sheet; on larger
   * screens a centered dialog. Mount it to open, unmount it to close.
   *
   * Keyboard aware: mobile browsers do not shrink the layout viewport when
   * the on-screen keyboard opens, so a plain fixed overlay ends up half
   * covered by the keys. The sheet instead follows the visual viewport, the
   * part of the page actually visible, and resizes as the keyboard comes and
   * goes so the text and the buttons stay reachable.
   */
  interface Props {
    /** Text to start from; the parent gets the edited copy through onsave. */
    value: string;
    title?: string;
    placeholder?: string;
    saveLabel?: string;
    onsave: (value: string) => void;
    oncancel: () => void;
  }

  let { value, title = "Edit Text", placeholder = "", saveLabel = "Done", onsave, oncancel }: Props = $props();

  // The dialog is mounted to open, so the prop is only a starting point.
  let draft = $state(untrack(() => value));
  let textarea = $state<HTMLTextAreaElement | null>(null);
  let viewport = $state<{ top: number; height: number } | null>(null);

  $effect(() => {
    const vv = window.visualViewport;
    if (!vv) return;
    const update = () => {
      viewport = { top: vv.offsetTop, height: vv.height };
    };
    update();
    vv.addEventListener("resize", update);
    vv.addEventListener("scroll", update);
    return () => {
      vv.removeEventListener("resize", update);
      vv.removeEventListener("scroll", update);
    };
  });

  // The page behind must not scroll while the sheet is open.
  $effect(() => {
    const previous = document.body.style.overflow;
    document.body.style.overflow = "hidden";
    return () => {
      document.body.style.overflow = previous;
    };
  });

  $effect(() => {
    if (!textarea) return;
    textarea.focus();
    const len = textarea.value.length;
    textarea.setSelectionRange(len, len);
  });

  function save() {
    onsave(draft);
  }

  function handleKeyDown(event: KeyboardEvent) {
    if (event.key === "Escape") {
      event.preventDefault();
      oncancel();
    } else if (event.key === "Enter" && (event.metaKey || event.ctrlKey)) {
      event.preventDefault();
      save();
    }
  }
</script>

<div
  class="fixed inset-x-0 top-0 z-50 flex h-full items-center justify-center bg-black/50 sm:p-4"
  style={viewport ? `top:${viewport.top}px;height:${viewport.height}px` : ""}
  role="dialog"
  aria-modal="true"
  aria-label={title}
>
  <div class="bg-card flex h-full w-full flex-col sm:h-[80vh] sm:max-h-full sm:max-w-4xl sm:rounded-lg sm:border sm:shadow-xl">
    <div class="flex shrink-0 items-center justify-between border-b px-4 py-3">
      <h3 class="pb-0 font-medium">{title}</h3>
      <Button variant="ghost" size="icon-sm" onclick={oncancel} title="Close">
        <X />
      </Button>
    </div>
    <div class="min-h-0 flex-1 p-3 sm:p-4">
      <Textarea
        bind:ref={textarea}
        class="h-full resize-none"
        {placeholder}
        bind:value={draft}
        onkeydown={handleKeyDown}
      />
    </div>
    <div class="flex shrink-0 justify-end gap-2 border-t p-3 sm:p-4">
      <Button variant="outline" onclick={oncancel}>Cancel</Button>
      <Button onclick={save}>{saveLabel}</Button>
    </div>
  </div>
</div>
