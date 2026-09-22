<script lang="ts">
	import { Combobox as ComboboxPrimitive } from "bits-ui";
	import { cn } from "$lib/utils.js";

	let {
		ref = $bindable(null),
		class: className,
		...restProps
	}: ComboboxPrimitive.GroupProps = $props();
</script>

<ComboboxPrimitive.Group
	bind:ref
	data-slot="combobox-group"
	class={cn("scroll-my-1 p-1", className)}
	{...restProps}
/>
