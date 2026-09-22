<script lang="ts">
	import { Combobox as ComboboxPrimitive } from "bits-ui";

	let {
		open = $bindable(false),
		value = $bindable(),
		...restProps
	}: ComboboxPrimitive.RootProps = $props();
</script>

<ComboboxPrimitive.Root bind:open bind:value={value as never} {...restProps} />
