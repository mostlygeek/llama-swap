<script lang="ts">
  import { params } from "svelte-spa-router";
  import { models, loadModel, unloadSingleModel } from "../stores/api";
  import type { Model } from "../lib/types";
  import * as Card from "$lib/components/ui/card/index.js";
  import * as Tabs from "$lib/components/ui/tabs/index.js";
  import { Play, PowerOff, Loader2, ExternalLink } from "@lucide/svelte";
  import ModelActivityTab from "../components/model/ModelActivityTab.svelte";
  import ModelLogsTab from "../components/model/ModelLogsTab.svelte";
  import ModelDetailsTab from "../components/model/ModelDetailsTab.svelte";

  let modelId = $derived($params?.id ?? "");

  let model = $derived<Model | undefined>($models.find((m) => m.id === modelId));

  // Load / unload orchestration (ported from AppSidebar.svelte)
  let pendingLoads = $state<Record<string, boolean>>({});
  const loadControllers = new Map<string, AbortController>();

  async function handleLoadModel(id: string): Promise<void> {
    if (pendingLoads[id]) return;
    const controller = new AbortController();
    loadControllers.set(id, controller);
    pendingLoads[id] = true;
    try {
      await loadModel(id, controller.signal);
    } catch (e) {
      console.error(e);
    } finally {
      loadControllers.delete(id);
      delete pendingLoads[id];
    }
  }

  function cancelLoad(id: string): void {
    loadControllers.get(id)?.abort();
  }

  function onToggleLoad(e: MouseEvent, m: Model): void {
    e.preventDefault();
    e.stopPropagation();
    if (m.state === "stopped" && pendingLoads[m.id]) {
      cancelLoad(m.id);
    } else if (m.state === "stopped") {
      handleLoadModel(m.id);
    } else if (m.state === "ready") {
      unloadSingleModel(m.id);
    }
  }

  function statusDotColor(m: Model | undefined): string {
    if (!m) return "bg-muted-foreground/40";
    if (m.state === "ready") return "bg-success";
    if (m.state === "starting" || m.state === "stopping") return "bg-warning";
    return "bg-muted-foreground/40";
  }
</script>

<div class="flex h-full flex-col gap-4 overflow-y-auto p-2">
  {#if !model}
    <Card.Root class="shrink-0 p-6">
      <p class="text-muted-foreground">Model “{modelId}” not found.</p>
      <a href="/" class="text-primary hover:underline">Back to Playground</a>
    </Card.Root>
  {:else}
    <Card.Root class="shrink-0 gap-0 overflow-hidden py-0">
      <Card.Header class="shrink-0 gap-2 border-b px-4 py-3">
        <div class="flex items-center gap-2">
          <span class={`size-2.5 shrink-0 rounded-full ${statusDotColor(model)}`}></span>
          <Card.Title class="text-lg">{model.name || model.id}</Card.Title>
          <span class="text-muted-foreground text-sm">({model.id})</span>
          <span class="text-muted-foreground text-xs uppercase tracking-wide">{model.state}</span>
          <div class="ml-auto flex items-center gap-2">
            <a
              href={`/upstream/${encodeURIComponent(modelId)}/`}
              target="_blank"
              rel="noopener noreferrer"
              class="text-muted-foreground hover:text-foreground"
              title="Open model server"
              aria-label="Open model server"
            >
              <ExternalLink class="size-4" />
            </a>
            <button
              type="button"
              class="flex size-5 shrink-0 items-center justify-center rounded-sm text-muted-foreground hover:bg-accent hover:text-accent-foreground disabled:opacity-50"
              title={model.state === "ready" ? "Unload" : pendingLoads[model.id] ? "Cancel" : "Load"}
              aria-label={model.state === "ready" ? "Unload model" : "Load model"}
              disabled={model.state === "starting" || model.state === "stopping"}
              onclick={(e) => onToggleLoad(e, model)}
            >
              {#if pendingLoads[model.id] && model.state === "stopped"}
                <Loader2 class="size-3.5 animate-spin" />
              {:else if model.state === "ready"}
                <PowerOff class="size-3.5" />
              {:else if model.state === "starting" || model.state === "stopping"}
                <Loader2 class="size-3.5 animate-spin" />
              {:else}
                <Play class="size-3.5" />
              {/if}
            </button>
          </div>
        </div>
        {#if model.description}
          <p class="text-muted-foreground text-sm"><em>{model.description}</em></p>
        {/if}
        {#if model.aliases && model.aliases.length > 0}
          <p class="text-muted-foreground text-xs">Aliases: {model.aliases.join(", ")}</p>
        {/if}
      </Card.Header>
    </Card.Root>

    <Tabs.Root value="activity" class="min-h-0 flex-1">
      <Tabs.List>
        <Tabs.Trigger value="activity" class="data-active:bg-primary/15 data-active:text-primary border border-b-2 data-active:border-primary rounded-none shadow-none">Activity</Tabs.Trigger>
        <Tabs.Trigger value="logs" class="data-active:bg-primary/15 data-active:text-primary border border-b-2 data-active:border-primary rounded-none shadow-none">Logs</Tabs.Trigger>
        <Tabs.Trigger value="details" class="data-active:bg-primary/15 data-active:text-primary border border-b-2 data-active:border-primary rounded-none shadow-none">Details</Tabs.Trigger>
      </Tabs.List>

      <!-- Activity -->
      <Tabs.Content value="activity">
        <ModelActivityTab modelId={modelId} />
      </Tabs.Content>

      <!-- Logs -->
      <Tabs.Content value="logs">
        <ModelLogsTab modelId={modelId} />
      </Tabs.Content>

      <!-- Details -->
      <Tabs.Content value="details">
        <ModelDetailsTab model={model} />
      </Tabs.Content>
    </Tabs.Root>
  {/if}
</div>
