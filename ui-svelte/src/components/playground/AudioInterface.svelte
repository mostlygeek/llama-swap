<script lang="ts">
  import { hasListedModels } from "../../stores/api";
  import { createPlaygroundInterface } from "../../lib/playgroundInterface";
  import { transcribeAudio } from "../../lib/audioApi";
  import { playgroundStores } from "../../stores/playgroundActivity";
  import ModelSelector from "./ModelSelector.svelte";
  import EmptyState from "../EmptyState.svelte";
  import { Button } from "$lib/components/ui/button/index.js";
  import { Copy, Check } from "@lucide/svelte";
  import { formatFileSize } from "../../lib/format";
  import { copyText } from "../../lib/clipboard";

  const iface = createPlaygroundInterface("playground-audio-model", playgroundStores.audioTranscribing);
  const selectedModelStore = iface.selectedModel;
  const transcribing = iface.busy;
  const error = iface.error;
  let isTranscribing = $derived($transcribing);

  let selectedFile = $state<File | null>(null);
  let transcriptionResult = $state<string | null>(null);
  let isDragging = $state(false);
  let fileInput = $state<HTMLInputElement | null>(null);
  let copied = $state(false);

  const ACCEPTED_FORMATS = ['.mp3', '.wav', '.ogg'];

  let canTranscribe = $derived(selectedFile !== null && $selectedModelStore !== "" && !isTranscribing);

  function validateFile(file: File): { valid: boolean; error?: string } {
    const ext = '.' + file.name.split('.').pop()?.toLowerCase();

    if (!ACCEPTED_FORMATS.includes(ext)) {
      return { valid: false, error: 'Invalid file type. Accepted: MP3, WAV, OGG' };
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
        $error = null;
        transcriptionResult = null;
      } else {
        $error = validation.error || "Invalid file";
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
        $error = null;
        transcriptionResult = null;
      } else {
        $error = validation.error || "Invalid file";
        selectedFile = null;
      }
    }
  }

  async function transcribe() {
    const file = selectedFile;
    if (!file || !$selectedModelStore || isTranscribing) return;

    transcriptionResult = null;
    await iface.run(async (signal) => {
      const response = await transcribeAudio($selectedModelStore, file, signal);
      transcriptionResult = response.text;
    });
  }

  function cancelTranscription() {
    iface.cancel();
  }

  function clearAll() {
    selectedFile = null;
    transcriptionResult = null;
    $error = null;
    if (fileInput) {
      fileInput.value = '';
    }
  }

  async function copyToClipboard() {
    if (transcriptionResult && (await copyText(transcriptionResult))) {
      copied = true;
      setTimeout(() => {
        copied = false;
      }, 2000);
    }
  }
</script>

<div class="flex flex-col h-full">
  <!-- Model selector -->
  <div class="shrink-0 flex flex-wrap gap-2 mb-4">
    <ModelSelector bind:value={$selectedModelStore} placeholder="Select an audio model..." disabled={isTranscribing} capabilities={["audio_transcriptions"]} />
  </div>

  <!-- Empty state for no models configured -->
  {#if !$hasListedModels}
    <EmptyState message="No models configured. Add models to your configuration to transcribe audio." />
  {:else}
    <!-- File upload / Result display area -->
    <div class="flex-1 overflow-auto mb-4 flex items-center justify-center bg-background border border-border rounded-md">
      {#if isTranscribing}
        <div class="text-center text-muted-foreground">
          <div class="inline-block w-8 h-8 border-4 border-primary border-t-transparent rounded-full animate-spin mb-2"></div>
          <p>Transcribing audio...</p>
        </div>
      {:else if $error}
        <div class="text-center text-red-500 p-4">
          <p class="font-medium">Error</p>
          <p class="text-sm mt-1">{$error}</p>
        </div>
      {:else if transcriptionResult}
        <div class="w-full h-full flex flex-col p-4">
          <div class="flex justify-between items-center mb-2">
            <h3 class="pb-0 font-medium">Transcription Result</h3>
            <Button
              variant="outline"
              size="icon-sm"
              onclick={copyToClipboard}
              title={copied ? 'Copied!' : 'Copy to clipboard'}
            >
              {#if copied}
                <Check class="text-success" />
              {:else}
                <Copy />
              {/if}
            </Button>
          </div>
          <div class="flex-1 overflow-auto p-3 rounded-md border border-border bg-background whitespace-pre-wrap">
            {transcriptionResult}
          </div>
        </div>
      {:else if selectedFile}
        <div class="text-center text-muted-foreground p-4">
          <p class="font-medium mb-2">File Selected</p>
          <p class="text-sm">{selectedFile.name}</p>
          <p class="text-xs mt-1">{formatFileSize(selectedFile.size)}</p>
        </div>
      {:else}
        <div
          role="region"
          aria-label="Audio file drop zone"
          class="w-full h-full flex items-center justify-center text-center text-muted-foreground p-8 {isDragging ? 'bg-primary/10' : ''}"
          ondragover={handleDragOver}
          ondragleave={handleDragLeave}
          ondrop={handleDrop}
        >
          <div>
            <p class="mb-2">Drag and drop an audio file here</p>
            <p class="text-sm">or use the Browse button below</p>
            <p class="text-xs mt-4">Accepted formats: MP3, WAV, OGG</p>
          </div>
        </div>
      {/if}
    </div>

    <!-- File input and transcribe button -->
    <div class="shrink-0 flex gap-2">
      <input
        type="file"
        accept=".mp3,.wav,.ogg"
        class="hidden"
        onchange={handleFileSelect}
        bind:this={fileInput}
      />
      <Button variant="outline" onclick={() => fileInput?.click()} disabled={isTranscribing}>
        Browse Files
      </Button>
      <div class="flex-1"></div>
      {#if isTranscribing}
        <Button variant="destructive" onclick={cancelTranscription}>Cancel</Button>
      {:else}
        <Button onclick={transcribe} disabled={!canTranscribe}>Transcribe</Button>
        <Button
          variant="outline"
          onclick={clearAll}
          disabled={!selectedFile && !transcriptionResult && !$error}
        >
          Clear
        </Button>
      {/if}
    </div>
  {/if}
</div>
