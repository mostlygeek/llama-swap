<script lang="ts">
  import { models } from "../../stores/api";
  import { persistentStore } from "../../stores/persistent";
  import { generateSpeech } from "../../lib/speechApi";

  const selectedModelStore = persistentStore<string>("playground-speech-model", "");
  const selectedVoiceStore = persistentStore<string>("playground-speech-voice", "coral");
  const autoPlayStore = persistentStore<boolean>("playground-speech-autoplay", false);

  let inputText = $state("");
  let isGenerating = $state(false);
  let generatedAudioUrl = $state<string | null>(null);
  let error = $state<string | null>(null);
  let abortController = $state<AbortController | null>(null);
  let audioElement = $state<HTMLAudioElement | null>(null);

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

  const voiceOptions = ["coral", "alloy", "echo", "fable", "onyx", "nova", "shimmer"];

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

      // Create object URL for the audio blob
      generatedAudioUrl = URL.createObjectURL(audioBlob);
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
    error = null;
    inputText = "";
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
      class="flex-1 px-3 py-2 rounded border border-gray-200 dark:border-white/10 bg-surface focus:outline-none focus:ring-2 focus:ring-primary"
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
      class="px-3 py-2 rounded border border-gray-200 dark:border-white/10 bg-surface focus:outline-none focus:ring-2 focus:ring-primary"
      bind:value={$selectedVoiceStore}
      disabled={isGenerating}
    >
      {#each voiceOptions as voice (voice)}
        <option value={voice}>{voice}</option>
      {/each}
    </select>
    <button class="btn" onclick={clearAudio} disabled={!generatedAudioUrl && !error && !inputText}>
      Clear
    </button>
  </div>

  <!-- Empty state for no models configured -->
  {#if availableModels.length === 0}
    <div class="flex-1 flex items-center justify-center text-txtsecondary">
      <p>No models configured. Add models to your configuration to generate speech.</p>
    </div>
  {:else}
    <!-- Audio display area -->
    <div class="flex-1 overflow-auto mb-4 flex items-center justify-center bg-surface border border-gray-200 dark:border-white/10 rounded">
      {#if isGenerating}
        <div class="text-center text-txtsecondary">
          <div class="inline-block w-8 h-8 border-4 border-primary border-t-transparent rounded-full animate-spin mb-2"></div>
          <p>Generating speech...</p>
        </div>
      {:else if error}
        <div class="text-center text-red-500 p-4">
          <p class="font-medium">Error</p>
          <p class="text-sm mt-1">{error}</p>
        </div>
      {:else if generatedAudioUrl}
        <audio bind:this={audioElement} controls class="w-full max-w-md">
          <source src={generatedAudioUrl} type="audio/mpeg" />
          Your browser does not support the audio element.
        </audio>
      {:else}
        <div class="text-center text-txtsecondary">
          <p>Enter text below to convert to speech</p>
        </div>
      {/if}
    </div>

    <!-- Text input area -->
    <div class="shrink-0 flex gap-2">
      <textarea
        class="flex-1 px-3 py-2 rounded border border-gray-200 dark:border-white/10 bg-surface focus:outline-none focus:ring-2 focus:ring-primary resize-none"
        placeholder="Enter text to convert to speech..."
        rows="3"
        bind:value={inputText}
        onkeydown={handleKeyDown}
        disabled={isGenerating || !$selectedModelStore}
      ></textarea>
      <div class="flex flex-col gap-2">
        {#if isGenerating}
          <button class="btn bg-red-500 hover:bg-red-600 text-white" onclick={cancelGeneration}>
            Cancel
          </button>
        {:else}
          <button
            class="btn bg-primary text-btn-primary-text hover:opacity-90"
            onclick={generate}
            disabled={!inputText.trim() || !$selectedModelStore}
          >
            Generate
          </button>
          <label class="flex items-center gap-2 text-sm cursor-pointer">
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
