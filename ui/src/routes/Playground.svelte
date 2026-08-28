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
  import { fetchPlaygroundModels, models } from "../stores/api";
  import { selectedPlaygroundTab, playgroundTabs, type PlaygroundTab } from "../stores/playground";

  const MODEL_REFRESH_DEBOUNCE_MS = 200;

  const tabComponents: Record<PlaygroundTab, Component> = {
    chat: ChatInterface,
    images: ImageInterface,
    speech: SpeechInterface,
    audio: AudioInterface,
    rerank: RerankInterface,
    concurrency: ConcurrencyInterface,
  };

  let initializedModels = false;
  $effect(() => {
    void $models;
    if (!initializedModels) {
      initializedModels = true;
      void fetchPlaygroundModels();
      return;
    }

    const timeout = window.setTimeout(() => {
      void fetchPlaygroundModels();
    }, MODEL_REFRESH_DEBOUNCE_MS);
    return () => window.clearTimeout(timeout);
  });
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
