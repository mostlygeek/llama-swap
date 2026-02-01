<script lang="ts">
  import { onMount } from "svelte";
  import Router from "svelte-spa-router";
  import Header from "./components/Header.svelte";
  import LogViewer from "./routes/LogViewer.svelte";
  import Models from "./routes/Models.svelte";
  import Activity from "./routes/Activity.svelte";
  import Playground from "./routes/Playground.svelte";
  import { enableAPIEvents } from "./stores/api";
  import { initScreenWidth, isDarkMode, appTitle, connectionState } from "./stores/theme";

  const routes = {
    "/": Playground,
    "/models": Models,
    "/logs": LogViewer,
    "/activity": Activity,
    "*": Playground,
  };

  // Sync theme to document attribute
  $effect(() => {
    document.documentElement.setAttribute("data-theme", $isDarkMode ? "dark" : "light");
  });

  // Sync title to document
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
    <Router {routes} />
  </main>
</div>
