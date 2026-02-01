<script lang="ts">
  import { models } from "../../stores/api";
  import { persistentStore } from "../../stores/persistent";
  import { transcribeAudio } from "../../lib/audioApi";
  import ModelSelector from "./ModelSelector.svelte";

  const selectedModelStore = persistentStore<string>("playground-audio-model", "");

  let selectedFile = $state<File | null>(null);
  let isTranscribing = $state(false);
  let transcriptionResult = $state<string | null>(null);
  let error = $state<string | null>(null);
  let abortController = $state<AbortController | null>(null);
  let isDragging = $state(false);
  let fileInput = $state<HTMLInputElement | null>(null);
  let copied = $state(false);

  const ACCEPTED_FORMATS = ['.mp3', '.wav'];
  const MAX_FILE_SIZE = 25 * 1024 * 1024; // 25MB

  let hasModels = $derived($models.some((m) => !m.unlisted));

  let canTranscribe = $derived(selectedFile !== null && $selectedModelStore !== "" && !isTranscribing);

  function validateFile(file: File): { valid: boolean; error?: string } {
    const ext = '.' + file.name.split('.').pop()?.toLowerCase();

    if (!ACCEPTED_FORMATS.includes(ext)) {
      return { valid: false, error: 'Invalid file type. Accepted: MP3, WAV' };
    }

    if (file.size > MAX_FILE_SIZE) {
      return { valid: false, error: 'File too large. Maximum: 25MB' };
    }

    return { valid: true };
  }

  function handleFileSelect(event: Event) {
    const target = event.target as HTMLInputElement;
    const file = target.files?.[0];
    if (file) {
      const validation = validateFile(file);
      if (validation.valid) {
        selectedFile = file;
        error = null;
        transcriptionResult = null;
      } else {
        error = validation.error || "Invalid file";
        selectedFile = null;
      }
    }
  }

  function handleDragOver(event: DragEvent) {
    event.preventDefault();
    isDragging = true;
  }

  function handleDragLeave() {
    isDragging = false;
  }

  function handleDrop(event: DragEvent) {
    event.preventDefault();
    isDragging = false;

    const file = event.dataTransfer?.files[0];
    if (file) {
      const validation = validateFile(file);
      if (validation.valid) {
        selectedFile = file;
        error = null;
        transcriptionResult = null;
      } else {
        error = validation.error || "Invalid file";
        selectedFile = null;
      }
    }
  }

  async function transcribe() {
    if (!selectedFile || !$selectedModelStore || isTranscribing) return;

    isTranscribing = true;
    error = null;
    transcriptionResult = null;
    abortController = new AbortController();

    try {
      const response = await transcribeAudio(
        $selectedModelStore,
        selectedFile,
        abortController.signal
      );

      transcriptionResult = response.text;
    } catch (err) {
      if (err instanceof Error && err.name === "AbortError") {
        // User cancelled
      } else {
        error = err instanceof Error ? err.message : "An error occurred";
      }
    } finally {
      isTranscribing = false;
      abortController = null;
    }
  }

  function cancelTranscription() {
    abortController?.abort();
  }

  function clearAll() {
    selectedFile = null;
    transcriptionResult = null;
    error = null;
    if (fileInput) {
      fileInput.value = '';
    }
  }

  function copyToClipboard() {
    if (transcriptionResult) {
      navigator.clipboard.writeText(transcriptionResult);
      copied = true;
      setTimeout(() => {
        copied = false;
      }, 2000);
    }
  }

  function formatFileSize(bytes: number): string {
    if (bytes < 1024) return bytes + ' B';
    if (bytes < 1024 * 1024) return (bytes / 1024).toFixed(1) + ' KB';
    return (bytes / (1024 * 1024)).toFixed(1) + ' MB';
  }
</script>

<div class="flex flex-col h-full">
  <!-- Model selector -->
  <div class="shrink-0 flex flex-wrap gap-2 mb-4">
    <ModelSelector bind:value={$selectedModelStore} placeholder="Select an audio model..." disabled={isTranscribing} />
  </div>

  <!-- Empty state for no models configured -->
  {#if !hasModels}
    <div class="flex-1 flex items-center justify-center text-txtsecondary">
      <p>No models configured. Add models to your configuration to transcribe audio.</p>
    </div>
  {:else}
    <!-- File upload / Result display area -->
    <div class="flex-1 overflow-auto mb-4 flex items-center justify-center bg-surface border border-gray-200 dark:border-white/10 rounded">
      {#if isTranscribing}
        <div class="text-center text-txtsecondary">
          <div class="inline-block w-8 h-8 border-4 border-primary border-t-transparent rounded-full animate-spin mb-2"></div>
          <p>Transcribing audio...</p>
        </div>
      {:else if error}
        <div class="text-center text-red-500 p-4">
          <p class="font-medium">Error</p>
          <p class="text-sm mt-1">{error}</p>
        </div>
      {:else if transcriptionResult}
        <div class="w-full h-full flex flex-col p-4">
          <div class="flex justify-between items-center mb-2">
            <h3 class="font-medium">Transcription Result</h3>
            <button
              class="btn btn-sm"
              onclick={copyToClipboard}
              title={copied ? 'Copied!' : 'Copy to clipboard'}
            >
              {#if copied}
                <svg class="w-5 h-5 text-green-500" fill="none" stroke="currentColor" viewBox="0 0 24 24">
                  <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M5 13l4 4L19 7"></path>
                </svg>
              {:else}
                <svg class="w-5 h-5" fill="none" stroke="currentColor" viewBox="0 0 24 24">
                  <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M8 16H6a2 2 0 01-2-2V6a2 2 0 012-2h8a2 2 0 012 2v2m-6 12h8a2 2 0 002-2v-8a2 2 0 00-2-2h-8a2 2 0 00-2 2v8a2 2 0 002 2z"></path>
                </svg>
              {/if}
            </button>
          </div>
          <div class="flex-1 overflow-auto p-3 rounded border border-gray-200 dark:border-white/10 bg-background whitespace-pre-wrap">
            {transcriptionResult}
          </div>
        </div>
      {:else if selectedFile}
        <div class="text-center text-txtsecondary p-4">
          <p class="font-medium mb-2">File Selected</p>
          <p class="text-sm">{selectedFile.name}</p>
          <p class="text-xs mt-1">{formatFileSize(selectedFile.size)}</p>
        </div>
      {:else}
        <div
          role="region"
          aria-label="Audio file drop zone"
          class="w-full h-full flex items-center justify-center text-center text-txtsecondary p-8 {isDragging ? 'bg-primary/10' : ''}"
          ondragover={handleDragOver}
          ondragleave={handleDragLeave}
          ondrop={handleDrop}
        >
          <div>
            <p class="mb-2">Drag and drop an audio file here</p>
            <p class="text-sm">or use the Browse button below</p>
            <p class="text-xs mt-4">Accepted formats: MP3, WAV (max 25MB)</p>
          </div>
        </div>
      {/if}
    </div>

    <!-- File input and transcribe button -->
    <div class="shrink-0 flex gap-2">
      <input
        type="file"
        accept=".mp3,.wav"
        class="hidden"
        onchange={handleFileSelect}
        bind:this={fileInput}
      />
      <button
        class="btn"
        onclick={() => fileInput?.click()}
        disabled={isTranscribing}
      >
        Browse Files
      </button>
      <div class="flex-1"></div>
      {#if isTranscribing}
        <button class="btn bg-red-500 hover:bg-red-600 text-white" onclick={cancelTranscription}>
          Cancel
        </button>
      {:else}
        <button
          class="btn bg-primary text-btn-primary-text hover:opacity-90"
          onclick={transcribe}
          disabled={!canTranscribe}
        >
          Transcribe
        </button>
        <button
          class="btn"
          onclick={clearAll}
          disabled={!selectedFile && !transcriptionResult && !error}
        >
          Clear
        </button>
      {/if}
    </div>
  {/if}
</div>
