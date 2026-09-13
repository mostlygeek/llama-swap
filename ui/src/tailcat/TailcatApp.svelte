<script lang="ts">
  import { onMount } from "svelte";
  import Playground from "../routes/Playground.svelte";
  import ServerManager from "./ServerManager.svelte";
  import { Button } from "$lib/components/ui/button/index.js";
  import * as Card from "$lib/components/ui/card/index.js";
  import { Check, Circle, LoaderCircle, RefreshCw } from "@lucide/svelte";
  import { isDarkMode, themeName, connectionState, initSystemThemeListener } from "../stores/theme";
  import { fetchPlaygroundModels } from "../stores/api";
  import { startTailcatWasm, type LoadStage } from "./wasm";
  import { bridge } from "./bridge";
  import { installTailcatFetch } from "./transport";
  import { activeServerID, tailcatClientKey, type TailcatServer } from "./servers";

  type Phase = "servers" | "connecting" | "connected";

  let phase = $state<Phase>("servers");
  let error = $state("");
  let nodeKey = $state("");
  let keyRegenerated = $state(false);
  let active = $state<TailcatServer | null>(null);
  let restoreFetch: (() => void) | undefined;

  // Where the module load is, shown on the server list as well as during a
  // connect, because the load starts at page open and can outlast the time it
  // takes to pick a server.
  type ModuleState = LoadStage | "ready" | "failed";
  let moduleState = $state<ModuleState>("fetching");
  let moduleDetail = $state("");
  let moduleError = $state("");
  let wasmReady: Promise<void>;

  const moduleMessages: Record<ModuleState, string> = {
    fetching: "Downloading the Tailcat module",
    decompressing: "Unpacking the Tailcat module",
    compiling: "Compiling the Tailcat module",
    starting: "Starting the Tailcat module",
    ready: "Tailcat module ready",
    failed: "The Tailcat module failed to load",
  };

  // The module has to be running before its client key exists, so this starts
  // on page load rather than on connect: the server form can show the node key
  // by the time anyone has finished typing a token into it.
  function loadModule() {
    moduleState = "fetching";
    moduleDetail = "";
    moduleError = "";
    wasmReady = startTailcatWasm((stage, detail) => {
      moduleState = stage;
      moduleDetail = detail;
    }).then(
      () => {
        const identity = bridge().identity($tailcatClientKey || null);
        nodeKey = identity.nodeKey;
        keyRegenerated = identity.regenerated;
        tailcatClientKey.set(identity.privateKeyJSON);
        moduleState = "ready";
        moduleDetail = "";
      },
      (e: unknown) => {
        moduleState = "failed";
        moduleError = e instanceof Error ? e.message : String(e);
        throw e;
      },
    );
    // Reported through moduleState; the rejection is also observed by whichever
    // connect awaits it.
    wasmReady.catch(() => {});
  }
  loadModule();

  // The connect sequence, shown as a checklist so a slow stage is visibly
  // still working rather than hung. The handshake is the usual one: it retries
  // for up to a minute when the token is stale or this browser's node key is
  // not on the node's allowlist.
  // "done" is a pause with every step ticked: a connect can finish in well
  // under a second, and a checklist that flashes past reads as a glitch. The
  // Go button counts it down and skips it when clicked.
  type StepID = "module" | "handshake" | "probe" | "models" | "done";
  const donePause = 2;
  let skipPause: (() => void) | undefined;
  const steps: { id: StepID; label: string }[] = [
    { id: "module", label: "Load the Tailcat module" },
    { id: "handshake", label: "Reach the node over Tailcat" },
    { id: "probe", label: "Check that the node answers HTTP" },
    { id: "models", label: "Read the model list" },
  ];
  let step = $state<StepID>("module");
  let stepDetail = $state("");
  let elapsed = $state(0);
  let stepStartedAt = 0;
  let ticker: ReturnType<typeof setInterval> | undefined;
  // Bumped by every connect and cancel, so a connect that was cancelled during
  // its pause cannot finish on top of the one started after it.
  let attempt = 0;

  const stepIndex = $derived(step === "done" ? steps.length : steps.findIndex((s) => s.id === step));
  // A handshake that has been retrying for a while is almost always one of
  // two configuration problems, so name them before the timeout does.
  const handshakeHint = $derived(step === "handshake" && elapsed >= 15);

  function setStep(id: StepID, detail = "") {
    step = id;
    stepDetail = detail;
    stepStartedAt = Date.now();
    elapsed = 0;
  }

  function stopTicker() {
    if (ticker) clearInterval(ticker);
    ticker = undefined;
  }

  /** Activating a server connects to it and remembers it for the next visit. */
  async function activate(server: TailcatServer) {
    active = server;
    activeServerID.set(server.id);
    error = "";
    phase = "connecting";
    connectionState.set("connecting");
    const thisAttempt = ++attempt;
    setStep("module");
    stopTicker();
    ticker = setInterval(() => {
      elapsed = Math.floor((Date.now() - stepStartedAt) / 1000);
    }, 1000);

    try {
      if (moduleState === "failed") loadModule();
      await wasmReady;

      setStep("handshake");
      await bridge().connect({
        token: server.token,
        privateKeyJSON: $tailcatClientKey,
        derpMapURL: server.derpMapURL,
        verbose: false,
        onProgress(stage, detail) {
          if (stage === "handshake") stepDetail = `attempt ${detail}`;
          else setStep("probe");
        },
      });

      restoreFetch?.();
      restoreFetch = installTailcatFetch(() => active?.apiKey ?? "");

      // The node is reachable by this point; this is the first request that
      // needs the API key, so it is where a credentials problem shows up.
      setStep("models");
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

      setStep("done");
      await new Promise<void>((resolve) => {
        skipPause = resolve;
        setTimeout(resolve, donePause * 1000);
      });
      skipPause = undefined;
      // Cancel during the pause has already torn the connection down.
      if (thisAttempt !== attempt) return;

      connectionState.set("connected");
      phase = "connected";
    } catch (e) {
      error = e instanceof Error ? e.message : String(e);
      connectionState.set("disconnected");
      phase = "servers";
      teardown();
    } finally {
      stopTicker();
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
    attempt++;
    stopTicker();
    teardown();
    active = null;
    connectionState.set("disconnected");
    phase = "servers";
  }

  onMount(() => {
    const cleanupTheme = initSystemThemeListener();
    return () => {
      cleanupTheme();
      stopTicker();
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
        <Card.Content class="flex flex-col gap-4 p-0">
          <div class="font-medium">
            {step === "done" ? "Connected to" : "Connecting to"}
            {active?.name}
          </div>
          <ol class="flex flex-col gap-2 text-sm" aria-live="polite">
            {#each steps as s, i (s.id)}
              {@const done = i < stepIndex}
              {@const current = i === stepIndex}
              <li class="flex items-start gap-2" class:text-muted-foreground={!current}>
                {#if done}
                  <Check class="mt-0.5 size-4 shrink-0 text-green-600 dark:text-green-500" />
                {:else if current}
                  <LoaderCircle class="mt-0.5 size-4 shrink-0 animate-spin" />
                {:else}
                  <Circle class="mt-0.5 size-4 shrink-0 opacity-40" />
                {/if}
                <div class="min-w-0">
                  <div>{s.label}</div>
                  {#if current}
                    <div class="text-muted-foreground text-xs">
                      {#if s.id === "module"}
                        {moduleMessages[moduleState]}{moduleDetail ? `, ${moduleDetail}` : ""}
                      {:else if stepDetail}
                        {stepDetail}
                      {/if}
                      {#if elapsed >= 3}
                        &middot; {elapsed}s
                      {/if}
                    </div>
                  {/if}
                </div>
              </li>
            {/each}
          </ol>
          {#if handshakeHint}
            <p class="text-muted-foreground text-xs">
              Still trying. If this does not complete, check the token, and that this browser's node
              key is in the node's <code>tailcat.allow</code> list. It gives up after a minute.
            </p>
          {/if}
          {#if step === "done"}
            <Button size="sm" class="self-center" onclick={() => skipPause?.()}>
              Go ({Math.max(donePause - elapsed, 1)})
            </Button>
          {:else}
            <Button variant="ghost" size="sm" class="self-center" onclick={disconnect}>Cancel</Button>
          {/if}
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
      <ServerManager
        {nodeKey}
        {keyRegenerated}
        moduleMessage={moduleMessages[moduleState] + (moduleDetail ? `, ${moduleDetail}` : "")}
        {moduleError}
        onretry={loadModule}
        onactivate={activate}
      />
    </main>
  {/if}
</div>
