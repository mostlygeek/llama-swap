<script lang="ts">
	import { cn, type WithElementRef } from "$lib/utils.js";
	import type { HTMLAttributes } from "svelte/elements";
	import { useSidebar } from "./context.svelte.js";

	let {
		ref = $bindable(null),
		class: className,
		children,
		...restProps
	}: WithElementRef<HTMLAttributes<HTMLButtonElement>, HTMLButtonElement> = $props();

	const sidebar = useSidebar();

	// Pointer movement (px) before a press on the rail counts as a resize drag
	// rather than a click that toggles the sidebar.
	const DRAG_THRESHOLD = 3;

	// Set when a drag ends so the click that follows pointerup doesn't also
	// toggle the sidebar.
	let suppressClick = false;

	function handlePointerDown(e: PointerEvent): void {
		if (e.button !== 0) return;
		const rail = e.currentTarget as HTMLButtonElement;
		const side = rail.closest("[data-side]")?.getAttribute("data-side") ?? "left";
		const startX = e.clientX;
		let dragging = false;
		suppressClick = false;
		rail.setPointerCapture(e.pointerId);

		const prevCursor = document.body.style.cursor;
		const prevUserSelect = document.body.style.userSelect;

		function onMove(ev: PointerEvent): void {
			if (!dragging) {
				if (Math.abs(ev.clientX - startX) < DRAG_THRESHOLD) return;
				dragging = true;
				sidebar.resizing = true;
				document.body.style.cursor = "col-resize";
				document.body.style.userSelect = "none";
				if (!sidebar.open) sidebar.setOpen(true);
			}
			const width = side === "right" ? window.innerWidth - ev.clientX : ev.clientX;
			sidebar.setWidth(width);
		}

		function onEnd(): void {
			rail.removeEventListener("pointermove", onMove);
			rail.removeEventListener("pointerup", onEnd);
			rail.removeEventListener("pointercancel", onEnd);
			if (dragging) {
				suppressClick = true;
				sidebar.resizing = false;
				document.body.style.cursor = prevCursor;
				document.body.style.userSelect = prevUserSelect;
			}
		}

		rail.addEventListener("pointermove", onMove);
		rail.addEventListener("pointerup", onEnd);
		rail.addEventListener("pointercancel", onEnd);
	}

	function handleClick(): void {
		if (suppressClick) {
			suppressClick = false;
			return;
		}
		sidebar.toggle();
	}
</script>

<button
	bind:this={ref}
	data-sidebar="rail"
	data-slot="sidebar-rail"
	aria-label="Resize or toggle sidebar"
	tabindex={-1}
	onpointerdown={handlePointerDown}
	onclick={handleClick}
	title="Drag to resize, click to toggle"
	class={cn(
		"hover:after:bg-sidebar-border group-data-[resizing=true]:after:bg-sidebar-border absolute inset-y-0 z-20 hidden w-4 -translate-x-1/2 touch-none transition-all ease-linear group-data-[side=left]:-right-4 group-data-[side=right]:left-0 after:absolute after:inset-y-0 after:left-1/2 after:w-[2px] sm:flex",
		"cursor-col-resize",
		"hover:group-data-[collapsible=offcanvas]:bg-sidebar group-data-[collapsible=offcanvas]:translate-x-0 group-data-[collapsible=offcanvas]:after:left-full",
		"[[data-side=left][data-collapsible=offcanvas]_&]:-right-2",
		"[[data-side=right][data-collapsible=offcanvas]_&]:-left-2",
		className
	)}
	{...restProps}
>
	{@render children?.()}
</button>
