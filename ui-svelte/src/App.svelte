<script lang="ts">
  import { onMount } from "svelte";
  import type { Component, ComponentType, SvelteComponent } from "svelte";
  import Router from "svelte-spa-router";
  import { wrap } from "svelte-spa-router/wrap";
  import AppSidebar from "./components/AppSidebar.svelte";
  import RouteLoadingImpl from "./components/RouteLoading.svelte";
  import PlaygroundStub from "./routes/PlaygroundStub.svelte";
  import * as Sidebar from "$lib/components/ui/sidebar/index.js";
  import * as Tooltip from "$lib/components/ui/tooltip/index.js";
  import { Separator } from "$lib/components/ui/separator/index.js";
  import * as Select from "$lib/components/ui/select/index.js";
  import {
    activeProfile,
    checkPerformanceEnabled,
    enableAPIEvents,
    profiles,
    setActiveProfile,
  } from "./stores/api";
  import { initScreenWidth, initSystemThemeListener, isDarkMode, themeName, appTitle, connectionState } from "./stores/theme";
  import { currentRoute } from "./stores/route";
  import { selectedPlaygroundTab, playgroundTabs } from "./stores/playground";

  // svelte-spa-router's types predate Svelte 5 (loadingComponent wants the
  // old class-component ComponentType); the cast is safe since Router.svelte
  // just instantiates whatever component object it's given.
  const RouteLoading = RouteLoadingImpl as unknown as ComponentType<SvelteComponent>;

  // Routes are lazy-loaded so their (and their dependencies') code isn't part
  // of the initial bundle; each becomes its own chunk fetched on first visit.
  // loadingComponent covers the (usually brief) fetch with a themed
  // placeholder instead of a blank/white flash.
  const routes = {
    "/": wrap({ asyncComponent: () => import("./routes/Activity.svelte"), loadingComponent: RouteLoading }),
    "/playground": PlaygroundStub,
    "/models": wrap({ asyncComponent: () => import("./routes/ModelsDash.svelte"), loadingComponent: RouteLoading }),
    "/models/:id": wrap({ asyncComponent: () => import("./routes/ModelDetail.svelte"), loadingComponent: RouteLoading }),
    "/models/hub": wrap({ asyncComponent: () => import("./routes/ModelManager.svelte"), loadingComponent: RouteLoading }),
    "/logs": wrap({ asyncComponent: () => import("./routes/LogViewer.svelte"), loadingComponent: RouteLoading }),
    "/activity": wrap({ asyncComponent: () => import("./routes/Activity.svelte"), loadingComponent: RouteLoading }),
    "/settings": wrap({ asyncComponent: () => import("./routes/Settings.svelte"), loadingComponent: RouteLoading }),
    "/config": wrap({ asyncComponent: () => import("./routes/ConfigEditor.svelte"), loadingComponent: RouteLoading }),
    "/backends": wrap({ asyncComponent: () => import("./routes/BackendManager.svelte"), loadingComponent: RouteLoading }),
    "/performance": wrap({ asyncComponent: () => import("./routes/Performance.svelte"), loadingComponent: RouteLoading }),
    "*": wrap({ asyncComponent: () => import("./routes/Activity.svelte"), loadingComponent: RouteLoading }),
  };

  const routeTitles: Record<string, string> = {
    "/": "Activity",
    "/playground": "Playground",
    "/models": "Models",
    "/models/hub": "Model Hub",
    "/activity": "Activity",
    "/logs": "Logs",
    "/settings": "Settings",
    "/config": "Configuration",
    "/backends": "Backends",
    "/performance": "Performance",
  };

  let sectionTitle = $derived.by(() => {
    if ($currentRoute === "/playground") {
      const tab = playgroundTabs.find((t) => t.id === $selectedPlaygroundTab);
      return `Playground / ${tab?.label ?? ""}`;
    }
    if ($currentRoute.startsWith("/models/")) {
      const id = $currentRoute.slice("/models/".length);
      return id ? `Models / ${decodeURIComponent(id)}` : "Models";
    }
    if ($currentRoute === "/models") {
      return "Models";
    }
    return routeTitles[$currentRoute] ?? "Activity";
  });

  const noProfileValue = "__none__";
  let switchingProfile = $state(false);

  async function handleProfileChange(value: string): Promise<void> {
    switchingProfile = true;
    try {
      await setActiveProfile(value === noProfileValue ? null : value);
    } catch (error) {
      console.error(error);
    } finally {
      switchingProfile = false;
    }
  }

  function handleRouteLoaded(event: { detail: { route: string | RegExp; location?: string } }) {
    const route = event.detail.route;
    // Prefer the actual URL path so parameterised routes (e.g. /models/:id)
    // are reflected accurately in currentRoute for sidebar highlighting.
    const loc = event.detail.location;
    currentRoute.set(loc ?? (typeof route === "string" ? route : "/"));
  }

  $effect(() => {
    document.documentElement.classList.toggle("dark", $isDarkMode);
  });

  $effect(() => {
    const el = document.documentElement;
    if ($themeName === "default") el.removeAttribute("data-theme");
    else el.setAttribute("data-theme", $themeName);
  });

  $effect(() => {
    const icon = $connectionState === "connecting" ? "\u{1F7E1}" : $connectionState === "connected" ? "\u{1F7E2}" : "\u{1F534}";
    document.title = `${icon} ${$appTitle}`;
  });

  // Playground is always mounted (rather than routed) so it keeps its state
  // when the user navigates away, but it's still lazy-loaded on app start so
  // its dependencies (chat markdown/KaTeX/highlight.js rendering) don't block
  // the initial page load.
  let PlaygroundComponent = $state<Component | null>(null);

  onMount(() => {
    const cleanupScreenWidth = initScreenWidth();
    const cleanupSystemTheme = initSystemThemeListener();
    enableAPIEvents(true);
    checkPerformanceEnabled();
    import("./routes/Playground.svelte").then((m) => {
      PlaygroundComponent = m.default;
    });

    return () => {
      cleanupScreenWidth();
      cleanupSystemTheme();
      enableAPIEvents(false);
    };
  });
</script>

<Tooltip.Provider>
  <Sidebar.Provider>
    <AppSidebar />
    <Sidebar.Inset class="h-screen min-w-0 overflow-hidden">
      <header
        class="bg-background sticky top-0 z-10 flex h-14 shrink-0 items-center gap-2 border-b px-4"
      >
        <Sidebar.Trigger class="-ml-1" />
        <Separator orientation="vertical" class="mr-2 !h-4" />
        <h2 class="truncate pb-0 text-sm font-semibold">{sectionTitle}</h2>
        {#if $profiles.length > 0}
          <div class="ml-auto flex items-center gap-2">
            <span class="text-muted-foreground hidden text-xs sm:inline">Profile</span>
            <Select.Root
              type="single"
              value={$activeProfile ?? noProfileValue}
              onValueChange={(value) => value && void handleProfileChange(value)}
            >
              <Select.Trigger
                class="w-40"
                aria-label="Active profile"
                disabled={switchingProfile}
              >
                {$activeProfile ?? "None"}
              </Select.Trigger>
              <Select.Content>
                <Select.Item value={noProfileValue}>None</Select.Item>
                {#each $profiles as profile (profile.id)}
                  <Select.Item value={profile.id}>{profile.id}</Select.Item>
                {/each}
              </Select.Content>
            </Select.Root>
          </div>
        {/if}
      </header>

      <main class="min-h-0 flex-1 overflow-auto p-4">
        <div class="h-full" class:hidden={$currentRoute !== "/playground"}>
          {#if PlaygroundComponent}
            <PlaygroundComponent />
          {:else}
            <RouteLoading />
          {/if}
        </div>
        <div class="h-full" class:hidden={$currentRoute === "/playground"}>
          <Router {routes} on:routeLoaded={handleRouteLoaded} />
        </div>
      </main>
    </Sidebar.Inset>
  </Sidebar.Provider>
</Tooltip.Provider>
