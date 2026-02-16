<script lang="ts">
  import { onMount } from "svelte";
  import { basicSetup } from "codemirror";
  import { yaml } from "@codemirror/lang-yaml";
  import { Compartment, EditorState } from "@codemirror/state";
  import { EditorView, keymap } from "@codemirror/view";
  import { getConfigEditorState, saveConfigEditorContent } from "../stores/api";
  import type { ConfigEditorState } from "../lib/types";
  import { collapseHomePath } from "../lib/pathDisplay";

  let loading = $state(true);
  let saving = $state(false);
  let error = $state<string | null>(null);
  let notice = $state<string | null>(null);
  let configPath = $state("");
  let updatedAt = $state("");
  let content = $state("");
  let originalContent = $state("");
  let refreshController: AbortController | null = null;
  let editorHost: HTMLDivElement | null = null;
  let editorView: EditorView | null = null;
  let syncingFromEditor = false;

  const editableCompartment = new Compartment();

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

  function syncEditorContent(nextContent: string): void {
    if (!editorView || syncingFromEditor) return;
    const currentContent = editorView.state.doc.toString();
    if (currentContent === nextContent) return;
    editorView.dispatch({
      changes: {
        from: 0,
        to: currentContent.length,
        insert: nextContent,
      },
    });
  }

  onMount(() => {
    void refresh();
    return () => {
      refreshController?.abort();
      editorView?.destroy();
      editorView = null;
    };
  });

  $effect(() => {
    if (!editorHost || editorView) return;

    editorView = new EditorView({
      parent: editorHost,
      state: EditorState.create({
        doc: content,
        extensions: [
          basicSetup,
          yaml(),
          EditorView.lineWrapping,
          editableCompartment.of(EditorView.editable.of(!(saving || loading))),
          keymap.of([
            {
              key: "Mod-s",
              run: () => {
                void save();
                return true;
              },
            },
          ]),
          EditorView.updateListener.of((update) => {
            if (!update.docChanged) return;
            syncingFromEditor = true;
            content = update.state.doc.toString();
            syncingFromEditor = false;
          }),
          EditorView.theme({
            "&": {
              height: "100%",
              fontSize: "13px",
              fontFamily:
                '"JetBrains Mono","Fira Code","Cascadia Code",Menlo,Monaco,Consolas,"Liberation Mono","Courier New",monospace',
              backgroundColor: "transparent",
            },
            "&.cm-focused": {
              outline: "none",
            },
            ".cm-scroller": {
              overflow: "auto",
              lineHeight: "1.5",
            },
            ".cm-content": {
              padding: "12px 0",
            },
            ".cm-line": {
              padding: "0 12px",
            },
            ".cm-gutters": {
              backgroundColor: "rgba(15, 23, 42, 0.35)",
              borderRight: "1px solid rgba(148, 163, 184, 0.2)",
            },
            ".cm-activeLine": {
              backgroundColor: "rgba(56, 189, 248, 0.08)",
            },
            ".cm-activeLineGutter": {
              backgroundColor: "rgba(56, 189, 248, 0.16)",
            },
          }),
        ],
      }),
    });
  });

  $effect(() => {
    syncEditorContent(content);
  });

  $effect(() => {
    if (!editorView) return;
    editorView.dispatch({
      effects: editableCompartment.reconfigure(EditorView.editable.of(!(saving || loading))),
    });
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
      File:
      <span class="font-mono" title={configPath || ""}>{collapseHomePath(configPath || "unknown")}</span>
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

  <div class="flex-1 min-h-0">
    <div class="card flex flex-col min-h-0 h-full">
      <div class="relative w-full flex-1 min-h-[50vh] rounded border border-card-border bg-background overflow-hidden">
        <div bind:this={editorHost} class="h-full w-full"></div>
        {#if loading}
          <div class="absolute inset-0 grid place-items-center bg-background/80 text-sm text-txtsecondary">
            Cargando config.yaml...
          </div>
        {/if}
      </div>
    </div>
  </div>
</div>
