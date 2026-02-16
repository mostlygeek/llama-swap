<script lang="ts">
  import {
    models,
    loadModel,
    unloadAllModels,
    stopClusterAndUnload,
    unloadSingleModel,
    startBenchy,
    getBenchyJob,
    cancelBenchyJob,
  } from "../stores/api";
  import { isNarrow } from "../stores/theme";
  import { persistentStore } from "../stores/persistent";
  import BenchyDialog from "./BenchyDialog.svelte";
  import RecipeManager from "./RecipeManager.svelte";
  import type { BenchyJob, BenchyStartOptions, Model } from "../lib/types";

  let isUnloading = $state(false);
  let isStoppingCluster = $state(false);
  let menuOpen = $state(false);
  let showRecipeManager = $state(false);

  const showUnlistedStore = persistentStore<boolean>("showUnlisted", true);
  const showIdorNameStore = persistentStore<"id" | "name">("showIdorName", "id");

  // Benchy state (single active job in UI)
  let benchyOpen = $state(false);
  let benchyStarting = $state(false);
  let benchyError: string | null = $state(null);
  let benchyJob: BenchyJob | null = $state(null);
  let benchyJobId: string | null = $state(null);
  let benchyModelID: string | null = $state(null);
  let benchyPollTimer: ReturnType<typeof setTimeout> | null = null;

  let benchyBusy = $derived.by(() => {
    return benchyStarting || benchyJob?.status === "running";
  });

  let filteredModels = $derived.by(() => {
    const filtered = $models.filter((model) => $showUnlistedStore || !model.unlisted);
    const peerModels = filtered.filter((m) => m.peerID);

    // Group peer models by peerID
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

  async function handleUnloadAllModels(): Promise<void> {
    isUnloading = true;
    try {
      await unloadAllModels();
    } catch (e) {
      console.error(e);
    } finally {
      setTimeout(() => (isUnloading = false), 1000);
    }
  }

  async function handleStopCluster(): Promise<void> {
    isStoppingCluster = true;
    try {
      await stopClusterAndUnload();
    } catch (e) {
      console.error(e);
    } finally {
      setTimeout(() => (isStoppingCluster = false), 1000);
    }
  }

  function toggleIdorName(): void {
    showIdorNameStore.update((prev) => (prev === "name" ? "id" : "name"));
  }

  function toggleShowUnlisted(): void {
    showUnlistedStore.update((prev) => !prev);
  }

  function toggleRecipeManager(): void {
    showRecipeManager = !showRecipeManager;
  }

  function getModelDisplay(model: Model): string {
    return $showIdorNameStore === "id" ? model.id : (model.name || model.id);
  }

  function clearBenchyPoll(): void {
    if (benchyPollTimer !== null) {
      clearTimeout(benchyPollTimer);
      benchyPollTimer = null;
    }
  }

  function closeBenchyDialog(): void {
    benchyOpen = false;
    benchyStarting = false;
    benchyError = null;
    benchyModelID = null;
    clearBenchyPoll();
  }

  async function pollBenchy(jobID: string): Promise<void> {
    try {
      const job = await getBenchyJob(jobID);
      benchyJob = job;

      if (job.status === "running") {
        benchyPollTimer = setTimeout(() => {
          void pollBenchy(jobID);
        }, 1000);
      } else {
        clearBenchyPoll();
      }
    } catch (e) {
      benchyError = e instanceof Error ? e.message : String(e);
      clearBenchyPoll();
    }
  }

  function waitForModelReady(modelID: string, timeoutMs = 5 * 60 * 1000): Promise<void> {
    // Resolve immediately if already ready
    const current = $models.find((m) => m.id === modelID);
    if (current?.state === "ready") return Promise.resolve();

    return new Promise((resolve, reject) => {
      const timer = setTimeout(() => {
        unsub();
        reject(new Error(`Timed out waiting for ${modelID} to become ready`));
      }, timeoutMs);

      const unsub = models.subscribe((list) => {
        const m = list.find((x) => x.id === modelID);
        if (m?.state === "ready") {
          clearTimeout(timer);
          unsub();
          resolve();
        }
      });
    });
  }

  function openBenchyForModel(modelID: string): void {
    benchyModelID = modelID;
    benchyOpen = true;
    benchyError = null;
  }

  async function runBenchyForModel(opts: BenchyStartOptions): Promise<void> {
    if (!benchyModelID) return;
    const modelID = benchyModelID;

    benchyStarting = true;
    benchyError = null;
    benchyJob = null;
    benchyJobId = null;
    clearBenchyPoll();

    try {
      const m = $models.find((x) => x.id === modelID);
      if (!m) throw new Error(`Model not found: ${modelID}`);
      if (m.state === "stopping" || m.state === "shutdown") {
        throw new Error(`Model is ${m.state}; wait until it is stopped/ready`);
      }

      if (m.state === "stopped") {
        await loadModel(modelID);
      }
      await waitForModelReady(modelID);

      const id = await startBenchy(modelID, opts);
      benchyJobId = id;
      benchyStarting = false;
      await pollBenchy(id);
    } catch (e) {
      benchyStarting = false;
      benchyError = e instanceof Error ? e.message : String(e);
    }
  }

  async function cancelBenchy(): Promise<void> {
    if (!benchyJobId) return;
    try {
      await cancelBenchyJob(benchyJobId);
      await pollBenchy(benchyJobId);
    } catch (e) {
      benchyError = e instanceof Error ? e.message : String(e);
    }
  }
</script>

<div class="card h-full flex flex-col">
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
              <button
                class="w-full text-left px-4 py-2 hover:bg-secondary-hover flex items-center gap-2"
                onclick={() => { toggleIdorName(); menuOpen = false; }}
              >
                <svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24" fill="currentColor" class="w-5 h-5">
                  <path fill-rule="evenodd" d="M15.97 2.47a.75.75 0 0 1 1.06 0l4.5 4.5a.75.75 0 0 1 0 1.06l-4.5 4.5a.75.75 0 1 1-1.06-1.06l3.22-3.22H7.5a.75.75 0 0 1 0-1.5h11.69l-3.22-3.22a.75.75 0 0 1 0-1.06Zm-7.94 9a.75.75 0 0 1 0 1.06l-3.22 3.22H16.5a.75.75 0 0 1 0 1.5H4.81l3.22 3.22a.75.75 0 1 1-1.06 1.06l-4.5-4.5a.75.75 0 0 1 0-1.06l4.5-4.5a.75.75 0 0 1 1.06 0Z" clip-rule="evenodd" />
                </svg>
                {$showIdorNameStore === "id" ? "Show Name" : "Show ID"}
              </button>
              <button
                class="w-full text-left px-4 py-2 hover:bg-secondary-hover flex items-center gap-2"
                onclick={() => { toggleShowUnlisted(); menuOpen = false; }}
              >
                {#if $showUnlistedStore}
                  <svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24" fill="currentColor" class="w-5 h-5">
                    <path d="M3.53 2.47a.75.75 0 0 0-1.06 1.06l18 18a.75.75 0 1 0 1.06-1.06l-18-18ZM22.676 12.553a11.249 11.249 0 0 1-2.631 4.31l-3.099-3.099a5.25 5.25 0 0 0-6.71-6.71L7.759 4.577a11.217 11.217 0 0 1 4.242-.827c4.97 0 9.185 3.223 10.675 7.69.12.362.12.752 0 1.113Z" />
                    <path d="M15.75 12c0 .18-.013.357-.037.53l-4.244-4.243A3.75 3.75 0 0 1 15.75 12ZM12.53 15.713l-4.243-4.244a3.75 3.75 0 0 0 4.244 4.243Z" />
                    <path d="M6.75 12c0-.619.107-1.213.304-1.764l-3.1-3.1a11.25 11.25 0 0 0-2.63 4.31c-.12.362-.12.752 0 1.114 1.489 4.467 5.704 7.69 10.675 7.69 1.5 0 2.933-.294 4.242-.827l-2.477-2.477A5.25 5.25 0 0 1 6.75 12Z" />
                  </svg>
                {:else}
                  <svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24" fill="currentColor" class="w-5 h-5">
                    <path d="M12 15a3 3 0 1 0 0-6 3 3 0 0 0 0 6Z" />
                    <path fill-rule="evenodd" d="M1.323 11.447C2.811 6.976 7.028 3.75 12.001 3.75c4.97 0 9.185 3.223 10.675 7.69.12.362.12.752 0 1.113-1.487 4.471-5.705 7.697-10.677 7.697-4.97 0-9.186-3.223-10.675-7.69a1.762 1.762 0 0 1 0-1.113ZM17.25 12a5.25 5.25 0 1 1-10.5 0 5.25 5.25 0 0 1 10.5 0Z" clip-rule="evenodd" />
                  </svg>
                {/if}
                {$showUnlistedStore ? "Hide Unlisted" : "Show Unlisted"}
              </button>
              <button
                class="w-full text-left px-4 py-2 hover:bg-secondary-hover flex items-center gap-2"
                onclick={() => { handleUnloadAllModels(); menuOpen = false; }}
                disabled={isUnloading || isStoppingCluster}
              >
                <svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24" fill="currentColor" class="w-6 h-6">
                  <path fill-rule="evenodd" d="M12 2.25c-5.385 0-9.75 4.365-9.75 9.75s4.365 9.75 9.75 9.75 9.75-4.365 9.75-9.75S17.385 2.25 12 2.25Zm.53 5.47a.75.75 0 0 0-1.06 0l-3 3a.75.75 0 1 0 1.06 1.06l1.72-1.72v5.69a.75.75 0 0 0 1.5 0v-5.69l1.72 1.72a.75.75 0 1 0 1.06-1.06l-3-3Z" clip-rule="evenodd" />
                </svg>
                {isUnloading ? "Unloading..." : "Unload All"}
              </button>
              <button
                class="w-full text-left px-4 py-2 hover:bg-secondary-hover flex items-center gap-2"
                onclick={() => { handleStopCluster(); menuOpen = false; }}
                disabled={isStoppingCluster || isUnloading}
              >
                <svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24" fill="currentColor" class="w-6 h-6">
                  <path fill-rule="evenodd" d="M6.75 6A2.25 2.25 0 0 0 4.5 8.25v7.5A2.25 2.25 0 0 0 6.75 18h10.5a2.25 2.25 0 0 0 2.25-2.25v-7.5A2.25 2.25 0 0 0 17.25 6H6.75Zm2.28 2.22a.75.75 0 0 0-1.06 1.06L10.69 12l-2.72 2.72a.75.75 0 1 0 1.06 1.06L11.75 13.06l2.72 2.72a.75.75 0 1 0 1.06-1.06L12.81 12l2.72-2.72a.75.75 0 1 0-1.06-1.06l-2.72 2.72-2.72-2.72Z" clip-rule="evenodd" />
                </svg>
                {isStoppingCluster ? "Stopping..." : "Stop Cluster"}
              </button>
              <button
                class="w-full text-left px-4 py-2 hover:bg-secondary-hover flex items-center gap-2"
                onclick={() => { toggleRecipeManager(); menuOpen = false; }}
              >
                <svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24" fill="currentColor" class="w-5 h-5">
                  <path d="M4.5 3.75A2.25 2.25 0 0 0 2.25 6v12A2.25 2.25 0 0 0 4.5 20.25h15A2.25 2.25 0 0 0 21.75 18V6A2.25 2.25 0 0 0 19.5 3.75h-15ZM5.25 7.5a.75.75 0 0 1 .75-.75h12a.75.75 0 0 1 0 1.5H6a.75.75 0 0 1-.75-.75Zm0 4.5A.75.75 0 0 1 6 11.25h12a.75.75 0 0 1 0 1.5H6a.75.75 0 0 1-.75-.75Zm.75 3.75a.75.75 0 0 0 0 1.5h7.5a.75.75 0 0 0 0-1.5H6Z" />
                </svg>
                {showRecipeManager ? "Hide Recipes" : "Manage Recipes"}
              </button>
            </div>
          {/if}
        </div>
      {/if}
    </div>
    {#if !$isNarrow}
      <div class="flex justify-between">
        <div class="flex gap-2">
          <button class="btn text-base flex items-center gap-2" onclick={toggleIdorName} style="line-height: 1.2">
            <svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24" fill="currentColor" class="w-5 h-5">
              <path fill-rule="evenodd" d="M15.97 2.47a.75.75 0 0 1 1.06 0l4.5 4.5a.75.75 0 0 1 0 1.06l-4.5 4.5a.75.75 0 1 1-1.06-1.06l3.22-3.22H7.5a.75.75 0 0 1 0-1.5h11.69l-3.22-3.22a.75.75 0 0 1 0-1.06Zm-7.94 9a.75.75 0 0 1 0 1.06l-3.22 3.22H16.5a.75.75 0 0 1 0 1.5H4.81l3.22 3.22a.75.75 0 1 1-1.06 1.06l-4.5-4.5a.75.75 0 0 1 0-1.06l4.5-4.5a.75.75 0 0 1 1.06 0Z" clip-rule="evenodd" />
            </svg>
            {$showIdorNameStore === "id" ? "ID" : "Name"}
          </button>

          <button class="btn text-base flex items-center gap-2" onclick={toggleShowUnlisted} style="line-height: 1.2">
            {#if $showUnlistedStore}
              <svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24" fill="currentColor" class="w-5 h-5">
                <path d="M12 15a3 3 0 1 0 0-6 3 3 0 0 0 0 6Z" />
                <path fill-rule="evenodd" d="M1.323 11.447C2.811 6.976 7.028 3.75 12.001 3.75c4.97 0 9.185 3.223 10.675 7.69.12.362.12.752 0 1.113-1.487 4.471-5.705 7.697-10.677 7.697-4.97 0-9.186-3.223-10.675-7.69a1.762 1.762 0 0 1 0-1.113ZM17.25 12a5.25 5.25 0 1 1-10.5 0 5.25 5.25 0 0 1 10.5 0Z" clip-rule="evenodd" />
              </svg>
            {:else}
              <svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24" fill="currentColor" class="w-5 h-5">
                <path d="M3.53 2.47a.75.75 0 0 0-1.06 1.06l18 18a.75.75 0 1 0 1.06-1.06l-18-18ZM22.676 12.553a11.249 11.249 0 0 1-2.631 4.31l-3.099-3.099a5.25 5.25 0 0 0-6.71-6.71L7.759 4.577a11.217 11.217 0 0 1 4.242-.827c4.97 0 9.185 3.223 10.675 7.69.12.362.12.752 0 1.113Z" />
                <path d="M15.75 12c0 .18-.013.357-.037.53l-4.244-4.243A3.75 3.75 0 0 1 15.75 12ZM12.53 15.713l-4.243-4.244a3.75 3.75 0 0 0 4.244 4.243Z" />
                <path d="M6.75 12c0-.619.107-1.213.304-1.764l-3.1-3.1a11.25 11.25 0 0 0-2.63 4.31c-.12.362-.12.752 0 1.114 1.489 4.467 5.704 7.69 10.675 7.69 1.5 0 2.933-.294 4.242-.827l-2.477-2.477A5.25 5.25 0 0 1 6.75 12Z" />
              </svg>
            {/if}
            unlisted
          </button>
        </div>
        <div class="flex gap-2">
          <button class="btn text-base flex items-center gap-2" onclick={handleStopCluster} disabled={isStoppingCluster || isUnloading}>
            <svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24" fill="currentColor" class="w-6 h-6">
              <path fill-rule="evenodd" d="M6.75 6A2.25 2.25 0 0 0 4.5 8.25v7.5A2.25 2.25 0 0 0 6.75 18h10.5a2.25 2.25 0 0 0 2.25-2.25v-7.5A2.25 2.25 0 0 0 17.25 6H6.75Zm2.28 2.22a.75.75 0 0 0-1.06 1.06L10.69 12l-2.72 2.72a.75.75 0 1 0 1.06 1.06L11.75 13.06l2.72 2.72a.75.75 0 1 0 1.06-1.06L12.81 12l2.72-2.72a.75.75 0 1 0-1.06-1.06l-2.72 2.72-2.72-2.72Z" clip-rule="evenodd" />
            </svg>
            {isStoppingCluster ? "Stopping..." : "Stop Cluster"}
          </button>
          <button class="btn text-base flex items-center gap-2" onclick={handleUnloadAllModels} disabled={isUnloading || isStoppingCluster}>
            <svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24" fill="currentColor" class="w-6 h-6">
              <path fill-rule="evenodd" d="M12 2.25c-5.385 0-9.75 4.365-9.75 9.75s4.365 9.75 9.75 9.75 9.75-4.365 9.75-9.75S17.385 2.25 12 2.25Zm.53 5.47a.75.75 0 0 0-1.06 0l-3 3a.75.75 0 1 0 1.06 1.06l1.72-1.72v5.69a.75.75 0 0 0 1.5 0v-5.69l1.72 1.72a.75.75 0 1 0 1.06-1.06l-3-3Z" clip-rule="evenodd" />
            </svg>
            {isUnloading ? "Unloading..." : "Unload All"}
          </button>
        </div>
      </div>
      <div class="mt-2">
        <button class="btn text-base flex items-center gap-2" onclick={toggleRecipeManager}>
          <svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24" fill="currentColor" class="w-5 h-5">
            <path d="M4.5 3.75A2.25 2.25 0 0 0 2.25 6v12A2.25 2.25 0 0 0 4.5 20.25h15A2.25 2.25 0 0 0 21.75 18V6A2.25 2.25 0 0 0 19.5 3.75h-15ZM5.25 7.5a.75.75 0 0 1 .75-.75h12a.75.75 0 0 1 0 1.5H6a.75.75 0 0 1-.75-.75Zm0 4.5A.75.75 0 0 1 6 11.25h12a.75.75 0 0 1 0 1.5H6a.75.75 0 0 1-.75-.75Zm.75 3.75a.75.75 0 0 0 0 1.5h7.5a.75.75 0 0 0 0-1.5H6Z" />
          </svg>
          {showRecipeManager ? "Hide Recipe Manager" : "Manage Recipes"}
        </button>
      </div>
    {/if}
  </div>

  {#if showRecipeManager}
    <div class="shrink-0">
      <RecipeManager />
    </div>
  {/if}

  <div class="flex-1 overflow-y-auto">
    <table class="w-full">
      <thead class="sticky top-0 bg-card z-10">
        <tr class="text-left border-b border-gray-200 dark:border-white/10 bg-surface">
          <th>{$showIdorNameStore === "id" ? "Model ID" : "Name"}</th>
          <th></th>
          <th>State</th>
        </tr>
      </thead>
      <tbody>
        {#each filteredModels.regularModels as model (model.id)}
          <tr class="border-b hover:bg-secondary-hover border-gray-200">
            <td class={model.unlisted ? "text-txtsecondary" : ""}>
              <a href="/upstream/{model.id}/" class="font-semibold" target="_blank">
                {getModelDisplay(model)}
              </a>
              {#if model.description}
                <p class={model.unlisted ? "text-opacity-70" : ""}><em>{model.description}</em></p>
              {/if}
            </td>
            <td class="w-44">
              <div class="flex justify-end gap-2">
                {#if model.state === "stopped"}
                  <button class="btn btn--sm" onclick={() => loadModel(model.id)} disabled={benchyBusy}>Load</button>
                {:else}
                  <button class="btn btn--sm" onclick={() => unloadSingleModel(model.id)} disabled={model.state !== "ready" || benchyBusy}>Unload</button>
                {/if}
                <button
                  class="btn btn--sm"
                  onclick={() => openBenchyForModel(model.id)}
                  disabled={benchyBusy || model.state === "stopping" || model.state === "shutdown" || model.state === "unknown"}
                  title={model.state === "stopped" ? "Load + configure llama-benchy" : "Configure llama-benchy"}
                >
                  Benchy
                </button>
              </div>
            </td>
            <td class="w-20">
              <span class="w-16 text-center status status--{model.state}">{model.state}</span>
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

<BenchyDialog
  model={benchyModelID}
  job={benchyJob}
  open={benchyOpen}
  canStart={!!benchyModelID && !benchyBusy}
  starting={benchyStarting}
  error={benchyError}
  onstart={runBenchyForModel}
  onclose={closeBenchyDialog}
  oncancel={cancelBenchy}
/>
