<script lang="ts">
  import { persistentStore } from "../stores/persistent";
  import ChatInterface from "../components/playground/ChatInterface.svelte";
  import PlaceholderTab from "../components/playground/PlaceholderTab.svelte";

  type Tab = "chat" | "images" | "audio";

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
      class="px-4 py-2 rounded font-medium transition-colors {$selectedTabStore === 'audio'
        ? 'bg-primary text-btn-primary-text'
        : 'bg-surface hover:bg-secondary-hover border border-gray-200 dark:border-white/10'}"
      onclick={() => selectTab("audio")}
    >
      Audio
    </button>
  </div>

  <!-- Tab content -->
  <div class="flex-1 overflow-hidden">
    {#if $selectedTabStore === "chat"}
      <ChatInterface />
    {:else if $selectedTabStore === "images"}
      <PlaceholderTab featureName="Image Generation" />
    {:else if $selectedTabStore === "audio"}
      <PlaceholderTab featureName="Audio Processing" />
    {/if}
  </div>
</div>
