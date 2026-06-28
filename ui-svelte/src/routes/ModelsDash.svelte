<script lang="ts">
  import { link } from "svelte-spa-router";
  import { models, unloadAllModels } from "../stores/api";
  import { pendingLoads, onToggleLoad, statusDotColor } from "../stores/modelLoad";
  import { showUnlistedModels as showUnlisted } from "../stores/modelDisplay";
  import * as Card from "$lib/components/ui/card/index.js";
  import { Button } from "$lib/components/ui/button/index.js";
  import * as Switch from "$lib/components/ui/switch/index.js";
  import * as Label from "$lib/components/ui/label/index.js";
  import { Play, PowerOff, Loader2, ExternalLink, SquareStack, Eye } from "@lucide/svelte";

  let unloadingAll = $state(false);

  let visibleModels = $derived(
    $showUnlisted ? $models : $models.filter((m) => !m.unlisted)
  );

  let readyCount = $derived($models.filter((m) => m.state === "ready").length);
  let anyReady = $derived(readyCount > 0);

  async function handleUnloadAll(): Promise<void> {
    unloadingAll = true;
    try {
      await unloadAllModels();
    } catch (e) {
      console.error(e);
    } finally {
      unloadingAll = false;
    }
  }
</script>

<div class="flex h-full flex-col gap-4 overflow-y-auto p-2">
  <Card.Root class="shrink-0 gap-0 overflow-hidden py-0">
    <Card.Header class="shrink-0 gap-2 border-b px-4 py-3">
      <div class="flex items-center gap-2">
        <SquareStack class="size-5" />
        <Card.Title class="text-lg">Models</Card.Title>
        <span class="text-muted-foreground text-sm">
          ({visibleModels.length} of {$models.length})
        </span>
        <span class="text-muted-foreground text-xs uppercase tracking-wide">
          {readyCount} ready
        </span>
        <div class="ml-auto flex items-center gap-2">
          <Button
            variant="outline"
            size="sm"
            onclick={handleUnloadAll}
            disabled={!anyReady || unloadingAll}
          >
            {#if unloadingAll}
              <Loader2 class="size-3.5 animate-spin" />
            {:else}
              <PowerOff class="size-3.5" />
            {/if}
            Unload All
          </Button>
        </div>
      </div>
    </Card.Header>
    <Card.Content class="flex items-center gap-2 px-4 py-2">
      <Eye class="text-muted-foreground size-4" />
      <Label.Root for="show-unlisted-toggle" class="text-sm">
        Show unlisted models
      </Label.Root>
      <Switch.Root
        id="show-unlisted-toggle"
        checked={$showUnlisted}
        onCheckedChange={(v) => showUnlisted.set(v)}
      />
      <span class="text-muted-foreground text-xs">
        {$models.filter((m) => m.unlisted).length} unlisted
      </span>
    </Card.Content>
  </Card.Root>

  <Card.Root class="min-h-0 flex-1 gap-0 overflow-hidden py-0">
    <Card.Content class="overflow-y-auto p-0">
      {#if visibleModels.length === 0}
        <div class="text-muted-foreground px-4 py-8 text-center text-sm">
          No models available
        </div>
      {:else}
        <div class="divide-y">
          {#each visibleModels as model (model.id)}
            <div class="hover:bg-muted/50 flex items-center gap-3 px-4 py-2.5">
              <span class={`size-2.5 shrink-0 rounded-full ${statusDotColor(model)}`}></span>
              <a
                href="/models/{encodeURIComponent(model.id)}"
                use:link
                class="min-w-0 flex-1"
              >
                <div class="truncate text-sm font-medium">{model.name || model.id}</div>
                <div class="text-muted-foreground truncate text-xs">
                  {model.id}
                  {#if model.aliases && model.aliases.length > 0}
                    · {model.aliases.join(", ")}
                  {/if}
                </div>
              </a>
              <span class="text-muted-foreground text-xs uppercase tracking-wide">
                {model.state}
              </span>
              {#if model.unlisted}
                <span class="bg-muted text-muted-foreground rounded-md px-1.5 py-0.5 text-[0.625rem] font-medium uppercase">
                  unlisted
                </span>
              {/if}
              <a
                href="/upstream/{encodeURIComponent(model.id)}/"
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
                class="flex size-7 shrink-0 items-center justify-center rounded-md text-muted-foreground hover:bg-accent hover:text-accent-foreground disabled:opacity-50"
                title={model.state === "ready" ? "Unload" : $pendingLoads[model.id] ? "Cancel" : "Load"}
                aria-label={model.state === "ready" ? "Unload model" : "Load model"}
                disabled={model.state === "starting" || model.state === "stopping"}
                onclick={() => onToggleLoad(model)}
              >
                {#if $pendingLoads[model.id] && model.state === "stopped"}
                  <Loader2 class="size-4 animate-spin" />
                {:else if model.state === "ready"}
                  <PowerOff class="size-4" />
                {:else if model.state === "starting" || model.state === "stopping"}
                  <Loader2 class="size-4 animate-spin" />
                {:else}
                  <Play class="size-4" />
                {/if}
              </button>
            </div>
          {/each}
        </div>
      {/if}
    </Card.Content>
  </Card.Root>
</div>
