<script lang="ts">
  import type { Snippet } from "svelte";
  import { cn } from "$lib/utils.js";

  interface Props {
    class?: string;
    children: Snippet;
  }

  let { class: className, children }: Props = $props();
</script>

<span
  class={cn("bg-muted text-muted-foreground rounded-md px-2 py-0.5 text-xs font-medium", className)}
>
  {@render children()}
</span>
