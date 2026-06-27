<script lang="ts">
  import { link } from "svelte-spa-router";
  import { House, Boxes, Activity, ScrollText, Gauge, Sun, Moon, Monitor, ChevronRight, Play, PowerOff, Loader2 } from "@lucide/svelte";
  import * as Sidebar from "$lib/components/ui/sidebar/index.js";
  import * as Collapsible from "$lib/components/ui/collapsible/index.js";
  import { Button } from "$lib/components/ui/button/index.js";
  import { toggleTheme, themeMode, appTitle } from "../stores/theme";
  import { currentRoute } from "../stores/route";
  import { playgroundActivity } from "../stores/playgroundActivity";
  import { performanceEnabled, models, loadModel, unloadSingleModel } from "../stores/api";
  import { selectedPlaygroundTab, playgroundTabs, playgroundMenuOpen } from "../stores/playground";
  import { modelsMenuOpen } from "../stores/sidebar";
  import type { Model } from "../lib/types";
  import ConnectionStatus from "./ConnectionStatus.svelte";

  let pendingLoads = $state<Record<string, boolean>>({});
  const loadControllers = new Map<string, AbortController>();

  function handleTitleChange(newTitle: string): void {
    const sanitized = newTitle.replace(/\n/g, "").trim().substring(0, 64) || "llama-swap";
    appTitle.set(sanitized);
  }

  function handleKeyDown(e: KeyboardEvent): void {
    if (e.key === "Enter") {
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

  type DotColor = "grey" | "yellow" | "green";
  function statusDotColor(model: Model): DotColor {
    if (pendingLoads[model.id] && model.state === "stopped") return "yellow";
    if (model.state === "ready") return "green";
    if (model.state === "starting" || model.state === "stopping") return "yellow";
    return "grey";
  }

  const dotClass: Record<DotColor, string> = {
    grey: "bg-muted-foreground/40",
    yellow: "bg-warning",
    green: "bg-success",
  };

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

  function onToggleLoad(e: MouseEvent, model: Model): void {
    e.preventDefault();
    e.stopPropagation();
    if (model.state === "stopped" && pendingLoads[model.id]) {
      cancelLoad(model.id);
    } else if (model.state === "stopped") {
      handleLoadModel(model.id);
    } else if (model.state === "ready") {
      unloadSingleModel(model.id);
    }
  }
</script>

<Sidebar.Root collapsible="icon">
  <Sidebar.Header>
    <div class="flex items-center gap-2 px-2 py-1.5">
      <div
        class="bg-primary text-primary-foreground flex aspect-square size-8 shrink-0 items-center justify-center rounded-lg font-bold"
      >
        ll
      </div>
      <h1
        contenteditable="true"
        class="truncate pb-0 text-base font-semibold outline-none rounded px-1 hover:bg-sidebar-accent group-data-[collapsible=icon]:hidden"
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
            <Collapsible.Root
              open={$modelsMenuOpen}
              onOpenChange={(v) => modelsMenuOpen.set(v)}
              class="gap-0"
            >
              <Collapsible.Trigger>
                {#snippet child({ props })}
                  <Sidebar.MenuButton
                    {...props}
                    isActive={isActive("/models", $currentRoute)}
                    tooltipContent="Models"
                  >
                    <Boxes />
                    <span>Models</span>
                    <ChevronRight
                      class="ml-auto transition-transform duration-200 {$modelsMenuOpen ? 'rotate-90' : ''}"
                    />
                  </Sidebar.MenuButton>
                {/snippet}
              </Collapsible.Trigger>
              <Collapsible.Content>
                <Sidebar.MenuSub>
                  {#each $models as model (model.id)}
                    <Sidebar.MenuSubItem>
                      <Sidebar.MenuSubButton
                        isActive={isActive("/models", $currentRoute)}
                      >
                        {#snippet child({ props })}
                          <a href="/models" use:link {...props}>
                            <span class={`size-2 shrink-0 rounded-full ${dotClass[statusDotColor(model)]}`}></span>
                            <span class="flex-1 truncate">{model.id}</span>
                            <button
                              type="button"
                              class="flex size-5 shrink-0 items-center justify-center rounded-sm text-muted-foreground hover:bg-sidebar-accent hover:text-sidebar-accent-foreground disabled:opacity-50"
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
                          </a>
                        {/snippet}
                      </Sidebar.MenuSubButton>
                    </Sidebar.MenuSubItem>
                  {/each}
                </Sidebar.MenuSub>
              </Collapsible.Content>
            </Collapsible.Root>
          </Sidebar.MenuItem>

          <Sidebar.MenuItem>
            <Collapsible.Root
              open={$playgroundMenuOpen}
              onOpenChange={(v) => playgroundMenuOpen.set(v)}
              class="gap-0"
            >
              <Collapsible.Trigger>
                {#snippet child({ props })}
                  <Sidebar.MenuButton
                    {...props}
                    isActive={isActive("/", $currentRoute)}
                    tooltipContent="Playground"
                  >
                    <House />
                    <span class={$playgroundActivity ? "activity-link" : ""}>Playground</span>
                    <ChevronRight
                      class="ml-auto transition-transform duration-200 {$playgroundMenuOpen ? 'rotate-90' : ''}"
                    />
                  </Sidebar.MenuButton>
                {/snippet}
              </Collapsible.Trigger>
              <Collapsible.Content>
                <Sidebar.MenuSub>
                  {#each playgroundTabs as tab (tab.id)}
                    <Sidebar.MenuSubItem>
                      <Sidebar.MenuSubButton
                        isActive={isActive("/", $currentRoute) && $selectedPlaygroundTab === tab.id}
                      >
                        {#snippet child({ props })}
                          <a
                            href="/"
                            use:link
                            {...props}
                            onclick={() => selectedPlaygroundTab.set(tab.id)}
                          >
                            <span>{tab.label}</span>
                          </a>
                        {/snippet}
                      </Sidebar.MenuSubButton>
                    </Sidebar.MenuSubItem>
                  {/each}
                </Sidebar.MenuSub>
              </Collapsible.Content>
            </Collapsible.Root>
          </Sidebar.MenuItem>

          <Sidebar.MenuItem>
            <Sidebar.MenuButton isActive={isActive("/activity", $currentRoute)} tooltipContent="Activity">
              {#snippet child({ props })}
                <a href="/activity" use:link {...props}>
                  <Activity />
                  <span>Activity</span>
                </a>
              {/snippet}
            </Sidebar.MenuButton>
          </Sidebar.MenuItem>

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
        </Sidebar.Menu>
      </Sidebar.GroupContent>
    </Sidebar.Group>
  </Sidebar.Content>

  <Sidebar.Footer>
    <div
      class="flex items-center justify-between gap-2 px-1 group-data-[collapsible=icon]:flex-col-reverse"
    >
      <div class="flex items-center gap-2 px-1">
        <ConnectionStatus />
      </div>
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
