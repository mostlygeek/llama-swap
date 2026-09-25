<script lang="ts">
  import { params } from "svelte-spa-router";
  import { models } from "../stores/api";
  import { statusDotColor } from "../stores/modelLoad";
  import type { Model } from "../lib/types";
  import ModelLoadButton from "../components/ModelLoadButton.svelte";
  import CopyableId from "../components/CopyableId.svelte";
  import * as Card from "$lib/components/ui/card/index.js";
  import { Tabs, TabsList, TabsTrigger, TabsContent } from "$lib/components/ui/tabs/index.js";
  import { ExternalLink } from "@lucide/svelte";
  import ModelActivityTab from "../components/model/ModelActivityTab.svelte";
  import ModelLogsTab from "../components/model/ModelLogsTab.svelte";
  import ModelDetailsTab from "../components/model/ModelDetailsTab.svelte";
  import { modelServerPath } from "../lib/modelUtils";

  let modelId = $derived($params?.id ?? "");

  // Resolve the route param to a model record by ID, falling back to an
  // alias match so links to alias targets (e.g. selector targets) resolve.
  let model = $derived<Model | undefined>(
    $models.find((m) => m.id === modelId) ??
      $models.find((m) => m.aliases?.includes(modelId)),
  );
  let resolvedId = $derived(model?.id ?? modelId);
  // Only show a separate name when it differs from the ID, so the ID isn't
  // displayed twice.
  let hasName = $derived(!!model?.name && model.name !== model.id);
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
        <div class="flex items-start gap-2">
          <span class={`mt-2 size-2.5 shrink-0 rounded-full ${statusDotColor(model)}`}></span>
          <div class="flex min-w-0 flex-1 flex-col gap-1">
            {#if hasName}
              <Card.Title class="text-lg break-words">{model.name}</Card.Title>
            {:else}
              <Card.Title class="-ml-1 text-lg">
                <CopyableId value={model.id} />
              </Card.Title>
            {/if}
            <div class="text-muted-foreground flex flex-wrap items-center gap-x-2 gap-y-1 text-sm">
              {#if hasName}
                <CopyableId value={model.id} class="-ml-1 font-mono text-xs" />
              {/if}
              <span class="text-xs uppercase tracking-wide">{model.state}</span>
            </div>
          </div>
          <div class="flex shrink-0 items-center gap-2">
            {#if !model.peerID}
              <a
                href={modelServerPath(resolvedId)}
                target="_blank"
                rel="noopener noreferrer"
                class="text-muted-foreground hover:text-foreground"
                title="Open model server"
                aria-label="Open model server"
              >
                <ExternalLink class="size-4" />
              </a>
              <ModelLoadButton {model} size="sm" />
            {/if}
          </div>
        </div>
        {#if model.description}
          <p class="text-muted-foreground text-sm"><em>{model.description}</em></p>
        {/if}
        {#if model.aliases && model.aliases.length > 0}
          <div class="text-muted-foreground flex flex-wrap items-center gap-x-1 gap-y-1 text-xs">
            <span>Aliases:</span>
            {#each model.aliases as alias (alias)}
              <CopyableId value={alias} class="font-mono" />
            {/each}
          </div>
        {/if}
      </Card.Header>
    </Card.Root>

    <Tabs value="activity" class="min-h-0 flex-1">
      <TabsList variant="line">
        <TabsTrigger value="activity">Activity</TabsTrigger>
        <TabsTrigger value="logs">Logs</TabsTrigger>
        <TabsTrigger value="details">Details</TabsTrigger>
      </TabsList>

      <!-- Activity -->
      <TabsContent value="activity">
        <ModelActivityTab modelId={resolvedId} />
      </TabsContent>

      <!-- Logs -->
      <TabsContent value="logs" class="min-h-0 flex-1">
        <ModelLogsTab modelId={resolvedId} />
      </TabsContent>

      <!-- Details -->
      <TabsContent value="details">
        <ModelDetailsTab model={model} />
      </TabsContent>
    </Tabs>
  {/if}
</div>
