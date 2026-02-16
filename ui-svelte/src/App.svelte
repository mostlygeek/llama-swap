<script lang="ts">
  import { onMount } from "svelte";
  import Router from "svelte-spa-router";
  import Header from "./components/Header.svelte";
  import LogViewer from "./routes/LogViewer.svelte";
  import Models from "./routes/Models.svelte";
  import Activity from "./routes/Activity.svelte";
  import ConfigEditor from "./routes/ConfigEditor.svelte";
  import ClusterStatus from "./routes/ClusterStatus.svelte";
  import Backend from "./routes/Backend.svelte";
  import Help from "./routes/Help.svelte";
  import Playground from "./routes/Playground.svelte";
  import PlaygroundStub from "./routes/PlaygroundStub.svelte";
  import { enableAPIEvents } from "./stores/api";
  import { initScreenWidth, isDarkMode, appTitle, connectionState } from "./stores/theme";
  import { currentRoute } from "./stores/route";

  const routes = {
    "/": PlaygroundStub,
    "/models": Models,
    "/logs": LogViewer,
    "/cluster": ClusterStatus,
    "/backend": Backend,
    "/editor": ConfigEditor,
    "/help": Help,
    "/activity": Activity,
    "*": PlaygroundStub,
  };

  function handleRouteLoaded(event: { detail: { route: string | RegExp } }) {
    const route = event.detail.route;
    currentRoute.set(typeof route === "string" ? route : "/");
  }

  $effect(() => {
    document.documentElement.setAttribute("data-theme", $isDarkMode ? "dark" : "light");
  });

  $effect(() => {
    const icon = $connectionState === "connecting" ? "\u{1F7E1}" : $connectionState === "connected" ? "\u{1F7E2}" : "\u{1F534}";
    document.title = `${icon} ${$appTitle}`;
  });

  onMount(() => {
    const cleanupScreenWidth = initScreenWidth();
    enableAPIEvents(true);

    return () => {
      cleanupScreenWidth();
      enableAPIEvents(false);
    };
  });
</script>

<div class="flex flex-col h-screen">
  <Header />

  <main class="flex-1 overflow-auto p-4">
    <div class="h-full" class:hidden={$currentRoute !== "/"}>
      <Playground />
    </div>
    <div class="h-full" class:hidden={$currentRoute === "/"}>
      <Router {routes} on:routeLoaded={handleRouteLoaded} />
    </div>
  </main>
</div>
