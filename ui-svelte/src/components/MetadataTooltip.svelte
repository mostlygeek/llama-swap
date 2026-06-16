<script lang="ts">
  import type { Snippet } from "svelte";

  interface Props {
    metadata: Record<string, string> | undefined;
    children: Snippet;
  }

  let { metadata, children }: Props = $props();

  let entries = $derived(Object.entries(metadata || {}));
  let triggerEl: HTMLElement | undefined = $state();
  let tooltipEl: HTMLDivElement | undefined = $state();
  let show = $state(false);
  let tooltipStyle = $state("");

  function positionTooltip() {
    if (!triggerEl || !tooltipEl) return;
    const triggerRect = triggerEl.getBoundingClientRect();
    const tooltipRect = tooltipEl.getBoundingClientRect();
    const margin = 8;
    const viewportWidth = window.innerWidth;
    const viewportHeight = window.innerHeight;

    let left = triggerRect.left;
    let top = triggerRect.bottom + margin;

    // Keep tooltip within horizontal viewport bounds
    if (left + tooltipRect.width > viewportWidth - margin) {
      left = triggerRect.right - tooltipRect.width;
    }
    if (left < margin) {
      left = margin;
    }

    // Flip above trigger if it would overflow the bottom
    if (top + tooltipRect.height > viewportHeight - margin) {
      top = triggerRect.top - tooltipRect.height - margin;
    }

    tooltipStyle = `left: ${left}px; top: ${top}px; max-width: calc(100vw - ${margin * 2}px);`;
  }

  function onEnter() {
    show = true;
    requestAnimationFrame(positionTooltip);
  }

  function onLeave() {
    show = false;
  }
</script>

<span
  bind:this={triggerEl}
  onmouseenter={onEnter}
  onmouseleave={onLeave}
  onfocus={onEnter}
  onblur={onLeave}
  class="inline-flex"
  role="button"
  tabindex="0"
  aria-label="Show metadata"
>
  {@render children()}
</span>

{#if show && entries.length > 0}
  <div
    bind:this={tooltipEl}
    style={tooltipStyle}
    class="fixed px-3 py-2 bg-gray-900 text-white text-sm rounded-md z-50 normal-case min-w-[12rem] max-w-[24rem] shadow-lg whitespace-normal"
  >
    <table class="w-full text-left">
      <tbody>
        {#each entries as [key, value]}
          <tr class="border-b border-white/10 last:border-0">
            <td class="py-1 pr-3 font-medium whitespace-nowrap text-primary">{key}</td>
            <td class="py-1 break-all">{value}</td>
          </tr>
        {/each}
      </tbody>
    </table>
  </div>
{/if}
