<script lang="ts">
  import type { Model } from "../../lib/types";
  import * as Card from "$lib/components/ui/card/index.js";

  interface Props {
    model: Model;
  }

  let { model }: Props = $props();

  const capabilityLabels: Record<string, string> = {
    vision: "Vision",
    audio_transcriptions: "Transcription",
    audio_speech: "Speech",
    image_generation: "Image Gen",
    image_to_image: "Img→Img",
    function_calling: "Function Calling",
    reranker: "Reranker",
  };

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
          <span class="bg-muted text-muted-foreground rounded-md px-2 py-0.5 text-xs font-medium">
            {capabilityLabels[key] ?? key}
          </span>
        {/each}
      </div>
    {/if}
  </Card.Content>
</Card.Root>
