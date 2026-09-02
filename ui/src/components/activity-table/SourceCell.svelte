<script lang="ts">
  import * as Tooltip from "$lib/components/ui/tooltip/index.js";
  import { compactActivitySource } from "$lib/activitySource";
  import { copyText } from "$lib/clipboard";

  interface Props {
    source: string;
  }

  let { source }: Props = $props();
  let display = $derived(compactActivitySource(source));
  let compacted = $derived(!!source && display !== source);
  let copied = $state(false);
  let copyTimer: ReturnType<typeof setTimeout> | null = null;

  async function copySource() {
    if (!(await copyText(source))) return;
    copied = true;
    if (copyTimer !== null) clearTimeout(copyTimer);
    copyTimer = setTimeout(() => {
      copied = false;
    }, 1500);
  }

  $effect(() => () => {
    if (copyTimer !== null) clearTimeout(copyTimer);
  });
</script>

{#if compacted}
  <Tooltip.Root>
    <Tooltip.Trigger
      class="cursor-copy whitespace-nowrap font-mono text-primary hover:underline"
      onclick={copySource}
      aria-label={copied ? "Copied full Tailcat caller ID" : "Copy full Tailcat caller ID"}
    >
      {copied ? "Copied!" : display}
    </Tooltip.Trigger>
    <Tooltip.Content class="max-w-[32rem] break-all font-mono normal-case">
      {source}
    </Tooltip.Content>
  </Tooltip.Root>
{:else}
  <span>{display}</span>
{/if}
