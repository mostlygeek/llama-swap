<script lang="ts">
  import * as Tooltip from "$lib/components/ui/tooltip/index.js";

  interface Props {
    label: string;
    tooltip?: string;
  }

  let { label, tooltip }: Props = $props();
</script>

{label}{#if tooltip}
  <Tooltip.Root>
    <Tooltip.Trigger class="cursor-help align-middle normal-case">&#9432;</Tooltip.Trigger>
    <Tooltip.Content>{tooltip}</Tooltip.Content>
  </Tooltip.Root>
{/if}
