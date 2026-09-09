<script lang="ts">
  import { onMount } from "svelte";
  import Playground from "../routes/Playground.svelte";
  import ServerManager from "./ServerManager.svelte";
  import { Button } from "$lib/components/ui/button/index.js";
  import * as Card from "$lib/components/ui/card/index.js";
  import { LoaderCircle, RefreshCw } from "@lucide/svelte";
  import { isDarkMode, themeName, connectionState, initSystemThemeListener } from "../stores/theme";
  import { fetchPlaygroundModels } from "../stores/api";
  import { startTailcatWasm, type LoadStage } from "./wasm";
  import { bridge } from "./bridge";
  import { installTailcatFetch } from "./transport";
  import { get } from "svelte/store";
  import {
    tailcatClientKey,
    tailcatServers,
    findByToken,
    newServerID,
    type TailcatServer,
  } from "./servers";

  type Phase = "servers" | "connecting" | "connected";

  let phase = $state<Phase>("servers");
  let status = $state("");
  let error = $state("");
  let nodeKey = $state("");
  let keyRegenerated = $state(false);
  let active = $state<TailcatServer | null>(null);
  let pendingToken = $state("");
  let restoreFetch: (() => void) | undefined;

  const loadMessages: Record<LoadStage, string> = {
    fetching: "Loading the Tailcat module",
    decompressing: "Unpacking the Tailcat module",
    compiling: "Compiling the Tailcat module",
    starting: "Starting the Tailcat module",
  };

  // The module has to be running before its client key exists, so this starts
  // on page load rather than on connect: the server form can show the node key
  // by the time anyone has finished typing a token into it.
  const wasmReady = startTailcatWasm((stage) => {
    if (phase === "connecting") status = loadMessages[stage];
  }).then(() => {
    const identity = bridge().identity($tailcatClientKey || null);
    nodeKey = identity.nodeKey;
    keyRegenerated = identity.regenerated;
    tailcatClientKey.set(identity.privateKeyJSON);
  });

  async function connect(server: TailcatServer) {
    active = server;
    error = "";
    phase = "connecting";
    connectionState.set("connecting");
    location.hash = server.token;

    try {
      status = "Loading the Tailcat module";
      await wasmReady;

      status = "Connecting over Tailcat";
      await bridge().connect({
        token: server.token,
        privateKeyJSON: $tailcatClientKey,
        derpMapURL: server.derpMapURL,
        verbose: false,
      });

      restoreFetch?.();
      restoreFetch = installTailcatFetch(() => active?.apiKey ?? "");

      // The node is reachable by this point; this is the first request that
      // needs the API key, so it is where a credentials problem shows up.
      status = "Reading the model list";
      const models = await fetch("/v1/models");
      if (models.status === 401 || models.status === 403) {
        throw new Error(
          "The node rejected the API key. Edit the server and set the key from its apiKeys config.",
        );
      }
      if (!models.ok) {
        throw new Error(`The node answered GET /v1/models with ${models.status}.`);
      }
      await fetchPlaygroundModels();

      connectionState.set("connected");
      phase = "connected";
    } catch (e) {
      error = e instanceof Error ? e.message : String(e);
      connectionState.set("disconnected");
      phase = "servers";
      teardown();
    }
  }

  function teardown() {
    restoreFetch?.();
    restoreFetch = undefined;
    try {
      bridge().disconnect();
    } catch {
      // Not connected, or the module never loaded. Nothing to tear down.
    }
  }

  function disconnect() {
    teardown();
    active = null;
    pendingToken = "";
    history.replaceState(null, "", location.pathname + location.search);
    connectionState.set("disconnected");
    phase = "servers";
  }

  /**
   * Connects to whatever "#tc..." token the URL currently names.
   *
   * A saved entry brings its API key along; an unknown token connects without
   * one and prefills the form, so it can be saved after it works.
   *
   * This also runs on hashchange, because pasting a token onto the end of an
   * already-open page is a same-document navigation: nothing reloads, and
   * without a listener the page would sit there ignoring it. Connecting sets
   * the hash itself, which is why this only acts from the server list.
   */
  function connectFromHash() {
    if (phase !== "servers") return;
    const token = decodeURIComponent(location.hash.replace(/^#/, "")).trim();
    if (!token.startsWith("tc")) return;

    const saved = findByToken(get(tailcatServers), token);
    if (!saved) pendingToken = token;
    void connect(saved ?? { id: newServerID(), name: token, token, apiKey: "", derpMapURL: "" });
  }

  onMount(() => {
    const cleanupTheme = initSystemThemeListener();
    connectFromHash();
    window.addEventListener("hashchange", connectFromHash);

    return () => {
      window.removeEventListener("hashchange", connectFromHash);
      cleanupTheme();
      teardown();
    };
  });

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
    document.title = `${icon} llama-swap Playground`;
  });
</script>

<div class="flex h-screen min-h-0 flex-col">
  {#if phase === "connected"}
    <header class="flex shrink-0 items-center gap-2 border-b px-4 py-2">
      <span class="min-w-0 truncate font-medium">{active?.name}</span>
      <span class="text-muted-foreground text-xs">over Tailcat</span>
      <div class="flex-1"></div>
      <Button size="sm" variant="ghost" onclick={() => fetchPlaygroundModels()}>
        <RefreshCw class="size-4" />
        Refresh models
      </Button>
      <Button size="sm" variant="outline" onclick={disconnect}>Disconnect</Button>
    </header>
    <main class="min-h-0 flex-1 overflow-auto p-4">
      <Playground />
    </main>
  {:else if phase === "connecting"}
    <div class="flex flex-1 items-center justify-center p-4">
      <Card.Root class="w-full max-w-md p-6">
        <Card.Content class="flex flex-col items-center gap-3 p-0 text-center">
          <LoaderCircle class="text-muted-foreground size-6 animate-spin" />
          <div class="font-medium">{status}</div>
          <p class="text-muted-foreground text-sm">
            Connecting to {active?.name}. The first connection takes longer while the WebAssembly
            module loads and the relay handshake completes.
          </p>
          <Button variant="ghost" size="sm" onclick={disconnect}>Cancel</Button>
        </Card.Content>
      </Card.Root>
    </div>
  {:else}
    <main class="min-h-0 flex-1 overflow-auto">
      {#if error}
        <div class="mx-auto w-full max-w-2xl px-4 pt-4">
          <div
            class="border-destructive/40 bg-destructive/10 text-destructive rounded-lg border p-3 text-sm"
            role="alert"
          >
            {error}
          </div>
        </div>
      {/if}
      <ServerManager {nodeKey} {keyRegenerated} initialToken={pendingToken} onconnect={connect} />
    </main>
  {/if}
</div>
