<script lang="ts">
  import type { Component } from "svelte";
  import ChatInterface from "../components/playground/ChatInterface.svelte";
  import ImageInterface from "../components/playground/ImageInterface.svelte";
  import AudioInterface from "../components/playground/AudioInterface.svelte";
  import SpeechInterface from "../components/playground/SpeechInterface.svelte";
  import RerankInterface from "../components/playground/RerankInterface.svelte";
  import ConcurrencyInterface from "../components/playground/ConcurrencyInterface.svelte";
  import * as Card from "$lib/components/ui/card/index.js";
  import { Tabs, TabsList, TabsTrigger } from "$lib/components/ui/tabs/index.js";
  import { refreshPlaygroundModels } from "$lib/hooks/playground-models.svelte";
  import { selectedPlaygroundTab, playgroundTabs, type PlaygroundTab } from "../stores/playground";

  const tabComponents: Record<PlaygroundTab, Component> = {
    chat: ChatInterface,
    images: ImageInterface,
    speech: SpeechInterface,
    audio: AudioInterface,
    rerank: RerankInterface,
    concurrency: ConcurrencyInterface,
  };

  refreshPlaygroundModels();
</script>

<!-- On a phone the card frame is dropped: its padding and ring would cost
     the tabs a fifth of the screen width for no gain. -->
<Card.Root class="flex h-full flex-col gap-0 overflow-hidden p-4 max-sm:bg-transparent max-sm:p-0 max-sm:ring-0">
  <Tabs
    value={$selectedPlaygroundTab}
    onValueChange={(v: string) => v && selectedPlaygroundTab.set(v as PlaygroundTab)}
    class="flex flex-1 w-full flex-col gap-2 overflow-hidden"
  >
    <!-- Hidden on phones: the header title becomes the tab picker there, so
         the whole height below it belongs to the tab. -->
    <TabsList
      variant="line"
      class="w-full justify-start overflow-x-auto max-sm:hidden [scrollbar-width:none] [&::-webkit-scrollbar]:hidden"
    >
      {#each playgroundTabs as tab (tab.id)}
        <TabsTrigger value={tab.id}>{tab.label}</TabsTrigger>
      {/each}
    </TabsList>

    <div class="relative flex-1 overflow-hidden">
      {#each playgroundTabs as tab (tab.id)}
        {@const TabComponent = tabComponents[tab.id]}
        <div class="h-full" class:hidden={$selectedPlaygroundTab !== tab.id}>
          <TabComponent />
        </div>
      {/each}
    </div>
  </Tabs>
</Card.Root>
