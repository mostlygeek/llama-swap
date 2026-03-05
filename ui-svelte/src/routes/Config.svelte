<script lang="ts">
  import { onMount } from "svelte";
  import { getConfigModels, saveConfigModels, type ConfigModel } from "../stores/api";

  let models = $state<Record<string, ConfigModel>>({});
  let selectedId = $state<string | null>(null);
  let isLoading = $state(true);
  let isSaving = $state(false);
  let error = $state<string | null>(null);
  let success = $state<string | null>(null);
  let newModelId = $state("");
  let showNewModelInput = $state(false);

  let modelIds = $derived(Object.keys(models).sort());
  let selectedModel = $derived(selectedId ? models[selectedId] ?? null : null);

  onMount(async () => {
    try {
      models = await getConfigModels();
      if (modelIds.length > 0) {
        selectedId = modelIds[0];
      }
    } catch (e: any) {
      error = e.message;
    } finally {
      isLoading = false;
    }
  });

  async function handleSave() {
    isSaving = true;
    error = null;
    success = null;
    try {
      await saveConfigModels(models);
      success = "Configuration saved. Server will reload automatically.";
      setTimeout(() => (success = null), 3000);
    } catch (e: any) {
      error = e.message;
    } finally {
      isSaving = false;
    }
  }

  function addModel() {
    const id = newModelId.trim();
    if (!id) return;
    if (models[id]) {
      error = `Model "${id}" already exists`;
      return;
    }
    models[id] = { cmd: "" };
    models = { ...models };
    selectedId = id;
    newModelId = "";
    showNewModelInput = false;
  }

  function deleteModel(id: string) {
    if (!confirm(`Delete model "${id}"?`)) return;
    const { [id]: _, ...rest } = models;
    models = rest;
    if (selectedId === id) {
      selectedId = modelIds.length > 0 ? modelIds[0] : null;
    }
  }

  function renameModel(oldId: string, newId: string) {
    const trimmed = newId.trim();
    if (!trimmed || trimmed === oldId) return;
    if (models[trimmed]) {
      error = `Model "${trimmed}" already exists`;
      return;
    }
    const { [oldId]: data, ...rest } = models;
    models = { ...rest, [trimmed]: data };
    selectedId = trimmed;
  }

  function updateField(id: string, field: keyof ConfigModel, value: any) {
    models[id] = { ...models[id], [field]: value };
    models = { ...models };
  }

  function parseList(value: string): string[] {
    return value
      .split("\n")
      .map((s) => s.trim())
      .filter((s) => s.length > 0);
  }

  function formatList(arr?: string[]): string {
    return arr?.join("\n") ?? "";
  }

  function formatJson(obj?: Record<string, unknown>): string {
    if (!obj || Object.keys(obj).length === 0) return "";
    return JSON.stringify(obj, null, 2);
  }

  function parseJson(value: string): Record<string, unknown> | undefined {
    const trimmed = value.trim();
    if (!trimmed) return undefined;
    return JSON.parse(trimmed);
  }
</script>

<div class="h-full flex flex-col">
  {#if isLoading}
    <div class="flex items-center justify-center h-full text-txtsecondary">Loading...</div>
  {:else}
    <div class="flex gap-4 flex-1 min-h-0">
      <!-- Model list (left) -->
      <div class="card w-56 shrink-0 flex flex-col">
        <div class="shrink-0">
          <div class="flex justify-between items-baseline">
            <h2 class="text-xl">Config</h2>
            <button class="btn" onclick={handleSave} disabled={isSaving}>
              {isSaving ? "Saving..." : "Save"}
            </button>
          </div>
          {#if success}
            <p class="text-sm text-success">{success}</p>
          {/if}
          {#if error}
            <p class="text-sm text-error">{error}</p>
          {/if}
        </div>

        <div class="flex-1 overflow-y-auto">
          <table class="w-full">
            <tbody>
              {#each modelIds as id (id)}
                <tr
                  class="border-b border-gray-200 dark:border-white/10 cursor-pointer {selectedId === id
                    ? 'bg-secondary-active'
                    : 'hover:bg-secondary-hover'}"
                  onclick={() => (selectedId = id)}
                >
                  <td class="text-sm font-semibold">{id}</td>
                </tr>
              {/each}
            </tbody>
          </table>
        </div>

        <div class="shrink-0 pt-2 border-t border-card-border-inner">
          {#if showNewModelInput}
            <div class="flex gap-1">
              <input
                type="text"
                bind:value={newModelId}
                onkeydown={(e) => e.key === "Enter" && addModel()}
                placeholder="model-id"
                class="config-input flex-1 min-w-0"
              />
              <button class="btn btn--sm" onclick={addModel}>+</button>
              <button
                class="btn btn--sm"
                onclick={() => {
                  showNewModelInput = false;
                  newModelId = "";
                }}
              >
                x
              </button>
            </div>
          {:else}
            <button
              class="btn w-full"
              onclick={() => (showNewModelInput = true)}
            >
              + Add Model
            </button>
          {/if}
        </div>
      </div>

      <!-- Model form (right) -->
      {#if selectedModel && selectedId}
        {@const id = selectedId}
        <div class="card flex-1 overflow-y-auto">
          <div class="flex items-center justify-between pb-4 border-b border-card-border-inner mb-4">
            <input
              type="text"
              value={id}
              onchange={(e) => renameModel(id, e.currentTarget.value)}
              class="text-lg font-bold bg-transparent border border-transparent hover:border-btn-border focus:border-btn-border rounded px-2 py-1 outline-none"
            />
            <button class="btn text-error" onclick={() => deleteModel(id)}>
              Delete
            </button>
          </div>

          <div class="grid gap-4">
            <!-- cmd -->
            <div>
              <label for="cmd" class="config-label">cmd</label>
              <textarea
                id="cmd"
                rows="4"
                value={selectedModel.cmd ?? ""}
                oninput={(e) => updateField(id, "cmd", e.currentTarget.value)}
                class="config-input font-mono"
                placeholder={"llama-server --port ${PORT} --model /path/to/model.gguf"}
              ></textarea>
            </div>

            <!-- name & proxy -->
            <div class="grid grid-cols-2 gap-4">
              <div>
                <label for="name" class="config-label">name</label>
                <input
                  id="name"
                  type="text"
                  value={selectedModel.name ?? ""}
                  oninput={(e) => updateField(id, "name", e.currentTarget.value)}
                  class="config-input"
                />
              </div>
              <div>
                <label for="proxy" class="config-label">proxy</label>
                <input
                  id="proxy"
                  type="text"
                  value={selectedModel.proxy ?? ""}
                  oninput={(e) => updateField(id, "proxy", e.currentTarget.value)}
                  class="config-input"
                  placeholder={"http://localhost:${PORT}"}
                />
              </div>
            </div>

            <!-- description -->
            <div>
              <label for="description" class="config-label">description</label>
              <input
                id="description"
                type="text"
                value={selectedModel.description ?? ""}
                oninput={(e) => updateField(id, "description", e.currentTarget.value)}
                class="config-input"
              />
            </div>

            <!-- ttl, concurrencyLimit, unlisted, sendLoadingState -->
            <div class="grid grid-cols-4 gap-4">
              <div>
                <label for="ttl" class="config-label">ttl (seconds)</label>
                <input
                  id="ttl"
                  type="number"
                  value={selectedModel.ttl ?? 0}
                  oninput={(e) => updateField(id, "ttl", parseInt(e.currentTarget.value) || 0)}
                  class="config-input"
                />
              </div>
              <div>
                <label for="concurrencyLimit" class="config-label">concurrencyLimit</label>
                <input
                  id="concurrencyLimit"
                  type="number"
                  value={selectedModel.concurrencyLimit ?? 0}
                  oninput={(e) => updateField(id, "concurrencyLimit", parseInt(e.currentTarget.value) || 0)}
                  class="config-input"
                />
              </div>
              <div class="flex items-end pb-2">
                <label class="flex items-center gap-2 text-sm">
                  <input
                    type="checkbox"
                    checked={selectedModel.unlisted ?? false}
                    onchange={(e) => updateField(id, "unlisted", e.currentTarget.checked)}
                  />
                  unlisted
                </label>
              </div>
              <div class="flex items-end pb-2">
                <label class="flex items-center gap-2 text-sm">
                  <input
                    type="checkbox"
                    checked={selectedModel.sendLoadingState ?? false}
                    onchange={(e) => updateField(id, "sendLoadingState", e.currentTarget.checked)}
                  />
                  sendLoadingState
                </label>
              </div>
            </div>

            <!-- aliases -->
            <div>
              <label for="aliases" class="config-label">aliases (one per line)</label>
              <textarea
                id="aliases"
                rows="2"
                value={formatList(selectedModel.aliases)}
                oninput={(e) => updateField(id, "aliases", parseList(e.currentTarget.value))}
                class="config-input font-mono"
              ></textarea>
            </div>

            <!-- env -->
            <div>
              <label for="env" class="config-label">env (one per line, KEY=VALUE)</label>
              <textarea
                id="env"
                rows="2"
                value={formatList(selectedModel.env)}
                oninput={(e) => updateField(id, "env", parseList(e.currentTarget.value))}
                class="config-input font-mono"
              ></textarea>
            </div>

            <!-- checkEndpoint & useModelName -->
            <div class="grid grid-cols-2 gap-4">
              <div>
                <label for="checkEndpoint" class="config-label">checkEndpoint</label>
                <input
                  id="checkEndpoint"
                  type="text"
                  value={selectedModel.checkEndpoint ?? ""}
                  oninput={(e) => updateField(id, "checkEndpoint", e.currentTarget.value)}
                  class="config-input"
                />
              </div>
              <div>
                <label for="useModelName" class="config-label">useModelName</label>
                <input
                  id="useModelName"
                  type="text"
                  value={selectedModel.useModelName ?? ""}
                  oninput={(e) => updateField(id, "useModelName", e.currentTarget.value)}
                  class="config-input"
                />
              </div>
            </div>

            <!-- cmdStop -->
            <div>
              <label for="cmdStop" class="config-label">cmdStop</label>
              <textarea
                id="cmdStop"
                rows="2"
                value={selectedModel.cmdStop ?? ""}
                oninput={(e) => updateField(id, "cmdStop", e.currentTarget.value)}
                class="config-input font-mono"
                placeholder={"docker stop ${MODEL_ID}"}
              ></textarea>
            </div>

            <!-- macros -->
            <div>
              <label for="macros" class="config-label">macros (JSON)</label>
              <textarea
                id="macros"
                rows="3"
                value={formatJson(selectedModel.macros)}
                onchange={(e) => {
                  try {
                    updateField(id, "macros", parseJson(e.currentTarget.value));
                  } catch {
                    error = "Invalid JSON in macros";
                  }
                }}
                class="config-input font-mono"
                placeholder={'{"key": "value"}'}
              ></textarea>
            </div>

            <!-- filters -->
            <div>
              <label for="filters" class="config-label">filters (JSON)</label>
              <textarea
                id="filters"
                rows="3"
                value={formatJson(selectedModel.filters)}
                onchange={(e) => {
                  try {
                    updateField(id, "filters", parseJson(e.currentTarget.value));
                  } catch {
                    error = "Invalid JSON in filters";
                  }
                }}
                class="config-input font-mono"
                placeholder={'{"stripParams": "temperature, top_p"}'}
              ></textarea>
            </div>

            <!-- metadata -->
            <div>
              <label for="metadata" class="config-label">metadata (JSON)</label>
              <textarea
                id="metadata"
                rows="3"
                value={formatJson(selectedModel.metadata)}
                onchange={(e) => {
                  try {
                    updateField(id, "metadata", parseJson(e.currentTarget.value));
                  } catch {
                    error = "Invalid JSON in metadata";
                  }
                }}
                class="config-input font-mono"
                placeholder={'{"key": "value"}'}
              ></textarea>
            </div>
          </div>
        </div>
      {:else}
        <div class="card flex-1 flex items-center justify-center text-txtsecondary">
          {modelIds.length === 0 ? "No models configured. Add one to get started." : "Select a model to edit."}
        </div>
      {/if}
    </div>
  {/if}
</div>

<style>
  .config-label {
    display: block;
    font-size: 0.875rem;
    font-weight: 500;
    margin-bottom: 0.25rem;
  }

  .config-input {
    width: 100%;
    padding: 0.5rem 0.75rem;
    font-size: 0.875rem;
    border: 1px solid var(--color-card-border);
    border-radius: 0.375rem;
    background: var(--color-background);
    outline: none;
  }

  .config-input:focus {
    border-color: var(--color-primary);
    box-shadow: 0 0 0 2px var(--color-focus-ring);
  }
</style>
