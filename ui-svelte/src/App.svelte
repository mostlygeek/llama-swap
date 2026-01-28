<script lang="ts">
  import { onMount } from "svelte";
  import Router from "svelte-spa-router";
  import { get } from "svelte/store";
  import Header from "./components/Header.svelte";
  import LogViewer from "./routes/LogViewer.svelte";
  import Models from "./routes/Models.svelte";
  import Activity from "./routes/Activity.svelte";
  import { enableAPIEvents } from "./stores/api";
  import { initTheme, syncThemeToDocument, connectionState, appTitle } from "./stores/theme";

  const routes = {
    "/": LogViewer,
    "/models": Models,
    "/activity": Activity,
    "*": LogViewer,
  };

  onMount(() => {
    // Initialize theme and responsive handlers
    const cleanupTheme = initTheme();

    // Sync theme to document
    syncThemeToDocument();

    // Sync title to document
    const unsubTitle = appTitle.subscribe((title) => {
      updateDocumentTitle(title);
    });
    const unsubConn = connectionState.subscribe(() => {
      const title = get(appTitle);
      updateDocumentTitle(title);
    });

    // Enable API events
    enableAPIEvents(true);

    return () => {
      cleanupTheme();
      unsubTitle();
      unsubConn();
      enableAPIEvents(false);
    };
  });

  function updateDocumentTitle(title: string): void {
    const currentConnection = get(connectionState);
    const connectionIcon = currentConnection === "connecting" ? "\u{1F7E1}" : currentConnection === "connected" ? "\u{1F7E2}" : "\u{1F534}";
    document.title = connectionIcon + " " + title;
  }
</script>

<div class="flex flex-col h-screen">
  <Header />

  <main class="flex-1 overflow-auto p-4">
    <Router {routes} />
  </main>
</div>
