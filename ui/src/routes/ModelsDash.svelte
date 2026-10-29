<script lang="ts">
  import { onMount } from "svelte";
  import { link } from "svelte-spa-router";
  import {
    activeProfile,
    fetchPlaygroundModels,
    inferencePaused,
    models,
    pauseInference,
    profiles,
    resumeInference,
    selectorModels,
    unloadAllModels,
  } from "../stores/api";
  import { loadingMessage, loadingPercent, statusDotColor } from "../stores/modelLoad";
  import { showUnlistedModels as showUnlisted, showCapabilityTags, groupByClass, collapsedSections } from "../stores/modelDisplay";
  import { listCapabilityBadges, capabilityBadgeClass } from "../lib/capabilities";
  import { groupModelsByClass, modelClassLabels } from "../lib/modelClasses";
  import type { Model } from "../lib/types";
  import ModelLoadButton from "../components/ModelLoadButton.svelte";
  import Tag from "../components/Tag.svelte";
  import * as Card from "$lib/components/ui/card/index.js";
  import { Button } from "$lib/components/ui/button/index.js";
  import * as DropdownMenu from "$lib/components/ui/dropdown-menu/index.js";
  import { PowerOff, Loader2, ExternalLink, SquareStack, Pause, Play, ChevronDown, SlidersHorizontal } from "@lucide/svelte";
  import { modelServerPath } from "../lib/modelUtils";

  let unloadingAll = $state(false);
  let togglingPause = $state(false);

  onMount(() => {
    void fetchPlaygroundModels();
  });

  let visibleModels = $derived(
    $showUnlisted ? $models : $models.filter((m) => !m.unlisted)
  );
  let localModels = $derived(visibleModels.filter((model) => !model.peerID));
  // Sections in MODEL_CLASS_ORDER with only the non-empty ones rendered, so a
  // fleet without video models never sees an empty Video card.
  let localClassGroups = $derived(
    groupModelsByClass(localModels).filter((group) => group.models.length > 0)
  );
  // Unified view: one list, same locale-aware order the sections use, so
  // switching modes does not reshuffle models that stay visible.
  let sortedLocalModels = $derived(
    [...localModels].sort((a, b) => a.id.localeCompare(b.id, undefined, { numeric: true }))
  );
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

  function isSectionCollapsed(sectionId: string): boolean {
    return $collapsedSections[sectionId] === true;
  }

  // Aggregate of the row status dots: green when any model in the section is
  // ready, amber while one is starting or stopping, gray when none is active.
  // Mirrors statusDotColor so a collapsed section reads like its rows.
  function sectionStatusDotColor(sectionModels: Model[]): string {
    let transitioning = false;
    for (const model of sectionModels) {
      if (model.state === "ready") return "bg-success";
      if (model.state === "starting" || model.state === "stopping") transitioning = true;
    }
    return transitioning ? "bg-warning" : "bg-muted-foreground/40";
  }

  function toggleSection(sectionId: string): void {
    collapsedSections.update((sections) => {
      const next = { ...sections };
      if (next[sectionId]) {
        delete next[sectionId];
      } else {
        next[sectionId] = true;
      }
      return next;
    });
  }

  async function handleTogglePause(): Promise<void> {
    togglingPause = true;
    try {
      if ($inferencePaused) {
        await resumeInference();
      } else {
        await pauseInference();
      }
    } catch (e) {
      console.error(e);
    } finally {
      togglingPause = false;
    }
  }
</script>

{#snippet modelRow(model: Model)}
  {@const percent = loadingPercent(model)}
  {@const message = loadingMessage(model)}
  <div class="hover:bg-muted/50 relative flex items-center gap-3 px-3 py-2.5 sm:px-4">
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
      {#if message}
        <div class="text-muted-foreground truncate text-xs italic" title={message}>{message}</div>
      {/if}
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
      {model.state}{#if percent !== undefined}&nbsp;<span class="tabular-nums">{percent}%</span>{/if}
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
    {#if percent !== undefined}
      <!-- Loading progress; the percentage next to the state carries it for screen readers. -->
      <div aria-hidden="true" class="bg-muted absolute inset-x-0 bottom-0 h-0.5">
        <div class="bg-warning h-full transition-[width] duration-500" style:width="{percent}%"></div>
      </div>
    {/if}
  </div>
{/snippet}

{#snippet modelSection(sectionId: string, title: string, sectionModels: Model[])}
  {@const collapsed = isSectionCollapsed(sectionId)}
  {@const readyCount = sectionModels.filter((m) => m.state === "ready").length}
  <Card.Root class="shrink-0 gap-0 overflow-hidden py-0">
    <Card.Header class="shrink-0 border-b px-3 py-2.5 sm:px-4">
      <button
        type="button"
        class="flex w-full cursor-pointer items-center gap-2"
        onclick={() => toggleSection(sectionId)}
        aria-expanded={!collapsed}
      >
        <span
          class={`size-2.5 shrink-0 rounded-full ${sectionStatusDotColor(sectionModels)}`}
          role="img"
          aria-label={`${readyCount} of ${sectionModels.length} ready`}
        ></span>
        <Card.Title class="text-sm">{title}</Card.Title>
        <span class="text-muted-foreground text-xs">{sectionModels.length}</span>
        <ChevronDown
          class={`text-muted-foreground ml-auto size-4 shrink-0 transition-transform ${collapsed ? "-rotate-90" : ""}`}
          aria-hidden="true"
        />
      </button>
    </Card.Header>
    {#if !collapsed}
      <Card.Content class="p-0">
      {#if sectionModels.length === 0}
        <div class="text-muted-foreground px-3 py-6 sm:px-4 text-center text-sm">
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
    {/if}
  </Card.Root>
{/snippet}

{#snippet displayOptions()}
  <DropdownMenu.Root>
    <DropdownMenu.Trigger
      class="text-muted-foreground hover:bg-muted hover:text-foreground inline-flex size-7 items-center justify-center rounded-lg"
      title="Display options"
      aria-label="Display options"
    >
      <SlidersHorizontal class="size-4" />
    </DropdownMenu.Trigger>
    <DropdownMenu.Content align="end" class="min-w-[14rem]">
      <DropdownMenu.Label class="text-xs font-medium uppercase tracking-wider">
        Display Options
      </DropdownMenu.Label>
      <DropdownMenu.CheckboxItem
        checked={$groupByClass}
        onCheckedChange={(v) => groupByClass.set(!!v)}
        closeOnSelect={false}
      >
        <span class="flex-1">Categories</span>
      </DropdownMenu.CheckboxItem>
      <DropdownMenu.CheckboxItem
        checked={$showUnlisted}
        onCheckedChange={(v) => showUnlisted.set(!!v)}
        closeOnSelect={false}
      >
        <span class="flex-1">Show unlisted models</span>
        <span class="text-muted-foreground text-xs">
          ({$models.filter((m) => m.unlisted).length})
        </span>
      </DropdownMenu.CheckboxItem>
    </DropdownMenu.Content>
  </DropdownMenu.Root>
{/snippet}

<div class="flex min-h-full flex-col gap-3 p-0 sm:gap-4 sm:p-2">
  <Card.Root class="shrink-0 gap-0 overflow-hidden py-0">
    <Card.Header class="shrink-0 gap-2 border-b px-3 py-2.5 sm:px-4 sm:py-3">
      <div class="flex flex-col gap-2 sm:flex-row sm:items-center">
        <div class="flex min-w-0 items-center gap-2">
          <SquareStack class="size-5 shrink-0" />
          <Card.Title class="text-lg">Models</Card.Title>
          <span class="text-muted-foreground truncate text-sm">
            ({visibleModels.length} of {$models.length})
          </span>
          <span class="text-muted-foreground text-xs uppercase tracking-wide">
            {readyCount} ready
          </span>
        </div>
        <div class="flex items-center gap-2 sm:ml-auto">
          <Button
            variant="outline"
            size="sm"
            class="flex-1 sm:flex-none"
            onclick={handleTogglePause}
            disabled={togglingPause}
            title={$inferencePaused
              ? "Accept inference requests again"
              : "Unload all models and reject inference requests until resumed"}
          >
            {#if togglingPause}
              <Loader2 class="size-3.5 animate-spin" />
            {:else if $inferencePaused}
              <Play class="size-3.5" />
            {:else}
              <Pause class="size-3.5" />
            {/if}
            {$inferencePaused ? "Resume" : "Pause"}
          </Button>
          <Button
            variant="outline"
            size="sm"
            class="flex-1 sm:flex-none"
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
          {@render displayOptions()}
        </div>
      </div>
    </Card.Header>
  </Card.Root>

  <div class="flex min-h-0 shrink-0 flex-col gap-3 sm:gap-4">
    {#if $profiles.length > 0}
      <Card.Root class="shrink-0 gap-0 overflow-hidden py-0">
        <Card.Header class="shrink-0 border-b px-3 py-2.5 sm:px-4">
          <button
            type="button"
            class="flex w-full cursor-pointer flex-col gap-1"
            onclick={() => toggleSection("profiles")}
            aria-expanded={!isSectionCollapsed("profiles")}
          >
            <span class="flex items-center gap-2">
              <Card.Title class="text-sm">Profiles</Card.Title>
              {#if selectedProfile}
                <Tag>{$activeProfile}</Tag>
                <Tag class="bg-success/15 text-success">Active</Tag>
              {/if}
              <span class="text-muted-foreground ml-auto text-xs">
                {profileMappings.length} {profileMappings.length === 1 ? "mapping" : "mappings"}
              </span>
              <ChevronDown
                class={`text-muted-foreground size-4 shrink-0 transition-transform ${isSectionCollapsed("profiles") ? "-rotate-90" : ""}`}
                aria-hidden="true"
              />
            </span>
            {#if selectedProfile?.description}
              <span class="text-muted-foreground text-left text-xs">{selectedProfile.description}</span>
            {/if}
          </button>
        </Card.Header>
        {#if !isSectionCollapsed("profiles")}
        <Card.Content class="p-0">
          {#if !selectedProfile}
            <div class="text-muted-foreground px-3 py-6 sm:px-4 text-center text-sm">
              No active profile
            </div>
          {:else}
            <div class="divide-y">
              {#each profileMappings as [modelID, target] (modelID)}
                {@const targetModel = target
                  ? $models.find((m) => m.id === target) ??
                    $models.find((m) => m.aliases?.includes(target))
                  : undefined}
                <div
                  class="hover:bg-muted/50 flex items-center gap-3 px-3 py-2.5 sm:px-4 has-[>button]:py-1.5"
                >
                  {#if targetModel && !targetModel.peerID}
                    <span class={`size-2.5 shrink-0 rounded-full ${statusDotColor(targetModel)}`} role="img" aria-label={`Model ${targetModel.state}`}></span>
                  {/if}
                  <span class="max-w-[45%] truncate text-sm font-medium">{modelID}</span>
                  <span class="text-muted-foreground text-xs" aria-hidden="true">→</span>
                  {#if target}
                    {#if targetModel}
                      <a
                        href="/models/{encodeURIComponent(targetModel.id)}"
                        use:link
                        class="min-w-0 flex-1 truncate text-sm hover:text-foreground hover:underline"
                      >{target}</a>
                      {#if !targetModel.peerID}
                        <ModelLoadButton model={targetModel} />
                      {/if}
                    {:else}
                      <span class="min-w-0 truncate text-sm">{target}</span>
                    {/if}
                  {:else}
                    <Tag class="px-1.5 text-[0.625rem] uppercase">disabled</Tag>
                  {/if}
                </div>
              {/each}
            </div>
          {/if}
        </Card.Content>
        {/if}
      </Card.Root>
    {/if}

    {#if $selectorModels.length > 0}
      <Card.Root class="shrink-0 gap-0 overflow-hidden py-0">
        <Card.Header class="shrink-0 border-b px-3 py-2.5 sm:px-4">
          <button
            type="button"
            class="flex w-full cursor-pointer items-center gap-2"
            onclick={() => toggleSection("selectors")}
            aria-expanded={!isSectionCollapsed("selectors")}
          >
            <Card.Title class="text-sm">Selectors</Card.Title>
            <span class="text-muted-foreground ml-auto text-xs">
              {$selectorModels.length} {$selectorModels.length === 1 ? "selector" : "selectors"}
            </span>
            <ChevronDown
              class={`text-muted-foreground size-4 shrink-0 transition-transform ${isSectionCollapsed("selectors") ? "-rotate-90" : ""}`}
              aria-hidden="true"
            />
          </button>
        </Card.Header>
        {#if !isSectionCollapsed("selectors")}
        <Card.Content class="p-0">
          <div class="divide-y">
            {#each $selectorModels as selector (selector.id)}
              <div class="hover:bg-muted/50 flex items-center gap-3 px-3 py-2.5 sm:px-4">
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
        {/if}
      </Card.Root>
    {/if}

    {#if $groupByClass}
      {#if localClassGroups.length === 0}
        {@render modelSection("local", "Local models", localModels)}
      {:else}
        {#each localClassGroups as group (group.class)}
          {@render modelSection(
            group.class,
            modelClassLabels[group.class],
            group.models
          )}
        {/each}
      {/if}
    {:else}
      {@render modelSection("local", "Local models", sortedLocalModels)}
    {/if}
    {@render modelSection("peers", "Peer models", peerModels)}
  </div>
</div>
