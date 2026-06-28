<script lang="ts">
  import * as Tooltip from "$lib/components/ui/tooltip/index.js";

  interface Props {
    label: string;
    tooltip?: string;
  }

  let { label, tooltip }: Props = $props();
</script>

{#if tooltip}
  <Tooltip.Root>
    <Tooltip.Trigger class="cursor-help border-b border-dotted border-current align-middle">
      {label}
    </Tooltip.Trigger>
    <Tooltip.Content>{tooltip}</Tooltip.Content>
  </Tooltip.Root>
{:else}
  {label}
{/if}
