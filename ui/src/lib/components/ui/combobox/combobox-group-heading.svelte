<script lang="ts">
	import { Combobox as ComboboxPrimitive } from "bits-ui";
	import { cn } from "$lib/utils.js";
	import type { ComponentProps } from "svelte";

	let {
		ref = $bindable(null),
		class: className,
		children,
		...restProps
	}: ComponentProps<typeof ComboboxPrimitive.GroupHeading> = $props();
</script>

<ComboboxPrimitive.GroupHeading
	bind:ref
	data-slot="combobox-group-heading"
	class={cn("text-muted-foreground px-1.5 py-1 text-xs", className)}
	{...restProps}
>
	{@render children?.()}
</ComboboxPrimitive.GroupHeading>
