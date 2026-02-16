<script lang="ts">
  import { onMount } from "svelte";
  import { getConfigEditorState, saveConfigEditorContent } from "../stores/api";
  import type { ConfigEditorState } from "../lib/types";

  let loading = $state(true);
  let saving = $state(false);
  let error = $state<string | null>(null);
  let notice = $state<string | null>(null);
  let configPath = $state("");
  let updatedAt = $state("");
  let content = $state("");
  let originalContent = $state("");
  let refreshController: AbortController | null = null;

  let isDirty = $derived(content !== originalContent);

  function applyState(state: ConfigEditorState): void {
    configPath = state.path || "";
    content = state.content || "";
    originalContent = state.content || "";
    updatedAt = state.updatedAt || "";
  }

  function formatUpdatedAt(value: string): string {
    if (!value) return "unknown";
    const parsed = new Date(value);
    if (Number.isNaN(parsed.getTime())) return value;
    return parsed.toLocaleString();
  }

  async function refresh(): Promise<void> {
    refreshController?.abort();
    const controller = new AbortController();
    refreshController = controller;
    const timeout = setTimeout(() => controller.abort(), 10000);

    loading = true;
    error = null;
    notice = null;
    try {
      const state = await getConfigEditorState(controller.signal);
      applyState(state);
    } catch (e) {
      if (controller.signal.aborted) {
        error = "Timeout al cargar config.yaml. Pulsa Refresh para reintentar.";
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

  async function save(): Promise<void> {
    if (saving || loading || !isDirty) return;
    saving = true;
    error = null;
    notice = null;
    try {
      const state = await saveConfigEditorContent(content);
      applyState(state);
      notice = "config.yaml guardado y validado correctamente.";
    } catch (e) {
      error = e instanceof Error ? e.message : String(e);
    } finally {
      saving = false;
    }
  }

  function handleEditorKeyDown(event: KeyboardEvent): void {
    if ((event.metaKey || event.ctrlKey) && event.key.toLowerCase() === "s") {
      event.preventDefault();
      void save();
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
      <h2 class="pb-0">Config Editor</h2>
      <div class="flex gap-2">
        <button class="btn btn--sm" onclick={refresh} disabled={loading || saving}>Refresh</button>
        <button class="btn btn--sm" onclick={save} disabled={loading || saving || !isDirty}>
          {saving ? "Saving..." : "Save"}
        </button>
      </div>
    </div>

    <div class="mt-2 text-xs text-txtsecondary break-all">
      File: {configPath || "unknown"}
      {#if updatedAt}
        | Updated: {formatUpdatedAt(updatedAt)}
      {/if}
    </div>
    <div class="mt-1 text-xs text-txtsecondary">Tip: Ctrl/Cmd+S para guardar.</div>

    {#if error}
      <div class="mt-2 p-2 border border-error/40 bg-error/10 rounded text-sm text-error break-words">{error}</div>
    {/if}
    {#if notice}
      <div class="mt-2 p-2 border border-green-400/30 bg-green-600/10 rounded text-sm text-green-300 break-words">{notice}</div>
    {/if}
  </div>

  <div class="card flex-1 flex flex-col min-h-0">
    {#if loading}
      <div class="text-sm text-txtsecondary">Cargando config.yaml...</div>
    {:else}
      <textarea
        class="w-full flex-1 min-h-[50vh] rounded border border-card-border bg-background p-3 font-mono text-sm leading-5"
        bind:value={content}
        onkeydown={handleEditorKeyDown}
        spellcheck="false"
        disabled={saving}
      ></textarea>
    {/if}
  </div>
</div>
