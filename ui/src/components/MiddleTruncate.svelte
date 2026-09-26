<script lang="ts">
  import { cn } from "$lib/utils.js";
  import { middleTruncate } from "../lib/middleTruncate";

  interface Props {
    text: string;
    /** Characters at the end that are always kept visible. */
    tail?: number;
    class?: string;
  }

  let { text, tail = 10, class: className }: Props = $props();

  let el: HTMLSpanElement | undefined = $state();
  let width = $state(0);
  // Bumped when web fonts finish loading, since that changes text widths.
  let fontsReady = $state(0);

  $effect(() => {
    document.fonts?.ready.then(() => fontsReady++);
  });

  let ctx: CanvasRenderingContext2D | null = null;

  // Shortens the middle to fit, measured in the element's own font, so the
  // ellipsis sits right against the kept tail with no gap. Truncating in the
  // middle keeps the end (a quant, a context size) readable.
  let display = $derived.by(() => {
    void fontsReady;
    if (!el || width <= 0) return text;
    ctx ??= document.createElement("canvas").getContext("2d");
    if (!ctx) return text;
    ctx.font = getComputedStyle(el).font;
    const c = ctx;
    // One pixel of slack for the sub-pixel widths clientWidth rounds away.
    return middleTruncate(text, width - 1, (s) => c.measureText(s).width, tail);
  });
</script>

<span
  bind:this={el}
  bind:clientWidth={width}
  class={cn("block min-w-0 truncate", className)}
  title={display === text ? undefined : text}
>{display}</span>
