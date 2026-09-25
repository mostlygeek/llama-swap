<script lang="ts">
  import { Check, Copy } from "@lucide/svelte";
  import { copyText } from "$lib/clipboard";
  import { cn } from "$lib/utils.js";

  interface Props {
    value: string;
    class?: string;
  }

  let { value, class: className }: Props = $props();

  let copied = $state(false);
  let copyTimer: ReturnType<typeof setTimeout> | null = null;

  async function copy(): Promise<void> {
    if (!(await copyText(value))) return;
    copied = true;
    if (copyTimer !== null) clearTimeout(copyTimer);
    copyTimer = setTimeout(() => {
      copied = false;
    }, 1500);
  }
</script>

<button
  type="button"
  onclick={copy}
  title={copied ? "Copied!" : `Copy ${value}`}
  aria-label={copied ? `Copied ${value}` : `Copy ${value}`}
  class={cn(
    "group/copy hover:bg-muted inline-flex max-w-full cursor-pointer items-center gap-1 rounded px-1 text-left break-all",
    className,
  )}
>
  <span>{value}</span>
  {#if copied}
    <Check class="text-success size-3 shrink-0" />
  {:else}
    <Copy class="size-3 shrink-0 opacity-0 transition-opacity group-hover/copy:opacity-60" />
  {/if}
</button>
