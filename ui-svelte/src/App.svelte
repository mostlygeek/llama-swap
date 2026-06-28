<script lang="ts">
  import { onMount } from "svelte";
  import Router from "svelte-spa-router";
  import AppSidebar from "./components/AppSidebar.svelte";
  import LogViewer from "./routes/LogViewer.svelte";
  import ModelDetail from "./routes/ModelDetail.svelte";
  import ModelsDash from "./routes/ModelsDash.svelte";
  import Activity from "./routes/Activity.svelte";
  import Performance from "./routes/Performance.svelte";
  import Playground from "./routes/Playground.svelte";
  import PlaygroundStub from "./routes/PlaygroundStub.svelte";
  import Settings from "./routes/Settings.svelte";
  import * as Sidebar from "$lib/components/ui/sidebar/index.js";
  import * as Tooltip from "$lib/components/ui/tooltip/index.js";
  import { Separator } from "$lib/components/ui/separator/index.js";
  import { enableAPIEvents, checkPerformanceEnabled } from "./stores/api";
  import { initScreenWidth, initSystemThemeListener, isDarkMode, themeName, appTitle, connectionState } from "./stores/theme";
  import { currentRoute } from "./stores/route";
  import { selectedPlaygroundTab, playgroundTabs } from "./stores/playground";

  const routes = {
    "/": Activity,
    "/playground": PlaygroundStub,
    "/models": ModelsDash,
    "/models/:id": ModelDetail,
    "/logs": LogViewer,
    "/activity": Activity,
    "/settings": Settings,
    "/performance": Performance,
    "*": Activity,
  };

  const routeTitles: Record<string, string> = {
    "/": "Activity",
    "/playground": "Playground",
    "/models": "Models",
    "/activity": "Activity",
    "/logs": "Logs",
    "/settings": "Settings",
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
      </header>

      <main class="min-h-0 flex-1 overflow-auto p-4">
        <div class="h-full" class:hidden={$currentRoute !== "/playground"}>
          <Playground />
        </div>
        <div class="h-full" class:hidden={$currentRoute === "/playground"}>
          <Router {routes} on:routeLoaded={handleRouteLoaded} />
        </div>
      </main>
    </Sidebar.Inset>
  </Sidebar.Provider>
</Tooltip.Provider>
