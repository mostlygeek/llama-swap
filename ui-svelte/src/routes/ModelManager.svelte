<script lang="ts">
  import { searchHFModels, listHFFiles, startDownload, cancelDownload, listLocalModels, deleteLocalModel } from "../lib/mantleApi";
  import type { HFModel, LocalModel } from "../lib/types";
  import { activeDownloads, trackDownload, removeDownload, syncTasks } from "../stores/tasks";

  let searchQuery = $state("");
  let searchResults = $state<HFModel[]>([]);
  let searching = $state(false);

  let localModels = $state<LocalModel[]>([]);
  let loadingLocal = $state(false);

  let selectedModel = $state<HFModel | null>(null);
  let modelFiles = $state<string[]>([]);
  let loadingFiles = $state(false);

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
    trackDownload(task);
  }

  async function doCancelDownload(taskID: string) {
    await cancelDownload(taskID);
    removeDownload(taskID);
  }

  async function refreshLocal() {
    loadingLocal = true;
    localModels = await listLocalModels();
    loadingLocal = false;
  }

  async function doDeleteLocal(name: string) {
    if (!confirm(`Delete ${name}?`)) return;
    await deleteLocalModel(name);
    await refreshLocal();
  }

  function formatSize(bytes: number): string {
    if (bytes === 0) return "0 B";
    const units = ["B", "KB", "MB", "GB", "TB"];
    const i = Math.floor(Math.log(bytes) / Math.log(1024));
    return (bytes / Math.pow(1024, i)).toFixed(1) + " " + units[i];
  }

  $effect(() => { refreshLocal(); syncTasks(); });
</script>

<div class="card h-full flex flex-col p-4">
  <h2>Model Manager</h2>

  <div class="tabs flex gap-2 mb-4 border-b border-border pb-2">
    <button class="btn" class:font-semibold={selectedModel === null} onclick={() => selectedModel = null}>Browse HF</button>
    <button class="btn" onclick={refreshLocal}>Local Models ({localModels.length})</button>
  </div>

  {#if selectedModel === null}
    <!-- HF Browser -->
    <div class="flex gap-2 mb-4">
      <input
        type="text"
        class="input flex-1 px-3 py-2 border rounded bg-surface"
        placeholder="Search HuggingFace models..."
        bind:value={searchQuery}
        onkeydown={(e) => e.key === "Enter" && doSearch()}
      />
      <button class="btn px-4 py-2" onclick={doSearch} disabled={searching}>
        {searching ? "Searching..." : "Search"}
      </button>
    </div>

    <div class="flex-1 overflow-y-auto">
      {#each searchResults as model (model.id)}
        <div
          class="flex items-center justify-between p-3 border-b border-border hover:bg-secondary-hover cursor-pointer"
          onclick={() => selectModel(model)}
        >
          <div>
            <span class="font-medium">{model.id}</span>
            {#if !model.gguf}
              <span class="text-xs ml-2 px-1.5 py-0.5 rounded bg-yellow-100 dark:bg-yellow-900">no GGUF</span>
            {/if}
          </div>
          <div class="text-sm text-txtsecondary">
            {model.downloads.toLocaleString()} downloads
          </div>
        </div>
      {/each}
      {#if searchResults.length === 0 && !searching}
        <p class="text-txtsecondary text-center mt-8">Search for models to get started</p>
      {/if}
    </div>
  {:else}
    <!-- Model detail + file list -->
    <div class="mb-4">
      <button class="btn text-sm mb-2" onclick={() => { selectedModel = null; modelFiles = []; }}>← Back to search</button>
      <h3 class="text-lg font-semibold">{selectedModel.id}</h3>
    </div>

    {#if loadingFiles}
      <p class="text-txtsecondary">Loading files...</p>
    {:else}
      <div class="flex-1 overflow-y-auto">
        <h4 class="text-sm font-medium mb-2 text-txtsecondary">GGUF Files</h4>
        {#each modelFiles as file}
          <div class="flex items-center justify-between p-2 border-b border-border">
            <span class="text-sm truncate mr-2">{file}</span>
            <button class="btn btn--sm shrink-0" onclick={() => doDownload(file)}>Download</button>
          </div>
        {/each}
        {#if modelFiles.length === 0}
          <p class="text-txtsecondary text-sm">No GGUF files found in this model</p>
        {/if}
      </div>
    {/if}
  {/if}

  <!-- Active downloads -->
  {#if $activeDownloads.size > 0}
    <div class="mt-4 border-t border-border pt-3">
      <h4 class="text-sm font-medium mb-2">Downloads</h4>
      {#each [...$activeDownloads.entries()] as [id, entry]}
        <div class="flex items-center gap-3 mb-2 text-sm">
          <div class="flex-1">
            <div class="flex justify-between">
              <span class="truncate">{entry.task.message}</span>
              <span class="text-txtsecondary">{entry.task.pct >= 0 ? `${entry.task.pct}%` : ""}</span>
            </div>
            {#if entry.task.pct >= 0}
              <div class="w-full bg-gray-200 dark:bg-gray-700 rounded h-1.5 mt-1">
                <div class="bg-blue-500 h-1.5 rounded" style="width: {entry.task.pct}%"></div>
              </div>
            {/if}
          </div>
          {#if entry.task.state === "running"}
            <button class="btn btn--sm text-red-500" onclick={() => doCancelDownload(id)}>Cancel</button>
          {/if}
        </div>
      {/each}
    </div>
  {/if}

  <!-- Local models section -->
  {#if loadingLocal}
    <p class="text-sm text-txtsecondary mt-4">Loading local models...</p>
  {:else if localModels.length > 0}
    <div class="mt-4 border-t border-border pt-3">
      <h4 class="text-sm font-medium mb-2">Downloaded Models ({localModels.length})</h4>
      {#each localModels as m}
        <div class="flex items-center justify-between py-1 text-sm">
          <span class="truncate">{m.name}</span>
          <div class="flex items-center gap-2 shrink-0">
            <span class="text-txtsecondary">{formatSize(m.size)}</span>
            <button class="btn btn--sm text-red-500" onclick={() => doDeleteLocal(m.name)}>Delete</button>
          </div>
        </div>
      {/each}
    </div>
  {/if}
</div>

<style>
  .tabs button {
    padding: 0.25rem 0.75rem;
    border-radius: 0.375rem;
  }
  .tabs button.font-semibold {
    background: var(--color-surface);
    box-shadow: 0 1px 2px rgba(0,0,0,0.1);
  }
  .input {
    border: 1px solid var(--color-border);
  }
</style>
