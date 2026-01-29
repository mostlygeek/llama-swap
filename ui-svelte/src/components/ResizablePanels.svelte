<script lang="ts">
  import type { Snippet } from "svelte";
  import { onMount } from "svelte";

  interface Props {
    direction: "horizontal" | "vertical";
    storageKey: string;
    leftPanel: Snippet;
    rightPanel: Snippet;
    defaultSize?: number;
    minSize?: number;
  }

  let { direction, storageKey, leftPanel, rightPanel, defaultSize = 50, minSize = 5 }: Props = $props();

  let containerRef: HTMLDivElement;
  let isDragging = $state(false);
  // svelte-ignore state_referenced_locally
  let leftSize = $state(defaultSize);

  // Load saved size from localStorage
  onMount(() => {
    const saved = localStorage.getItem(`panel-size-${storageKey}`);
    if (saved) {
      const parsed = parseFloat(saved);
      if (!isNaN(parsed) && parsed >= minSize && parsed <= 100 - minSize) {
        leftSize = parsed;
      }
    }
  });

  function saveSize(): void {
    localStorage.setItem(`panel-size-${storageKey}`, String(leftSize));
  }

  function handleMouseDown(e: MouseEvent): void {
    e.preventDefault();
    isDragging = true;
    document.addEventListener("mousemove", handleMouseMove);
    document.addEventListener("mouseup", handleMouseUp);
  }

  function handleTouchStart(_e: TouchEvent): void {
    isDragging = true;
    document.addEventListener("touchmove", handleTouchMove);
    document.addEventListener("touchend", handleTouchEnd);
  }

  function handleMouseMove(e: MouseEvent): void {
    if (!isDragging || !containerRef) return;
    updateSize(e.clientX, e.clientY);
  }

  function handleTouchMove(e: TouchEvent): void {
    if (!isDragging || !containerRef || e.touches.length === 0) return;
    updateSize(e.touches[0].clientX, e.touches[0].clientY);
  }

  function updateSize(clientX: number, clientY: number): void {
    const rect = containerRef.getBoundingClientRect();

    let newSize: number;
    if (direction === "horizontal") {
      newSize = ((clientX - rect.left) / rect.width) * 100;
    } else {
      newSize = ((clientY - rect.top) / rect.height) * 100;
    }

    // Clamp size
    newSize = Math.max(minSize, Math.min(100 - minSize, newSize));
    leftSize = newSize;
  }

  function handleMouseUp(): void {
    isDragging = false;
    saveSize();
    document.removeEventListener("mousemove", handleMouseMove);
    document.removeEventListener("mouseup", handleMouseUp);
  }

  function handleTouchEnd(): void {
    isDragging = false;
    saveSize();
    document.removeEventListener("touchmove", handleTouchMove);
    document.removeEventListener("touchend", handleTouchEnd);
  }

  function handleKeyDown(e: KeyboardEvent): void {
    const step = 2; // 2% increment for keyboard navigation
    const key = e.key;

    if (direction === "horizontal" && (key === "ArrowLeft" || key === "ArrowRight")) {
      e.preventDefault();
      const delta = key === "ArrowLeft" ? -step : step;
      const newSize = Math.max(minSize, Math.min(100 - minSize, leftSize + delta));
      leftSize = newSize;
      saveSize();
    } else if (direction === "vertical" && (key === "ArrowUp" || key === "ArrowDown")) {
      e.preventDefault();
      const delta = key === "ArrowUp" ? -step : step;
      const newSize = Math.max(minSize, Math.min(100 - minSize, leftSize + delta));
      leftSize = newSize;
      saveSize();
    }
  }

  let containerClass = $derived(direction === "horizontal" ? "flex-row" : "flex-col");

  let handleClass = $derived(
    direction === "horizontal"
      ? "w-2 h-full cursor-col-resize"
      : "w-full h-2 cursor-row-resize"
  );

  let leftStyle = $derived(
    direction === "horizontal"
      ? `width: ${leftSize}%; min-width: ${minSize}%`
      : `height: ${leftSize}%; min-height: ${minSize}%`
  );

  let rightStyle = $derived(
    direction === "horizontal"
      ? `width: ${100 - leftSize}%; min-width: ${minSize}%`
      : `height: ${100 - leftSize}%; min-height: ${minSize}%`
  );
</script>

<div bind:this={containerRef} class="flex {containerClass} h-full w-full gap-2">
  <div style={leftStyle} class="overflow-hidden">
    {@render leftPanel()}
  </div>

  <!-- svelte-ignore a11y_no_noninteractive_tabindex -->
  <!-- svelte-ignore a11y_no_noninteractive_element_interactions -->
  <div
    role="separator"
    tabindex="0"
    class="{handleClass} bg-primary hover:bg-success transition-colors rounded flex-shrink-0"
    onmousedown={handleMouseDown}
    ontouchstart={handleTouchStart}
    onkeydown={handleKeyDown}
    aria-label="Resize panels"
    aria-orientation={direction}
    aria-valuenow={Math.round(leftSize)}
    aria-valuemin={minSize}
    aria-valuemax={100 - minSize}
  ></div>

  <div style={rightStyle} class="overflow-hidden">
    {@render rightPanel()}
  </div>
</div>
