<script lang="ts">
  import { hasListedModels } from "../../stores/api";
  import { persistentStore } from "../../stores/persistent";
  import { createPlaygroundInterface } from "../../lib/playgroundInterface";
  import { createVideoSync, generateVideoAsync } from "../../lib/videoApi";
  import { playgroundStores } from "../../stores/playgroundActivity";
  import ModelSelector from "./ModelSelector.svelte";
  import ExpandableTextarea from "./ExpandableTextarea.svelte";
  import EmptyState from "../EmptyState.svelte";
  import type { VideoJob } from "../../lib/types";
  import { Button } from "$lib/components/ui/button/index.js";
  import { Input } from "$lib/components/ui/input/index.js";
  import { Textarea } from "$lib/components/ui/textarea/index.js";
  import * as Select from "$lib/components/ui/select/index.js";
  import * as Collapsible from "$lib/components/ui/collapsible/index.js";
  import { Download, X, Video as VideoIcon, ChevronRight } from "@lucide/svelte";
  import { formatFileSize } from "../../lib/format";

  const iface = createPlaygroundInterface("playground-video-model", playgroundStores.videoGenerating);
  const selectedModelStore = iface.selectedModel;
  const busyStore = iface.busy;
  const error = iface.error;
  const sizeStore = persistentStore<string>("playground-video-size", "1280x720");
  const secondsStore = persistentStore<number>("playground-video-seconds", 5);
  const fpsStore = persistentStore<number>("playground-video-fps", 24);
  const modeStore = persistentStore<"async" | "sync">("playground-video-mode", "async");
  const negativePromptStore = persistentStore<string>("playground-video-negative-prompt", "");
  const advancedStore = persistentStore<string>("playground-video-advanced", "");

  let prompt = $state("");
  let isGenerating = $derived($busyStore);
  let advancedOpen = $state(false);
  let referenceFile = $state<File | null>(null);
  let isDragging = $state(false);
  let fileInput = $state<HTMLInputElement | null>(null);
  let generatedVideoUrl = $state<string | null>(null);
  let generatedTimestamp = $state<Date | null>(null);
  let jobStatus = $state<string | null>(null);

  // input_reference accepts either an image (image-to-video) or a video
  // (video-to-video); see vLLM-omni's video API extension fields.
  const ACCEPTED_FORMATS = [".png", ".jpg", ".jpeg", ".webp", ".mp4", ".mov", ".webm"];

  function validateFile(file: File): { valid: boolean; error?: string } {
    const ext = "." + file.name.split(".").pop()?.toLowerCase();
    if (!ACCEPTED_FORMATS.includes(ext)) {
      return { valid: false, error: "Invalid file type. Accepted: PNG, JPG, WEBP, MP4, MOV, WEBM" };
    }
    return { valid: true };
  }

  function applyReferenceFile(file: File) {
    const validation = validateFile(file);
    if (validation.valid) {
      referenceFile = file;
      $error = null;
    } else {
      $error = validation.error || "Invalid file";
      referenceFile = null;
    }
  }

  function handleFileSelect(event: Event) {
    const target = event.target as HTMLInputElement;
    const file = target.files?.[0];
    if (file) applyReferenceFile(file);
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
    if (file) applyReferenceFile(file);
  }

  function removeReferenceFile() {
    referenceFile = null;
    if (fileInput) fileInput.value = "";
  }

  // Parses the Advanced JSON box into a plain object, or returns an error
  // message. An empty box is valid (no advanced overrides).
  function parseAdvanced(): { value?: Record<string, unknown>; error?: string } {
    const raw = $advancedStore.trim();
    if (!raw) return { value: undefined };
    let parsed: unknown;
    try {
      parsed = JSON.parse(raw);
    } catch {
      return { error: "Advanced parameters must be valid JSON." };
    }
    if (typeof parsed !== "object" || parsed === null || Array.isArray(parsed)) {
      return { error: "Advanced parameters must be a JSON object, e.g. {\"width\": 832}." };
    }
    return { value: parsed as Record<string, unknown> };
  }

  async function generate() {
    const trimmedPrompt = prompt.trim();
    if (!trimmedPrompt || !$selectedModelStore || isGenerating) return;

    const { value: advanced, error: advancedError } = parseAdvanced();
    if (advancedError) {
      $error = advancedError;
      return;
    }

    jobStatus = null;
    await iface.run(async (signal) => {
      const params = {
        size: $sizeStore,
        seconds: $secondsStore,
        fps: $fpsStore,
        negativePrompt: $negativePromptStore.trim() || undefined,
        advanced,
      };

      const videoBlob =
        $modeStore === "sync"
          ? await createVideoSync($selectedModelStore, trimmedPrompt, params, referenceFile, signal)
          : await generateVideoAsync($selectedModelStore, trimmedPrompt, params, referenceFile, signal, (job: VideoJob) => {
              jobStatus = job.status;
            });

      if (generatedVideoUrl) {
        URL.revokeObjectURL(generatedVideoUrl);
      }
      generatedVideoUrl = URL.createObjectURL(videoBlob);
      generatedTimestamp = new Date();
    });
  }

  function cancelGeneration() {
    iface.cancel();
  }

  function clearAll() {
    prompt = "";
    referenceFile = null;
    if (generatedVideoUrl) {
      URL.revokeObjectURL(generatedVideoUrl);
    }
    generatedVideoUrl = null;
    generatedTimestamp = null;
    jobStatus = null;
    $error = null;
    if (fileInput) fileInput.value = "";
  }

  function downloadVideo() {
    if (!generatedVideoUrl) return;

    const timestamp = (generatedTimestamp || new Date()).toISOString().replace(/[:.]/g, "-").slice(0, -5);
    const a = document.createElement("a");
    a.href = generatedVideoUrl;
    a.download = `video-${timestamp}.mp4`;
    document.body.appendChild(a);
    a.click();
    document.body.removeChild(a);
  }

  function formatTimestamp(date: Date): string {
    return date.toLocaleString(undefined, {
      month: "short",
      day: "numeric",
      hour: "numeric",
      minute: "2-digit",
      hour12: true,
    });
  }
</script>

<div class="flex flex-col h-full">
  <!-- Model, mode, size and duration controls -->
  <div class="shrink-0 flex flex-wrap items-center gap-2 mb-4">
    <ModelSelector
      bind:value={$selectedModelStore}
      placeholder="Select a video model..."
      disabled={isGenerating}
      capabilities={["video_generation", "image_to_video", "video_to_video"]}
      matchAny={true}
    />

    <Select.Root type="single" value={$modeStore} onValueChange={(v) => v && modeStore.set(v as "async" | "sync")}>
      <Select.Trigger class="h-9 w-28">{$modeStore === "sync" ? "Sync" : "Async"}</Select.Trigger>
      <Select.Content>
        <Select.Item value="async">Async</Select.Item>
        <Select.Item value="sync">Sync</Select.Item>
      </Select.Content>
    </Select.Root>

    <Select.Root type="single" value={$sizeStore} onValueChange={(v) => v && sizeStore.set(v)}>
      <Select.Trigger class="h-9 w-40">{$sizeStore}</Select.Trigger>
      <Select.Content>
        <Select.Group>
          <Select.Label>Landscape</Select.Label>
          <Select.Item value="1280x720">1280x720 (16:9)</Select.Item>
          <Select.Item value="1920x1080">1920x1080 (16:9)</Select.Item>
        </Select.Group>
        <Select.Separator />
        <Select.Group>
          <Select.Label>Portrait</Select.Label>
          <Select.Item value="720x1280">720x1280 (9:16)</Select.Item>
          <Select.Item value="480x854">480x854 (9:16)</Select.Item>
        </Select.Group>
        <Select.Separator />
        <Select.Group>
          <Select.Label>Square</Select.Label>
          <Select.Item value="720x720">720x720</Select.Item>
        </Select.Group>
      </Select.Content>
    </Select.Root>

    <label class="flex items-center gap-1 text-sm">
      <span class="text-xs text-muted-foreground">Seconds</span>
      <Input type="number" class="h-9 w-16" bind:value={$secondsStore} min="1" max="60" />
    </label>

    <label class="flex items-center gap-1 text-sm">
      <span class="text-xs text-muted-foreground">FPS</span>
      <Input type="number" class="h-9 w-16" bind:value={$fpsStore} min="1" max="60" />
    </label>
  </div>

  <!-- Empty state for no models configured -->
  {#if !$hasListedModels}
    <EmptyState message="No models configured. Add models to your configuration to generate video." />
  {:else}
    <!-- Video display area -->
    <div class="flex-1 overflow-auto mb-4 flex items-center justify-center bg-background border border-border rounded-md">
      {#if isGenerating}
        <div class="text-center text-muted-foreground">
          <div class="inline-block w-8 h-8 border-4 border-primary border-t-transparent rounded-full animate-spin mb-2"></div>
          <p>Generating video{jobStatus ? ` (${jobStatus})` : ""}...</p>
          {#if $modeStore === "async"}
            <p class="text-xs mt-1">Video jobs can take a while to complete.</p>
          {/if}
        </div>
      {:else if $error}
        <div class="text-center text-red-500 p-4">
          <p class="font-medium">Error</p>
          <p class="text-sm mt-1">{$error}</p>
        </div>
      {:else if generatedVideoUrl}
        <div class="w-full h-full flex flex-col p-4 gap-2">
          <div class="flex items-center justify-between gap-4">
            <div class="flex flex-wrap gap-3 text-sm text-muted-foreground">
              {#if generatedTimestamp}
                <span>{formatTimestamp(generatedTimestamp)}</span>
              {/if}
            </div>
            <Button variant="outline" size="icon" class="shrink-0" onclick={downloadVideo} title="Download video">
              <Download />
            </Button>
          </div>
          <!-- svelte-ignore a11y_media_has_caption -->
          <video controls class="w-full flex-1 min-h-0 rounded-md bg-black">
            <source src={generatedVideoUrl} />
            Your browser does not support the video element.
          </video>
        </div>
      {:else}
        <div class="text-center text-muted-foreground p-8">
          <VideoIcon class="w-12 h-12 mx-auto mb-2 opacity-40" />
          <p>Enter a prompt below to generate a video</p>
        </div>
      {/if}
    </div>

    <!-- Reference file drop zone -->
    <div
      role="region"
      aria-label="Reference image or video drop zone"
      class="shrink-0 mb-4 flex items-center justify-between gap-2 p-3 rounded-md border border-dashed border-border text-sm {isDragging
        ? 'bg-primary/10'
        : ''}"
      ondragover={handleDragOver}
      ondragleave={handleDragLeave}
      ondrop={handleDrop}
    >
      {#if referenceFile}
        <div class="flex items-center gap-2 min-w-0">
          <span class="truncate">{referenceFile.name}</span>
          <span class="text-xs text-muted-foreground shrink-0">{formatFileSize(referenceFile.size)}</span>
        </div>
        <Button
          variant="outline"
          size="icon-sm"
          onclick={removeReferenceFile}
          disabled={isGenerating}
          aria-label="Remove reference file"
        >
          <X class="size-3" />
        </Button>
      {:else}
        <span class="text-muted-foreground">Optional: drop a reference image or video (image-to-video / video-to-video)</span>
        <input
          type="file"
          accept={ACCEPTED_FORMATS.join(",")}
          class="hidden"
          onchange={handleFileSelect}
          bind:this={fileInput}
        />
        <Button variant="outline" size="sm" onclick={() => fileInput?.click()} disabled={isGenerating}>Browse</Button>
      {/if}
    </div>

    <!-- Negative prompt -->
    <label class="shrink-0 mb-4 flex flex-col gap-1 text-sm">
      <span class="text-xs text-muted-foreground">Negative prompt (optional)</span>
      <Input
        type="text"
        placeholder="What to avoid, e.g. low quality, blurry, static"
        bind:value={$negativePromptStore}
        disabled={isGenerating}
      />
    </label>

    <!-- Advanced parameters: raw, backend-specific fields (e.g. vLLM-omni's
         width/height/num_frames/seed/extra_params) that override the basic
         size/seconds/fps/negative-prompt fields above when present. Keeps
         the base controls universal across OpenAI-video-compatible
         backends while still supporting a specific backend's exact fields. -->
    <Collapsible.Root open={advancedOpen} onOpenChange={(v) => (advancedOpen = v)} class="shrink-0 mb-4">
      <Collapsible.Trigger class="flex items-center gap-1 text-sm text-muted-foreground hover:text-foreground">
        <ChevronRight class="size-4 transition-transform {advancedOpen ? 'rotate-90' : ''}" />
        Advanced parameters
      </Collapsible.Trigger>
      <Collapsible.Content>
        <div class="mt-2 flex flex-col gap-1">
          <Textarea
            bind:value={$advancedStore}
            placeholder={`{"width": 832, "height": 480, "num_frames": 33, "seed": 42, "extra_params": {"sample_solver": "euler"}}`}
            rows={4}
            disabled={isGenerating}
            class="font-mono text-xs"
          />
          <span class="text-xs text-muted-foreground">
            Backend-specific fields (e.g. vLLM-omni's width/height/num_frames/seed/extra_params) as a JSON object — sent
            as-is, overrides the fields above.
          </span>
        </div>
      </Collapsible.Content>
    </Collapsible.Root>

    <!-- Prompt input area -->
    <div class="shrink-0 flex flex-col md:flex-row gap-2">
      <ExpandableTextarea
        bind:value={prompt}
        placeholder="Describe the video you want to generate..."
        rows={3}
        disabled={isGenerating || !$selectedModelStore}
      />
      <div class="flex flex-row md:flex-col gap-2">
        {#if isGenerating}
          <Button variant="destructive" class="flex-1 md:flex-none" onclick={cancelGeneration}>Cancel</Button>
        {:else}
          <Button class="flex-1 md:flex-none" onclick={generate} disabled={!prompt.trim() || !$selectedModelStore}>
            Generate
          </Button>
          <Button
            variant="outline"
            class="flex-1 md:flex-none"
            onclick={clearAll}
            disabled={!prompt.trim() && !generatedVideoUrl && !$error}
          >
            Clear
          </Button>
        {/if}
      </div>
    </div>
  {/if}
</div>
