<script lang="ts">
  import { link } from "svelte-spa-router";
  import { FerrisWheel, Boxes, Activity, Cat, ScrollText, Gauge, Cpu, Sun, Moon, Monitor, ChevronRight, Settings, CircleQuestionMark, Network } from "@lucide/svelte";
  import * as Sidebar from "$lib/components/ui/sidebar/index.js";
  import * as Collapsible from "$lib/components/ui/collapsible/index.js";
  import { Button } from "$lib/components/ui/button/index.js";
  import { toggleTheme, themeMode, appTitle } from "../stores/theme";
  import { currentRoute } from "../stores/route";
  import { playgroundActivity, docsAgentStreaming } from "../stores/playgroundActivity";
  import { performanceEnabled, models, tailcatStatus, helpAgentEnabled } from "../stores/api";
  import { showUnlistedModels, groupByClass } from "../stores/modelDisplay";
  import { modelsMenuOpen, peersMenuOpen, modelsGroupsCollapsed } from "../stores/sidebar";
  import { groupModelsByClass, modelClassLabels, sectionStatusDotColor } from "../lib/modelClasses";
  import type { Model } from "../lib/types";
  import { isComposingKey } from "../lib/ime";
  import { groupModelsByPeer, peerLocalModelID } from "../lib/modelUtils";
  import ConnectionStatus from "./ConnectionStatus.svelte";
  import MiddleTruncate from "./MiddleTruncate.svelte";

  function handleTitleChange(newTitle: string): void {
    const sanitized = newTitle.replace(/\n/g, "").trim().substring(0, 64) || "llama-swap";
    appTitle.set(sanitized);
  }

  function handleKeyDown(e: KeyboardEvent): void {
    if (e.key === "Enter" && !isComposingKey(e)) {
      e.preventDefault();
      const target = e.currentTarget as HTMLElement;
      handleTitleChange(target.textContent || "(set title)");
      target.blur();
    }
  }

  function handleBlur(e: FocusEvent): void {
    const target = e.currentTarget as HTMLElement;
    handleTitleChange(target.textContent || "(set title)");
  }

  function isActive(path: string, current: string): boolean {
    return path === "/" ? current === "/" : current.startsWith(path);
  }

  // On small viewports the sidebar is an overlay drawer; collapse it when
  // navigation changes the page so the page is not hidden behind it.
  const sidebarCtx = Sidebar.useSidebar();
  let previousRoute = "";
  $effect(() => {
    const route = $currentRoute;
    if (route !== previousRoute) {
      previousRoute = route;
      if (sidebarCtx.isMobile) sidebarCtx.setOpenMobile(false);
    }
  });

  let visibleLocalModels = $derived(
    $models.filter((model) => !model.peerID && ($showUnlistedModels || !model.unlisted)),
  );
  let visiblePeerModels = $derived(
    $models.filter((model) => model.peerID && ($showUnlistedModels || !model.unlisted)),
  );
  // Category groups for the Models sub-menu, non-empty ones only, in
  // MODEL_CLASS_ORDER - the same grouping the Models dashboard uses.
  let peerGroups = $derived(groupModelsByPeer(visiblePeerModels));
  // A peer model's detail page highlights Peers rather than Models.
  let peerModelRoute = $derived.by(() => {
    if (!$currentRoute.startsWith("/models/")) return false;
    let id: string;
    try {
      id = decodeURIComponent($currentRoute.slice("/models/".length));
    } catch {
      return false;
    }
    return $models.some((model) => model.peerID && model.id === id);
  });
  let localClassGroups = $derived(
    groupModelsByClass(visibleLocalModels).filter((group) => group.models.length > 0),
  );
  // Without categories (the Models page's "Categories" display option), one
  // list in the same order the Models page uses.
  let sortedLocalModels = $derived(
    [...visibleLocalModels].sort((a, b) => a.id.localeCompare(b.id, undefined, { numeric: true })),
  );

  function isGroupCollapsed(classId: string): boolean {
    return $modelsGroupsCollapsed[classId] === true;
  }

  function toggleGroup(classId: string): void {
    modelsGroupsCollapsed.update((groups) => {
      const next = { ...groups };
      if (next[classId]) {
        delete next[classId];
      } else {
        next[classId] = true;
      }
      return next;
    });
  }

  type DotColor = "grey" | "yellow" | "green";
  function statusDotColor(model: Model): DotColor {
    if (model.state === "ready") return "green";
    if (model.state === "starting" || model.state === "stopping") return "yellow";
    return "grey";
  }

  const dotClass: Record<DotColor, string> = {
    grey: "bg-muted-foreground/40",
    yellow: "bg-warning",
    green: "bg-success",
  };
</script>

{#snippet modelMenuItem(model: Model)}
  <Sidebar.MenuSubItem>
    <Sidebar.MenuSubButton
      isActive={$currentRoute === `/models/${encodeURIComponent(model.id)}`}
    >
      {#snippet child({ props })}
        <a href="/models/{encodeURIComponent(model.id)}" use:link {...props} title={model.id}>
          <!-- Peer models have no load state to show, and are listed under their peer. -->
          {#if model.peerID}
            <MiddleTruncate text={peerLocalModelID(model)} class="flex-1" />
          {:else}
            <span class={`size-2 shrink-0 rounded-full ${dotClass[statusDotColor(model)]}`}></span>
            <MiddleTruncate text={model.id} class="flex-1" />
          {/if}
        </a>
      {/snippet}
    </Sidebar.MenuSubButton>
  </Sidebar.MenuSubItem>
{/snippet}

{#snippet categoryGroupItem(classId: string, label: string, groupModels: Model[])}
  {@const open = !isGroupCollapsed(classId)}
  <li>
    <Collapsible.Root open={open} class="gap-0">
      <button
        type="button"
        class="flex h-7 w-full cursor-pointer min-w-0 items-center gap-2 rounded-md px-2 text-xs font-medium text-sidebar-foreground/70 hover:bg-sidebar-accent hover:text-sidebar-accent-foreground"
        onclick={() => toggleGroup(classId)}
        aria-expanded={open}
      >
        <span class="flex size-4 shrink-0 items-center justify-center">
          <span
            class={`size-2 rounded-full ${sectionStatusDotColor(groupModels)}`}
            role="img"
            aria-label={`${groupModels.filter((m) => m.state === "ready").length} of ${groupModels.length} ready`}
          ></span>
        </span>
        <span class="flex-1 truncate text-left">{label}</span>
        <span class="text-sidebar-foreground/50 tabular-nums">{groupModels.length}</span>
        <ChevronRight class={`size-3.5 shrink-0 transition-transform ${open ? "rotate-90" : ""}`} aria-hidden="true" />
      </button>
      <Collapsible.Content>
        <Sidebar.MenuSub class="my-0.5">
          {#each groupModels as model (model.id)}
            {@render modelMenuItem(model)}
          {/each}
        </Sidebar.MenuSub>
      </Collapsible.Content>
    </Collapsible.Root>
  </li>
{/snippet}

<!-- A peer's folding heading row, aligned with the category labels under
     Models so peer IDs do not push their model names further right. -->
{#snippet peerHeading(peerID: string, count: number)}
  {@const key = `peer:${peerID}`}
  {@const open = !isGroupCollapsed(key)}
  <button
    type="button"
    class="flex h-7 w-full cursor-pointer min-w-0 items-center gap-2 rounded-md px-2 text-xs font-medium text-sidebar-foreground/70 hover:bg-sidebar-accent hover:text-sidebar-accent-foreground"
    onclick={() => toggleGroup(key)}
    aria-expanded={open}
    aria-label={`Toggle peer ${peerID}`}
  >
    <span class="size-4 shrink-0"></span>
    <span class="flex-1 truncate text-left">{peerID}</span>
    <span class="text-sidebar-foreground/50 tabular-nums">{count}</span>
    <ChevronRight class={`size-3.5 shrink-0 transition-transform ${open ? "rotate-90" : ""}`} aria-hidden="true" />
  </button>
{/snippet}

{#snippet peersMenuItem()}
  <Sidebar.MenuItem>
    <Collapsible.Root
      open={$peersMenuOpen}
      onOpenChange={(v) => peersMenuOpen.set(v)}
      class="gap-0"
    >
      <Sidebar.MenuButton isActive={peerModelRoute} tooltipContent="Peers">
        {#snippet child({ props })}
          <a href="/models" use:link {...props}>
            <Network />
            <span>Peers</span>
            <span class="text-sidebar-foreground/50 ml-auto text-xs tabular-nums">{visiblePeerModels.length}</span>
            <span
              class="transition-transform duration-200 {$peersMenuOpen ? 'rotate-90' : ''}"
              role="button"
              tabindex="0"
              aria-label="Toggle peers section"
              onclick={(e) => {
                e.preventDefault();
                e.stopPropagation();
                peersMenuOpen.update((v) => !v);
              }}
              onkeydown={(e) => {
                if (e.key === 'Enter' || e.key === ' ') {
                  e.preventDefault();
                  e.stopPropagation();
                  peersMenuOpen.update((v) => !v);
                }
              }}
            >
              <ChevronRight />
            </span>
          </a>
        {/snippet}
      </Sidebar.MenuButton>
      <Collapsible.Content>
        <ul class="m-0 flex min-w-0 flex-col gap-0.5 p-0">
          {#each peerGroups as group (group.peerID)}
            <li>
              <Collapsible.Root open={!isGroupCollapsed(`peer:${group.peerID}`)} class="gap-0">
                {@render peerHeading(group.peerID, group.models.length)}
                <Collapsible.Content>
                  <Sidebar.MenuSub class="my-0.5">
                    {#each group.models as model (model.id)}
                      {@render modelMenuItem(model)}
                    {/each}
                  </Sidebar.MenuSub>
                </Collapsible.Content>
              </Collapsible.Root>
            </li>
          {/each}
        </ul>
      </Collapsible.Content>
    </Collapsible.Root>
  </Sidebar.MenuItem>
{/snippet}

<!-- Sits below the inference-paused banner; see --banner-h in App.svelte. -->
<Sidebar.Root collapsible="icon" class="top-[var(--banner-h,0px)] h-[calc(100svh-var(--banner-h,0px))]">
  <Sidebar.Header>
    <div class="flex items-center gap-2 px-2 py-1.5">
      <div class="flex shrink-0 items-center justify-center">
        <ConnectionStatus />
      </div>
      <h1
        contenteditable="true"
        class="truncate pb-0 text-base font-semibold outline-none rounded-md px-1 hover:bg-sidebar-accent group-data-[collapsible=icon]:hidden"
        onblur={handleBlur}
        onkeydown={handleKeyDown}
      >
        {$appTitle}
      </h1>
    </div>
  </Sidebar.Header>

  <Sidebar.Content>
    <Sidebar.Group>
      <Sidebar.GroupContent>
        <Sidebar.Menu class="gap-1">
          <Sidebar.MenuItem>
            <Sidebar.MenuButton isActive={$currentRoute === "/" || isActive("/activity", $currentRoute)} tooltipContent="Activity">
              {#snippet child({ props })}
                <a href="/" use:link {...props}>
                  <Activity />
                  <span>Activity</span>
                </a>
              {/snippet}
            </Sidebar.MenuButton>
          </Sidebar.MenuItem>

          {#if $tailcatStatus.enabled}
            <Sidebar.MenuItem>
              <Sidebar.MenuButton isActive={isActive("/tailcat", $currentRoute)} tooltipContent="Tailcat">
                {#snippet child({ props })}
                  <a href="/tailcat" use:link {...props}>
                    <Cat />
                    <span>Tailcat</span>
                  </a>
                {/snippet}
              </Sidebar.MenuButton>
            </Sidebar.MenuItem>
          {/if}

          <Sidebar.MenuItem>
            <Sidebar.MenuButton isActive={isActive("/playground", $currentRoute)} tooltipContent="Playground">
              {#snippet child({ props })}
                <a href="/playground" use:link {...props}>
                  <FerrisWheel />
                  <span class={$playgroundActivity ? "activity-link" : ""}>Playground</span>
                </a>
              {/snippet}
            </Sidebar.MenuButton>
          </Sidebar.MenuItem>

          <Sidebar.MenuItem>
            <Collapsible.Root
              open={$modelsMenuOpen}
              onOpenChange={(v) => modelsMenuOpen.set(v)}
              class="gap-0"
            >
              <Sidebar.MenuButton
                isActive={$currentRoute.startsWith("/models") && !peerModelRoute}
                tooltipContent="Models"
              >
                {#snippet child({ props })}
                  <a href="/models" use:link {...props}>
                    <Boxes />
                    <span>Models</span>
                    <span
                      class="ml-auto transition-transform duration-200 {$modelsMenuOpen ? 'rotate-90' : ''}"
                      role="button"
                      tabindex="0"
                      aria-label="Toggle models section"
                      onclick={(e) => {
                        e.preventDefault();
                        e.stopPropagation();
                        modelsMenuOpen.update((v) => !v);
                      }}
                      onkeydown={(e) => {
                        if (e.key === 'Enter' || e.key === ' ') {
                          e.preventDefault();
                          e.stopPropagation();
                          modelsMenuOpen.update((v) => !v);
                        }
                      }}
                    >
                      <ChevronRight />
                    </span>
                  </a>
                {/snippet}
              </Sidebar.MenuButton>
              <Collapsible.Content>
                {#if $groupByClass}
                  <ul class="m-0 flex min-w-0 flex-col gap-0.5 p-0">
                    {#each localClassGroups as group (group.class)}
                      {@render categoryGroupItem(group.class, modelClassLabels[group.class], group.models)}
                    {/each}
                  </ul>
                {:else if sortedLocalModels.length > 0}
                  <Sidebar.MenuSub class="my-0.5">
                    {#each sortedLocalModels as model (model.id)}
                      {@render modelMenuItem(model)}
                    {/each}
                  </Sidebar.MenuSub>
                {/if}
              </Collapsible.Content>
            </Collapsible.Root>
          </Sidebar.MenuItem>

          {#if peerGroups.length > 0}
            {@render peersMenuItem()}
          {/if}

          <Sidebar.MenuItem>
            <Sidebar.MenuButton isActive={isActive("/logs", $currentRoute)} tooltipContent="Logs">
              {#snippet child({ props })}
                <a href="/logs" use:link {...props}>
                  <ScrollText />
                  <span>Logs</span>
                </a>
              {/snippet}
            </Sidebar.MenuButton>
          </Sidebar.MenuItem>

          {#if $performanceEnabled}
            <Sidebar.MenuItem>
              <Sidebar.MenuButton isActive={isActive("/performance", $currentRoute)} tooltipContent="Performance">
                {#snippet child({ props })}
                  <a href="/performance" use:link {...props}>
                    <Gauge />
                    <span>Performance</span>
                  </a>
                {/snippet}
              </Sidebar.MenuButton>
            </Sidebar.MenuItem>
          {/if}

          <Sidebar.MenuItem>
            <Sidebar.MenuButton isActive={isActive("/hardware", $currentRoute)} tooltipContent="Hardware">
              {#snippet child({ props })}
                <a href="/hardware" use:link {...props}>
                  <Cpu />
                  <span>Hardware</span>
                </a>
              {/snippet}
            </Sidebar.MenuButton>
          </Sidebar.MenuItem>
        </Sidebar.Menu>
      </Sidebar.GroupContent>
    </Sidebar.Group>
  </Sidebar.Content>

  <Sidebar.Footer>
    {#if $helpAgentEnabled}
      <Sidebar.Menu>
        <Sidebar.MenuItem>
          <Sidebar.MenuButton isActive={isActive("/help", $currentRoute)} tooltipContent="Help">
            {#snippet child({ props })}
              <a href="/help" use:link {...props}>
                <CircleQuestionMark />
                <span class={$docsAgentStreaming ? "activity-link" : ""}>Help</span>
              </a>
            {/snippet}
          </Sidebar.MenuButton>
        </Sidebar.MenuItem>
      </Sidebar.Menu>
    {/if}

    <div
      class="flex items-center justify-between gap-2 group-data-[collapsible=icon]:flex-col-reverse"
    >
      <Sidebar.MenuButton
        isActive={isActive("/settings", $currentRoute)}
        tooltipContent="Settings"
      >
        {#snippet child({ props })}
          <a href="/settings" use:link {...props}>
            <Settings />
            <span>Settings</span>
          </a>
        {/snippet}
      </Sidebar.MenuButton>
      <Button
        variant="ghost"
        size="icon"
        onclick={toggleTheme}
        title="Toggle theme (current: {$themeMode})"
      >
        {#if $themeMode === "system"}
          <Monitor />
        {:else if $themeMode === "light"}
          <Sun />
        {:else}
          <Moon />
        {/if}
        <span class="sr-only">Toggle theme</span>
      </Button>
    </div>
  </Sidebar.Footer>
  <Sidebar.Rail />
</Sidebar.Root>
