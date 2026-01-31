<script lang="ts">
  import { models } from "../../stores/api";
  import { persistentStore } from "../../stores/persistent";
  import { generateSpeech } from "../../lib/speechApi";
  import ExpandableTextarea from "./ExpandableTextarea.svelte";

  const selectedModelStore = persistentStore<string>("playground-speech-model", "");
  const selectedVoiceStore = persistentStore<string>("playground-speech-voice", "coral");
  const autoPlayStore = persistentStore<boolean>("playground-speech-autoplay", false);

  let inputText = $state("");
  let isGenerating = $state(false);
  let generatedAudioUrl = $state<string | null>(null);
  let generatedText = $state<string | null>(null);
  let generatedVoice = $state<string | null>(null);
  let generatedTimestamp = $state<Date | null>(null);
  let error = $state<string | null>(null);
  let abortController = $state<AbortController | null>(null);
  let audioElement = $state<HTMLAudioElement | null>(null);
  let availableVoices = $state<string[]>(["coral", "alloy", "echo", "fable", "onyx", "nova", "shimmer"]);

  // Default voices to fall back to if API call fails
  const defaultVoices = ["coral", "alloy", "echo", "fable", "onyx", "nova", "shimmer"];

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

  // Fetch available voices when model changes
  $effect(() => {
    const model = $selectedModelStore;
    if (!model) {
      availableVoices = defaultVoices;
      return;
    }

    // Fetch voices from API
    fetch(`/v1/audio/voices?model=${encodeURIComponent(model)}`)
      .then(async (response) => {
        if (!response.ok) {
          // Fall back to default voices if API call fails
          availableVoices = defaultVoices;
          return;
        }
        const data = await response.json();
        // Expect response to be an array of voice strings or an object with a voices array
        const voices = Array.isArray(data) ? data : (data.voices || defaultVoices);
        availableVoices = voices.length > 0 ? voices : defaultVoices;

        // If current voice is not in the new list, reset to first available voice
        if (!availableVoices.includes($selectedVoiceStore)) {
          selectedVoiceStore.set(availableVoices[0]);
        }
      })
      .catch(() => {
        // Fall back to default voices on error
        availableVoices = defaultVoices;
      });
  });

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
      generatedText = trimmedText;
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

  function clearAudio() {
    if (generatedAudioUrl) {
      URL.revokeObjectURL(generatedAudioUrl);
    }
    generatedAudioUrl = null;
    generatedText = null;
    generatedVoice = null;
    generatedTimestamp = null;
    error = null;
    inputText = "";
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
    <select
      class="min-w-0 flex-1 px-3 py-2 rounded border border-gray-200 dark:border-white/10 bg-surface focus:outline-none focus:ring-2 focus:ring-primary"
      bind:value={$selectedModelStore}
      disabled={isGenerating}
    >
      <option value="">Select a speech model...</option>
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
      class="shrink-0 px-3 py-2 rounded border border-gray-200 dark:border-white/10 bg-surface focus:outline-none focus:ring-2 focus:ring-primary"
      bind:value={$selectedVoiceStore}
      disabled={isGenerating}
    >
      {#each availableVoices as voice (voice)}
        <option value={voice}>{voice}</option>
      {/each}
    </select>
  </div>

  <!-- Empty state for no models configured -->
  {#if availableModels.length === 0}
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
