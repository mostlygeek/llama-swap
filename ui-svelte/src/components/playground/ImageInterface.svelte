<script lang="ts">
  import { models } from "../../stores/api";
  import { persistentStore } from "../../stores/persistent";
  import { generateImage } from "../../lib/imageApi";
  import ModelSelector from "./ModelSelector.svelte";
  import ExpandableTextarea from "./ExpandableTextarea.svelte";

  const selectedModelStore = persistentStore<string>("playground-image-model", "");
  const selectedSizeStore = persistentStore<string>("playground-image-size", "1024x1024");

  let prompt = $state("");
  let isGenerating = $state(false);
  let generatedImage = $state<string | null>(null);
  let error = $state<string | null>(null);
  let abortController = $state<AbortController | null>(null);
  let showFullscreen = $state(false);

  let hasModels = $derived($models.some((m) => !m.unlisted));

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
    prompt = "";
  }

  function downloadImage() {
    if (!generatedImage) return;

    const link = document.createElement("a");
    link.href = generatedImage;
    link.download = `generated-image-${Date.now()}.png`;
    document.body.appendChild(link);
    link.click();
    document.body.removeChild(link);
  }

  function openFullscreen() {
    showFullscreen = true;
  }

  function closeFullscreen(event?: MouseEvent) {
    // Only close if clicking the background, not the image
    if (event && event.target !== event.currentTarget) {
      return;
    }
    showFullscreen = false;
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
  <div class="shrink-0 flex flex-wrap gap-2 mb-4">
    <ModelSelector bind:value={$selectedModelStore} placeholder="Select an image model..." disabled={isGenerating} />
    <select
      class="px-3 py-2 rounded border border-gray-200 dark:border-white/10 bg-surface focus:outline-none focus:ring-2 focus:ring-primary"
      bind:value={$selectedSizeStore}
      disabled={isGenerating}
    >
      <optgroup label="Square">
        <option value="512x512">512x512</option>
        <option value="1024x1024">1024x1024</option>
      </optgroup>
      <optgroup label="Landscape">
        <option value="1024x768">1024x768 (4:3)</option>
        <option value="1280x720">1280x720 (16:9)</option>
        <option value="1792x1024">1792x1024 (SDXL)</option>
      </optgroup>
      <optgroup label="Portrait">
        <option value="768x1024">768x1024 (3:4)</option>
        <option value="720x1280">720x1280 (9:16)</option>
        <option value="1024x1792">1024x1792 (SDXL)</option>
      </optgroup>
    </select>
  </div>

  <!-- Empty state for no models configured -->
  {#if !hasModels}
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
        <div class="relative max-w-full max-h-full flex items-center justify-center">
          <button
            class="p-0 border-0 bg-transparent cursor-pointer"
            onclick={openFullscreen}
            aria-label="View fullscreen"
          >
            <img
              src={generatedImage}
              alt="AI generated content"
              class="max-w-full max-h-full object-contain hover:opacity-90 transition-opacity"
            />
          </button>
          <button
            class="absolute bottom-2 right-2 p-2 bg-black/60 hover:bg-black/80 text-white rounded-full transition-colors"
            onclick={(e) => { e.stopPropagation(); downloadImage(); }}
            aria-label="Download image"
          >
            <svg class="w-5 h-5" fill="none" stroke="currentColor" viewBox="0 0 24 24">
              <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M4 16v1a3 3 0 003 3h10a3 3 0 003-3v-1m-4-4l-4 4m0 0l-4-4m4 4V4"></path>
            </svg>
          </button>
        </div>
      {:else}
        <div class="text-center text-txtsecondary">
          <p>Enter a prompt below to generate an image</p>
        </div>
      {/if}
    </div>

    <!-- Prompt input area -->
    <div class="shrink-0 flex flex-col md:flex-row gap-2">
      <ExpandableTextarea
        bind:value={prompt}
        placeholder="Describe the image you want to generate..."
        rows={3}
        onkeydown={handleKeyDown}
        disabled={isGenerating || !$selectedModelStore}
      />
      <div class="flex flex-row md:flex-col gap-2">
        {#if isGenerating}
          <button class="btn bg-red-500 hover:bg-red-600 text-white flex-1 md:flex-none" onclick={cancelGeneration}>
            Cancel
          </button>
        {:else}
          <button
            class="btn bg-primary text-btn-primary-text hover:opacity-90 flex-1 md:flex-none"
            onclick={generate}
            disabled={!prompt.trim() || !$selectedModelStore}
          >
            Generate
          </button>
          <button
            class="btn flex-1 md:flex-none"
            onclick={clearImage}
            disabled={!generatedImage && !error && !prompt.trim()}
          >
            Clear
          </button>
        {/if}
      </div>
    </div>
  {/if}
</div>

<!-- Fullscreen dialog -->
{#if showFullscreen && generatedImage}
  <div
    class="fixed inset-0 bg-black/90 z-50 flex items-center justify-center p-4"
    onclick={(e) => closeFullscreen(e)}
    onkeydown={(e) => e.key === 'Escape' && closeFullscreen()}
    role="dialog"
    aria-modal="true"
    tabindex="-1"
  >
    <button
      class="absolute top-4 right-4 text-white hover:text-gray-300 text-2xl w-10 h-10 flex items-center justify-center rounded-full hover:bg-white/10 transition-colors"
      onclick={() => closeFullscreen()}
      aria-label="Close fullscreen"
    >
      ×
    </button>
    <img
      src={generatedImage}
      alt="AI generated content"
      class="max-w-full max-h-full object-contain pointer-events-none"
    />
  </div>
{/if}
