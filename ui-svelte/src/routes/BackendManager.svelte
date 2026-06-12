<script lang="ts">
  import { startBuild, cancelBuild, listBackends, deleteBackend } from "../lib/mantleApi";
  import type { BackendEntry } from "../lib/types";
  import { activeBuilds, trackBuild, removeBuild, syncTasks } from "../stores/tasks";

  let repo = $state("https://github.com/danielhanchen/llama.cpp");
  let branch = $state("main");
  let building = $state(false);

  let backends = $state<BackendEntry[]>([]);
  let loadingBackends = $state(false);

  async function doBuild() {
    if (!repo.trim()) return;
    building = true;
    const task = await startBuild(repo.trim(), branch.trim() || "main");
    building = false;
    if (!task) return;
    trackBuild(task);
  }

  async function doCancelBuild(taskID: string) {
    await cancelBuild(taskID);
    removeBuild(taskID);
  }

  async function refreshBackends() {
    loadingBackends = true;
    backends = await listBackends();
    loadingBackends = false;
  }

  async function doDeleteBackend(name: string) {
    if (!confirm(`Delete backend "${name}"?`)) return;
    await deleteBackend(name);
    await refreshBackends();
  }

  function formatSize(bytes: number): string {
    if (bytes === 0) return "0 B";
    const units = ["B", "KB", "MB", "GB"];
    const i = Math.floor(Math.log(bytes) / Math.log(1024));
    return (bytes / Math.pow(1024, i)).toFixed(1) + " " + units[i];
  }

  function shortRepo(url: string): string {
    const parts = url.replace(/https?:\/\//, "").split("/");
    return parts.slice(0, 2).join("/");
  }

  $effect(() => { refreshBackends(); syncTasks(); });
</script>

<div class="card h-full flex flex-col p-4">
  <h2>Backend Manager</h2>

  <!-- Build trigger -->
  <div class="border border-border rounded p-4 mb-4">
    <h3 class="text-sm font-semibold mb-3">Build llama.cpp Fork</h3>
    <div class="flex flex-col gap-2">
      <div class="flex gap-2 items-center">
        <label class="text-sm text-txtsecondary w-12">Repo:</label>
        <input
          type="text"
          class="input flex-1 px-3 py-1.5 border rounded bg-surface text-sm"
          placeholder="https://github.com/user/llama.cpp"
          bind:value={repo}
        />
      </div>
      <div class="flex gap-2 items-center">
        <label class="text-sm text-txtsecondary w-12">Branch:</label>
        <input
          type="text"
          class="input flex-1 px-3 py-1.5 border rounded bg-surface text-sm"
          placeholder="main"
          bind:value={branch}
        />
      </div>
      <button class="btn px-4 py-1.5 self-end" onclick={doBuild} disabled={building || !repo.trim()}>
        {building ? "Starting..." : "Build"}
      </button>
    </div>
  </div>

  <!-- Active builds -->
  {#if $activeBuilds.size > 0}
    <div class="mb-4">
      <h3 class="text-sm font-semibold mb-2">Active Builds</h3>
      {#each [...$activeBuilds.entries()] as [id, entry]}
        <div class="flex items-center gap-3 mb-2 text-sm p-2 border border-border rounded">
          <div class="flex-1 min-w-0">
            <div class="flex justify-between">
              <span class="truncate font-medium">{shortRepo(entry.task.repo || "")}@{entry.task.branch || "main"}</span>
              <span class="text-txtsecondary">{entry.task.pct >= 0 ? `${entry.task.pct}%` : ""}</span>
            </div>
            <p class="text-xs text-txtsecondary truncate">{entry.task.message}</p>
            {#if entry.task.pct >= 0}
              <div class="w-full bg-gray-200 dark:bg-gray-700 rounded h-1.5 mt-1">
                <div class="bg-green-500 h-1.5 rounded" style="width: {entry.task.pct}%"></div>
              </div>
            {/if}
          </div>
          {#if entry.task.state === "running"}
            <button class="btn btn--sm text-red-500" onclick={() => doCancelBuild(id)}>Cancel</button>
          {/if}
        </div>
      {/each}
    </div>
  {/if}

  <!-- Compiled backends -->
  <div class="flex-1 min-h-0">
    <div class="flex items-center justify-between mb-2">
      <h3 class="text-sm font-semibold">Compiled Backends</h3>
      <button class="btn btn--sm" onclick={refreshBackends}>Refresh</button>
    </div>
    {#if loadingBackends}
      <p class="text-sm text-txtsecondary">Loading...</p>
    {:else if backends.length === 0}
      <p class="text-sm text-txtsecondary">No compiled backends yet. Build one above.</p>
    {:else}
      <div class="overflow-y-auto max-h-64">
        {#each backends as be}
          <div class="flex items-center justify-between py-2 px-2 border-b border-border hover:bg-secondary-hover text-sm">
            <div class="flex-1 min-w-0">
              <span class="font-medium">{be.name}</span>
              {#if be.size}
                <span class="text-txtsecondary ml-2">{formatSize(be.size)} (llama-server)</span>
              {/if}
            </div>
            <button class="btn btn--sm text-red-500 shrink-0 ml-2" onclick={() => doDeleteBackend(be.name)}>Delete</button>
          </div>
        {/each}
      </div>
    {/if}
  </div>
</div>

<style>
  .input {
    border: 1px solid var(--color-border);
  }
  .input:focus {
    outline: none;
    border-color: #3b82f6;
    box-shadow: 0 0 0 1px #3b82f6;
  }
</style>
