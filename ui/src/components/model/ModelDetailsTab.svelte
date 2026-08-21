<script lang="ts">
  import type { Model } from "../../lib/types";
  import { capabilityLabels } from "../../lib/capabilities";
  import * as Card from "$lib/components/ui/card/index.js";
  import Tag from "../Tag.svelte";

  interface Props {
    model: Model;
  }

  let { model }: Props = $props();

  let capabilities = $derived.by(() => {
    const caps = model?.capabilities ?? {};
    return Object.entries(caps).filter(([, v]) => v);
  });
</script>

<Card.Root class="shrink-0 gap-0 overflow-hidden py-0">
  <Card.Header class="border-b px-4 py-2">
    <Card.Title class="text-sm font-semibold">Capabilities</Card.Title>
  </Card.Header>
  <Card.Content class="p-3">
    {#if capabilities.length === 0}
      <span class="text-muted-foreground text-sm">No capabilities reported.</span>
    {:else}
      <div class="flex flex-wrap gap-1.5">
        {#each capabilities as [key] (key)}
          <Tag>{capabilityLabels[key] ?? key}</Tag>
        {/each}
      </div>
    {/if}
  </Card.Content>
</Card.Root>
