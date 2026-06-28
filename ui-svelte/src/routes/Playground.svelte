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
  import { selectedPlaygroundTab, playgroundTabs, type PlaygroundTab } from "../stores/playground";

  const tabComponents: Record<PlaygroundTab, Component> = {
    chat: ChatInterface,
    images: ImageInterface,
    speech: SpeechInterface,
    audio: AudioInterface,
    rerank: RerankInterface,
    concurrency: ConcurrencyInterface,
  };
</script>

<Card.Root class="flex h-full flex-col gap-0 overflow-hidden p-4">
  <Tabs
    value={$selectedPlaygroundTab}
    onValueChange={(v: string) => v && selectedPlaygroundTab.set(v as PlaygroundTab)}
    class="flex flex-1 w-full flex-col gap-2 overflow-hidden"
  >
    <TabsList variant="line">
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
