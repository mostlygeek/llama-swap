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

  <div class="flex-1 min-h-0 grid grid-cols-1 xl:grid-cols-[minmax(0,1fr)_22rem] gap-2">
    <div class="card flex flex-col min-h-0">
      <div class="relative w-full flex-1 min-h-[50vh] rounded border border-card-border bg-background overflow-hidden">
        <div bind:this={editorHost} class="h-full w-full"></div>
        {#if loading}
          <div class="absolute inset-0 grid place-items-center bg-background/80 text-sm text-txtsecondary">
            Cargando config.yaml...
          </div>
        {/if}
      </div>
    </div>

    <aside class="card shrink-0 overflow-y-auto">
      <h3 class="pb-1">Help</h3>
      <p class="text-sm text-txtsecondary">
        Esta configuración usa como backend de recetas:
      </p>
      <p class="text-sm break-all">
        <a class="underline" href="https://github.com/eugr/spark-vllm-docker" target="_blank" rel="noreferrer">
          eugr/spark-vllm-docker
        </a>
      </p>

      <p class="text-sm text-txtsecondary mt-3">
        Integración de benchmark (Benchy):
      </p>
      <p class="text-sm break-all">
        <a class="underline" href="https://github.com/christopherowen/llama-benchy" target="_blank" rel="noreferrer">
          christopherowen/llama-benchy
        </a>
      </p>

      <p class="text-xs text-txtsecondary mt-3">
        Tip: puedes sobreescribir rutas con variables de entorno como
        <code class="font-mono">LLAMA_SWAP_RECIPES_BACKEND_DIR</code> y
        <code class="font-mono">LLAMA_SWAP_LOCAL_RECIPES_DIR</code>.
      </p>

      <p class="text-sm text-txtsecondary mt-4">
        Troubleshooting (Intelligence / SWE-bench):
      </p>
      <p class="text-xs text-txtsecondary mt-1">
        Si aparece <code class="font-mono">PermissionError</code> con locks en
        <code class="font-mono">~/.cache/huggingface/datasets</code>, corrige permisos del cache existente:
      </p>
      <pre class="mt-1 text-xs font-mono bg-card/60 border border-card-border rounded p-2 overflow-x-auto"><code>sudo chown -R $USER:$USER ~/.cache/huggingface/datasets</code></pre>
      <p class="text-xs text-txtsecondary mt-2">
        Prueba rápida de carga para validar que el lock no falla:
      </p>
      <pre class="mt-1 text-xs font-mono bg-card/60 border border-card-border rounded p-2 overflow-x-auto"><code>uvx --from "llama-benchy[intelligence] @ git+https://github.com/christopherowen/llama-benchy.git@intelligence" \
python -c "from datasets import load_dataset; ds=load_dataset('SWE-bench/SWE-bench_Verified', split='test[:1]'); print('ok', len(ds))"</code></pre>
    </aside>
  </div>
</div>
