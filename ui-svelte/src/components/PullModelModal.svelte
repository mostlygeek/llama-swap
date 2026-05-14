<script lang="ts">
  import { pullModel } from "../stores/api";
  import type { PullProgress } from "../lib/types";

  let { onClose, onSuccess }: { onClose: () => void; onSuccess: () => void } = $props();

  let modelInput = $state("");
  let subdirInput = $state("");
  let registerEnabled = $state(true);
  let registerID = $state("");
  let registerFlags = $state("");
  let pulling = $state(false);
  let events = $state<PullProgress[]>([]);
  let error = $state("");

  let lastEvent = $derived(events[events.length - 1]);
  let progressPct = $derived.by(() => {
    const ev = events.findLast((e) => e.status === "downloading");
    if (!ev || !ev.total) return 0;
    return Math.round(((ev.completed ?? 0) / ev.total) * 100);
  });

  function fmt(bytes: number): string {
    if (bytes >= 1e9) return (bytes / 1e9).toFixed(1) + " GB";
    if (bytes >= 1e6) return (bytes / 1e6).toFixed(0) + " MB";
    return bytes + " B";
  }

  async function doPull() {
    if (!modelInput.trim()) return;
    pulling = true;
    error = "";
    events = [];

    const opts: Parameters<typeof pullModel>[1] = {};
    if (subdirInput.trim()) opts.subdir = subdirInput.trim();
    if (registerEnabled) {
      opts.register = {};
      if (registerID.trim()) opts.register.id = registerID.trim();
      if (registerFlags.trim()) opts.register.flags = registerFlags.trim();
    }

    try {
      await pullModel(modelInput.trim(), opts, (ev) => {
        events = [...events, ev];
        if (ev.status === "success" && !registerEnabled) {
          // done without register — will complete shortly
        }
      });
      // success — wait a moment for reload then notify parent
      setTimeout(() => onSuccess(), 500);
    } catch (e) {
      error = (e as Error).message;
      pulling = false;
    }
  }

  function handleKeydown(e: KeyboardEvent) {
    if (e.key === "Escape") onClose();
  }
</script>

<svelte:window onkeydown={handleKeydown} />

<!-- backdrop -->
<div
  class="fixed inset-0 z-50 flex items-center justify-center bg-black/50"
  onclick={(e) => { if (e.target === e.currentTarget) onClose(); }}
  onkeydown={(e) => { if (e.key === "Escape") onClose(); }}
  role="dialog"
  tabindex="-1"
  aria-modal="true"
  aria-label="Pull model"
>
  <div class="bg-surface border border-gray-200 dark:border-white/10 rounded-lg shadow-xl w-full max-w-lg mx-4 p-6 flex flex-col gap-4">
    <div class="flex justify-between items-center">
      <h2 class="text-lg font-semibold">Pull Model</h2>
      <button class="btn btn--sm" onclick={onClose} aria-label="Close">✕</button>
    </div>

    <div class="flex flex-col gap-3">
      <label class="flex flex-col gap-1">
        <span class="text-sm font-medium">HuggingFace model</span>
        <input
          class="input w-full"
          type="text"
          placeholder="owner/repo/filename.gguf  or  https://huggingface.co/..."
          bind:value={modelInput}
          disabled={pulling}
        />
        <span class="text-xs text-txtsecondary">Example: bartowski/Llama-3.2-3B-Instruct-GGUF/Llama-3.2-3B-Instruct-Q4_K_M.gguf</span>
      </label>

      <label class="flex flex-col gap-1">
        <span class="text-sm font-medium">Subdirectory <span class="text-txtsecondary font-normal">(optional)</span></span>
        <input
          class="input w-full"
          type="text"
          placeholder="e.g. llama-3.2-3b"
          bind:value={subdirInput}
          disabled={pulling}
        />
      </label>

      <label class="flex items-center gap-2 cursor-pointer select-none">
        <input type="checkbox" class="w-4 h-4" bind:checked={registerEnabled} disabled={pulling} />
        <span class="text-sm font-medium">Register in config after pull</span>
      </label>

      {#if registerEnabled}
        <div class="pl-6 flex flex-col gap-2">
          <label class="flex flex-col gap-1">
            <span class="text-xs font-medium">Model ID <span class="text-txtsecondary font-normal">(leave blank to use filename)</span></span>
            <input
              class="input w-full text-sm"
              type="text"
              placeholder="auto"
              bind:value={registerID}
              disabled={pulling}
            />
          </label>
          <label class="flex flex-col gap-1">
            <span class="text-xs font-medium">Extra flags <span class="text-txtsecondary font-normal">(inherits from existing model if blank)</span></span>
            <input
              class="input w-full text-sm font-mono"
              type="text"
              placeholder="--ctx-size 32768 --n-gpu-layers 99"
              bind:value={registerFlags}
              disabled={pulling}
            />
          </label>
        </div>
      {/if}
    </div>

    <!-- progress -->
    {#if events.length > 0}
      <div class="flex flex-col gap-2">
        {#if lastEvent?.status === "downloading" && lastEvent.total}
          <div class="flex items-center gap-2">
            <div class="flex-1 bg-gray-200 dark:bg-white/10 rounded-full h-2">
              <div class="bg-blue-500 h-2 rounded-full transition-all" style="width: {progressPct}%"></div>
            </div>
            <span class="text-xs w-20 text-right">{fmt(lastEvent.completed ?? 0)} / {fmt(lastEvent.total)}</span>
          </div>
        {/if}
        <div class="text-xs text-txtsecondary flex items-center gap-1">
          {#if lastEvent?.status === "downloading"}
            <span class="animate-pulse">⬇</span>
          {:else if lastEvent?.status === "success" || lastEvent?.status === "registered"}
            <span class="text-green-500">✓</span>
          {:else if lastEvent?.status === "error"}
            <span class="text-red-500">✗</span>
          {:else}
            <span class="animate-pulse">•</span>
          {/if}
          <span>{lastEvent?.status}{lastEvent?.filename ? `: ${lastEvent.filename}` : ""}</span>
        </div>
      </div>
    {/if}

    {#if error}
      <p class="text-red-500 text-sm">{error}</p>
    {/if}

    <div class="flex justify-end gap-2">
      <button class="btn" onclick={onClose} disabled={pulling}>Cancel</button>
      <button
        class="btn btn--primary"
        onclick={doPull}
        disabled={pulling || !modelInput.trim()}
      >
        {pulling ? "Pulling…" : "Pull"}
      </button>
    </div>
  </div>
</div>
