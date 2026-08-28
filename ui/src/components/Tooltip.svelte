<script lang="ts">
  import * as Tooltip from "$lib/components/ui/tooltip/index.js";

  interface Props {
    content: string;
  }

  let { content }: Props = $props();
</script>

<Tooltip.Root>
  <Tooltip.Trigger class="cursor-help align-middle normal-case">&#9432;</Tooltip.Trigger>
  <Tooltip.Content>{content}</Tooltip.Content>
</Tooltip.Root>
