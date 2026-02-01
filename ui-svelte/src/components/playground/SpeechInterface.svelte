<script lang="ts">
  import { models } from "../../stores/api";
  import { persistentStore } from "../../stores/persistent";
  import { generateSpeech } from "../../lib/speechApi";
  import ModelSelector from "./ModelSelector.svelte";
  import ExpandableTextarea from "./ExpandableTextarea.svelte";

  const selectedModelStore = persistentStore<string>("playground-speech-model", "");
  const selectedVoiceStore = persistentStore<string>("playground-speech-voice", "coral");
  const autoPlayStore = persistentStore<boolean>("playground-speech-autoplay", false);

  let inputText = $state("");
  let isGenerating = $state(false);
  let generatedAudioUrl = $state<string | null>(null);
  let generatedVoice = $state<string | null>(null);
  let generatedTimestamp = $state<Date | null>(null);
  let error = $state<string | null>(null);
  let abortController = $state<AbortController | null>(null);
  let audioElement = $state<HTMLAudioElement | null>(null);
  let availableVoices = $state<string[]>(["coral", "alloy", "echo", "fable", "onyx", "nova", "shimmer"]);
  let isLoadingVoices = $state(false);

  // Default voices to fall back to if API call fails
  const defaultVoices = ["coral", "alloy", "echo", "fable", "onyx", "nova", "shimmer"];
  const CACHE_KEY = "playground-speech-voices-cache";

  // Load voices cache from localStorage
  function getVoicesCache(): Record<string, string[]> {
    if (typeof window === "undefined") return {};
    try {
      const saved = localStorage.getItem(CACHE_KEY);
      return saved ? JSON.parse(saved) : {};
    } catch {
      return {};
    }
  }

  // Save voices cache to localStorage
  function saveVoicesCache(cache: Record<string, string[]>) {
    if (typeof window === "undefined") return;
    try {
      localStorage.setItem(CACHE_KEY, JSON.stringify(cache));
    } catch (e) {
      console.error("Error saving voices cache", e);
    }
  }

  let hasModels = $derived($models.some((m) => !m.unlisted));

  // Track if this is the initial page load to avoid fetching on refresh
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
      const response = await fetch(`/v1/audio/voices?model=${encodeURIComponent(model)}`);
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

  function handleVoiceChange(event: Event) {
    const value = (event.target as HTMLSelectElement).value;
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

    isGenerating = true;
    error = null;
    abortController = new AbortController();

    try {
      const audioBlob = await generateSpeech(
        $selectedModelStore,
        trimmedText,
        $selectedVoiceStore,
        abortController.signal
      );

      // Revoke previous URL to prevent memory leaks
      if (generatedAudioUrl) {
        URL.revokeObjectURL(generatedAudioUrl);
      }

      // Create object URL for the audio blob and store metadata
      generatedAudioUrl = URL.createObjectURL(audioBlob);
      generatedVoice = $selectedVoiceStore;
      generatedTimestamp = new Date();
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
    <ModelSelector bind:value={$selectedModelStore} placeholder="Select a speech model..." disabled={isGenerating} />
    <div class="flex gap-2">
      <select
        class="shrink-0 px-3 py-2 rounded border border-gray-200 dark:border-white/10 bg-surface focus:outline-none focus:ring-2 focus:ring-primary"
        value={$selectedVoiceStore}
        onchange={handleVoiceChange}
        disabled={isGenerating || isLoadingVoices || !$selectedModelStore}
      >
        {#each availableVoices as voice (voice)}
          <option value={voice}>{voice}</option>
        {/each}
        <option value="(refresh)">(refresh)</option>
      </select>
      {#if $selectedModelStore && !getVoicesCache()[$selectedModelStore]}
        <button
          class="btn shrink-0"
          onclick={refreshVoices}
          disabled={isLoadingVoices}
          title={isLoadingVoices ? "Loading voices..." : "Load voices for this model"}
        >
          {#if isLoadingVoices}
            <svg class="w-5 h-5 animate-spin" fill="none" viewBox="0 0 24 24">
              <circle class="opacity-25" cx="12" cy="12" r="10" stroke="currentColor" stroke-width="4"></circle>
              <path class="opacity-75" fill="currentColor" d="M4 12a8 8 0 018-8V0C5.373 0 0 5.373 0 12h4zm2 5.291A7.962 7.962 0 014 12H0c0 3.042 1.135 5.824 3 7.938l3-2.647z"></path>
            </svg>
          {:else}
            <svg class="w-5 h-5" fill="none" stroke="currentColor" viewBox="0 0 24 24">
              <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M4 4v5h.582m15.356 2A8.001 8.001 0 004.582 9m0 0H9m11 11v-5h-.581m0 0a8.003 8.003 0 01-15.357-2m15.357 2H15"></path>
            </svg>
          {/if}
        </button>
      {/if}
    </div>
  </div>

  <!-- Empty state for no models configured -->
  {#if !hasModels}
    <div class="flex-1 flex items-center justify-center text-txtsecondary">
      <p>No models configured. Add models to your configuration to generate speech.</p>
    </div>
  {:else}
    <!-- Audio display area -->
    <div class="shrink-0 mb-4 bg-surface border border-gray-200 dark:border-white/10 rounded p-4 md:p-6">
      {#if isGenerating}
        <div class="flex items-center justify-center text-txtsecondary py-8">
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
            <div class="flex flex-wrap gap-3 text-sm text-txtsecondary">
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
            <button
              class="btn shrink-0"
              onclick={downloadAudio}
              title="Download audio file"
            >
              <svg class="w-5 h-5" fill="none" stroke="currentColor" viewBox="0 0 24 24">
                <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M4 16v1a3 3 0 003 3h10a3 3 0 003-3v-1m-4-4l-4 4m0 0l-4-4m4 4V4"></path>
              </svg>
            </button>
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
        <div class="flex items-center justify-center text-txtsecondary py-8">
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
          <button class="btn bg-red-500 hover:bg-red-600 text-white flex-1 md:flex-none" onclick={cancelGeneration}>
            Cancel
          </button>
        {:else}
          <button
            class="btn bg-primary text-btn-primary-text hover:opacity-90 flex-1 md:flex-none"
            onclick={generate}
            disabled={!inputText.trim() || !$selectedModelStore}
          >
            Generate
          </button>
          <button
            class="btn flex-1 md:flex-none"
            onclick={clearInput}
            disabled={!inputText.trim()}
          >
            Clear
          </button>
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
