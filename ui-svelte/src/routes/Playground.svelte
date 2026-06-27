<script lang="ts">
  import { persistentStore } from "../stores/persistent";
  import ChatInterface from "../components/playground/ChatInterface.svelte";
  import ImageInterface from "../components/playground/ImageInterface.svelte";
  import AudioInterface from "../components/playground/AudioInterface.svelte";
  import SpeechInterface from "../components/playground/SpeechInterface.svelte";
  import RerankInterface from "../components/playground/RerankInterface.svelte";
  import ConcurrencyInterface from "../components/playground/ConcurrencyInterface.svelte";
  import * as Card from "$lib/components/ui/card/index.js";
  import * as Tabs from "$lib/components/ui/tabs/index.js";

  type Tab = "chat" | "images" | "speech" | "audio" | "rerank" | "concurrency";

  const selectedTabStore = persistentStore<Tab>("playground-selected-tab", "chat");

  const tabs: { id: Tab; label: string }[] = [
    { id: "chat", label: "Chat" },
    { id: "images", label: "Images" },
    { id: "speech", label: "Speech" },
    { id: "audio", label: "Transcription" },
    { id: "rerank", label: "Rerank" },
    { id: "concurrency", label: "Load Test" },
  ];
</script>

<Card.Root class="flex h-full flex-col gap-0 overflow-hidden p-4">
  <!-- Tab navigation: triggers update the store; content stays mounted to preserve state -->
  <div class="mb-4 shrink-0">
    <Tabs.Root bind:value={() => $selectedTabStore, (v) => selectedTabStore.set(v as Tab)}>
      <Tabs.List class="h-auto flex-wrap">
        {#each tabs as tab (tab.id)}
          <Tabs.Trigger value={tab.id}>{tab.label}</Tabs.Trigger>
        {/each}
      </Tabs.List>
    </Tabs.Root>
  </div>

  <!-- Tab content (all interfaces stay mounted) -->
  <div class="relative flex-1 overflow-hidden">
    <div class="h-full" class:hidden={$selectedTabStore !== "chat"}>
      <ChatInterface />
    </div>
    <div class="h-full" class:hidden={$selectedTabStore !== "images"}>
      <ImageInterface />
    </div>
    <div class="h-full" class:hidden={$selectedTabStore !== "speech"}>
      <SpeechInterface />
    </div>
    <div class="h-full" class:hidden={$selectedTabStore !== "audio"}>
      <AudioInterface />
    </div>
    <div class="h-full" class:hidden={$selectedTabStore !== "rerank"}>
      <RerankInterface />
    </div>
    <div class="h-full" class:hidden={$selectedTabStore !== "concurrency"}>
      <ConcurrencyInterface />
    </div>
  </div>
</Card.Root>
