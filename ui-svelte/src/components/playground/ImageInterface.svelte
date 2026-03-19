<script lang="ts">
  import { models } from "../../stores/api";
  import { persistentStore } from "../../stores/persistent";
  import { generateImage } from "../../lib/imageApi";
  import { generateSdImage, fetchSdLoras } from "../../lib/sdApi";
  import { playgroundStores } from "../../stores/playgroundActivity";
  import ModelSelector from "./ModelSelector.svelte";
  import ExpandableTextarea from "./ExpandableTextarea.svelte";
  import type { ImageApiMode, SdApiLora, SdApiLoraRef } from "../../lib/types";

  const selectedModelStore = persistentStore<string>("playground-image-model", "");
  const selectedSizeStore = persistentStore<string>("playground-image-size", "1024x1024");
  const apiModeStore = persistentStore<ImageApiMode>("playground-image-api-mode", "openai");

  // SDAPI persistent settings
  const sdNegativePromptStore = persistentStore<string>("playground-sdapi-negative-prompt", "");
  const sdStepsStore = persistentStore<number>("playground-sdapi-steps", 20);
  const sdCfgScaleStore = persistentStore<number>("playground-sdapi-cfg-scale", 7);
  const sdSeedStore = persistentStore<number>("playground-sdapi-seed", -1);
  const sdSamplerStore = persistentStore<string>("playground-sdapi-sampler", "");
  const sdSchedulerStore = persistentStore<string>("playground-sdapi-scheduler", "");
  const sdBatchSizeStore = persistentStore<number>("playground-sdapi-batch-size", 1);

  let prompt = $state("");
  let isGenerating = $state(false);
  let generatedImages = $state<string[]>([]);
  let error = $state<string | null>(null);
  let abortController = $state<AbortController | null>(null);
  let showFullscreen = $state(false);
  let fullscreenIndex = $state(0);
  let showSettings = $state(false);

  // SDAPI lora state
  let availableLoras = $state<SdApiLora[]>([]);
  let selectedLoras = $state<SdApiLoraRef[]>([]);
  let isLoadingLoras = $state(false);
  let lorasLoaded = $state(false);
  let loraError = $state<string | null>(null);

  let hasModels = $derived($models.some((m) => !m.unlisted));
  let isSdapi = $derived($apiModeStore === "sdapi");

  $effect(() => {
    playgroundStores.imageGenerating.set(isGenerating);
  });

  async function loadLoras() {
    if (!$selectedModelStore || isLoadingLoras) return;
    isLoadingLoras = true;
    loraError = null;
    try {
      const loras = await fetchSdLoras($selectedModelStore);
      availableLoras = loras;
      lorasLoaded = true;
    } catch (err) {
      availableLoras = [];
      loraError = err instanceof Error ? err.message : "Failed to load LoRAs";
      lorasLoaded = false;
    } finally {
      isLoadingLoras = false;
    }
  }

  function addLora(event: Event) {
    const select = event.target as HTMLSelectElement;
    const path = select.value;
    if (!path) return;

    const lora = availableLoras.find((l) => l.path === path);
    if (lora && !selectedLoras.some((l) => l.path === path)) {
      selectedLoras = [...selectedLoras, { path: lora.path, multiplier: 1.0 }];
    }
    select.value = "";
  }

  function removeLora(path: string) {
    selectedLoras = selectedLoras.filter((l) => l.path !== path);
  }

  function updateLoraMultiplier(path: string, multiplier: number) {
    selectedLoras = selectedLoras.map((l) =>
      l.path === path ? { ...l, multiplier } : l
    );
  }

  function getLoraName(path: string): string {
    return availableLoras.find((l) => l.path === path)?.name ?? path;
  }

  async function generate() {
    const trimmedPrompt = prompt.trim();
    if (!trimmedPrompt || !$selectedModelStore || isGenerating) return;

    isGenerating = true;
    error = null;
    abortController = new AbortController();

    try {
      if (isSdapi) {
        const [w, h] = $selectedSizeStore.split("x").map(Number);
        const request = {
          model: $selectedModelStore,
          prompt: trimmedPrompt,
          negative_prompt: $sdNegativePromptStore || undefined,
          width: w,
          height: h,
          steps: $sdStepsStore,
          cfg_scale: $sdCfgScaleStore,
          seed: $sdSeedStore,
          batch_size: $sdBatchSizeStore,
          sampler_name: $sdSamplerStore || undefined,
          scheduler: $sdSchedulerStore || undefined,
          lora: selectedLoras.length > 0 ? selectedLoras : undefined,
        };

        const response = await generateSdImage(request, abortController.signal);
        if (response.images && response.images.length > 0) {
          generatedImages = response.images.map(
            (img) => `data:image/png;base64,${img}`
          );
        }
      } else {
        const response = await generateImage(
          $selectedModelStore,
          trimmedPrompt,
          $selectedSizeStore,
          abortController.signal
        );

        if (response.data && response.data.length > 0) {
          const imageData = response.data[0];
          if (imageData.b64_json) {
            generatedImages = [`data:image/png;base64,${imageData.b64_json}`];
          } else if (imageData.url) {
            generatedImages = [imageData.url];
          }
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
    generatedImages = [];
    error = null;
    prompt = "";
  }

  function downloadImage(index: number = 0) {
    const img = generatedImages[index];
    if (!img) return;

    const link = document.createElement("a");
    link.href = img;
    link.download = `generated-image-${Date.now()}-${index}.png`;
    document.body.appendChild(link);
    link.click();
    document.body.removeChild(link);
  }

  function openFullscreen(index: number = 0) {
    fullscreenIndex = index;
    showFullscreen = true;
  }

  function closeFullscreen(event?: MouseEvent) {
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
  <!-- Model selector and mode toggle -->
  <div class="shrink-0 flex flex-wrap gap-2 mb-4">
    <ModelSelector bind:value={$selectedModelStore} placeholder="Select an image model..." disabled={isGenerating} />

    <select
      class="px-3 py-2 rounded border border-gray-200 dark:border-white/10 bg-surface focus:outline-none focus:ring-2 focus:ring-primary"
      bind:value={$apiModeStore}
      disabled={isGenerating}
    >
      <option value="openai">OpenAI</option>
      <option value="sdapi">SDAPI</option>
    </select>

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

    {#if isSdapi}
      <button
        class="px-3 py-2 rounded border border-gray-200 dark:border-white/10 bg-surface hover:bg-secondary-hover transition-colors"
        onclick={() => showSettings = !showSettings}
      >
        {showSettings ? "Hide Settings" : "Settings"}
      </button>
    {/if}
  </div>

  <!-- SDAPI Settings Panel -->
  {#if isSdapi && showSettings}
    <div class="shrink-0 mb-4 p-4 rounded border border-gray-200 dark:border-white/10 bg-surface">
      <div class="grid grid-cols-2 md:grid-cols-4 gap-3 mb-3">
        <label class="flex flex-col gap-1">
          <span class="text-xs text-txtsecondary">Steps</span>
          <input
            type="number"
            class="px-2 py-1 rounded border border-gray-200 dark:border-white/10 bg-surface focus:outline-none focus:ring-2 focus:ring-primary"
            bind:value={$sdStepsStore}
            min="1"
            max="150"
          />
        </label>
        <label class="flex flex-col gap-1">
          <span class="text-xs text-txtsecondary">CFG Scale</span>
          <input
            type="number"
            class="px-2 py-1 rounded border border-gray-200 dark:border-white/10 bg-surface focus:outline-none focus:ring-2 focus:ring-primary"
            bind:value={$sdCfgScaleStore}
            min="1"
            max="30"
            step="0.5"
          />
        </label>
        <label class="flex flex-col gap-1">
          <span class="text-xs text-txtsecondary">Seed (-1 = random)</span>
          <input
            type="number"
            class="px-2 py-1 rounded border border-gray-200 dark:border-white/10 bg-surface focus:outline-none focus:ring-2 focus:ring-primary"
            bind:value={$sdSeedStore}
            min="-1"
          />
        </label>
        <label class="flex flex-col gap-1">
          <span class="text-xs text-txtsecondary">Batch Size</span>
          <input
            type="number"
            class="px-2 py-1 rounded border border-gray-200 dark:border-white/10 bg-surface focus:outline-none focus:ring-2 focus:ring-primary"
            bind:value={$sdBatchSizeStore}
            min="1"
            max="8"
          />
        </label>
        <label class="flex flex-col gap-1">
          <span class="text-xs text-txtsecondary">Sampler</span>
          <select
            class="px-2 py-1 rounded border border-gray-200 dark:border-white/10 bg-surface focus:outline-none focus:ring-2 focus:ring-primary"
            bind:value={$sdSamplerStore}
          >
            <option value="">Default</option>
            <option value="euler_a">euler_a</option>
            <option value="euler">euler</option>
            <option value="heun">heun</option>
            <option value="dpm2">dpm2</option>
            <option value="dpmpp2s_a">dpmpp2s_a</option>
            <option value="dpmpp2m">dpmpp2m</option>
            <option value="dpmpp2mv2">dpmpp2mv2</option>
            <option value="ipndm">ipndm</option>
            <option value="ipndm_v">ipndm_v</option>
            <option value="lcm">lcm</option>
            <option value="ddim_trailing">ddim_trailing</option>
            <option value="tcd">tcd</option>
          </select>
        </label>
        <label class="flex flex-col gap-1">
          <span class="text-xs text-txtsecondary">Scheduler</span>
          <select
            class="px-2 py-1 rounded border border-gray-200 dark:border-white/10 bg-surface focus:outline-none focus:ring-2 focus:ring-primary"
            bind:value={$sdSchedulerStore}
          >
            <option value="">Auto for model</option>
            <option value="discrete">discrete</option>
            <option value="karras">karras</option>
            <option value="exponential">exponential</option>
            <option value="ays">ays</option>
            <option value="gits">gits</option>
          </select>
        </label>
      </div>

      <label class="flex flex-col gap-1 mb-3">
        <span class="text-xs text-txtsecondary">Negative Prompt</span>
        <textarea
          class="px-2 py-1 rounded border border-gray-200 dark:border-white/10 bg-surface focus:outline-none focus:ring-2 focus:ring-primary resize-y text-sm"
          bind:value={$sdNegativePromptStore}
          rows="2"
          placeholder="Elements to avoid..."
        ></textarea>
      </label>

      <!-- LoRA Selection -->
      <div>
        <span class="text-xs text-txtsecondary block mb-1">LoRAs</span>
        <div class="flex items-center gap-2 mb-2">
          <button
            class="px-3 py-1.5 text-sm rounded border border-gray-200 dark:border-white/10 bg-surface hover:bg-secondary-hover transition-colors disabled:opacity-50"
            onclick={loadLoras}
            disabled={!$selectedModelStore || isLoadingLoras}
          >
            {isLoadingLoras ? "Loading..." : lorasLoaded ? "Reload LoRAs" : "Load LoRAs"}
          </button>
          {#if lorasLoaded && availableLoras.length > 0}
            <select
              class="flex-1 px-2 py-1.5 text-sm rounded border border-gray-200 dark:border-white/10 bg-surface focus:outline-none focus:ring-2 focus:ring-primary"
              onchange={addLora}
            >
              <option value="">Add a LoRA...</option>
              {#each availableLoras.filter((l) => !selectedLoras.some((s) => s.path === l.path)) as lora}
                <option value={lora.path}>{lora.name}</option>
              {/each}
            </select>
          {/if}
        </div>
        {#if loraError}
          <p class="text-xs text-red-500 mb-1">{loraError}</p>
        {/if}
        {#if lorasLoaded && availableLoras.length === 0}
          <p class="text-xs text-txtsecondary">No LoRAs available</p>
        {/if}
        {#if selectedLoras.length > 0}
          <div class="flex flex-col gap-1.5">
            {#each selectedLoras as lora}
              <div class="flex items-center gap-2 text-sm">
                <span class="flex-1 truncate">{getLoraName(lora.path)}</span>
                <input
                  type="number"
                  class="w-20 px-1.5 py-1 text-xs rounded border border-gray-200 dark:border-white/10 bg-surface focus:outline-none focus:ring-1 focus:ring-primary"
                  value={lora.multiplier}
                  oninput={(e) => updateLoraMultiplier(lora.path, parseFloat((e.target as HTMLInputElement).value) || 1)}
                  min="0"
                  max="2"
                  step="0.1"
                />
                <button
                  class="px-1.5 py-0.5 text-xs rounded border border-gray-200 dark:border-white/10 hover:bg-red-500 hover:text-white hover:border-red-500 transition-colors"
                  onclick={() => removeLora(lora.path)}
                  aria-label="Remove LoRA"
                >
                  x
                </button>
              </div>
            {/each}
          </div>
        {/if}
      </div>
    </div>
  {/if}

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
      {:else if generatedImages.length > 1}
        <!-- Grid for multiple images (batch) -->
        <div class="grid grid-cols-2 gap-2 p-2 w-full h-full overflow-auto">
          {#each generatedImages as img, i}
            <div class="relative flex items-center justify-center">
              <button
                class="p-0 border-0 bg-transparent cursor-pointer"
                onclick={() => openFullscreen(i)}
                aria-label="View fullscreen"
              >
                <img
                  src={img}
                  alt="AI generated content {i + 1}"
                  class="max-w-full max-h-full object-contain hover:opacity-90 transition-opacity"
                />
              </button>
              <button
                class="absolute bottom-2 right-2 p-1.5 bg-black/60 hover:bg-black/80 text-white rounded-full transition-colors"
                onclick={(e) => { e.stopPropagation(); downloadImage(i); }}
                aria-label="Download image"
              >
                <svg class="w-4 h-4" fill="none" stroke="currentColor" viewBox="0 0 24 24">
                  <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M4 16v1a3 3 0 003 3h10a3 3 0 003-3v-1m-4-4l-4 4m0 0l-4-4m4 4V4"></path>
                </svg>
              </button>
            </div>
          {/each}
        </div>
      {:else if generatedImages.length === 1}
        <div class="relative max-w-full max-h-full flex items-center justify-center">
          <button
            class="p-0 border-0 bg-transparent cursor-pointer"
            onclick={() => openFullscreen(0)}
            aria-label="View fullscreen"
          >
            <img
              src={generatedImages[0]}
              alt="AI generated content"
              class="max-w-full max-h-full object-contain hover:opacity-90 transition-opacity"
            />
          </button>
          <button
            class="absolute bottom-2 right-2 p-2 bg-black/60 hover:bg-black/80 text-white rounded-full transition-colors"
            onclick={(e) => { e.stopPropagation(); downloadImage(0); }}
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
            disabled={generatedImages.length === 0 && !error && !prompt.trim()}
          >
            Clear
          </button>
        {/if}
      </div>
    </div>
  {/if}
</div>

<!-- Fullscreen dialog -->
{#if showFullscreen && generatedImages[fullscreenIndex]}
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
      src={generatedImages[fullscreenIndex]}
      alt="AI generated content"
      class="max-w-full max-h-full object-contain pointer-events-none"
    />
  </div>
{/if}
