<script lang="ts">
  import { onMount } from "svelte";
  import { deleteRecipeModel, getRecipeUIState, upsertRecipeModel } from "../stores/api";
  import type { RecipeManagedModel, RecipeUIState } from "../lib/types";

  let loading = $state(true);
  let saving = $state(false);
  let error = $state<string | null>(null);
  let notice = $state<string | null>(null);

  let state = $state<RecipeUIState | null>(null);
  let selectedModelID = $state<string>("");

  let modelId = $state("");
  let recipeRef = $state("");
  let name = $state("");
  let description = $state("");
  let aliasesCsv = $state("");
  let useModelName = $state("");
  let mode = $state<"solo" | "cluster">("cluster");
  let tensorParallel = $state<number>(2);
  let nodes = $state("");
  let extraArgs = $state("");
  let group = $state("managed-recipes");
  let unlisted = $state(false);
  let benchyTrustRemoteCode = $state<"auto" | "true" | "false">("auto");
  let refreshController: AbortController | null = null;

  function clearForm(): void {
    selectedModelID = "";
    modelId = "";
    recipeRef = "";
    name = "";
    description = "";
    aliasesCsv = "";
    useModelName = "";
    mode = "cluster";
    tensorParallel = 2;
    nodes = "";
    extraArgs = "";
    group = "managed-recipes";
    unlisted = false;
    benchyTrustRemoteCode = "auto";
  }

  function loadModelIntoForm(model: RecipeManagedModel): void {
    selectedModelID = model.modelId;
    modelId = model.modelId;
    recipeRef = model.recipeRef || "";
    name = model.name || "";
    description = model.description || "";
    aliasesCsv = (model.aliases || []).join(", ");
    useModelName = model.useModelName || "";
    mode = model.mode || "cluster";
    tensorParallel = model.tensorParallel || 1;
    nodes = model.nodes || "";
    extraArgs = model.extraArgs || "";
    group = model.group || "managed-recipes";
    unlisted = !!model.unlisted;
    if (model.benchyTrustRemoteCode === true) {
      benchyTrustRemoteCode = "true";
    } else if (model.benchyTrustRemoteCode === false) {
      benchyTrustRemoteCode = "false";
    } else {
      benchyTrustRemoteCode = "auto";
    }
  }

  async function refreshState(): Promise<void> {
    refreshController?.abort();
    const controller = new AbortController();
    refreshController = controller;
    const timeout = setTimeout(() => controller.abort(), 10000);

    loading = true;
    error = null;
    try {
      state = await getRecipeUIState(controller.signal);
      if (state.groups.length > 0 && !state.groups.includes(group)) {
        group = state.groups[0];
      }
    } catch (e) {
      if (controller.signal.aborted) {
        error = "Timeout al cargar recetas. Pulsa Refresh para reintentar.";
      } else {
        error = e instanceof Error ? e.message : String(e);
      }
    } finally {
      clearTimeout(timeout);
      if (refreshController === controller) {
        refreshController = null;
      }
      loading = false;
    }
  }

  function parseAliases(raw: string): string[] {
    return raw
      .split(",")
      .map((a) => a.trim())
      .filter(Boolean);
  }

  async function save(): Promise<void> {
    const id = modelId.trim();
    const recipe = recipeRef.trim();
    if (!id) {
      error = "modelId es obligatorio";
      return;
    }
    if (!recipe) {
      error = "recipeRef es obligatorio";
      return;
    }

    saving = true;
    error = null;
    notice = null;
    try {
      const payload: any = {
        modelId: id,
        recipeRef: recipe,
        name: name.trim(),
        description: description.trim(),
        aliases: parseAliases(aliasesCsv),
        useModelName: useModelName.trim(),
        mode,
        tensorParallel,
        nodes: nodes.trim(),
        extraArgs: extraArgs.trim(),
        group: group.trim(),
        unlisted,
      };
      if (benchyTrustRemoteCode === "true") {
        payload.benchyTrustRemoteCode = true;
      } else if (benchyTrustRemoteCode === "false") {
        payload.benchyTrustRemoteCode = false;
      }

      state = await upsertRecipeModel(payload);
      notice = "Guardado. llama-swap regeneró config.yaml automáticamente.";
      selectedModelID = id;
    } catch (e) {
      error = e instanceof Error ? e.message : String(e);
    } finally {
      saving = false;
    }
  }

  async function removeSelected(): Promise<void> {
    const id = (selectedModelID || modelId).trim();
    if (!id) {
      return;
    }
    saving = true;
    error = null;
    notice = null;
    try {
      state = await deleteRecipeModel(id);
      notice = `Modelo ${id} eliminado y config.yaml actualizado.`;
      clearForm();
    } catch (e) {
      error = e instanceof Error ? e.message : String(e);
    } finally {
      saving = false;
    }
  }

  onMount(() => {
    void refreshState();
    return () => {
      refreshController?.abort();
    };
  });
</script>

<div class="card mt-4">
  <div class="flex items-center justify-between">
    <h3>Recipe Manager</h3>
    <button class="btn btn--sm" onclick={refreshState} disabled={loading || saving}>Refresh</button>
  </div>

  {#if loading}
    <div class="text-sm text-txtsecondary">Loading recipe state...</div>
  {:else}
    <div class="text-xs text-txtsecondary mb-3 break-all">
      Config: {state?.configPath} | Backend: {state?.backendDir}
    </div>

    {#if error}
      <div class="p-2 border border-error/40 bg-error/10 rounded text-sm text-error break-words mb-2">{error}</div>
    {/if}
    {#if notice}
      <div class="p-2 border border-green-400/30 bg-green-600/10 rounded text-sm text-green-300 break-words mb-2">{notice}</div>
    {/if}

    <div class="grid grid-cols-1 md:grid-cols-2 gap-3">
      <label class="text-sm">
        <div class="text-txtsecondary mb-1">Model ID</div>
        <input class="w-full px-2 py-1 rounded border border-card-border bg-background" bind:value={modelId} placeholder="Qwen/Qwen3-Coder-Next-FP8" />
      </label>
      <label class="text-sm">
        <div class="text-txtsecondary mb-1">Recipe Ref</div>
        <input class="w-full px-2 py-1 rounded border border-card-border bg-background" bind:value={recipeRef} list="recipes-list" placeholder="qwen3-coder-next-fp8 o /ruta/recipe.yaml" />
        <datalist id="recipes-list">
          {#each state?.recipes || [] as r}
            <option value={r.ref}>{r.id} - {r.name || r.model}</option>
          {/each}
        </datalist>
      </label>
      <label class="text-sm">
        <div class="text-txtsecondary mb-1">Name</div>
        <input class="w-full px-2 py-1 rounded border border-card-border bg-background" bind:value={name} placeholder="Display name" />
      </label>
      <label class="text-sm">
        <div class="text-txtsecondary mb-1">Description</div>
        <input class="w-full px-2 py-1 rounded border border-card-border bg-background" bind:value={description} placeholder="Optional description" />
      </label>
      <label class="text-sm">
        <div class="text-txtsecondary mb-1">Aliases (comma separated)</div>
        <input class="w-full px-2 py-1 rounded border border-card-border bg-background" bind:value={aliasesCsv} placeholder="alias1, alias2" />
      </label>
      <label class="text-sm">
        <div class="text-txtsecondary mb-1">useModelName</div>
        <input class="w-full px-2 py-1 rounded border border-card-border bg-background" bind:value={useModelName} placeholder="HF model id served by vLLM" />
      </label>
      <label class="text-sm">
        <div class="text-txtsecondary mb-1">Mode</div>
        <select class="w-full px-2 py-1 rounded border border-card-border bg-background" bind:value={mode}>
          <option value="cluster">cluster</option>
          <option value="solo">solo</option>
        </select>
      </label>
      <label class="text-sm">
        <div class="text-txtsecondary mb-1">Tensor Parallel (--tp)</div>
        <input class="w-full px-2 py-1 rounded border border-card-border bg-background font-mono" type="number" min="1" bind:value={tensorParallel} />
      </label>
      <label class="text-sm">
        <div class="text-txtsecondary mb-1">Nodes (-n)</div>
        <input class="w-full px-2 py-1 rounded border border-card-border bg-background font-mono" bind:value={nodes} placeholder="${vllm_nodes}" disabled={mode === "solo"} />
      </label>
      <label class="text-sm">
        <div class="text-txtsecondary mb-1">Group</div>
        <input class="w-full px-2 py-1 rounded border border-card-border bg-background" bind:value={group} list="groups-list" placeholder="managed-recipes" />
        <datalist id="groups-list">
          {#each state?.groups || [] as g}
            <option value={g}></option>
          {/each}
        </datalist>
      </label>
      <label class="text-sm md:col-span-2">
        <div class="text-txtsecondary mb-1">Extra Args</div>
        <input class="w-full px-2 py-1 rounded border border-card-border bg-background font-mono" bind:value={extraArgs} placeholder="--gpu-mem 0.9 --max-model-len 185000 -- --enable-prefix-caching" />
      </label>
      <label class="text-sm">
        <div class="text-txtsecondary mb-1">Benchy trust_remote_code</div>
        <select class="w-full px-2 py-1 rounded border border-card-border bg-background" bind:value={benchyTrustRemoteCode}>
          <option value="auto">auto</option>
          <option value="true">true</option>
          <option value="false">false</option>
        </select>
      </label>
      <label class="text-sm flex items-center gap-2 pt-6">
        <input type="checkbox" bind:checked={unlisted} />
        unlisted
      </label>
    </div>

    <div class="flex gap-2 mt-3">
      <button class="btn btn--sm" onclick={save} disabled={saving}>{selectedModelID ? "Update" : "Add"}</button>
      <button class="btn btn--sm" onclick={removeSelected} disabled={saving || (!selectedModelID && !modelId.trim())}>Delete</button>
      <button class="btn btn--sm" onclick={clearForm} disabled={saving}>New</button>
    </div>

    <div class="mt-4">
      <h4 class="mb-1">Managed Models</h4>
      <div class="overflow-x-auto border border-card-border rounded">
        <table class="w-full text-sm">
          <thead class="bg-surface text-left">
            <tr>
              <th class="px-2 py-1">Model ID</th>
              <th class="px-2 py-1">Recipe</th>
              <th class="px-2 py-1">Mode</th>
              <th class="px-2 py-1">Group</th>
              <th class="px-2 py-1">Actions</th>
            </tr>
          </thead>
          <tbody>
            {#if (state?.models.length || 0) === 0}
              <tr><td class="px-2 py-2 text-txtsecondary" colspan="5">No recipe models yet.</td></tr>
            {:else}
              {#each state?.models || [] as m (m.modelId)}
                <tr class="border-t border-card-border">
                  <td class="px-2 py-1 font-mono">{m.modelId}</td>
                  <td class="px-2 py-1 font-mono">{m.recipeRef}</td>
                  <td class="px-2 py-1">{m.mode}</td>
                  <td class="px-2 py-1">{m.group}</td>
                  <td class="px-2 py-1">
                    <div class="flex gap-1">
                      <button class="btn btn--sm" onclick={() => loadModelIntoForm(m)} disabled={saving}>Edit</button>
                    </div>
                  </td>
                </tr>
              {/each}
            {/if}
          </tbody>
        </table>
      </div>
    </div>
  {/if}
</div>
