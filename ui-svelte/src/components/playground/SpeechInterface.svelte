<script lang="ts">
  import { hasListedModels } from "../../stores/api";
  import { persistentStore } from "../../stores/persistent";
  import { createPlaygroundInterface } from "../../lib/playgroundInterface";
  import { generateSpeech } from "../../lib/speechApi";
  import { playgroundStores } from "../../stores/playgroundActivity";
  import ModelSelector from "./ModelSelector.svelte";
  import ExpandableTextarea from "./ExpandableTextarea.svelte";
  import EmptyState from "../EmptyState.svelte";
  import { Button } from "$lib/components/ui/button/index.js";
  import * as Select from "$lib/components/ui/select/index.js";
  import { RefreshCw, Download } from "@lucide/svelte";
  import { playgroundSessionHeaders } from "../../lib/playgroundSession";

  const iface = createPlaygroundInterface("playground-speech-model", playgroundStores.speechGenerating);
  const selectedModelStore = iface.selectedModel;
  const busyStore = iface.busy;
  const errorStore = iface.error;
  const selectedVoiceStore = persistentStore<string>("playground-speech-voice", "coral");
  const autoPlayStore = persistentStore<boolean>("playground-speech-autoplay", false);

  let inputText = $state("");
  let isGenerating = $derived($busyStore);
  let error = $derived($errorStore);
  let generatedAudioUrl = $state<string | null>(null);
  let generatedVoice = $state<string | null>(null);
  let generatedTimestamp = $state<Date | null>(null);
  let audioElement = $state<HTMLAudioElement | null>(null);
  let availableVoices = $state<string[]>(["coral", "alloy", "echo", "fable", "onyx", "nova", "shimmer"]);
  let isLoadingVoices = $state(false);

  const defaultVoices = ["coral", "alloy", "echo", "fable", "onyx", "nova", "shimmer"];
  const CACHE_KEY = "playground-speech-voices-cache";

  function getVoicesCache(): Record<string, string[]> {
    if (typeof window === "undefined") return {};
    try {
      const saved = localStorage.getItem(CACHE_KEY);
      return saved ? JSON.parse(saved) : {};
    } catch {
      return {};
    }
  }

  function saveVoicesCache(cache: Record<string, string[]>) {
    if (typeof window === "undefined") return;
    try {
      localStorage.setItem(CACHE_KEY, JSON.stringify(cache));
    } catch (e) {
      console.error("Error saving voices cache", e);
    }
  }


  let isInitialLoad = $state(true);

  // On page load, restore cached voices for the selected model if available
  $effect(() => {
    const model = $selectedModelStore;

    if (isInitialLoad) {
      isInitialLoad = false;
      // If we have cached voices for this model, use them
      const cache = getVoicesCache();
      if (model && cache[model]) {
        availableVoices = cache[model];
      }
    }
  });

  async function refreshVoices() {
    const model = $selectedModelStore;
    if (!model || isLoadingVoices) return;

    isLoadingVoices = true;

    try {
      const response = await fetch(`/v1/audio/voices?model=${encodeURIComponent(model)}`, {
        headers: playgroundSessionHeaders,
      });
      if (!response.ok) {
        // Fall back to default voices if API call fails
        availableVoices = defaultVoices;
        const cache = getVoicesCache();
        cache[model] = defaultVoices;
        saveVoicesCache(cache);
        selectedVoiceStore.set(defaultVoices[0]);
        return;
      }
      const data = await response.json();
      // Expect response to be an array of voice strings or an object with a voices array
      const voices = Array.isArray(data) ? data : (data.voices || defaultVoices);
      const newVoices = voices.length > 0 ? voices : defaultVoices;

      availableVoices = newVoices;
      const cache = getVoicesCache();
      cache[model] = newVoices;
      saveVoicesCache(cache);

      // Reset to first available voice
      selectedVoiceStore.set(newVoices[0]);
    } catch {
      // Fall back to default voices on error
      availableVoices = defaultVoices;
      const cache = getVoicesCache();
      cache[model] = defaultVoices;
      saveVoicesCache(cache);
      selectedVoiceStore.set(defaultVoices[0]);
    } finally {
      isLoadingVoices = false;
    }
  }

  function handleVoiceChange(value: string) {
    if (value === "(refresh)") {
      refreshVoices();
    } else {
      selectedVoiceStore.set(value);
    }
  }

  // Auto-play effect when new audio is generated
  $effect(() => {
    if (generatedAudioUrl && $autoPlayStore && audioElement) {
      audioElement.load();
      audioElement.play().catch(() => {
        // Ignore auto-play errors (e.g., browser policy blocks)
      });
    }
  });

  async function generate() {
    const trimmedText = inputText.trim();
    if (!trimmedText || !$selectedModelStore || isGenerating) return;

    await iface.run(async (signal) => {
      const audioBlob = await generateSpeech(
        $selectedModelStore,
        trimmedText,
        $selectedVoiceStore,
        signal
      );

      // Revoke previous URL to prevent memory leaks
      if (generatedAudioUrl) {
        URL.revokeObjectURL(generatedAudioUrl);
      }

      // Create object URL for the audio blob and store metadata
      generatedAudioUrl = URL.createObjectURL(audioBlob);
      generatedVoice = $selectedVoiceStore;
      generatedTimestamp = new Date();
    });
  }

  function cancelGeneration() {
    iface.cancel();
  }

  function clearInput() {
    inputText = "";
  }

  function downloadAudio() {
    if (!generatedAudioUrl) return;

    const timestamp = (generatedTimestamp || new Date()).toISOString().replace(/[:.]/g, '-').slice(0, -5);
    const voice = generatedVoice || 'speech';
    const filename = `${voice}-${timestamp}.mp3`;

    const a = document.createElement('a');
    a.href = generatedAudioUrl;
    a.download = filename;
    document.body.appendChild(a);
    a.click();
    document.body.removeChild(a);
  }

  function formatTimestamp(date: Date): string {
    return date.toLocaleString(undefined, {
      month: 'short',
      day: 'numeric',
      hour: 'numeric',
      minute: '2-digit',
      hour12: true
    });
  }

  function handleKeyDown(event: KeyboardEvent) {
    if (event.key === "Enter" && !event.shiftKey) {
      event.preventDefault();
      generate();
    }
  }
</script>

<div class="flex flex-col h-full">
  <!-- Model and voice selectors -->
  <div class="shrink-0 flex gap-2 mb-4">
    <ModelSelector bind:value={$selectedModelStore} placeholder="Select a speech model..." disabled={isGenerating} capabilities={["audio_speech"]} />
    <div class="flex gap-2">
      <Select.Root
        type="single"
        value={$selectedVoiceStore}
        onValueChange={(v) => v && handleVoiceChange(v)}
      >
        <Select.Trigger class="h-9 w-40">{$selectedVoiceStore}</Select.Trigger>
        <Select.Content>
          {#each availableVoices as voice (voice)}
            <Select.Item value={voice}>{voice}</Select.Item>
          {/each}
          <Select.Item value="(refresh)">(refresh)</Select.Item>
        </Select.Content>
      </Select.Root>
      {#if $selectedModelStore && !getVoicesCache()[$selectedModelStore]}
        <Button
          variant="outline"
          size="icon"
          class="shrink-0"
          onclick={refreshVoices}
          disabled={isLoadingVoices}
          title={isLoadingVoices ? "Loading voices..." : "Load voices for this model"}
        >
          <RefreshCw class={isLoadingVoices ? "animate-spin" : ""} />
        </Button>
      {/if}
    </div>
  </div>

  <!-- Empty state for no models configured -->
  {#if !$hasListedModels}
    <EmptyState message="No models configured. Add models to your configuration to generate speech." />
  {:else}
    <!-- Audio display area -->
    <div class="shrink-0 mb-4 bg-background border border-border rounded-md p-4 md:p-6">
      {#if isGenerating}
        <div class="flex items-center justify-center text-muted-foreground py-8">
          <div class="text-center">
            <div class="inline-block w-8 h-8 border-4 border-primary border-t-transparent rounded-full animate-spin mb-2"></div>
            <p>Generating speech...</p>
          </div>
        </div>
      {:else if error}
        <div class="flex items-center justify-center py-8">
          <div class="text-center text-red-500">
            <p class="font-medium">Error</p>
            <p class="text-sm mt-1">{error}</p>
          </div>
        </div>
      {:else if generatedAudioUrl}
        <div class="flex flex-col gap-4">
          <!-- Header with metadata and download -->
          <div class="flex items-center justify-between gap-4">
            <div class="flex flex-wrap gap-3 text-sm text-muted-foreground">
              {#if generatedVoice}
                <span class="flex items-center gap-1">
                  <svg class="w-4 h-4" fill="none" stroke="currentColor" viewBox="0 0 24 24">
                    <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M19 11a7 7 0 01-7 7m0 0a7 7 0 01-7-7m7 7v4m0 0H8m4 0h4m-4-8a3 3 0 01-3-3V5a3 3 0 116 0v6a3 3 0 01-3 3z"></path>
                  </svg>
                  {generatedVoice}
                </span>
              {/if}
              {#if generatedTimestamp}
                <span class="flex items-center gap-1">
                  <svg class="w-4 h-4" fill="none" stroke="currentColor" viewBox="0 0 24 24">
                    <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M12 8v4l3 3m6-3a9 9 0 11-18 0 9 9 0 0118 0z"></path>
                  </svg>
                  {formatTimestamp(generatedTimestamp)}
                </span>
              {/if}
            </div>
            <Button variant="outline" size="icon" class="shrink-0" onclick={downloadAudio} title="Download audio file">
              <Download />
            </Button>
          </div>

          <!-- Audio player with larger controls -->
          <div class="w-full">
            <audio bind:this={audioElement} controls class="w-full h-12 md:h-16">
              <source src={generatedAudioUrl} type="audio/mpeg" />
              Your browser does not support the audio element.
            </audio>
          </div>
        </div>
      {:else}
        <div class="flex items-center justify-center text-muted-foreground py-8">
          <div class="text-center">
            <svg class="w-12 h-12 md:w-16 md:h-16 mx-auto mb-2 opacity-40" fill="none" stroke="currentColor" viewBox="0 0 24 24">
              <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M19 11a7 7 0 01-7 7m0 0a7 7 0 01-7-7m7 7v4m0 0H8m4 0h4m-4-8a3 3 0 01-3-3V5a3 3 0 116 0v6a3 3 0 01-3 3z"></path>
            </svg>
            <p>Enter text below to convert to speech</p>
          </div>
        </div>
      {/if}
    </div>

    <!-- Text input area -->
    <div class="flex-1 flex flex-col md:flex-row gap-2 min-h-0">
      <ExpandableTextarea
        bind:value={inputText}
        placeholder="Enter text to convert to speech..."
        rows={8}
        onkeydown={handleKeyDown}
        disabled={isGenerating || !$selectedModelStore}
      />
      <div class="shrink-0 flex md:flex-col gap-2">
        {#if isGenerating}
          <Button variant="destructive" class="flex-1 md:flex-none" onclick={cancelGeneration}>
            Cancel
          </Button>
        {:else}
          <Button
            class="flex-1 md:flex-none"
            onclick={generate}
            disabled={!inputText.trim() || !$selectedModelStore}
          >
            Generate
          </Button>
          <Button
            variant="outline"
            class="flex-1 md:flex-none"
            onclick={clearInput}
            disabled={!inputText.trim()}
          >
            Clear
          </Button>
          <label class="flex items-center justify-center gap-2 text-sm cursor-pointer">
            <input
              type="checkbox"
              bind:checked={$autoPlayStore}
              class="cursor-pointer"
            />
            Auto-play
          </label>
        {/if}
      </div>
    </div>
  {/if}
</div>
