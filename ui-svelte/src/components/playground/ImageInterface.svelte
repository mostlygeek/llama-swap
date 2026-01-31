<script lang="ts">
  import { models } from "../../stores/api";
  import { persistentStore } from "../../stores/persistent";
  import { generateImage } from "../../lib/imageApi";
  import ExpandableTextarea from "./ExpandableTextarea.svelte";

  const selectedModelStore = persistentStore<string>("playground-image-model", "");
  const selectedSizeStore = persistentStore<string>("playground-image-size", "1024x1024");

  let prompt = $state("");
  let isGenerating = $state(false);
  let generatedImage = $state<string | null>(null);
  let error = $state<string | null>(null);
  let abortController = $state<AbortController | null>(null);

  // Show all models (excluding unlisted), backend will auto-load as needed
  let availableModels = $derived($models.filter((m) => !m.unlisted));

  // Group models into local and peer models by provider
  let groupedModels = $derived.by(() => {
    const local = availableModels.filter((m) => !m.peerID);
    const peerModels = availableModels.filter((m) => m.peerID);

    // Group peer models by peerID
    const peersByProvider = peerModels.reduce(
      (acc, model) => {
        const peerId = model.peerID || "unknown";
        if (!acc[peerId]) acc[peerId] = [];
        acc[peerId].push(model);
        return acc;
      },
      {} as Record<string, typeof availableModels>
    );

    return { local, peersByProvider };
  });

  async function generate() {
    const trimmedPrompt = prompt.trim();
    if (!trimmedPrompt || !$selectedModelStore || isGenerating) return;

    isGenerating = true;
    error = null;
    abortController = new AbortController();

    try {
      const response = await generateImage(
        $selectedModelStore,
        trimmedPrompt,
        $selectedSizeStore,
        abortController.signal
      );

      if (response.data && response.data.length > 0) {
        const imageData = response.data[0];
        if (imageData.b64_json) {
          generatedImage = `data:image/png;base64,${imageData.b64_json}`;
        } else if (imageData.url) {
          generatedImage = imageData.url;
        }
      }
    } catch (err) {
      if (err instanceof Error && err.name === "AbortError") {
        // User cancelled
      } else {
        error = err instanceof Error ? err.message : "An error occurred";
      }
    } finally {
      isGenerating = false;
      abortController = null;
    }
  }

  function cancelGeneration() {
    abortController?.abort();
  }

  function clearImage() {
    generatedImage = null;
    error = null;
  }

  function handleKeyDown(event: KeyboardEvent) {
    if (event.key === "Enter" && !event.shiftKey) {
      event.preventDefault();
      generate();
    }
  }
</script>

<div class="flex flex-col h-full">
  <!-- Model selector -->
  <div class="shrink-0 flex gap-2 mb-4">
    <select
      class="flex-1 px-3 py-2 rounded border border-gray-200 dark:border-white/10 bg-surface focus:outline-none focus:ring-2 focus:ring-primary"
      bind:value={$selectedModelStore}
      disabled={isGenerating}
    >
      <option value="">Select an image model...</option>
      {#if groupedModels.local.length > 0}
        <optgroup label="Local">
          {#each groupedModels.local as model (model.id)}
            <option value={model.id}>{model.id}</option>
          {/each}
        </optgroup>
      {/if}
      {#each Object.entries(groupedModels.peersByProvider).sort(([a], [b]) => a.localeCompare(b)) as [peerId, peerModels] (peerId)}
        <optgroup label="Peer: {peerId}">
          {#each peerModels as model (model.id)}
            <option value={model.id}>{model.id}</option>
          {/each}
        </optgroup>
      {/each}
    </select>
    <select
      class="px-3 py-2 rounded border border-gray-200 dark:border-white/10 bg-surface focus:outline-none focus:ring-2 focus:ring-primary"
      bind:value={$selectedSizeStore}
      disabled={isGenerating}
    >
      <option value="512x512">512x512</option>
      <option value="1024x1024">1024x1024</option>
    </select>
    <button class="btn" onclick={clearImage} disabled={!generatedImage && !error}>
      Clear
    </button>
  </div>

  <!-- Empty state for no models configured -->
  {#if availableModels.length === 0}
    <div class="flex-1 flex items-center justify-center text-txtsecondary">
      <p>No models configured. Add models to your configuration to generate images.</p>
    </div>
  {:else}
    <!-- Image display area -->
    <div class="flex-1 overflow-auto mb-4 flex items-center justify-center bg-surface border border-gray-200 dark:border-white/10 rounded">
      {#if isGenerating}
        <div class="text-center text-txtsecondary">
          <div class="inline-block w-8 h-8 border-4 border-primary border-t-transparent rounded-full animate-spin mb-2"></div>
          <p>Generating image...</p>
        </div>
      {:else if error}
        <div class="text-center text-red-500 p-4">
          <p class="font-medium">Error</p>
          <p class="text-sm mt-1">{error}</p>
        </div>
      {:else if generatedImage}
        <img
          src={generatedImage}
          alt="AI generated content"
          class="max-w-full max-h-full object-contain"
        />
      {:else}
        <div class="text-center text-txtsecondary">
          <p>Enter a prompt below to generate an image</p>
        </div>
      {/if}
    </div>

    <!-- Prompt input area -->
    <div class="shrink-0 flex gap-2">
      <ExpandableTextarea
        bind:value={prompt}
        placeholder="Describe the image you want to generate..."
        rows={2}
        onkeydown={handleKeyDown}
        disabled={isGenerating || !$selectedModelStore}
      />
      <div class="flex flex-col gap-2">
        {#if isGenerating}
          <button class="btn bg-red-500 hover:bg-red-600 text-white" onclick={cancelGeneration}>
            Cancel
          </button>
        {:else}
          <button
            class="btn bg-primary text-btn-primary-text hover:opacity-90"
            onclick={generate}
            disabled={!prompt.trim() || !$selectedModelStore}
          >
            Generate
          </button>
        {/if}
      </div>
    </div>
  {/if}
</div>
