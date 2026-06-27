<script lang="ts">
  import { onMount } from "svelte";
  import Router from "svelte-spa-router";
  import AppSidebar from "./components/AppSidebar.svelte";
  import LogViewer from "./routes/LogViewer.svelte";
  import Models from "./routes/Models.svelte";
  import Activity from "./routes/Activity.svelte";
  import Performance from "./routes/Performance.svelte";
  import Playground from "./routes/Playground.svelte";
  import PlaygroundStub from "./routes/PlaygroundStub.svelte";
  import * as Sidebar from "$lib/components/ui/sidebar/index.js";
  import { Separator } from "$lib/components/ui/separator/index.js";
  import { enableAPIEvents, checkPerformanceEnabled } from "./stores/api";
  import { initScreenWidth, initSystemThemeListener, isDarkMode, appTitle, connectionState } from "./stores/theme";
  import { currentRoute } from "./stores/route";
  import { selectedPlaygroundTab, playgroundTabs } from "./stores/playground";

  const routes = {
    "/": PlaygroundStub,
    "/models": Models,
    "/logs": LogViewer,
    "/activity": Activity,
    "/performance": Performance,
    "*": PlaygroundStub,
  };

  const routeTitles: Record<string, string> = {
    "/": "Playground",
    "/models": "Models",
    "/activity": "Activity",
    "/logs": "Logs",
    "/performance": "Performance",
  };

  let sectionTitle = $derived.by(() => {
    if ($currentRoute === "/") {
      const tab = playgroundTabs.find((t) => t.id === $selectedPlaygroundTab);
      return `Playground / ${tab?.label ?? ""}`;
    }
    return routeTitles[$currentRoute] ?? "Playground";
  });

  function handleRouteLoaded(event: { detail: { route: string | RegExp } }) {
    const route = event.detail.route;
    currentRoute.set(typeof route === "string" ? route : "/");
  }

  $effect(() => {
    document.documentElement.classList.toggle("dark", $isDarkMode);
  });

  $effect(() => {
    const icon = $connectionState === "connecting" ? "\u{1F7E1}" : $connectionState === "connected" ? "\u{1F7E2}" : "\u{1F534}";
    document.title = `${icon} ${$appTitle}`;
  });

  onMount(() => {
    const cleanupScreenWidth = initScreenWidth();
    const cleanupSystemTheme = initSystemThemeListener();
    enableAPIEvents(true);
    checkPerformanceEnabled();

    return () => {
      cleanupScreenWidth();
      cleanupSystemTheme();
      enableAPIEvents(false);
    };
  });
</script>

<Sidebar.Provider>
  <AppSidebar />
  <Sidebar.Inset class="h-screen min-w-0 overflow-hidden">
    <header
      class="bg-background sticky top-0 z-10 flex h-14 shrink-0 items-center gap-2 border-b px-4"
    >
      <Sidebar.Trigger class="-ml-1" />
      <Separator orientation="vertical" class="mr-2 !h-4" />
      <h2 class="truncate pb-0 text-sm font-semibold">{sectionTitle}</h2>
    </header>

    <main class="min-h-0 flex-1 overflow-auto p-4">
      <div class="h-full" class:hidden={$currentRoute !== "/"}>
        <Playground />
      </div>
      <div class="h-full" class:hidden={$currentRoute === "/"}>
        <Router {routes} on:routeLoaded={handleRouteLoaded} />
      </div>
    </main>
  </Sidebar.Inset>
</Sidebar.Provider>
