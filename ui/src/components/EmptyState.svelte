<script lang="ts">
  import type { Snippet } from "svelte";
  import { cn } from "$lib/utils.js";

  interface Props {
    /** Message rendered in a <p> when no children are provided. */
    message?: string;
    /** Use h-full instead of flex-1 (for non-flex parents that need full height). */
    full?: boolean;
    class?: string;
    children?: Snippet;
  }

  let { message, full = false, class: className, children }: Props = $props();
</script>

<div
  class={cn(
    "text-muted-foreground flex items-center justify-center",
    full ? "h-full" : "flex-1",
    className
  )}
>
  {#if children}
    {@render children()}
  {:else}
    <p>{message}</p>
  {/if}
</div>
