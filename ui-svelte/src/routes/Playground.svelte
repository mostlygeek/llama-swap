<script lang="ts">
  import { persistentStore } from "../stores/persistent";
  import ChatInterface from "../components/playground/ChatInterface.svelte";
  import ImageInterface from "../components/playground/ImageInterface.svelte";
  import AudioInterface from "../components/playground/AudioInterface.svelte";
  import SpeechInterface from "../components/playground/SpeechInterface.svelte";

  type Tab = "chat" | "images" | "speech" | "audio";

  const selectedTabStore = persistentStore<Tab>("playground-selected-tab", "chat");
  let mobileMenuOpen = $state(false);

  const tabs: { id: Tab; label: string }[] = [
    { id: "chat", label: "Chat" },
    { id: "images", label: "Images" },
    { id: "speech", label: "Speech" },
    { id: "audio", label: "Transcription" },
  ];

  function selectTab(tab: Tab) {
    selectedTabStore.set(tab);
    mobileMenuOpen = false;
  }

  function getTabLabel(tabId: Tab): string {
    return tabs.find(t => t.id === tabId)?.label || "";
  }
</script>

<div class="card h-full flex flex-col">
  <!-- Tab navigation -->
  <div class="shrink-0 mb-4">
    <!-- Mobile: Dropdown menu (hidden on md and up) -->
    <div class="block md:hidden relative">
      <button
        class="w-full px-4 py-2 rounded font-medium transition-colors flex items-center justify-between bg-surface hover:bg-secondary-hover border border-gray-200 dark:border-white/10"
        onclick={() => (mobileMenuOpen = !mobileMenuOpen)}
      >
        <span>{getTabLabel($selectedTabStore)}</span>
        <svg
          class="w-5 h-5 transition-transform {mobileMenuOpen ? 'rotate-180' : ''}"
          fill="none"
          stroke="currentColor"
          viewBox="0 0 24 24"
        >
          <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M19 9l-7 7-7-7"></path>
        </svg>
      </button>
      {#if mobileMenuOpen}
        <div class="absolute top-full left-0 right-0 mt-1 bg-surface border border-gray-200 dark:border-white/10 rounded shadow-lg z-10">
          {#each tabs as tab (tab.id)}
            <button
              class="w-full px-4 py-2 text-left hover:bg-secondary-hover transition-colors first:rounded-t last:rounded-b {$selectedTabStore === tab.id ? 'bg-primary/10 font-medium' : ''}"
              onclick={() => selectTab(tab.id)}
            >
              {tab.label}
            </button>
          {/each}
        </div>
      {/if}
    </div>

    <!-- Desktop: Tab buttons (shown on md and up) -->
    <div class="hidden md:flex flex-wrap gap-2">
      {#each tabs as tab (tab.id)}
        <button
          class="px-4 py-2 rounded font-medium transition-colors {$selectedTabStore === tab.id
            ? 'bg-primary text-btn-primary-text'
            : 'bg-surface hover:bg-secondary-hover border border-gray-200 dark:border-white/10'}"
          onclick={() => selectTab(tab.id)}
        >
          {tab.label}
        </button>
      {/each}
    </div>
  </div>

  <!-- Tab content -->
  <div class="flex-1 overflow-hidden relative">
    <div class="h-full" class:tab-hidden={$selectedTabStore !== "chat"}>
      <ChatInterface />
    </div>
    <div class="h-full" class:tab-hidden={$selectedTabStore !== "images"}>
      <ImageInterface />
    </div>
    <div class="h-full" class:tab-hidden={$selectedTabStore !== "speech"}>
      <SpeechInterface />
    </div>
    <div class="h-full" class:tab-hidden={$selectedTabStore !== "audio"}>
      <AudioInterface />
    </div>
  </div>
</div>

<style>
  .tab-hidden {
    display: none;
  }
</style>
