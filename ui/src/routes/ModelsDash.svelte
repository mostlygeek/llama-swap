<script lang="ts">
  import { onMount } from "svelte";
  import type { Snippet } from "svelte";
  import { link } from "svelte-spa-router";
  import {
    activeProfile,
    fetchPlaygroundModels,
    models,
    profiles,
    selectorModels,
    unloadAllModels,
  } from "../stores/api";
  import { statusDotColor } from "../stores/modelLoad";
  import { showUnlistedModels as showUnlisted, showCapabilityTags } from "../stores/modelDisplay";
  import { listCapabilityBadges, capabilityBadgeClass } from "../lib/capabilities";
  import type { Model } from "../lib/types";
  import ModelLoadButton from "../components/ModelLoadButton.svelte";
  import Tag from "../components/Tag.svelte";
  import * as Card from "$lib/components/ui/card/index.js";
  import { Button } from "$lib/components/ui/button/index.js";
  import * as Switch from "$lib/components/ui/switch/index.js";
  import * as Label from "$lib/components/ui/label/index.js";
  import { PowerOff, Loader2, ExternalLink, SquareStack } from "@lucide/svelte";
  import { modelServerPath } from "../lib/modelUtils";

  let unloadingAll = $state(false);

  onMount(() => {
    void fetchPlaygroundModels();
  });

  let visibleModels = $derived(
    $showUnlisted ? $models : $models.filter((m) => !m.unlisted)
  );
  let localModels = $derived(visibleModels.filter((model) => !model.peerID));
  let peerModels = $derived(visibleModels.filter((model) => model.peerID));
  let selectedProfile = $derived(
    $profiles.find((profile) => profile.id === $activeProfile)
  );
  let profileMappings = $derived(
    Object.entries(selectedProfile?.pins ?? {}).sort(([a], [b]) =>
      a.localeCompare(b, undefined, { numeric: true })
    )
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

{#snippet modelRow(model: Model)}
  <div class="hover:bg-muted/50 flex items-center gap-3 px-4 py-2.5">
    {#if !model.peerID}
      <span class={`size-2.5 shrink-0 rounded-full ${statusDotColor(model)}`}></span>
    {/if}
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
    {#if $showCapabilityTags}
      {@const badges = listCapabilityBadges(model)}
      {#if badges.length > 0}
        <div class="hidden min-w-0 flex-wrap items-center gap-1 sm:flex">
          {#each badges as badge (badge.key)}
            <Tag class={`px-1.5 text-[0.625rem] ${capabilityBadgeClass[badge.key] ?? ""}`}>{badge.label}</Tag>
          {/each}
        </div>
      {/if}
    {/if}
    <span class="text-muted-foreground text-xs uppercase tracking-wide">
      {model.state}
    </span>
    {#if model.unlisted}
      <Tag class="px-1.5 text-[0.625rem] uppercase">unlisted</Tag>
    {/if}
    {#if !model.peerID}
      <a
        href={modelServerPath(model.id)}
        target="_blank"
        rel="noopener noreferrer"
        class="text-muted-foreground hover:text-foreground"
        title="Open model server"
        aria-label="Open model server"
      >
        <ExternalLink class="size-4" />
      </a>
      <ModelLoadButton {model} />
    {/if}
  </div>
{/snippet}

{#snippet modelSection(title: string, sectionModels: Model[], headerExtra?: Snippet)}
  <Card.Root class="shrink-0 gap-0 overflow-hidden py-0">
    <Card.Header class="shrink-0 border-b px-4 py-2.5">
      <div class="flex items-center gap-2">
        <Card.Title class="text-sm">{title}</Card.Title>
        <span class="text-muted-foreground text-xs">{sectionModels.length}</span>
        {#if headerExtra}
          <div class="ml-auto flex items-center gap-2">
            {@render headerExtra()}
          </div>
        {/if}
      </div>
    </Card.Header>
    <Card.Content class="p-0">
      {#if sectionModels.length === 0}
        <div class="text-muted-foreground px-4 py-6 text-center text-sm">
          No {title.toLowerCase()} available
        </div>
      {:else}
        <div class="divide-y">
          {#each sectionModels as model (model.id)}
            {@render modelRow(model)}
          {/each}
        </div>
      {/if}
    </Card.Content>
  </Card.Root>
{/snippet}

{#snippet unlistedToggle()}
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
{/snippet}

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
  </Card.Root>

  <div class="flex min-h-0 shrink-0 flex-col gap-4">
    {#if $profiles.length > 0}
      <Card.Root class="shrink-0 gap-0 overflow-hidden py-0">
        <Card.Header class="shrink-0 border-b px-4 py-2.5">
          <div class="flex items-center gap-2">
            <Card.Title class="text-sm">Profiles</Card.Title>
            {#if selectedProfile}
              <Tag>{$activeProfile}</Tag>
              <Tag class="bg-success/15 text-success">Active</Tag>
            {/if}
            <span class="text-muted-foreground ml-auto text-xs">
              {profileMappings.length} {profileMappings.length === 1 ? "mapping" : "mappings"}
            </span>
          </div>
          {#if selectedProfile?.description}
            <p class="text-muted-foreground text-xs">{selectedProfile.description}</p>
          {/if}
        </Card.Header>
        <Card.Content class="p-0">
          {#if !selectedProfile}
            <div class="text-muted-foreground px-4 py-6 text-center text-sm">
              No active profile
            </div>
          {:else}
            <div class="divide-y">
              {#each profileMappings as [modelID, target] (modelID)}
                <div class="hover:bg-muted/50 flex items-center gap-2 px-4 py-2.5">
                  <span class="max-w-[45%] truncate text-sm font-medium">{modelID}</span>
                  <span class="text-muted-foreground text-xs" aria-hidden="true">→</span>
                  {#if target}
                    <span class="min-w-0 truncate text-sm">{target}</span>
                  {:else}
                    <Tag class="px-1.5 text-[0.625rem] uppercase">disabled</Tag>
                  {/if}
                </div>
              {/each}
            </div>
          {/if}
        </Card.Content>
      </Card.Root>
    {/if}

    {#if $selectorModels.length > 0}
      <Card.Root class="shrink-0 gap-0 overflow-hidden py-0">
        <Card.Header class="shrink-0 border-b px-4 py-2.5">
          <div class="flex items-center gap-2">
            <Card.Title class="text-sm">Selectors</Card.Title>
            <span class="text-muted-foreground ml-auto text-xs">
              {$selectorModels.length} {$selectorModels.length === 1 ? "selector" : "selectors"}
            </span>
          </div>
        </Card.Header>
        <Card.Content class="p-0">
          <div class="divide-y">
            {#each $selectorModels as selector (selector.id)}
              <div class="hover:bg-muted/50 flex items-center gap-2 px-4 py-2.5">
                <div class="min-w-0 flex-1">
                  <div class="truncate text-sm font-medium">
                    {selector.name ? `${selector.id} - ${selector.name}` : selector.id}
                  </div>
                  {#if selector.description}
                    <div class="text-muted-foreground truncate text-xs">
                      {selector.description}
                    </div>
                  {/if}
                  <div class="text-muted-foreground flex flex-wrap items-center gap-x-1 text-xs">
                    <span>targets:</span>
                    {#each selector.targets ?? [] as target, i (target)}
                      {#if i > 0}<span>,</span>{/if}
                      <a
                        href="/models/{encodeURIComponent(target)}"
                        use:link
                        class="hover:text-foreground hover:underline"
                      >{target}</a>
                    {/each}
                  </div>
                </div>
                {#if selector.strategy === "spillover" && selector.spillover}
                  <Tag>spillover {selector.spillover}</Tag>
                {/if}
                <Tag class="px-1.5 text-[0.625rem] uppercase">{selector.strategy}</Tag>
              </div>
            {/each}
          </div>
        </Card.Content>
      </Card.Root>
    {/if}

    {@render modelSection("Local models", localModels, unlistedToggle)}
    {@render modelSection("Peer models", peerModels)}
  </div>
</div>
