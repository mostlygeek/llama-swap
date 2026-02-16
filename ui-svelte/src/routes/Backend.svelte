<script lang="ts">
  import { onMount } from "svelte";
  import { getRecipeBackendState, setRecipeBackend } from "../stores/api";
  import type { RecipeBackendState } from "../lib/types";

  let loading = $state(true);
  let refreshing = $state(false);
  let saving = $state(false);
  let error = $state<string | null>(null);
  let notice = $state<string | null>(null);
  let state = $state<RecipeBackendState | null>(null);
  let selected = $state("");
  let customPath = $state("");
  let useCustom = $state(false);
  let refreshController: AbortController | null = null;

  function sourceLabel(source: RecipeBackendState["backendSource"]): string {
    if (source === "override") return "override (UI)";
    if (source === "env") return "env";
    return "default";
  }

  function syncSelectionFromState(next: RecipeBackendState): void {
    if (next.options.includes(next.backendDir)) {
      selected = next.backendDir;
      customPath = "";
      useCustom = false;
      return;
    }
    selected = "";
    customPath = next.backendDir;
    useCustom = true;
  }

  async function refresh(): Promise<void> {
    refreshController?.abort();
    const controller = new AbortController();
    refreshController = controller;
    const timeout = setTimeout(() => controller.abort(), 15000);

    refreshing = true;
    error = null;
    notice = null;
    if (!state) loading = true;
    try {
      const next = await getRecipeBackendState(controller.signal);
      state = next;
      syncSelectionFromState(next);
    } catch (e) {
      if (controller.signal.aborted) {
        error = "Timeout consultando backend. Pulsa Refresh para reintentar.";
      } else {
        error = e instanceof Error ? e.message : String(e);
      }
    } finally {
      clearTimeout(timeout);
      if (refreshController === controller) {
        refreshController = null;
      }
      refreshing = false;
      loading = false;
    }
  }

  async function applySelection(): Promise<void> {
    if (saving) return;
    const backendDir = useCustom ? customPath.trim() : selected.trim();
    if (!backendDir) {
      error = "Selecciona un backend o introduce una ruta.";
      return;
    }

    saving = true;
    error = null;
    notice = null;
    try {
      const next = await setRecipeBackend(backendDir);
      state = next;
      syncSelectionFromState(next);
      notice = "Backend actualizado correctamente.";
    } catch (e) {
      error = e instanceof Error ? e.message : String(e);
    } finally {
      saving = false;
    }
  }

  onMount(() => {
    void refresh();
    return () => {
      refreshController?.abort();
    };
  });
</script>

<div class="h-full flex flex-col gap-2">
  <div class="card shrink-0">
    <div class="flex items-center justify-between gap-2">
      <h2 class="pb-0">Backend</h2>
      <button class="btn btn--sm" onclick={refresh} disabled={refreshing || saving}>
        {refreshing ? "Refreshing..." : "Refresh"}
      </button>
    </div>

    {#if state}
      <div class="mt-2 text-sm text-txtsecondary break-all">
        Actual: <span class="font-mono text-txtmain">{state.backendDir}</span>
      </div>
      <div class="text-xs text-txtsecondary">Fuente: {sourceLabel(state.backendSource)}</div>
    {/if}

    {#if error}
      <div class="mt-2 p-2 border border-error/40 bg-error/10 rounded text-sm text-error break-words">{error}</div>
    {/if}
    {#if notice}
      <div class="mt-2 p-2 border border-green-400/30 bg-green-600/10 rounded text-sm text-green-300 break-words">{notice}</div>
    {/if}
  </div>

  <div class="card flex-1 min-h-0 overflow-auto">
    {#if loading}
      <div class="text-sm text-txtsecondary">Cargando opciones de backend...</div>
    {:else if state}
      <div class="text-sm text-txtsecondary mb-2">
        Selecciona qué backend usar para recetas y operaciones de cluster.
      </div>

      <div class="space-y-2">
        {#each state.options as option}
          <label class="flex items-center gap-2 text-sm">
            <input
              type="radio"
              name="backend-option"
              checked={!useCustom && selected === option}
              onchange={() => {
                useCustom = false;
                selected = option;
              }}
            />
            <span class="font-mono break-all">{option}</span>
          </label>
        {/each}

        <label class="flex items-center gap-2 text-sm pt-1">
          <input
            type="radio"
            name="backend-option"
            checked={useCustom}
            onchange={() => {
              useCustom = true;
            }}
          />
          <span>Ruta personalizada</span>
        </label>
        <input
          class="input w-full font-mono text-sm"
          placeholder="/home/USER/spark-vllm-docker"
          bind:value={customPath}
          onfocus={() => {
            useCustom = true;
          }}
        />
      </div>

      <div class="mt-3">
        <button class="btn btn--sm" onclick={applySelection} disabled={saving || refreshing}>
          {saving ? "Applying..." : "Apply Backend"}
        </button>
      </div>
    {:else}
      <div class="text-sm text-txtsecondary">No se pudo cargar el estado del backend.</div>
    {/if}
  </div>
</div>
