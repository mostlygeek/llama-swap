<script lang="ts">
  import {
    searchHFModels, listHFFiles, startDownload, cancelDownload,
    listLocalModels, deleteLocalModel, listBackends, getConfig, putConfig
  } from "../lib/mantleApi";
  import type { HFModel, LocalModel, BackendEntry } from "../lib/types";
  import { activeDownloads, trackDownload, removeDownload, retryDownload, syncTasks } from "../stores/tasks";

  let searchQuery   = $state("");
  let searchResults = $state<HFModel[]>([]);
  let searching     = $state(false);
  let selectedModel = $state<HFModel | null>(null);
  let modelFiles    = $state<string[]>([]);
  let loadingFiles  = $state(false);

  let localModels    = $state<LocalModel[]>([]);
  let loadingLocal   = $state(false);
  let localExpanded  = $state(true);

  let backends       = $state<BackendEntry[]>([]);

  // per-model add-to-config form; key = model path
  let addConfigFor   = $state<string | null>(null);
  let formModelId    = $state("");
  let formBackend    = $state("llama-server");
  let formGpuLayers  = $state(99);
  let formMmproj     = $state("");
  let formTtl        = $state(300);
  let savingConfig   = $state(false);

  // ── helpers ──────────────────────────────────────────────────────────────

  function formatSize(bytes: number): string {
    if (bytes === 0) return "0 B";
    const units = ["B", "KB", "MB", "GB", "TB"];
    const i = Math.floor(Math.log(bytes) / Math.log(1024));
    return (bytes / Math.pow(1024, i)).toFixed(1) + " " + units[i];
  }

  function suggestModelId(path: string): string {
    const file = path.split("/").pop() ?? path;
    return file.replace(/\.gguf$/i, "").toLowerCase().replace(/[_\s]+/g, "-");
  }

  function generateYaml(modelPath: string): string {
    const mmline = formMmproj.trim() ? `\n      --mmproj ${formMmproj.trim()}` : "";
    return (
      `\n  ${formModelId}:\n` +
      `    cmd: >-\n` +
      `      ${formBackend} --port \${PORT}\n` +
      `      -m ${modelPath}${mmline}\n` +
      `      -ngl ${formGpuLayers}\n` +
      `    proxy: http://127.0.0.1:\${PORT}\n` +
      `    ttl: ${formTtl}\n`
    );
  }

  // ── HF browse ────────────────────────────────────────────────────────────

  async function doSearch() {
    if (!searchQuery.trim()) return;
    searching = true;
    searchResults = await searchHFModels(searchQuery.trim());
    searching = false;
  }

  async function selectModel(m: HFModel) {
    selectedModel = m;
    loadingFiles = true;
    modelFiles = await listHFFiles(m.id);
    loadingFiles = false;
  }

  async function doDownload(filename: string) {
    if (!selectedModel) return;
    const task = await startDownload(selectedModel.id, filename);
    if (!task) return;
    trackDownload(task, filename);
  }

  async function doCancelDownload(taskID: string) {
    await cancelDownload(taskID);
    removeDownload(taskID);
  }

  // ── local models ─────────────────────────────────────────────────────────

  async function refreshLocal() {
    loadingLocal = true;
    try {
      localModels = await listLocalModels();
    } finally {
      loadingLocal = false;
    }
  }

  function openAddConfig(model: LocalModel) {
    addConfigFor  = model.path;
    formModelId   = suggestModelId(model.path);
    formBackend   = "llama-server";
    formGpuLayers = 99;
    formMmproj    = "";
    formTtl       = 300;
  }

  async function doAddToConfig(model: LocalModel) {
    if (!formModelId.trim()) return;
    savingConfig = true;
    const current = await getConfig();
    if (current !== null) {
      await putConfig(current + generateYaml(model.path));
    }
    savingConfig = false;
    addConfigFor = null;
  }

  async function doDeleteLocal(model: LocalModel) {
    const cfg = await getConfig();
    const inConfig = cfg !== null && cfg.includes(model.path);
    const question = inConfig
      ? `"${model.name}" is referenced in your config.\nDelete the file AND remove it from config?`
      : `Delete ${model.name}?`;
    if (!confirm(question)) return;

    if (inConfig && cfg !== null) {
      // strip the model block from config — find the 2-space-indented key block
      // simple approach: remove lines that reference the path
      const lines = cfg.split("\n");
      const cleaned = stripModelFromConfig(lines, model.path);
      await putConfig(cleaned);
    }

    await deleteLocalModel(model.name);
    await refreshLocal();
  }

  function stripModelFromConfig(lines: string[], modelPath: string): string {
    // Find the model entry block that contains modelPath and remove it.
    // A block starts with a 2-space-indented key (^  \S) and ends before the next one.
    const result: string[] = [];
    let skip = false;
    for (let i = 0; i < lines.length; i++) {
      const line = lines[i];
      // New top-level model key
      if (/^  \S/.test(line) && !/^  #/.test(line)) {
        // Peek ahead: does this block contain the model path?
        let j = i + 1;
        let found = false;
        while (j < lines.length && !/^  \S/.test(lines[j])) {
          if (lines[j].includes(modelPath)) { found = true; }
          j++;
        }
        skip = found;
      }
      if (!skip) result.push(line);
    }
    return result.join("\n");
  }

  // ── init ─────────────────────────────────────────────────────────────────

  $effect(() => {
    refreshLocal();
    listBackends().then((b) => { backends = b; });
    syncTasks();
  });
</script>

<div class="card h-full flex flex-col p-4 gap-4 overflow-hidden">
  <h2>Model Hub</h2>

  <!-- ── HF browser ── -->
  <div class="flex gap-2 border-b border-border pb-2">
    <input
      type="text"
      class="input flex-1 px-3 py-2 border rounded bg-surface text-sm"
      placeholder="Search HuggingFace models..."
      bind:value={searchQuery}
      onkeydown={(e) => e.key === "Enter" && doSearch()}
    />
    <button class="btn px-4 py-2 text-sm" onclick={doSearch} disabled={searching}>
      {searching ? "Searching…" : "Search"}
    </button>
  </div>

  {#if selectedModel === null}
    <div class="flex-1 overflow-y-auto min-h-0">
      {#each searchResults as model (model.id)}
        <div
          class="flex items-center justify-between p-3 border-b border-border hover:bg-secondary-hover cursor-pointer"
          onclick={() => selectModel(model)}
        >
          <div>
            <span class="font-medium text-sm">{model.id}</span>
            {#if !model.gguf}
              <span class="text-xs ml-2 px-1.5 py-0.5 rounded bg-yellow-100 dark:bg-yellow-900">no GGUF</span>
            {/if}
          </div>
          <span class="text-xs text-txtsecondary">{model.downloads.toLocaleString()} dl</span>
        </div>
      {/each}
      {#if searchResults.length === 0 && !searching}
        <p class="text-txtsecondary text-center mt-8 text-sm">Search for models to get started</p>
      {/if}
    </div>
  {:else}
    <div class="flex-1 overflow-y-auto min-h-0">
      <button class="btn text-sm mb-3" onclick={() => { selectedModel = null; modelFiles = []; }}>← Back</button>
      <h3 class="font-semibold mb-2">{selectedModel.id}</h3>
      {#if loadingFiles}
        <p class="text-txtsecondary text-sm">Loading files…</p>
      {:else}
        <p class="text-xs text-txtsecondary mb-2">GGUF files</p>
        {#each modelFiles as file}
          <div class="flex items-center justify-between p-2 border-b border-border">
            <span class="text-sm truncate mr-2">{file}</span>
            <button class="btn btn--sm shrink-0" onclick={() => doDownload(file)}>Download</button>
          </div>
        {/each}
        {#if modelFiles.length === 0}
          <p class="text-txtsecondary text-sm">No GGUF files found</p>
        {/if}
      {/if}
    </div>
  {/if}

  <!-- ── active downloads ── -->
  {#if $activeDownloads.size > 0}
    <div class="border-t border-border pt-3 shrink-0">
      <h4 class="text-sm font-semibold mb-2">Downloads</h4>
      {#each [...$activeDownloads.entries()] as [id, entry]}
        <div class="flex items-center gap-3 mb-2 text-sm">
          <div class="flex-1 min-w-0">
            <div class="flex justify-between gap-2">
              <span class="truncate text-xs">{entry.task.message}</span>
              {#if entry.task.pct >= 0}
                <span class="text-txtsecondary text-xs shrink-0">{entry.task.pct}%</span>
              {/if}
            </div>
            {#if entry.task.pct >= 0 && entry.task.state === "running"}
              <div class="w-full bg-gray-200 dark:bg-gray-700 rounded h-1 mt-1">
                <div class="bg-blue-500 h-1 rounded" style="width: {entry.task.pct}%"></div>
              </div>
            {/if}
          </div>
          {#if entry.task.state === "running"}
            <button class="btn btn--sm text-red-500 shrink-0" onclick={() => doCancelDownload(id)}>Cancel</button>
          {:else if entry.task.state === "failed" || entry.task.state === "cancelled"}
            {#if entry.filename}
              <button class="btn btn--sm shrink-0" onclick={() => retryDownload(id)}>Retry</button>
            {/if}
            <button class="btn btn--sm text-txtsecondary shrink-0" onclick={() => removeDownload(id)}>✕</button>
          {:else if entry.task.state === "completed"}
            <button class="btn btn--sm text-txtsecondary shrink-0" onclick={() => removeDownload(id)}>✕</button>
          {/if}
        </div>
      {/each}
    </div>
  {/if}

  <!-- ── local models (collapsible) ── -->
  <div class="border-t border-border pt-3 shrink-0 min-h-0 flex flex-col">
    <button
      class="flex items-center justify-between w-full text-sm font-semibold mb-2 hover:text-blue-500"
      onclick={() => localExpanded = !localExpanded}
    >
      <span>Local Models ({localModels.length})</span>
      <span>{localExpanded ? "▲" : "▼"}</span>
    </button>

    {#if localExpanded}
      {#if loadingLocal}
        <p class="text-xs text-txtsecondary">Loading…</p>
      {:else if localModels.length === 0}
        <p class="text-xs text-txtsecondary">No models downloaded yet</p>
      {:else}
        <div class="overflow-y-auto max-h-60 flex flex-col gap-1">
          {#each localModels as model (model.path)}
            <div class="border border-border rounded p-2 text-sm">
              <div class="flex items-center justify-between gap-2">
                <span class="truncate font-medium text-xs">{model.name}</span>
                <div class="flex items-center gap-1 shrink-0">
                  <span class="text-txtsecondary text-xs">{formatSize(model.size)}</span>
                  <button
                    class="btn btn--sm"
                    onclick={() => addConfigFor === model.path ? addConfigFor = null : openAddConfig(model)}
                  >
                    {addConfigFor === model.path ? "Cancel" : "+ Config"}
                  </button>
                  <button class="btn btn--sm text-red-500" onclick={() => doDeleteLocal(model)}>Delete</button>
                </div>
              </div>

              {#if addConfigFor === model.path}
                <div class="mt-2 pt-2 border-t border-border flex flex-col gap-1.5 text-xs">
                  <div class="flex gap-2 items-center">
                    <label class="w-20 text-txtsecondary">Model ID</label>
                    <input class="input flex-1 px-2 py-1 border rounded bg-surface text-xs" bind:value={formModelId} />
                  </div>
                  <div class="flex gap-2 items-center">
                    <label class="w-20 text-txtsecondary">Backend</label>
                    <select class="input flex-1 px-2 py-1 border rounded bg-surface text-xs" bind:value={formBackend}>
                      <option value="llama-server">llama-server (default)</option>
                      <option value="ik-llama-server">ik-llama-server (bundled)</option>
                      {#each backends as be}
                        <option value={be.path}>{be.name}</option>
                      {/each}
                    </select>
                  </div>
                  <div class="flex gap-2 items-center">
                    <label class="w-20 text-txtsecondary">GPU Layers</label>
                    <input class="input w-16 px-2 py-1 border rounded bg-surface text-xs" type="number" bind:value={formGpuLayers} />
                  </div>
                  <div class="flex gap-2 items-center">
                    <label class="w-20 text-txtsecondary">MMProj</label>
                    <input class="input flex-1 px-2 py-1 border rounded bg-surface text-xs" placeholder="optional path" bind:value={formMmproj} />
                  </div>
                  <div class="flex gap-2 items-center">
                    <label class="w-20 text-txtsecondary">TTL (s)</label>
                    <input class="input w-20 px-2 py-1 border rounded bg-surface text-xs" type="number" bind:value={formTtl} />
                  </div>
                  <button
                    class="btn btn--sm self-end mt-1"
                    onclick={() => doAddToConfig(model)}
                    disabled={savingConfig || !formModelId.trim()}
                  >
                    {savingConfig ? "Saving…" : "Save to Config"}
                  </button>
                </div>
              {/if}
            </div>
          {/each}
        </div>
      {/if}
    {/if}
  </div>
</div>

<style>
  .input { border: 1px solid var(--color-border); }
  .input:focus { outline: none; border-color: #3b82f6; box-shadow: 0 0 0 1px #3b82f6; }
</style>