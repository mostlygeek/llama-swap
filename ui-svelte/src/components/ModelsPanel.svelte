<script lang="ts">
  import { onMount } from "svelte";
  import {
    models, loadModel, unloadAllModels, unloadSingleModel,
    fetchResources, fetchConfigInfo, deleteModelFile,
  } from "../stores/api";
  import { isNarrow } from "../stores/theme";
  import { persistentStore } from "../stores/persistent";
  import type { Model, ResourcesResponse, ModelFileInfo } from "../lib/types";
  import StorageBar from "./StorageBar.svelte";
  import PullModelModal from "./PullModelModal.svelte";

  let isUnloading = $state(false);
  let menuOpen = $state(false);
  let showPullModal = $state(false);
  let resources = $state<ResourcesResponse | null>(null);
  let fileInfo = $state<Map<string, ModelFileInfo>>(new Map());
  let confirmDelete = $state<string | null>(null);

  const showUnlistedStore = persistentStore<boolean>("showUnlisted", true);
  const showIdorNameStore = persistentStore<"id" | "name">("showIdorName", "id");

  async function refreshResources() {
    const r = await fetchResources();
    if (r) resources = r;
  }

  async function refreshConfigInfo() {
    const info = await fetchConfigInfo();
    if (info) {
      const m = new Map<string, ModelFileInfo>();
      for (const mf of info.models) m.set(mf.id, mf);
      fileInfo = m;
    }
  }

  onMount(() => {
    refreshResources();
    refreshConfigInfo();
    const tid = setInterval(refreshResources, 30_000);
    return () => clearInterval(tid);
  });

  $effect(() => {
    void $models;
    refreshConfigInfo();
  });

  let filteredModels = $derived.by(() => {
    const filtered = $models.filter((model) => $showUnlistedStore || !model.unlisted);
    const peerModels = filtered.filter((m) => m.peerID);
    const grouped = peerModels.reduce(
      (acc, model) => {
        const peerId = model.peerID || "unknown";
        if (!acc[peerId]) acc[peerId] = [];
        acc[peerId].push(model);
        return acc;
      },
      {} as Record<string, Model[]>
    );
    return {
      regularModels: filtered.filter((m) => !m.peerID),
      peerModelsByPeerId: grouped,
    };
  });

  async function handleUnloadAllModels() {
    isUnloading = true;
    try { await unloadAllModels(); } catch (e) { console.error(e); }
    finally { setTimeout(() => (isUnloading = false), 1000); }
  }

  async function handleDeleteModel(id: string) {
    confirmDelete = null;
    try {
      await deleteModelFile(id);
      await refreshConfigInfo();
    } catch (e) {
      console.error("delete model:", e);
      alert(`Failed to delete: ${(e as Error).message}`);
    }
  }

  function toggleIdorName() { showIdorNameStore.update((prev) => (prev === "name" ? "id" : "name")); }
  function toggleShowUnlisted() { showUnlistedStore.update((prev) => !prev); }
  function getModelDisplay(model: Model) {
    return $showIdorNameStore === "id" ? model.id : (model.name || model.id);
  }
</script>

<div class="card h-full flex flex-col">
  <!-- Header -->
  <div class="shrink-0">
    <div class="flex justify-between items-baseline">
      <h2 class={$isNarrow ? "text-xl" : ""}>Models</h2>
      {#if $isNarrow}
        <div class="relative">
          <button class="btn text-base flex items-center gap-2 py-1" onclick={() => (menuOpen = !menuOpen)} aria-label="Toggle menu">
            <svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24" fill="currentColor" class="w-5 h-5">
              <path fill-rule="evenodd" d="M3 6.75A.75.75 0 0 1 3.75 6h16.5a.75.75 0 0 1 0 1.5H3.75A.75.75 0 0 1 3 6.75ZM3 12a.75.75 0 0 1 .75-.75h16.5a.75.75 0 0 1 0 1.5H3.75A.75.75 0 0 1 3 12Zm0 5.25a.75.75 0 0 1 .75-.75h16.5a.75.75 0 0 1 0 1.5H3.75a.75.75 0 0 1-.75-.75Z" clip-rule="evenodd" />
            </svg>
          </button>
          {#if menuOpen}
            <div class="absolute right-0 mt-2 w-48 bg-surface border border-gray-200 dark:border-white/10 rounded shadow-lg z-20">
              <button class="w-full text-left px-4 py-2 hover:bg-secondary-hover" onclick={() => { toggleIdorName(); menuOpen = false; }}>
                {$showIdorNameStore === "id" ? "Show Name" : "Show ID"}
              </button>
              <button class="w-full text-left px-4 py-2 hover:bg-secondary-hover" onclick={() => { toggleShowUnlisted(); menuOpen = false; }}>
                {$showUnlistedStore ? "Hide Unlisted" : "Show Unlisted"}
              </button>
              <button class="w-full text-left px-4 py-2 hover:bg-secondary-hover" onclick={() => { showPullModal = true; menuOpen = false; }}>
                Pull Model
              </button>
              <button class="w-full text-left px-4 py-2 hover:bg-secondary-hover" onclick={() => { handleUnloadAllModels(); menuOpen = false; }} disabled={isUnloading}>
                {isUnloading ? "Unloading..." : "Unload All"}
              </button>
            </div>
          {/if}
        </div>
      {/if}
    </div>

    {#if !$isNarrow}
      <div class="flex justify-between mb-2">
        <div class="flex gap-2">
          <button class="btn text-base flex items-center gap-2" onclick={toggleIdorName} style="line-height: 1.2">
            <svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24" fill="currentColor" class="w-5 h-5">
              <path fill-rule="evenodd" d="M15.97 2.47a.75.75 0 0 1 1.06 0l4.5 4.5a.75.75 0 0 1 0 1.06l-4.5 4.5a.75.75 0 1 1-1.06-1.06l3.22-3.22H7.5a.75.75 0 0 1 0-1.5h11.69l-3.22-3.22a.75.75 0 0 1 0-1.06Zm-7.94 9a.75.75 0 0 1 0 1.06l-3.22 3.22H16.5a.75.75 0 0 1 0 1.5H4.81l3.22 3.22a.75.75 0 1 1-1.06 1.06l-4.5-4.5a.75.75 0 0 1 0-1.06l4.5-4.5a.75.75 0 0 1 1.06 0Z" clip-rule="evenodd" />
            </svg>
            {$showIdorNameStore === "id" ? "ID" : "Name"}
          </button>
          <button class="btn text-base flex items-center gap-2" onclick={toggleShowUnlisted} style="line-height: 1.2">
            unlisted
          </button>
          <button class="btn text-base flex items-center gap-2" onclick={() => (showPullModal = true)} style="line-height: 1.2" title="Pull model from HuggingFace">
            <svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24" fill="currentColor" class="w-4 h-4">
              <path fill-rule="evenodd" d="M12 2.25a.75.75 0 0 1 .75.75v11.69l3.22-3.22a.75.75 0 1 1 1.06 1.06l-4.5 4.5a.75.75 0 0 1-1.06 0l-4.5-4.5a.75.75 0 1 1 1.06-1.06l3.22 3.22V3a.75.75 0 0 1 .75-.75Zm-9 13.5a.75.75 0 0 1 .75.75v2.25a1.5 1.5 0 0 0 1.5 1.5h13.5a1.5 1.5 0 0 0 1.5-1.5V16.5a.75.75 0 0 1 1.5 0v2.25a3 3 0 0 1-3 3H5.25a3 3 0 0 1-3-3V16.5a.75.75 0 0 1 .75-.75Z" clip-rule="evenodd" />
            </svg>
            Pull
          </button>
        </div>
        <button class="btn text-base flex items-center gap-2" onclick={handleUnloadAllModels} disabled={isUnloading}>
          <svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24" fill="currentColor" class="w-6 h-6">
            <path fill-rule="evenodd" d="M12 2.25c-5.385 0-9.75 4.365-9.75 9.75s4.365 9.75 9.75 9.75 9.75-4.365 9.75-9.75S17.385 2.25 12 2.25Zm.53 5.47a.75.75 0 0 0-1.06 0l-3 3a.75.75 0 1 0 1.06 1.06l1.72-1.72v5.69a.75.75 0 0 0 1.5 0v-5.69l1.72 1.72a.75.75 0 1 0 1.06-1.06l-3-3Z" clip-rule="evenodd" />
          </svg>
          {isUnloading ? "Unloading..." : "Unload All"}
        </button>
      </div>
    {/if}

    <StorageBar {resources} />
  </div>

  <!-- Model list -->
  <div class="flex-1 overflow-y-auto mt-2">
    <table class="w-full">
      <thead class="sticky top-0 bg-card z-10">
        <tr class="text-left border-b border-gray-200 dark:border-white/10 bg-surface">
          <th>{$showIdorNameStore === "id" ? "Model ID" : "Name"}</th>
          <th></th>
          <th>State</th>
          <th class="w-10"></th>
        </tr>
      </thead>
      <tbody>
        {#each filteredModels.regularModels as model (model.id)}
          {@const fi = fileInfo.get(model.id)}
          <tr class="border-b hover:bg-secondary-hover border-gray-200">
            <td class={model.unlisted ? "text-txtsecondary" : ""}>
              <div class="flex items-start gap-2">
                {#if fi}
                  <span
                    class="mt-1.5 w-2 h-2 rounded-full shrink-0 {fi.file_exists ? 'bg-green-500' : 'bg-red-500'}"
                    title={fi.file_exists ? fi.file_path : `File missing: ${fi.file_path}`}
                  ></span>
                {/if}
                <div>
                  <a href="/upstream/{model.id}/" class="font-semibold" target="_blank">
                    {getModelDisplay(model)}
                  </a>
                  {#if model.description}
                    <p class="text-sm {model.unlisted ? 'text-opacity-70' : ''}"><em>{model.description}</em></p>
                  {/if}
                  {#if model.aliases && model.aliases.length > 0}
                    <p class="text-xs text-txtsecondary">Aliases: {model.aliases.join(", ")}</p>
                  {/if}
                </div>
              </div>
            </td>
            <td class="w-14">
              {#if model.state === "stopped"}
                <button class="btn btn--sm" onclick={() => loadModel(model.id)}>Load</button>
              {:else}
                <button class="btn btn--sm" onclick={() => unloadSingleModel(model.id)} disabled={model.state !== "ready"}>Unload</button>
              {/if}
            </td>
            <td class="w-20">
              <span class="w-16 text-center status status--{model.state}">{model.state}</span>
            </td>
            <td class="w-10 text-right pr-1">
              {#if confirmDelete === model.id}
                <div class="flex gap-1 justify-end">
                  <button class="btn btn--sm !text-red-500" onclick={() => handleDeleteModel(model.id)} title="Confirm delete">✓</button>
                  <button class="btn btn--sm" onclick={() => (confirmDelete = null)}>✕</button>
                </div>
              {:else}
                <button
                  class="btn btn--sm opacity-20 hover:opacity-80 hover:text-red-500"
                  onclick={() => (confirmDelete = model.id)}
                  title="Delete model file from disk"
                >
                  <svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24" fill="currentColor" class="w-4 h-4">
                    <path fill-rule="evenodd" d="M16.5 4.478v.227a48.816 48.816 0 0 1 3.878.512.75.75 0 1 1-.256 1.478l-.209-.035-1.005 13.07a3 3 0 0 1-2.991 2.77H8.084a3 3 0 0 1-2.991-2.77L4.087 6.66l-.209.035a.75.75 0 0 1-.256-1.478A48.567 48.567 0 0 1 7.5 4.705v-.227c0-1.564 1.213-2.9 2.816-2.951a52.662 52.662 0 0 1 3.369 0c1.603.051 2.815 1.387 2.815 2.951Zm-6.136-1.452a51.196 51.196 0 0 1 3.273 0C14.39 3.05 15 3.684 15 4.478v.113a49.488 49.488 0 0 0-6 0v-.113c0-.794.609-1.428 1.364-1.452Zm-.355 5.945a.75.75 0 1 0-1.5.058l.347 9a.75.75 0 1 0 1.499-.058l-.346-9Zm5.48.058a.75.75 0 1 0-1.498-.058l-.347 9a.75.75 0 0 0 1.5.058l.345-9Z" clip-rule="evenodd" />
                  </svg>
                </button>
              {/if}
            </td>
          </tr>
        {/each}
      </tbody>
    </table>

    {#if Object.keys(filteredModels.peerModelsByPeerId).length > 0}
      <h3 class="mt-8 mb-2">Peer Models</h3>
      {#each Object.entries(filteredModels.peerModelsByPeerId).sort(([a], [b]) => a.localeCompare(b)) as [peerId, peerModels] (peerId)}
        <div class="mb-4">
          <table class="w-full">
            <thead class="sticky top-0 bg-card z-10">
              <tr class="text-left border-b border-gray-200 dark:border-white/10 bg-surface">
                <th class="font-semibold">{peerId}</th>
              </tr>
            </thead>
            <tbody>
              {#each peerModels as model (model.id)}
                <tr class="border-b hover:bg-secondary-hover border-gray-200">
                  <td class="pl-8 {model.unlisted ? 'text-txtsecondary' : ''}">
                    <span>{model.id}</span>
                  </td>
                </tr>
              {/each}
            </tbody>
          </table>
        </div>
      {/each}
    {/if}
  </div>
</div>

{#if showPullModal}
  <PullModelModal
    onClose={() => (showPullModal = false)}
    onSuccess={async () => {
      showPullModal = false;
      await refreshConfigInfo();
      await refreshResources();
    }}
  />
{/if}
