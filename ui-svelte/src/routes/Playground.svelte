<script lang="ts">
  import { persistentStore } from "../stores/persistent";
  import ChatInterface from "../components/playground/ChatInterface.svelte";
  import ImageInterface from "../components/playground/ImageInterface.svelte";
  import AudioInterface from "../components/playground/AudioInterface.svelte";
  import SpeechInterface from "../components/playground/SpeechInterface.svelte";

  type Tab = "chat" | "images" | "speech" | "audio";

  const selectedTabStore = persistentStore<Tab>("playground-selected-tab", "chat");

  function selectTab(tab: Tab) {
    selectedTabStore.set(tab);
  }
</script>

<div class="card h-full flex flex-col">
  <!-- Tab navigation -->
  <div class="shrink-0 flex gap-2 mb-4">
    <button
      class="px-4 py-2 rounded font-medium transition-colors {$selectedTabStore === 'chat'
        ? 'bg-primary text-btn-primary-text'
        : 'bg-surface hover:bg-secondary-hover border border-gray-200 dark:border-white/10'}"
      onclick={() => selectTab("chat")}
    >
      Chat
    </button>
    <button
      class="px-4 py-2 rounded font-medium transition-colors {$selectedTabStore === 'images'
        ? 'bg-primary text-btn-primary-text'
        : 'bg-surface hover:bg-secondary-hover border border-gray-200 dark:border-white/10'}"
      onclick={() => selectTab("images")}
    >
      Images
    </button>
    <button
      class="px-4 py-2 rounded font-medium transition-colors {$selectedTabStore === 'speech'
        ? 'bg-primary text-btn-primary-text'
        : 'bg-surface hover:bg-secondary-hover border border-gray-200 dark:border-white/10'}"
      onclick={() => selectTab("speech")}
    >
      Speech
    </button>
    <button
      class="px-4 py-2 rounded font-medium transition-colors {$selectedTabStore === 'audio'
        ? 'bg-primary text-btn-primary-text'
        : 'bg-surface hover:bg-secondary-hover border border-gray-200 dark:border-white/10'}"
      onclick={() => selectTab("audio")}
    >
      Transcription
    </button>
  </div>

  <!-- Tab content -->
  <div class="flex-1 overflow-hidden relative">
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
  </div>
</div>

<style>
  .hidden {
    display: none;
  }
</style>
