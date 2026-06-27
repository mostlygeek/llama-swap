<script lang="ts">
  import { ArrowLeftRight, Eye, EyeOff, CircleArrowDown, MoreVertical } from "@lucide/svelte";
  import { models, loadModel, unloadAllModels, unloadSingleModel } from "../stores/api";
  import { isNarrow } from "../stores/theme";
  import { persistentStore } from "../stores/persistent";
  import type { Model } from "../lib/types";
  import * as Card from "$lib/components/ui/card/index.js";
  import * as DropdownMenu from "$lib/components/ui/dropdown-menu/index.js";
  import { Button } from "$lib/components/ui/button/index.js";
  import { Badge } from "$lib/components/ui/badge/index.js";

  let isUnloading = $state(false);
  let pendingLoads = $state<Record<string, boolean>>({});
  const loadControllers = new Map<string, AbortController>();

  const showUnlistedStore = persistentStore<boolean>("showUnlisted", true);
  const showIdorNameStore = persistentStore<"id" | "name">("showIdorName", "id");

  let filteredModels = $derived.by(() => {
    const filtered = $models.filter((model) => $showUnlistedStore || !model.unlisted);
    const peerModels = filtered.filter((m) => m.peerID);

    // Group peer models by peerID
    const grouped = peerModels.reduce(
      (acc, model) => {
        const peerId = model.peerID || "unknown";
        if (!acc[peerId]) acc[peerId] = [];
        acc[peerId].push(model);
        return acc;
      },
      {} as Record<string, Model[]>
    );

    return {
      regularModels: filtered.filter((m) => !m.peerID),
      peerModelsByPeerId: grouped,
    };
  });

  async function handleUnloadAllModels(): Promise<void> {
    isUnloading = true;
    try {
      await unloadAllModels();
    } catch (e) {
      console.error(e);
    } finally {
      setTimeout(() => (isUnloading = false), 1000);
    }
  }

  async function handleLoadModel(modelId: string): Promise<void> {
    if (pendingLoads[modelId]) return;
    const controller = new AbortController();
    loadControllers.set(modelId, controller);
    pendingLoads[modelId] = true;
    try {
      await loadModel(modelId, controller.signal);
    } catch (e) {
      console.error(e);
    } finally {
      loadControllers.delete(modelId);
      delete pendingLoads[modelId];
    }
  }

  function cancelLoad(modelId: string): void {
    loadControllers.get(modelId)?.abort();
  }

  function toggleIdorName(): void {
    showIdorNameStore.update((prev) => (prev === "name" ? "id" : "name"));
  }

  function toggleShowUnlisted(): void {
    showUnlistedStore.update((prev) => !prev);
  }

  function getModelDisplay(model: Model): string {
    return $showIdorNameStore === "id" ? model.id : model.name || model.id;
  }

  function statusClasses(state: string): string {
    if (state === "ready") return "border-success/30 bg-success/10 text-success";
    if (state === "starting" || state === "stopping" || state === "queued")
      return "border-warning/30 bg-warning/10 text-warning";
    return "border-border bg-muted text-muted-foreground";
  }
</script>

<Card.Root class="flex h-full flex-col gap-0 overflow-hidden py-0">
  <Card.Header class="shrink-0 gap-2 border-b px-4 py-3 [.border-b]:pb-3">
    <div class="flex items-center justify-between gap-2">
      <Card.Title class="text-lg">Models</Card.Title>

      {#if $isNarrow}
        <DropdownMenu.Root>
          <DropdownMenu.Trigger>
            {#snippet child({ props })}
              <Button {...props} variant="outline" size="icon" aria-label="Model options">
                <MoreVertical />
              </Button>
            {/snippet}
          </DropdownMenu.Trigger>
          <DropdownMenu.Content align="end">
            <DropdownMenu.Item onSelect={toggleIdorName}>
              <ArrowLeftRight />
              {$showIdorNameStore === "id" ? "Show Name" : "Show ID"}
            </DropdownMenu.Item>
            <DropdownMenu.Item onSelect={toggleShowUnlisted}>
              {#if $showUnlistedStore}<EyeOff />{:else}<Eye />{/if}
              {$showUnlistedStore ? "Hide Unlisted" : "Show Unlisted"}
            </DropdownMenu.Item>
            <DropdownMenu.Separator />
            <DropdownMenu.Item onSelect={handleUnloadAllModels} disabled={isUnloading}>
              <CircleArrowDown />
              {isUnloading ? "Unloading..." : "Unload All"}
            </DropdownMenu.Item>
          </DropdownMenu.Content>
        </DropdownMenu.Root>
      {:else}
        <div class="flex items-center gap-2">
          <Button variant="outline" size="sm" onclick={toggleIdorName}>
            <ArrowLeftRight />
            {$showIdorNameStore === "id" ? "ID" : "Name"}
          </Button>
          <Button variant="outline" size="sm" onclick={toggleShowUnlisted}>
            {#if $showUnlistedStore}<Eye />{:else}<EyeOff />{/if}
            unlisted
          </Button>
          <Button variant="outline" size="sm" onclick={handleUnloadAllModels} disabled={isUnloading}>
            <CircleArrowDown />
            {isUnloading ? "Unloading..." : "Unload All"}
          </Button>
        </div>
      {/if}
    </div>
  </Card.Header>

  <Card.Content class="flex-1 overflow-y-auto p-0">
    <table class="w-full text-sm">
      <thead class="bg-card sticky top-0 z-10">
        <tr class="text-muted-foreground border-b text-left">
          <th class="px-4 py-2 font-medium">{$showIdorNameStore === "id" ? "Model ID" : "Name"}</th>
          <th class="px-4 py-2"></th>
          <th class="px-4 py-2 font-medium">State</th>
        </tr>
      </thead>
      <tbody>
        {#each filteredModels.regularModels as model (model.id)}
          <tr class="hover:bg-muted/50 border-b transition-colors">
            <td class="px-4 py-2 {model.unlisted ? 'text-muted-foreground' : ''}">
              <a href="/upstream/{model.id}/" class="font-semibold hover:underline" target="_blank">
                {getModelDisplay(model)}
              </a>
              {#if model.description}
                <p class="text-muted-foreground"><em>{model.description}</em></p>
              {/if}
              {#if model.aliases && model.aliases.length > 0}
                <p class="text-muted-foreground text-xs">Aliases: {model.aliases.join(", ")}</p>
              {/if}
            </td>
            <td class="w-12 px-4 py-2">
              {#if model.state === "stopped" && pendingLoads[model.id]}
                <Button variant="outline" size="xs" onclick={() => cancelLoad(model.id)}>Cancel</Button>
              {:else if model.state === "stopped"}
                <Button variant="outline" size="xs" onclick={() => handleLoadModel(model.id)}>Load</Button>
              {:else}
                <Button
                  variant="outline"
                  size="xs"
                  onclick={() => unloadSingleModel(model.id)}
                  disabled={model.state !== "ready"}
                >
                  Unload
                </Button>
              {/if}
            </td>
            <td class="w-24 px-4 py-2">
              {#if model.state === "stopped" && pendingLoads[model.id]}
                <Badge variant="outline" class={statusClasses("queued")}>queued</Badge>
              {:else}
                <Badge variant="outline" class={statusClasses(model.state)}>{model.state}</Badge>
              {/if}
            </td>
          </tr>
        {/each}
      </tbody>
    </table>

    {#if Object.keys(filteredModels.peerModelsByPeerId).length > 0}
      <h3 class="px-4 pt-6 pb-2 text-base">Peer Models</h3>
      {#each Object.entries(filteredModels.peerModelsByPeerId).sort(([a], [b]) => a.localeCompare(b)) as [peerId, peerModels] (peerId)}
        <div class="mb-4">
          <table class="w-full text-sm">
            <thead class="bg-card sticky top-0 z-10">
              <tr class="text-muted-foreground border-b text-left">
                <th class="px-4 py-2 font-semibold">{peerId}</th>
              </tr>
            </thead>
            <tbody>
              {#each peerModels as model (model.id)}
                <tr class="hover:bg-muted/50 border-b transition-colors">
                  <td class="px-4 py-2 pl-8 {model.unlisted ? 'text-muted-foreground' : ''}">
                    <span>{model.id}</span>
                  </td>
                </tr>
              {/each}
            </tbody>
          </table>
        </div>
      {/each}
    {/if}
  </Card.Content>
</Card.Root>
