<script lang="ts">
  import { link } from "svelte-spa-router";
  import { FerrisWheel, Boxes, Activity, Cat, ScrollText, Gauge, Cpu, Sun, Moon, Monitor, ChevronRight, Settings, CircleQuestionMark } from "@lucide/svelte";
  import * as Sidebar from "$lib/components/ui/sidebar/index.js";
  import * as Collapsible from "$lib/components/ui/collapsible/index.js";
  import { Button } from "$lib/components/ui/button/index.js";
  import { toggleTheme, themeMode, appTitle } from "../stores/theme";
  import { currentRoute } from "../stores/route";
  import { playgroundActivity, docsAgentStreaming } from "../stores/playgroundActivity";
  import { performanceEnabled, models, tailcatStatus } from "../stores/api";
  import { showUnlistedModels } from "../stores/modelDisplay";
  import { modelsMenuOpen } from "../stores/sidebar";
  import type { Model } from "../lib/types";
  import { statusDotAppearance, statusDotStyle, statusDotRingColor, type StatusDotKind } from "../lib/statusDot";
  import { isComposingKey } from "../lib/ime";
  import ConnectionStatus from "./ConnectionStatus.svelte";

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

  let visibleLocalModels = $derived(
    $models.filter((model) => !model.peerID && ($showUnlistedModels || !model.unlisted)),
  );
  let visiblePeerModels = $derived(
    $models.filter((model) => model.peerID && ($showUnlistedModels || !model.unlisted)),
  );

  // Wall clock driving the loading pie in each model's status dot. Only local
  // models carry load timing (peers never do), so only they can keep the timer
  // alive; the tick stops as soon as nothing local is starting.
  let now = $state(Date.now());
  const anyLocalModelStarting = $derived(
    visibleLocalModels.some((model) => model.state === "starting"),
  );

  $effect(() => {
    if (!anyLocalModelStarting) {
      return;
    }

    const tick = () => {
      now = Date.now();
    };
    tick(); // seed immediately so the pie doesn't sit at a stale angle for 250ms

    const id = setInterval(tick, 250);

    return () => {
      clearInterval(id);
    };
  });

  // Flat interior colour per dot kind. Filling kinds keep the grey track as
  // their background so the unfilled portion of the conic pie (inline style from
  // statusDotStyle) shows through. No animate-pulse on any loading kind: the
  // animated ring (statusDotRingColor) is now the single "loading" motion, so a
  // second interior pulse would just fight it.
  const dotClass: Record<StatusDotKind, string> = {
    stopped: "bg-muted-foreground/40",
    stopping: "bg-warning",
    indeterminate: "bg-warning",
    filling: "bg-muted-foreground/40",
    overrun: "bg-muted-foreground/40",
    ready: "bg-success",
  };
</script>

{#snippet modelMenuItem(model: Model)}
  <Sidebar.MenuSubItem>
    <Sidebar.MenuSubButton
      isActive={$currentRoute === `/models/${encodeURIComponent(model.id)}`}
    >
      {#snippet child({ props })}
        {@const dot = statusDotAppearance(model, now)}
        {@const ringColor = statusDotRingColor(dot)}
        <a href="/models/{encodeURIComponent(model.id)}" use:link {...props}>
          <span class="relative inline-flex size-2 shrink-0">
            {#if ringColor}
              <span
                class="absolute inset-0 rounded-full border animate-ping"
                style={`border-color: ${ringColor}`}
                aria-hidden="true"
              ></span>
            {/if}
            <span
              class={`relative size-2 rounded-full ${dotClass[dot.kind]}`}
              style={statusDotStyle(dot)}
            ></span>
          </span>
          <span class="flex-1 truncate">{model.id}</span>
        </a>
      {/snippet}
    </Sidebar.MenuSubButton>
  </Sidebar.MenuSubItem>
{/snippet}

<Sidebar.Root collapsible="icon">
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
                isActive={$currentRoute.startsWith("/models")}
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
                <Sidebar.MenuSub>
                  {#each visibleLocalModels as model (model.id)}
                    {@render modelMenuItem(model)}
                  {/each}
                  {#if visiblePeerModels.length > 0}
                    <li class="text-sidebar-foreground/70 px-2 pt-2 pb-1 text-xs font-medium">
                      Peers
                    </li>
                    {#each visiblePeerModels as model (model.id)}
                      {@render modelMenuItem(model)}
                    {/each}
                  {/if}
                </Sidebar.MenuSub>
              </Collapsible.Content>
            </Collapsible.Root>
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
