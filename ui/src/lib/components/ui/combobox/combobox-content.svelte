<script lang="ts">
	import { Combobox as ComboboxPrimitive } from "bits-ui";
	import { cn, type WithoutChild } from "$lib/utils.js";

	let {
		ref = $bindable(null),
		class: className,
		sideOffset = 4,
		children,
		...restProps
	}: WithoutChild<ComboboxPrimitive.ContentProps> = $props();
</script>

<ComboboxPrimitive.Portal>
	<ComboboxPrimitive.Content
		bind:ref
		{sideOffset}
		data-slot="combobox-content"
		class={cn(
			"bg-popover text-popover-foreground data-open:animate-in data-closed:animate-out data-closed:fade-out-0 data-open:fade-in-0 ring-foreground/10 relative isolate z-50 min-w-36 overflow-x-hidden overflow-y-auto rounded-lg shadow-md ring-1 duration-100",
			className
		)}
		{...restProps}
	>
		<ComboboxPrimitive.ScrollUpButton
			class="bg-popover z-10 top-0 flex w-full cursor-default items-center justify-center py-1"
		/>
		<ComboboxPrimitive.Viewport class="w-full min-w-(--bits-combobox-anchor-width) scroll-my-1">
			{@render children?.()}
		</ComboboxPrimitive.Viewport>
		<ComboboxPrimitive.ScrollDownButton
			class="bg-popover z-10 bottom-0 flex w-full cursor-default items-center justify-center py-1"
		/>
	</ComboboxPrimitive.Content>
</ComboboxPrimitive.Portal>
