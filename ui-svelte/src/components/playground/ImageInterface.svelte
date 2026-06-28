<script lang="ts">
  import { models } from "../../stores/api";
  import { persistentStore } from "../../stores/persistent";
  import { generateImage } from "../../lib/imageApi";
  import { generateSdImage, fetchSdLoras } from "../../lib/sdApi";
  import { playgroundStores } from "../../stores/playgroundActivity";
  import ModelSelector from "./ModelSelector.svelte";
  import ExpandableTextarea from "./ExpandableTextarea.svelte";
  import type { ImageApiMode, SdApiLora, SdApiLoraRef } from "../../lib/types";
  import { Button } from "$lib/components/ui/button/index.js";
  import { Input } from "$lib/components/ui/input/index.js";
  import { Textarea } from "$lib/components/ui/textarea/index.js";
  import * as Select from "$lib/components/ui/select/index.js";
  import { Download, X } from "@lucide/svelte";

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
    <ModelSelector bind:value={$selectedModelStore} placeholder="Select an image model..." disabled={isGenerating} capabilities={["image_generation", "image_to_image"]} matchAny={true} />

    <Select.Root
      type="single"
      value={$apiModeStore}
      onValueChange={(v) => v && apiModeStore.set(v as ImageApiMode)}
    >
      <Select.Trigger class="h-9 w-32">{$apiModeStore}</Select.Trigger>
      <Select.Content>
        <Select.Item value="openai">OpenAI</Select.Item>
        <Select.Item value="sdapi">SDAPI</Select.Item>
      </Select.Content>
    </Select.Root>

    <Select.Root
      type="single"
      value={$selectedSizeStore}
      onValueChange={(v) => v && selectedSizeStore.set(v)}
    >
      <Select.Trigger class="h-9 w-40">{$selectedSizeStore}</Select.Trigger>
      <Select.Content>
        <Select.Group>
          <Select.Label>Square</Select.Label>
          <Select.Item value="512x512">512x512</Select.Item>
          <Select.Item value="1024x1024">1024x1024</Select.Item>
        </Select.Group>
        <Select.Separator />
        <Select.Group>
          <Select.Label>Landscape</Select.Label>
          <Select.Item value="1024x768">1024x768 (4:3)</Select.Item>
          <Select.Item value="1280x720">1280x720 (16:9)</Select.Item>
          <Select.Item value="1792x1024">1792x1024 (SDXL)</Select.Item>
        </Select.Group>
        <Select.Separator />
        <Select.Group>
          <Select.Label>Portrait</Select.Label>
          <Select.Item value="768x1024">768x1024 (3:4)</Select.Item>
          <Select.Item value="720x1280">720x1280 (9:16)</Select.Item>
          <Select.Item value="1024x1792">1024x1792 (SDXL)</Select.Item>
        </Select.Group>
      </Select.Content>
    </Select.Root>

    {#if isSdapi}
      <Button variant="outline" onclick={() => showSettings = !showSettings}>
        {showSettings ? "Hide Settings" : "Settings"}
      </Button>
    {/if}
  </div>

  <!-- SDAPI Settings Panel -->
  {#if isSdapi && showSettings}
    <div class="shrink-0 mb-4 p-4 rounded-md border border-border bg-background">
      <div class="grid grid-cols-2 md:grid-cols-4 gap-3 mb-3">
        <label class="flex flex-col gap-1">
          <span class="text-xs text-muted-foreground">Steps</span>
          <Input
            type="number"
            class="h-8"
            bind:value={$sdStepsStore}
            min="1"
            max="150"
          />
        </label>
        <label class="flex flex-col gap-1">
          <span class="text-xs text-muted-foreground">CFG Scale</span>
          <Input
            type="number"
            class="h-8"
            bind:value={$sdCfgScaleStore}
            min="1"
            max="30"
            step="0.5"
          />
        </label>
        <label class="flex flex-col gap-1">
          <span class="text-xs text-muted-foreground">Seed (-1 = random)</span>
          <Input
            type="number"
            class="h-8"
            bind:value={$sdSeedStore}
            min="-1"
          />
        </label>
        <label class="flex flex-col gap-1">
          <span class="text-xs text-muted-foreground">Batch Size</span>
          <Input
            type="number"
            class="h-8"
            bind:value={$sdBatchSizeStore}
            min="1"
            max="8"
          />
        </label>
        <label class="flex flex-col gap-1">
          <span class="text-xs text-muted-foreground">Sampler</span>
          <Select.Root
            type="single"
            value={$sdSamplerStore}
            onValueChange={(v) => sdSamplerStore.set(v ?? "")}
          >
            <Select.Trigger class="h-8">{$sdSamplerStore || "Default"}</Select.Trigger>
            <Select.Content>
              <Select.Item value="">Default</Select.Item>
              <Select.Item value="euler_a">euler_a</Select.Item>
              <Select.Item value="euler">euler</Select.Item>
              <Select.Item value="heun">heun</Select.Item>
              <Select.Item value="dpm2">dpm2</Select.Item>
              <Select.Item value="dpmpp2s_a">dpmpp2s_a</Select.Item>
              <Select.Item value="dpmpp2m">dpmpp2m</Select.Item>
              <Select.Item value="dpmpp2mv2">dpmpp2mv2</Select.Item>
              <Select.Item value="ipndm">ipndm</Select.Item>
              <Select.Item value="ipndm_v">ipndm_v</Select.Item>
              <Select.Item value="lcm">lcm</Select.Item>
              <Select.Item value="ddim_trailing">ddim_trailing</Select.Item>
              <Select.Item value="tcd">tcd</Select.Item>
            </Select.Content>
          </Select.Root>
        </label>
        <label class="flex flex-col gap-1">
          <span class="text-xs text-muted-foreground">Scheduler</span>
          <Select.Root
            type="single"
            value={$sdSchedulerStore}
            onValueChange={(v) => sdSchedulerStore.set(v ?? "")}
          >
            <Select.Trigger class="h-8">{$sdSchedulerStore || "Auto for model"}</Select.Trigger>
            <Select.Content>
              <Select.Item value="">Auto for model</Select.Item>
              <Select.Item value="discrete">discrete</Select.Item>
              <Select.Item value="karras">karras</Select.Item>
              <Select.Item value="exponential">exponential</Select.Item>
              <Select.Item value="ays">ays</Select.Item>
              <Select.Item value="gits">gits</Select.Item>
            </Select.Content>
          </Select.Root>
        </label>
      </div>

      <label class="flex flex-col gap-1 mb-3">
        <span class="text-xs text-muted-foreground">Negative Prompt</span>
        <Textarea
          bind:value={$sdNegativePromptStore}
          rows={2}
          placeholder="Elements to avoid..."
        ></Textarea>
      </label>

      <!-- LoRA Selection -->
      <div>
        <span class="text-xs text-muted-foreground block mb-1">LoRAs</span>
        <div class="flex items-center gap-2 mb-2">
          <Button
            variant="outline"
            size="sm"
            onclick={loadLoras}
            disabled={!$selectedModelStore || isLoadingLoras}
          >
            {isLoadingLoras ? "Loading..." : lorasLoaded ? "Reload LoRAs" : "Load LoRAs"}
          </Button>
          {#if lorasLoaded && availableLoras.length > 0}
            <Select.Root
              type="single"
              value=""
              onValueChange={(v) => {
                if (v) {
                  const lora = availableLoras.find((l) => l.path === v);
                  if (lora && !selectedLoras.some((s) => s.path === v)) {
                    selectedLoras = [...selectedLoras, { path: lora.path, multiplier: 1.0 }];
                  }
                }
              }}
            >
              <Select.Trigger class="h-8 flex-1">Add a LoRA...</Select.Trigger>
              <Select.Content>
                {#each availableLoras.filter((l) => !selectedLoras.some((s) => s.path === l.path)) as lora (lora.path)}
                  <Select.Item value={lora.path}>{lora.name}</Select.Item>
                {/each}
              </Select.Content>
            </Select.Root>
          {/if}
        </div>
        {#if loraError}
          <p class="text-xs text-red-500 mb-1">{loraError}</p>
        {/if}
        {#if lorasLoaded && availableLoras.length === 0}
          <p class="text-xs text-muted-foreground">No LoRAs available</p>
        {/if}
        {#if selectedLoras.length > 0}
          <div class="flex flex-col gap-1.5">
            {#each selectedLoras as lora}
              <div class="flex items-center gap-2 text-sm">
                <span class="flex-1 truncate">{getLoraName(lora.path)}</span>
                <Input
                  type="number"
                  class="h-7 w-20 text-xs"
                  value={lora.multiplier}
                  oninput={(e) => updateLoraMultiplier(lora.path, parseFloat((e.target as HTMLInputElement).value) || 1)}
                  min="0"
                  max="2"
                  step="0.1"
                />
                <Button
                  variant="outline"
                  size="sm"
                  class="h-7 px-1.5 text-xs hover:bg-destructive hover:text-destructive-foreground"
                  onclick={() => removeLora(lora.path)}
                  aria-label="Remove LoRA"
                >
                  <X class="size-3" />
                </Button>
              </div>
            {/each}
          </div>
        {/if}
      </div>
    </div>
  {/if}

  <!-- Empty state for no models configured -->
  {#if !hasModels}
    <div class="flex-1 flex items-center justify-center text-muted-foreground">
      <p>No models configured. Add models to your configuration to generate images.</p>
    </div>
  {:else}
    <!-- Image display area -->
    <div class="flex-1 overflow-auto mb-4 flex items-center justify-center bg-background border border-border rounded-md">
      {#if isGenerating}
        <div class="text-center text-muted-foreground">
          <div class="inline-block w-8 h-8 border-4 border-primary border-t-transparent rounded-full animate-spin mb-2"></div>
          <p>Generating image...</p>
        </div>
      {:else if error}
        <div class="text-center text-red-500 p-4">
          <p class="font-medium">Error</p>
          <p class="text-sm mt-1">{error}</p>
        </div>
      {:else if generatedImages.length > 1}
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
              <Button
                variant="secondary"
                size="icon"
                class="absolute bottom-2 right-2 h-8 w-8 bg-black/60 hover:bg-black/80 text-white"
                onclick={(e) => { e.stopPropagation(); downloadImage(i); }}
                aria-label="Download image"
              >
                <Download class="size-4" />
              </Button>
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
          <Button
            variant="secondary"
            size="icon"
            class="absolute bottom-2 right-2 bg-black/60 hover:bg-black/80 text-white"
            onclick={(e) => { e.stopPropagation(); downloadImage(0); }}
            aria-label="Download image"
          >
            <Download class="size-5" />
          </Button>
        </div>
      {:else}
        <div class="text-center text-muted-foreground">
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
          <Button variant="destructive" class="flex-1 md:flex-none" onclick={cancelGeneration}>
            Cancel
          </Button>
        {:else}
          <Button
            class="flex-1 md:flex-none"
            onclick={generate}
            disabled={!prompt.trim() || !$selectedModelStore}
          >
            Generate
          </Button>
          <Button
            variant="outline"
            class="flex-1 md:flex-none"
            onclick={clearImage}
            disabled={generatedImages.length === 0 && !error && !prompt.trim()}
          >
            Clear
          </Button>
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
    <Button
      variant="secondary"
      size="icon"
      class="absolute top-4 right-4 bg-black/60 hover:bg-black/80 text-white"
      onclick={() => closeFullscreen()}
      aria-label="Close fullscreen"
    >
      <X class="size-6" />
    </Button>
    <img
      src={generatedImages[fullscreenIndex]}
      alt="AI generated content"
      class="max-w-full max-h-full object-contain pointer-events-none"
    />
  </div>
{/if}
