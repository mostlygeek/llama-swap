<script lang="ts">
  import { onMount } from "svelte";
  import Router from "svelte-spa-router";
  import Header from "./components/Header.svelte";
  import LogViewer from "./routes/LogViewer.svelte";
  import Models from "./routes/Models.svelte";
  import Activity from "./routes/Activity.svelte";
  import { enableAPIEvents } from "./stores/api";
  import { initTheme, syncThemeToDocument, syncTitleToDocument } from "./stores/theme";

  const routes = {
    "/": LogViewer,
    "/models": Models,
    "/activity": Activity,
    "*": LogViewer,
  };

  onMount(() => {
    const cleanupTheme = initTheme();
    syncThemeToDocument();
    const cleanupTitle = syncTitleToDocument();
    enableAPIEvents(true);

    return () => {
      cleanupTheme();
      cleanupTitle();
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
