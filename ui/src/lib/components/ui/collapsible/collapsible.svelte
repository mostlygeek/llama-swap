<script lang="ts">
	import { Collapsible as CollapsiblePrimitive } from "bits-ui";

	let {
		ref = $bindable(null),
		class: className,
		children,
		...restProps
	}: CollapsiblePrimitive.RootProps = $props();
</script>

<CollapsiblePrimitive.Root
	bind:ref
	data-slot="collapsible"
	class={className}
	{...restProps}
>
	{@render children?.()}
</CollapsiblePrimitive.Root>
