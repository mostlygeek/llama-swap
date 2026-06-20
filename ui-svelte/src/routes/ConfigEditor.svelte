<script lang="ts">
  import { getConfig, putConfig } from "../lib/mantleApi";

  let yaml = $state("");
  let loading = $state(true);
  let saving = $state(false);
  let message = $state("");
  let messageType = $state<"success" | "error" | "">("");

  $effect(() => {
    loadConfig();
  });

  async function loadConfig() {
    loading = true;
    const data = await getConfig();
    if (data !== null) {
      yaml = data;
    } else {
      message = "Failed to load config";
      messageType = "error";
    }
    loading = false;
  }

  async function saveConfig() {
    saving = true;
    message = "";
    const ok = await putConfig(yaml);
    if (ok) {
      message = "Config saved and hot-reloaded";
      messageType = "success";
    } else {
      message = "Failed to save config (check YAML syntax)";
      messageType = "error";
    }
    saving = false;
  }

  function handleKeydown(e: KeyboardEvent) {
    if ((e.ctrlKey || e.metaKey) && e.key === "s") {
      e.preventDefault();
      saveConfig();
    }
  }
</script>

<div class="card h-full flex flex-col p-4">
  <div class="flex items-center justify-between mb-4">
    <h2>Config Editor</h2>
    <div class="flex items-center gap-2">
      {#if message}
        <span class="text-sm" class:text-green-500={messageType === "success"} class:text-red-500={messageType === "error"}>
          {message}
        </span>
      {/if}
      <button class="btn px-4 py-1.5" onclick={saveConfig} disabled={saving || loading}>
        {saving ? "Saving..." : "Save & Reload"}
      </button>
    </div>
  </div>

  {#if loading}
    <div class="flex-1 flex items-center justify-center text-txtsecondary">
      Loading config...
    </div>
  {:else}
    <textarea
      class="flex-1 w-full font-mono text-sm p-4 rounded border border-border bg-surface resize-none focus:outline-none focus:ring-1 focus:ring-blue-500"
      bind:value={yaml}
      onkeydown={handleKeydown}
      spellcheck="false"
    ></textarea>
    <p class="text-xs text-txtsecondary mt-2">
      Ctrl+S to save. Invalid YAML will be rejected before the file is written.
    </p>
  {/if}
</div>
