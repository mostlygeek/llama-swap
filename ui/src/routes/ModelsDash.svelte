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
    fetchPeers,
    refreshPeer,
    setActiveProfile,
    pendingProfileSwitch,
  } from "../stores/api";
  import { loadingMessage, loadingPercent, statusDotColor } from "../stores/modelLoad";
  import {
    showUnlistedModels as showUnlisted,
    showCapabilityTags,
    groupByClass,
    collapsedSections,
    modelsDashTab,
    type ModelsDashTab,
  } from "../stores/modelDisplay";
  import { listCapabilityBadges, capabilityBadgeClass } from "../lib/capabilities";
  import { groupModelsByClass, modelClassLabels, sectionStatusDotColor } from "../lib/modelClasses";
  import type { Model, PeerStatus, Profile } from "../lib/types";
  import ModelLoadButton from "../components/ModelLoadButton.svelte";
  import Tag from "../components/Tag.svelte";
  import * as Card from "$lib/components/ui/card/index.js";
  import { Button } from "$lib/components/ui/button/index.js";
  import * as DropdownMenu from "$lib/components/ui/dropdown-menu/index.js";
  import { Tabs, TabsList, TabsTrigger, TabsContent } from "$lib/components/ui/tabs/index.js";
  import { PowerOff, Loader2, ExternalLink, Boxes, Pause, Play, ChevronDown, ChevronRight, SlidersHorizontal, RefreshCw } from "@lucide/svelte";
  import { modelServerPath, groupModelsByPeer, peerLocalModelID } from "../lib/modelUtils";

  let unloadingAll = $state(false);
  let togglingPause = $state(false);

  // Catalog status per peer, from /api/peers. Only peers with model rules
  // have a catalog, and only those can be refreshed.
  let peerStatus = $state<Record<string, PeerStatus>>({});
  let refreshingPeers = $state<Record<string, boolean>>({});
  let peerRefreshErrors = $state<Record<string, string>>({});

  // A fresh status replaces any manual refresh error: its lastError is the
  // current word on the catalog, failure or recovery.
  async function loadPeerStatus(): Promise<void> {
    try {
      const peers = await fetchPeers();
      peerStatus = Object.fromEntries(peers.map((p) => [p.id, p]));
      peerRefreshErrors = {};
    } catch (e) {
      console.error(e);
    }
  }

  async function handleRefreshPeer(peerID: string): Promise<void> {
    refreshingPeers = { ...refreshingPeers, [peerID]: true };
    const { [peerID]: _, ...otherErrors } = peerRefreshErrors;
    peerRefreshErrors = otherErrors;
    try {
      const status = await refreshPeer(peerID);
      peerStatus = { ...peerStatus, [peerID]: status };
    } catch (e) {
      await loadPeerStatus();
      peerRefreshErrors = { ...peerRefreshErrors, [peerID]: e instanceof Error ? e.message : String(e) };
    } finally {
      const { [peerID]: _, ...rest } = refreshingPeers;
      refreshingPeers = rest;
    }
  }

  function peerRefreshTitle(peerID: string): string {
    const catalog = peerStatus[peerID]?.catalog;
    if (!catalog) return "No model rules; this peer's models are listed in the config";
    const parts = ["Refresh models from this peer"];
    if (catalog.fetchedAt) parts.push(`Last fetched ${new Date(catalog.fetchedAt).toLocaleString()}`);
    if (catalog.lastError) parts.push(`Last error: ${catalog.lastError}`);
    return parts.join("\n");
  }

  // Scheduled catalog fetches run in the background and only announce model
  // list changes, so poll the status to show their errors and recoveries.
  const PEER_STATUS_POLL_MS = 10_000;

  onMount(() => {
    void fetchPlaygroundModels();
    void loadPeerStatus();
    const timer = setInterval(() => {
      if (document.visibilityState === "visible") void loadPeerStatus();
    }, PEER_STATUS_POLL_MS);
    return () => clearInterval(timer);
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
  // One section per configured peer from /api/peers, so a peer without
  // models (a failed first fetch, an empty catalog) keeps its error and
  // refresh button. Peers only known from the model list are kept too.
  let peerGroups = $derived.by(() => {
    const groups = groupModelsByPeer(peerModels);
    const known = new Set(groups.map((g) => g.peerID));
    for (const peerID of Object.keys(peerStatus)) {
      if (!known.has(peerID)) groups.push({ peerID, models: [] });
    }
    return groups.sort((a, b) => a.peerID.localeCompare(b.peerID, undefined, { numeric: true }));
  });

  // Tabs with nothing to show are hidden; Local stays as the empty state when
  // every tab is empty. A stored tab that is hidden (no profiles configured
  // any more, peers not loaded yet) falls back to the first tab without
  // overwriting the stored choice, so it comes back once it has content again.
  let tabs = $derived.by(() => {
    const list: { id: ModelsDashTab; label: string; count: number }[] = [];
    if (localModels.length > 0) list.push({ id: "local", label: "Local", count: localModels.length });
    if (peerGroups.length > 0) list.push({ id: "peers", label: "Peers", count: peerModels.length });
    if ($selectorModels.length > 0) list.push({ id: "selectors", label: "Selectors", count: $selectorModels.length });
    if ($profiles.length > 0) list.push({ id: "profiles", label: "Profiles", count: $profiles.length });
    if (list.length === 0) list.push({ id: "local", label: "Local", count: 0 });
    return list;
  });
  let currentTab = $derived<ModelsDashTab>(
    tabs.some((tab) => tab.id === $modelsDashTab) ? $modelsDashTab : tabs[0].id
  );

  function profileMappings(profile: Profile): [string, string][] {
    return Object.entries(profile.pins ?? {}).sort(([a], [b]) =>
      a.localeCompare(b, undefined, { numeric: true })
    );
  }

  function findModel(id: string): Model | undefined {
    return $models.find((m) => m.id === id) ?? $models.find((m) => m.aliases?.includes(id));
  }

  let profileError = $state("");

  async function handleSetProfile(id: string | null): Promise<void> {
    profileError = "";
    try {
      await setActiveProfile(id);
    } catch (e) {
      profileError = e instanceof Error ? e.message : String(e);
    }
  }

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

{#snippet modelRow(model: Model, indented = false)}
  {@const percent = loadingPercent(model)}
  {@const message = loadingMessage(model)}
  <div class={`hover:bg-muted/50 relative flex items-center gap-3 py-2.5 pr-3 sm:pr-4 ${indented ? "pl-9 sm:pl-10" : "pl-3 sm:pl-4"}`}>
    {#if !model.peerID}
      <span class={`size-2.5 shrink-0 rounded-full ${statusDotColor(model)}`}></span>
    {/if}
    <a
      href="/models/{encodeURIComponent(model.id)}"
      use:link
      class="min-w-0 flex-1"
    >
      <div class="truncate text-sm font-medium">{model.name || (indented ? peerLocalModelID(model) : model.id)}</div>
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

{#snippet peerSection()}
  <Card.Root class="shrink-0 gap-0 overflow-hidden py-0">
    <Card.Content class="p-0">
    {#if peerGroups.length === 0}
      <div class="text-muted-foreground px-3 py-6 sm:px-4 text-center text-sm">
        No peer models available
      </div>
    {:else}
      <div class="divide-y">
        {#each peerGroups as group (group.peerID)}
          {@const key = `peer:${group.peerID}`}
          {@const peerCollapsed = isSectionCollapsed(key)}
          {@const refreshing = refreshingPeers[group.peerID] === true}
          {@const refreshError = peerRefreshErrors[group.peerID] ?? peerStatus[group.peerID]?.catalog?.lastError}
          <div>
            <div class="bg-muted/30 flex items-center gap-2 px-3 py-1.5 sm:px-4">
              <button
                type="button"
                class="flex min-w-0 flex-1 cursor-pointer items-center gap-2"
                onclick={() => toggleSection(key)}
                aria-expanded={!peerCollapsed}
              >
                <ChevronRight
                  class={`text-muted-foreground size-4 shrink-0 transition-transform ${peerCollapsed ? "" : "rotate-90"}`}
                  aria-hidden="true"
                />
                <span class="truncate text-sm font-medium">{group.peerID}</span>
                <span class="text-muted-foreground text-xs">{group.models.length}</span>
                {#if refreshError}
                  <span class="text-destructive min-w-0 truncate text-xs" title={refreshError}>{refreshError}</span>
                {/if}
              </button>
              <Button
                variant="ghost"
                size="icon"
                class="size-7 shrink-0"
                onclick={() => handleRefreshPeer(group.peerID)}
                disabled={refreshing || !peerStatus[group.peerID]?.catalog}
                title={peerRefreshTitle(group.peerID)}
                aria-label={`Refresh models from ${group.peerID}`}
              >
                <RefreshCw class={`size-3.5 ${refreshing ? "animate-spin" : ""}`} />
              </Button>
            </div>
            {#if !peerCollapsed}
              <div class="divide-y border-t">
                {#each group.models as model (model.id)}
                  {@render modelRow(model, true)}
                {:else}
                  <div class="text-muted-foreground py-3 pr-3 pl-9 text-sm sm:pr-4 sm:pl-10">
                    No models from this peer
                  </div>
                {/each}
              </div>
            {/if}
          </div>
        {/each}
      </div>
    {/if}
    </Card.Content>
  </Card.Root>
{/snippet}

{#snippet selectorSection()}
  <Card.Root class="shrink-0 gap-0 overflow-hidden py-0">
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
            {#if selector.strategy === "fit" && selector.noEvict}
              <Tag>no evict</Tag>
            {/if}
            <Tag class="px-1.5 text-[0.625rem] uppercase">{selector.strategy}</Tag>
          </div>
        {/each}
      </div>
    </Card.Content>
  </Card.Root>
{/snippet}

{#snippet profilesSection()}
  {#if profileError}
    <div class="text-destructive text-sm" role="alert">{profileError}</div>
  {/if}
  {#if !$activeProfile}
    <div class="text-muted-foreground text-sm">No profile is active. Activate one to pin its models.</div>
  {/if}
  {#each $profiles as profile (profile.id)}
    {@const active = profile.id === $activeProfile}
    {@const mappings = profileMappings(profile)}
    <Card.Root class={`shrink-0 gap-0 overflow-hidden py-0 ${active ? "ring-success/60 ring-2" : ""}`}>
      <Card.Header class="shrink-0 border-b px-3 py-2.5 sm:px-4">
        <div class="flex items-center gap-2">
          <div class="flex min-w-0 flex-1 flex-col gap-0.5">
            <span class="flex min-w-0 items-center gap-2">
              <Card.Title class="truncate text-sm">{profile.id}</Card.Title>
              {#if active}
                <Tag class="bg-success/15 text-success">Active</Tag>
              {/if}
              <span class="text-muted-foreground text-xs">
                {mappings.length} {mappings.length === 1 ? "mapping" : "mappings"}
              </span>
            </span>
            {#if profile.description}
              <span class="text-muted-foreground text-xs">{profile.description}</span>
            {/if}
          </div>
          <Button
            variant={active ? "ghost" : "outline"}
            size="sm"
            class="shrink-0"
            disabled={$pendingProfileSwitch !== undefined}
            onclick={() => handleSetProfile(active ? null : profile.id)}
            title={active ? "Deactivate this profile" : "Make this the active profile"}
          >
            {#if $pendingProfileSwitch !== undefined && ($pendingProfileSwitch === profile.id || (active && $pendingProfileSwitch === null))}
              <Loader2 class="size-3.5 animate-spin" />
            {/if}
            {active ? "Deactivate" : "Activate"}
          </Button>
        </div>
      </Card.Header>
      <Card.Content class="p-0">
        {#if mappings.length === 0}
          <div class="text-muted-foreground px-3 py-4 text-center text-sm sm:px-4">No mappings</div>
        {:else}
          <div class="divide-y">
            {#each mappings as [modelID, target] (modelID)}
              {@const targetModel = target ? findModel(target) : undefined}
              <div class="hover:bg-muted/50 flex items-center gap-3 px-3 py-2.5 sm:px-4 has-[>button]:py-1.5">
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
                    {#if active && !targetModel.peerID}
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
    </Card.Root>
  {/each}
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
      <div class="flex flex-col gap-2 sm:flex-row sm:flex-wrap sm:items-center">
        <div class="flex min-w-0 items-center gap-2">
          <Boxes class="size-5 shrink-0" />
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

  <Tabs value={currentTab} onValueChange={(v) => v && modelsDashTab.set(v as ModelsDashTab)}>
    <TabsList
      variant="line"
      class="max-w-full justify-start overflow-x-auto [scrollbar-width:none] [&::-webkit-scrollbar]:hidden"
    >
      {#each tabs as tab (tab.id)}
        <TabsTrigger value={tab.id} class="flex-none px-2">
          {tab.label}
          <span class="text-muted-foreground text-xs tabular-nums">{tab.count}</span>
        </TabsTrigger>
      {/each}
    </TabsList>

    <TabsContent value="local" class="mt-2 flex flex-col gap-3 sm:gap-4">
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
    </TabsContent>

    <TabsContent value="peers" class="mt-2">
      {@render peerSection()}
    </TabsContent>

    <TabsContent value="selectors" class="mt-2">
      {@render selectorSection()}
    </TabsContent>

    <TabsContent value="profiles" class="mt-2 flex flex-col gap-3 sm:gap-4">
      {@render profilesSection()}
    </TabsContent>
  </Tabs>
</div>
