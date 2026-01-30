<script lang="ts">
  import { onMount } from "svelte";
  import { isNarrow, isDarkMode } from "../stores/theme";
  import { EditorView, basicSetup } from "codemirror";
  import { yaml } from "@codemirror/lang-yaml";
  import { EditorState, Compartment } from "@codemirror/state";
  import * as jsyaml from "js-yaml";

  let currentConfig = $state("");
  let exampleConfig = $state("");
  let loading = $state(true);
  let error = $state("");
  let validationError = $state("");
  let saving = $state(false);
  let direction = $derived<"horizontal" | "vertical">($isNarrow ? "vertical" : "horizontal");
  
  let editorContainer: HTMLDivElement;
  let exampleContainer: HTMLDivElement;
  let editorView: EditorView | null = null;
  let exampleView: EditorView | null = null;
  let themeCompartment = new Compartment();

  function validateYAML(text: string): string | null {
    try {
      jsyaml.load(text);
      return null;
    } catch (e) {
      return e instanceof Error ? e.message : "Invalid YAML";
    }
  }

  function getTheme(dark: boolean, readOnly: boolean) {
    return EditorView.theme({
      "&": { 
        height: "100%",
        backgroundColor: dark ? (readOnly ? "#1a1a1a" : "#1f1f1f") : (readOnly ? "#f9fafb" : "#ffffff"),
      },
      ".cm-scroller": { 
        overflow: "auto",
      },
      ".cm-content": { 
        fontFamily: "monospace",
        color: dark ? "#e0e0e0" : "#1f2937",
      },
      ".cm-gutters": {
        backgroundColor: dark ? "#2a2a2a" : "#f3f4f6",
        color: dark ? "#6b7280" : "#9ca3af",
        border: "none",
      },
      ".cm-activeLineGutter": {
        backgroundColor: dark ? "#374151" : "#e5e7eb",
      },
      ".cm-activeLine": {
        backgroundColor: dark ? "#374151" : "#f3f4f6",
      },
      ".cm-selectionBackground, ::selection": {
        backgroundColor: dark ? "#3b82f6" : "#bfdbfe",
      },
      ".cm-cursor": {
        borderLeftColor: dark ? "#60a5fa" : "#2563eb",
      },
      // YAML syntax colors
      ".cm-atom": { color: dark ? "#fbbf24" : "#d97706" }, // true/false/null
      ".cm-number": { color: dark ? "#a78bfa" : "#7c3aed" }, // numbers
      ".cm-string": { color: dark ? "#34d399" : "#059669" }, // strings
      ".cm-property": { color: dark ? "#60a5fa" : "#2563eb" }, // keys
      ".cm-comment": { color: dark ? "#6b7280" : "#9ca3af" }, // comments
    }, { dark });
  }

  function createEditor(parent: HTMLElement, content: string, readOnly: boolean) {
    const state = EditorState.create({
      doc: content,
      extensions: [
        basicSetup,
        yaml(),
        EditorView.lineWrapping,
        EditorView.editable.of(!readOnly),
        themeCompartment.of(getTheme($isDarkMode, readOnly)),
        EditorView.updateListener.of((update) => {
          if (!readOnly && update.docChanged) {
            currentConfig = update.state.doc.toString();
            const err = validateYAML(currentConfig);
            validationError = err || "";
          }
        }),
      ],
    });

    return new EditorView({
      state,
      parent,
    });
  }

  // Update theme when dark mode changes
  $effect(() => {
    if (editorView) {
      editorView.dispatch({
        effects: themeCompartment.reconfigure(getTheme($isDarkMode, false))
      });
    }
    if (exampleView) {
      exampleView.dispatch({
        effects: themeCompartment.reconfigure(getTheme($isDarkMode, true))
      });
    }
  });

  async function loadConfigs() {
    loading = true;
    error = "";
    validationError = "";
    try {
      const [currentRes, exampleRes] = await Promise.all([
        fetch("/api/config/current"),
        fetch("/api/config/example"),
      ]);

      if (!currentRes.ok) {
        throw new Error(`Failed to load current config: ${currentRes.statusText}`);
      }
      if (!exampleRes.ok) {
        throw new Error(`Failed to load example config: ${exampleRes.statusText}`);
      }

      currentConfig = await currentRes.text();
      exampleConfig = await exampleRes.text();
      
      // Validate on load
      const err = validateYAML(currentConfig);
      validationError = err || "";
    } catch (e) {
      error = e instanceof Error ? e.message : "Failed to load configs";
    } finally {
      loading = false;
    }
  }

  async function saveConfig() {
    // Validate before saving
    const validationErr = validateYAML(currentConfig);
    if (validationErr) {
      alert(`Cannot save: ${validationErr}`);
      return;
    }

    saving = true;
    error = "";
    try {
      const res = await fetch("/api/config", {
        method: "POST",
        headers: { "Content-Type": "text/yaml" },
        body: currentConfig,
      });

      if (!res.ok) {
        const errData = await res.json();
        throw new Error(errData.error || "Failed to save config");
      }

      alert("Config saved successfully! Application is reloading...");
      // Reload after a delay to see the changes
      setTimeout(() => window.location.reload(), 2000);
    } catch (e) {
      error = e instanceof Error ? e.message : "Failed to save config";
      alert(`Error: ${error}`);
    } finally {
      saving = false;
    }
  }

  function exportConfig() {
    const blob = new Blob([currentConfig], { type: "text/yaml" });
    const url = URL.createObjectURL(blob);
    const a = document.createElement("a");
    a.href = url;
    a.download = "config.yaml";
    document.body.appendChild(a);
    a.click();
    document.body.removeChild(a);
    URL.revokeObjectURL(url);
  }

  function importConfig() {
    const input = document.createElement("input");
    input.type = "file";
    input.accept = ".yaml,.yml";
    input.onchange = async (e) => {
      const file = (e.target as HTMLInputElement).files?.[0];
      if (file) {
        try {
          const text = await file.text();
          const err = validateYAML(text);
          if (err) {
            alert(`Invalid YAML file: ${err}`);
            return;
          }
          currentConfig = text;
          if (editorView) {
            editorView.dispatch({
              changes: { from: 0, to: editorView.state.doc.length, insert: text }
            });
          }
        } catch (e) {
          error = e instanceof Error ? e.message : "Failed to read file";
          alert(`Error: ${error}`);
        }
      }
    };
    input.click();
  }

  onMount(() => {
    loadConfigs();
  });

  $effect(() => {
    if (!loading && editorContainer && !editorView && currentConfig) {
      editorView = createEditor(editorContainer, currentConfig, false);
    }
  });

  $effect(() => {
    if (!loading && exampleContainer && !exampleView && exampleConfig) {
      exampleView = createEditor(exampleContainer, exampleConfig, true);
    }
  });
</script>

<div class="flex flex-col h-full">
  <div class="mb-4 flex items-center justify-between">
    <h2 class="text-xl font-semibold">Configuration Editor</h2>
    <div class="flex gap-2">
      <button
        onclick={importConfig}
        class="px-4 py-2 bg-blue-500 hover:bg-blue-600 text-white rounded disabled:opacity-50"
        disabled={loading || saving}
      >
        Import
      </button>
      <button
        onclick={exportConfig}
        class="px-4 py-2 bg-green-500 hover:bg-green-600 text-white rounded disabled:opacity-50"
        disabled={loading || saving || !currentConfig}
      >
        Export
      </button>
      <button
        onclick={saveConfig}
        class="px-4 py-2 bg-orange-500 hover:bg-orange-600 text-white rounded disabled:opacity-50"
        disabled={loading || saving || !currentConfig || !!validationError}
      >
        {saving ? "Saving..." : "Save & Reload"}
      </button>
    </div>
  </div>

  {#if validationError}
    <div class="mb-4 p-3 bg-yellow-100 dark:bg-yellow-900 text-yellow-800 dark:text-yellow-200 rounded">
      <strong>Validation Error:</strong> {validationError}
    </div>
  {/if}

  {#if error}
    <div class="mb-4 p-3 bg-red-100 dark:bg-red-900 text-red-800 dark:text-red-200 rounded">
      {error}
    </div>
  {/if}

  {#if loading}
    <div class="flex items-center justify-center h-full">
      <div class="text-gray-500">Loading configuration...</div>
    </div>
  {:else}
    <div
      class="flex-1 flex gap-4 min-h-0"
      class:flex-col={direction === "vertical"}
      class:flex-row={direction === "horizontal"}
    >
      <!-- Left panel: Editable config -->
      <div class="flex-1 flex flex-col min-h-0 min-w-0">
        <h3 class="text-lg font-semibold mb-2">Current Config (Editable)</h3>
        <div 
          bind:this={editorContainer}
          class="flex-1 w-full border border-gray-300 dark:border-gray-600 rounded overflow-hidden bg-white dark:bg-gray-800"
        ></div>
      </div>

      <!-- Right panel: Example config (read-only) -->
      <div class="flex-1 flex flex-col min-h-0 min-w-0">
        <h3 class="text-lg font-semibold mb-2">Example Config (Reference)</h3>
        <div 
          bind:this={exampleContainer}
          class="flex-1 w-full border border-gray-300 dark:border-gray-600 rounded overflow-hidden bg-gray-50 dark:bg-gray-900"
        ></div>
      </div>
    </div>
  {/if}
</div>
