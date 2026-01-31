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
  let exampleThemeCompartment = new Compartment();

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
        backgroundColor: dark ? (readOnly ? "#1a1a1a" : "#252525") : (readOnly ? "#f9fafb" : "#ffffff"),
      },
      ".cm-scroller": {
        overflow: "auto",
      },
      ".cm-content": {
        fontFamily: "monospace",
        color: dark ? "#d1d5db" : "#1f2937",
      },
      ".cm-gutters": {
        backgroundColor: dark ? (readOnly ? "#151515" : "#1f1f1f") : "#f3f4f6",
        color: dark ? "#6b7280" : "#9ca3af",
        border: "none",
      },
      ".cm-activeLineGutter": {
        backgroundColor: dark ? "#2d3748" : "#e5e7eb",
      },
      ".cm-activeLine": {
        backgroundColor: dark ? "#2d3748" : "#f3f4f6",
      },
      ".cm-selectionBackground, ::selection": {
        backgroundColor: dark ? "#2d5a7b" : "#bfdbfe",
      },
      ".cm-cursor": {
        borderLeftColor: dark ? "#14b8a6" : "#2563eb",
      },
      // YAML syntax colors
      ".cm-atom": { color: dark ? "#fbbf24" : "#d97706" }, // true/false/null
      ".cm-number": { color: dark ? "#c4b5fd" : "#7c3aed" }, // numbers
      ".cm-string": { color: dark ? "#6ee7b7" : "#059669" }, // strings
      ".cm-property": { color: dark ? "#7dd3fc" : "#2563eb" }, // keys
      ".cm-comment": { color: dark ? "#6b7280" : "#9ca3af" }, // comments
    }, { dark });
  }

  function createEditor(parent: HTMLElement, content: string, readOnly: boolean, compartment: Compartment) {
    const state = EditorState.create({
      doc: content,
      extensions: [
        basicSetup,
        yaml(),
        EditorView.lineWrapping,
        EditorView.editable.of(!readOnly),
        compartment.of(getTheme($isDarkMode, readOnly)),
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
        effects: exampleThemeCompartment.reconfigure(getTheme($isDarkMode, true))
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
        let errMsg: string;
        try {
          const errData = await res.json();
          errMsg = errData.error || JSON.stringify(errData);
        } catch {
          errMsg = await res.text();
        }
        throw new Error(`${res.status} ${res.statusText}: ${errMsg || "Failed to save config"}`);
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
      editorView = createEditor(editorContainer, currentConfig, false, themeCompartment);
    }

    return () => {
      if (editorView) {
        editorView.destroy();
        editorView = null;
      }
    };
  });

  $effect(() => {
    if (!loading && exampleContainer && !exampleView && exampleConfig) {
      exampleView = createEditor(exampleContainer, exampleConfig, true, exampleThemeCompartment);
    }

    return () => {
      if (exampleView) {
        exampleView.destroy();
        exampleView = null;
      }
    };
  });
</script>

<div class="flex flex-col h-full">
  <div class="mb-4 flex items-center justify-between">
    <h2 class="text-xl font-semibold text-gray-900 dark:text-gray-100">Configuration Editor</h2>
    <div class="flex gap-2">
      <button
        onclick={importConfig}
        class="px-4 py-2 bg-blue-600 hover:bg-blue-700 dark:bg-blue-700 dark:hover:bg-blue-800 text-white rounded disabled:opacity-50"
        disabled={loading || saving}
      >
        Import
      </button>
      <button
        onclick={exportConfig}
        class="px-4 py-2 bg-teal-600 hover:bg-teal-700 dark:bg-teal-700 dark:hover:bg-teal-800 text-white rounded disabled:opacity-50"
        disabled={loading || saving || !currentConfig}
      >
        Export
      </button>
      <button
        onclick={saveConfig}
        class="px-4 py-2 bg-gray-500 hover:bg-gray-600 dark:bg-gray-600 dark:hover:bg-gray-700 text-white rounded disabled:opacity-50"
        disabled={loading || saving || !currentConfig || !!validationError}
      >
        {saving ? "Saving..." : "Save & Reload"}
      </button>
    </div>
  </div>

  {#if validationError}
    <div class="mb-4 p-3 bg-yellow-50 dark:bg-yellow-900/30 border border-yellow-200 dark:border-yellow-700 text-yellow-900 dark:text-yellow-200 rounded">
      <strong>Validation Error:</strong> {validationError}
    </div>
  {/if}

  {#if error}
    <div class="mb-4 p-3 bg-red-50 dark:bg-red-900/30 border border-red-200 dark:border-red-700 text-red-900 dark:text-red-200 rounded">
      {error}
    </div>
  {/if}

  {#if loading}
    <div class="flex items-center justify-center h-full">
      <div class="text-gray-500 dark:text-gray-400">Loading configuration...</div>
    </div>
  {:else}
    <div
      class="flex-1 flex gap-4 min-h-0"
      class:flex-col={direction === "vertical"}
      class:flex-row={direction === "horizontal"}
    >
      <!-- Left panel: Editable config -->
      <div class="flex-1 flex flex-col min-h-0 min-w-0">
        <h3 class="text-lg font-semibold mb-2 text-gray-900 dark:text-gray-100">Current Config (Editable)</h3>
        <div
          bind:this={editorContainer}
          class="flex-1 w-full border border-gray-300 dark:border-gray-700 rounded overflow-hidden bg-white dark:bg-[#252525]"
        ></div>
      </div>

      <!-- Right panel: Example config (read-only) -->
      <div class="flex-1 flex flex-col min-h-0 min-w-0">
        <h3 class="text-lg font-semibold mb-2 text-gray-900 dark:text-gray-100">Example Config (Reference)</h3>
        <div
          bind:this={exampleContainer}
          class="flex-1 w-full border border-gray-300 dark:border-gray-700 rounded overflow-hidden bg-gray-50 dark:bg-[#1a1a1a]"
        ></div>
      </div>
    </div>
  {/if}
</div>
