<script lang="ts">
  import { models } from "../../stores/api";
  import { persistentStore } from "../../stores/persistent";
  import { streamChatCompletion } from "../../lib/chatApi";
  import { Button } from "$lib/components/ui/button/index.js";
  import { Input } from "$lib/components/ui/input/index.js";
  import { Textarea } from "$lib/components/ui/textarea/index.js";
  import { X } from "@lucide/svelte";

  type Status = "waiting" | "streaming" | "done" | "error";
  type Phase = "waiting" | "loading" | "reasoning" | "content";
  type RunState = {
    status: Status;
    loadingText: string;
    reasoningContent: string;
    content: string;
    loadingDone: boolean;
    waitingMs: number;
    loadingMs: number;
    reasoningMs: number;
    contentMs: number;
    phase: Phase;
    elapsedMs: number;
    error?: string;
  };
  type TestEntry = { id: string; model: string };

  const LOAD_MARKER = "━━━━━";

  const DEFAULT_PROMPT = "Write a few sentences about the history of computing.";
  const DEFAULT_MAX_TOKENS = 256;

  const promptStore = persistentStore<string>("concurrency-prompt", DEFAULT_PROMPT);
  const maxTokensStore = persistentStore<number>("concurrency-max-tokens", DEFAULT_MAX_TOKENS);
  const testListStore = persistentStore<TestEntry[]>("concurrency-test-list", []);

  let runs = $state<Record<string, RunState>>({});
  let isRunning = $state(false);
  let abortController: AbortController | null = null;
  let dragIndex = $state<number | null>(null);
  let dragOverIndex = $state<number | null>(null);

  const timelineCollapsedStore = persistentStore<boolean>("concurrency-timeline-collapsed", false);

  let timelineMaxMs = $derived(Math.max(100, ...Object.values(runs).map((r) => r.elapsedMs)));

  let availableModels = $derived($models.filter((m) => !m.unlisted));
  let hasModels = $derived(availableModels.length > 0);
  let canRun = $derived(!isRunning && $testListStore.length > 0 && $promptStore.trim() !== "");

  function newId(): string {
    if (typeof crypto !== "undefined" && "randomUUID" in crypto) {
      return crypto.randomUUID();
    }
    return `${Date.now()}-${Math.random().toString(36).slice(2)}`;
  }

  function addModel(modelId: string) {
    if (isRunning) return;
    testListStore.update((list) => [...list, { id: newId(), model: modelId }]);
  }

  function removeEntry(id: string) {
    if (isRunning) return;
    testListStore.update((list) => list.filter((e) => e.id !== id));
    const next = { ...runs };
    delete next[id];
    runs = next;
  }

  function clearAll() {
    if (isRunning) return;
    testListStore.set([]);
    runs = {};
  }

  function onDragStart(i: number, e: DragEvent) {
    if (isRunning) return;
    dragIndex = i;
    if (e.dataTransfer) {
      e.dataTransfer.effectAllowed = "move";
      e.dataTransfer.setData("text/plain", String(i));
    }
  }

  function onDragOver(i: number, e: DragEvent) {
    if (isRunning || dragIndex === null) return;
    e.preventDefault();
    if (e.dataTransfer) e.dataTransfer.dropEffect = "move";
    dragOverIndex = i;
  }

  function onDrop(i: number, e: DragEvent) {
    if (isRunning || dragIndex === null) return;
    e.preventDefault();
    const from = dragIndex;
    const to = i;
    dragIndex = null;
    dragOverIndex = null;
    if (from === to) return;
    testListStore.update((list) => {
      const next = [...list];
      const [moved] = next.splice(from, 1);
      next.splice(to, 0, moved);
      return next;
    });
  }

  function onDragEnd() {
    dragIndex = null;
    dragOverIndex = null;
  }

  function emptyRun(): RunState {
    return {
      status: "waiting",
      loadingText: "",
      reasoningContent: "",
      content: "",
      loadingDone: false,
      waitingMs: 0,
      loadingMs: 0,
      reasoningMs: 0,
      contentMs: 0,
      phase: "waiting",
      elapsedMs: 0,
    };
  }

  // Detect and split the llama-swap loading block (wrapped in ━━━━━ markers,
  // delivered as reasoning_content) from the model's own reasoning tokens.
  function ingestReasoning(
    prev: RunState,
    chunk: string
  ): { loadingText: string; reasoningContent: string; loadingDone: boolean; nowPhase: Phase } {
    if (prev.loadingDone) {
      return {
        loadingText: prev.loadingText,
        reasoningContent: prev.reasoningContent + chunk,
        loadingDone: true,
        nowPhase: "reasoning",
      };
    }

    const combined = prev.loadingText + chunk;
    // Not enough to decide whether this is a loading marker
    if (combined.length < LOAD_MARKER.length) {
      if (LOAD_MARKER.startsWith(combined)) {
        return { loadingText: combined, reasoningContent: prev.reasoningContent, loadingDone: false, nowPhase: "loading" };
      }
      return {
        loadingText: "",
        reasoningContent: prev.reasoningContent + combined,
        loadingDone: true,
        nowPhase: "reasoning",
      };
    }

    if (!combined.startsWith(LOAD_MARKER)) {
      return {
        loadingText: "",
        reasoningContent: prev.reasoningContent + combined,
        loadingDone: true,
        nowPhase: "reasoning",
      };
    }

    // We're inside a loading block — look for the closing marker
    const closingIdx = combined.indexOf(LOAD_MARKER, LOAD_MARKER.length);
    if (closingIdx < 0) {
      return { loadingText: combined, reasoningContent: prev.reasoningContent, loadingDone: false, nowPhase: "loading" };
    }
    const newlineIdx = combined.indexOf("\n", closingIdx);
    const sliceEnd = newlineIdx >= 0 ? newlineIdx + 1 : combined.length;
    const loadingPart = combined.substring(0, sliceEnd);
    // Strip the trailing " \n" the loader sends after the closing marker
    const remainder = combined.substring(sliceEnd).replace(/^[ \t]*\n?/, "");
    return {
      loadingText: loadingPart,
      reasoningContent: prev.reasoningContent + remainder,
      loadingDone: true,
      nowPhase: remainder ? "reasoning" : "waiting",
    };
  }

  async function runOne(entry: TestEntry, signal: AbortSignal) {
    const start = performance.now();
    let phaseStart = start;
    runs[entry.id] = { ...emptyRun(), status: "streaming" };

    const accrue = (
      prev: RunState,
      now: number
    ): { waitingMs: number; loadingMs: number; reasoningMs: number; contentMs: number } => {
      const delta = now - phaseStart;
      const base = {
        waitingMs: prev.waitingMs,
        loadingMs: prev.loadingMs,
        reasoningMs: prev.reasoningMs,
        contentMs: prev.contentMs,
      };
      if (prev.phase === "waiting") return { ...base, waitingMs: base.waitingMs + delta };
      if (prev.phase === "loading") return { ...base, loadingMs: base.loadingMs + delta };
      if (prev.phase === "reasoning") return { ...base, reasoningMs: base.reasoningMs + delta };
      if (prev.phase === "content") return { ...base, contentMs: base.contentMs + delta };
      return base;
    };

    const ticker = window.setInterval(() => {
      const prev = runs[entry.id];
      if (!prev || prev.status !== "streaming") return;
      const now = performance.now();
      const accrued = accrue(prev, now);
      phaseStart = now;
      runs[entry.id] = { ...prev, ...accrued, elapsedMs: now - start };
    }, 50);

    try {
      const stream = streamChatCompletion(entry.model, [{ role: "user", content: $promptStore }], signal, {
        endpoint: "v1/chat/completions",
        max_tokens: $maxTokensStore,
      });
      for await (const chunk of stream) {
        if (chunk.done) break;
        const prev = runs[entry.id];
        if (!prev) break;
        const now = performance.now();
        const accrued = accrue(prev, now);
        phaseStart = now;

        let nextPhase: Phase = prev.phase;
        let loadingText = prev.loadingText;
        let reasoningContent = prev.reasoningContent;
        let loadingDone = prev.loadingDone;

        if (chunk.reasoning_content) {
          const parsed = ingestReasoning(prev, chunk.reasoning_content);
          loadingText = parsed.loadingText;
          reasoningContent = parsed.reasoningContent;
          loadingDone = parsed.loadingDone;
          nextPhase = parsed.nowPhase;
        }
        if (chunk.content) nextPhase = "content";

        runs[entry.id] = {
          ...prev,
          ...accrued,
          loadingText,
          reasoningContent,
          content: prev.content + (chunk.content ?? ""),
          loadingDone,
          phase: nextPhase,
          elapsedMs: now - start,
        };
      }
      const prev = runs[entry.id];
      if (prev) {
        const now = performance.now();
        const accrued = accrue(prev, now);
        runs[entry.id] = { ...prev, ...accrued, status: "done", elapsedMs: now - start };
      }
    } catch (err) {
      const prev = runs[entry.id] ?? emptyRun();
      const now = performance.now();
      const accrued = accrue(prev, now);
      const aborted = err instanceof Error && err.name === "AbortError";
      runs[entry.id] = {
        ...prev,
        ...accrued,
        status: "error",
        elapsedMs: now - start,
        error: aborted ? "aborted" : err instanceof Error ? err.message : String(err),
      };
    } finally {
      window.clearInterval(ticker);
    }
  }

  async function run() {
    if (!canRun) return;
    const entries = $testListStore;
    const initial: Record<string, RunState> = {};
    for (const e of entries) {
      initial[e.id] = emptyRun();
    }
    runs = initial;
    isRunning = true;
    abortController = new AbortController();
    try {
      await Promise.allSettled(entries.map((e) => runOne(e, abortController!.signal)));
    } finally {
      isRunning = false;
      abortController = null;
    }
  }

  function stop() {
    abortController?.abort();
  }

  function waitingBarClass(run: RunState): string {
    if (run.status === "error" && run.phase === "waiting") return "bg-red-500";
    return "bg-slate-200 dark:bg-white/10";
  }

  function loadingBarClass(run: RunState): string {
    if (run.status === "error" && run.phase === "loading") return "bg-red-500";
    return "bg-slate-400 dark:bg-slate-500";
  }

  function reasoningBarClass(run: RunState): string {
    if (run.status === "error" && run.phase === "reasoning") return "bg-red-500";
    return "bg-purple-500";
  }

  function contentBarClass(run: RunState): string {
    if (run.status === "error" && run.phase === "content") return "bg-red-500";
    if (run.status === "done") return "bg-green-500";
    return "bg-amber-400 dark:bg-amber-500";
  }

  function niceStepMs(maxMs: number): number {
    if (maxMs <= 500) return 100;
    if (maxMs <= 2000) return 500;
    if (maxMs <= 5000) return 1000;
    if (maxMs <= 20000) return 5000;
    if (maxMs <= 60000) return 10000;
    return 30000;
  }

  function formatTickMs(ms: number): string {
    if (ms < 1000) return `${ms}`;
    return `${(ms / 1000).toFixed(ms % 1000 === 0 ? 0 : 1)}s`;
  }

  let timelineTicks = $derived.by(() => {
    const step = niceStepMs(timelineMaxMs);
    const ticks: number[] = [];
    for (let t = 0; t <= timelineMaxMs; t += step) ticks.push(t);
    return ticks;
  });

  function statusBadgeClass(status: Status): string {
    switch (status) {
      case "waiting":
        return "bg-gray-200 text-gray-700 dark:bg-gray-700 dark:text-gray-200";
      case "streaming":
        return "bg-amber-200 text-amber-900 dark:bg-amber-500/30 dark:text-amber-200";
      case "done":
        return "bg-green-200 text-green-900 dark:bg-green-500/30 dark:text-green-200";
      case "error":
        return "bg-red-200 text-red-900 dark:bg-red-500/30 dark:text-red-200";
    }
  }

  function formatElapsed(ms: number): string {
    if (ms < 1000) return `${Math.round(ms)}ms`;
    return `${(ms / 1000).toFixed(2)}s`;
  }

  function resetDefaults() {
    promptStore.set(DEFAULT_PROMPT);
    maxTokensStore.set(DEFAULT_MAX_TOKENS);
  }
</script>

<div class="flex flex-col md:flex-row gap-4 h-full min-h-0">
  <!-- Left column: run controls, model picker, settings -->
  <div class="md:w-72 shrink-0 flex flex-col gap-3 min-h-0">
    <!-- Run controls -->
    <div class="flex items-center gap-2">
      {#if isRunning}
        <Button variant="destructive" onclick={stop}>
          <span class="mr-1 inline-block h-3 w-3 bg-current align-middle"></span>Stop
        </Button>
      {:else}
        <Button
          onclick={run}
          disabled={!canRun}
          title={$testListStore.length === 0 ? "Add models from the list below" : "Run concurrent requests"}
        >
          <span class="mr-1 inline-block align-middle" aria-hidden="true">▶</span>Go
        </Button>
      {/if}
      <Button variant="outline" size="sm" onclick={clearAll} disabled={isRunning || $testListStore.length === 0}>
        Clear ({$testListStore.length})
      </Button>
    </div>

    <!-- Available models -->
    <div class="flex flex-col min-h-0 flex-1">
      <div class="text-xs font-medium text-muted-foreground mb-1">
        Models <span class="text-[10px] font-normal">— click to queue (add the same model more than once to test parallel requests)</span>
      </div>
      <div class="flex-1 border border-border rounded-md overflow-y-auto min-h-0">
        {#if !hasModels}
          <div class="p-3 text-sm text-muted-foreground text-center">No models configured.</div>
        {:else}
          <ul class="divide-y divide-gray-100 dark:divide-white/5">
            {#each availableModels as m (m.id)}
              <li>
                <Button
                  variant="ghost"
                  class="w-full justify-start px-2 py-1.5 text-sm h-auto font-normal"
                  onclick={() => addModel(m.id)}
                  disabled={isRunning}
                  title="Add {m.id}"
                >
                  <span class="text-primary" aria-hidden="true">+</span>
                  <span class="truncate flex-1">{m.id}</span>
                </Button>
              </li>
            {/each}
          </ul>
        {/if}
      </div>
    </div>

    <!-- Settings -->
    <div class="flex flex-col gap-2 border-t border-border pt-3">
      <div class="flex items-center justify-between">
        <label for="concurrency-prompt" class="text-xs font-medium text-muted-foreground">Prompt</label>
        <Button
          variant="link"
          size="sm"
          class="h-auto p-0 text-[10px]"
          onclick={resetDefaults}
          disabled={isRunning}
        >
          reset defaults
        </Button>
      </div>
      <Textarea
        id="concurrency-prompt"
        class="resize-none text-sm"
        rows={3}
        bind:value={$promptStore}
        disabled={isRunning}
      ></Textarea>
      <label for="concurrency-max-tokens" class="text-xs font-medium text-muted-foreground">max_tokens</label>
      <Input
        id="concurrency-max-tokens"
        type="number"
        min="1"
        class="h-8 text-sm"
        bind:value={$maxTokensStore}
        disabled={isRunning}
      />
    </div>
  </div>

  <!-- Right column: result panels (draggable to reorder) -->
  <div class="flex-1 min-w-0 min-h-0 overflow-y-auto">
    {#if $testListStore.length === 0}
      <div class="h-full flex items-center justify-center px-6">
        <div class="max-w-md text-sm text-muted-foreground space-y-4">
          <h4 class="text-base font-semibold text-foreground pb-0">Load Test</h4>
          <p>
            Fire several streaming chat completions at llama-swap at the same time to see how it handles parallel
            loading and concurrent inference. Each request streams into its own panel with a live timer and status.
          </p>
          <ol class="list-decimal list-inside space-y-1">
            <li>Click models on the left to queue them — repeat a model to hit it with parallel requests.</li>
            <li>Tweak the prompt and <code>max_tokens</code> if you want.</li>
            <li>Press <span class="font-semibold text-foreground">Go</span> to launch them concurrently.</li>
          </ol>
          <p class="text-xs">Tip: drag a result card's header to reorder, or hit × to drop it.</p>
        </div>
      </div>
    {:else}
      <!-- Gantt-style timeline -->
      <div class="mb-3 border border-border rounded-md">
          <button
            class="w-full flex items-center gap-2 px-2 py-1.5 text-xs font-medium text-muted-foreground hover:bg-accent transition-colors {$timelineCollapsedStore ? 'rounded-md' : 'rounded-t border-b border-border'}"
            onclick={() => timelineCollapsedStore.update((v) => !v)}
            aria-expanded={!$timelineCollapsedStore}
          >
            <svg
              class="w-4 h-4 transition-transform {$timelineCollapsedStore ? '-rotate-90' : ''}"
              fill="none"
              stroke="currentColor"
              viewBox="0 0 24 24"
              aria-hidden="true"
            >
              <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M19 9l-7 7-7-7"></path>
            </svg>
            <span>Timeline</span>
            {#if !$timelineCollapsedStore}
              <span class="flex items-center gap-3 text-[10px] text-muted-foreground font-normal ml-3" aria-hidden="true">
                <span class="flex items-center gap-1"><span class="inline-block w-2.5 h-2.5 rounded-sm bg-slate-200 dark:bg-white/10 border border-gray-300 dark:border-white/10"></span>waiting</span>
                <span class="flex items-center gap-1"><span class="inline-block w-2.5 h-2.5 rounded-sm bg-slate-400 dark:bg-slate-500"></span>loading</span>
                <span class="flex items-center gap-1"><span class="inline-block w-2.5 h-2.5 rounded-sm bg-purple-500"></span>reasoning</span>
                <span class="flex items-center gap-1"><span class="inline-block w-2.5 h-2.5 rounded-sm bg-amber-400 dark:bg-amber-500"></span>streaming</span>
                <span class="flex items-center gap-1"><span class="inline-block w-2.5 h-2.5 rounded-sm bg-green-500"></span>done</span>
                <span class="flex items-center gap-1"><span class="inline-block w-2.5 h-2.5 rounded-sm bg-red-500"></span>error</span>
              </span>
            {/if}
            <span class="ml-auto tabular-nums text-muted-foreground">
              max {formatElapsed(timelineMaxMs)} · {$testListStore.length} request{$testListStore.length === 1 ? "" : "s"}
            </span>
          </button>
          {#if !$timelineCollapsedStore}
            <div class="px-2 py-2">
              <!-- X axis ticks -->
              <div class="flex" aria-hidden="true">
                <div class="w-40 shrink-0"></div>
                <div class="relative flex-1 h-4 border-b border-border">
                  {#each timelineTicks as t (t)}
                    <div
                      class="absolute top-0 bottom-0 border-l border-border"
                      style="left: {(t / timelineMaxMs) * 100}%;"
                    >
                      <span class="absolute -top-0.5 left-1 text-[10px] text-muted-foreground tabular-nums">{formatTickMs(t)}</span>
                    </div>
                  {/each}
                </div>
                <div class="w-16 shrink-0"></div>
              </div>
              <!-- Bars -->
              <div class="flex flex-col gap-1 mt-1">
                {#each $testListStore as entry, i (entry.id)}
                  {@const run = runs[entry.id]}
                  {@const waitingPct = run ? (run.waitingMs / timelineMaxMs) * 100 : 0}
                  {@const loadingPct = run ? (run.loadingMs / timelineMaxMs) * 100 : 0}
                  {@const reasoningPct = run ? (run.reasoningMs / timelineMaxMs) * 100 : 0}
                  {@const contentPct = run ? (run.contentMs / timelineMaxMs) * 100 : 0}
                  <div class="flex items-center text-xs">
                    <div class="w-40 shrink-0 flex items-center gap-1 pr-2 text-muted-foreground">
                      <span class="tabular-nums w-5 text-right">{i + 1}.</span>
                      <span class="truncate" title={entry.model}>{entry.model}</span>
                    </div>
                    <div class="relative flex-1 h-4">
                      {#each timelineTicks as t (t)}
                        <div
                          class="absolute top-0 bottom-0 border-l border-border"
                          style="left: {(t / timelineMaxMs) * 100}%;"
                          aria-hidden="true"
                        ></div>
                      {/each}
                      {#if run && run.waitingMs > 0}
                        <div
                          class="absolute top-0.5 bottom-0.5 rounded-l-sm transition-all {waitingBarClass(run)}"
                          style="left: 0; width: {waitingPct}%;"
                          title="waiting {formatElapsed(run.waitingMs)}"
                        ></div>
                      {/if}
                      {#if run && run.loadingMs > 0}
                        <div
                          class="absolute top-0.5 bottom-0.5 transition-all {loadingBarClass(run)} {run.waitingMs === 0 ? 'rounded-l-sm' : ''}"
                          style="left: {waitingPct}%; width: {loadingPct}%;"
                          title="loading {formatElapsed(run.loadingMs)}"
                        ></div>
                      {/if}
                      {#if run && run.reasoningMs > 0}
                        <div
                          class="absolute top-0.5 bottom-0.5 transition-all {reasoningBarClass(run)} {run.waitingMs === 0 && run.loadingMs === 0 ? 'rounded-l-sm' : ''}"
                          style="left: {waitingPct + loadingPct}%; width: {reasoningPct}%;"
                          title="reasoning {formatElapsed(run.reasoningMs)}"
                        ></div>
                      {/if}
                      {#if run && run.contentMs > 0}
                        <div
                          class="absolute top-0.5 bottom-0.5 transition-all {contentBarClass(run)} {run.waitingMs === 0 && run.loadingMs === 0 && run.reasoningMs === 0 ? 'rounded-l-sm' : ''} {run.status === 'done' || run.status === 'error' ? 'rounded-r-sm' : ''}"
                          style="left: {waitingPct + loadingPct + reasoningPct}%; width: {contentPct}%;"
                          title="content {formatElapsed(run.contentMs)}"
                        ></div>
                      {/if}
                    </div>
                    <div class="w-16 shrink-0 pl-2 tabular-nums text-muted-foreground text-right">
                      {run ? formatElapsed(run.elapsedMs) : "—"}
                    </div>
                  </div>
                {/each}
              </div>
            </div>
          {/if}
        </div>
      <div class="grid grid-cols-1 lg:grid-cols-2 xl:grid-cols-3 gap-3" role="list">
        {#each $testListStore as entry, i (entry.id)}
          {@const run = runs[entry.id]}
          {@const status = run?.status ?? "waiting"}
          <div
            class="border rounded-md flex flex-col min-h-0 transition-colors {dragOverIndex === i && dragIndex !== i
              ? 'border-primary ring-2 ring-primary/40'
              : 'border-border'} {dragIndex === i ? 'opacity-40' : ''}"
            style="height: 280px;"
            role="listitem"
            ondragover={(e) => onDragOver(i, e)}
            ondrop={(e) => onDrop(i, e)}
          >
            <div
              class="shrink-0 flex items-center gap-2 px-2 py-1.5 border-b border-border bg-secondary/40 rounded-t"
              draggable={!isRunning}
              role="button"
              tabindex="-1"
              aria-label="Drag to reorder {entry.model}"
              ondragstart={(e) => onDragStart(i, e)}
              ondragend={onDragEnd}
              class:cursor-grab={!isRunning}
              title={isRunning ? "" : "Drag to reorder"}
            >
              <span class="text-muted-foreground select-none" aria-hidden="true">⋮⋮</span>
              <span class="text-muted-foreground tabular-nums text-xs w-5 text-right">{i + 1}.</span>
              <span class="flex-1 truncate text-sm font-medium" title={entry.model}>{entry.model}</span>
              <span class="text-xs tabular-nums text-muted-foreground">
                {run ? formatElapsed(run.elapsedMs) : "—"}
              </span>
              <span class="status text-[10px] {statusBadgeClass(status)}">{status}</span>
              <Button
                variant="ghost"
                size="icon-sm"
                class="h-5 w-5 text-muted-foreground hover:text-red-500"
                onclick={() => removeEntry(entry.id)}
                disabled={isRunning}
                aria-label="Remove"
                tabindex={-1}
              >
                <X class="size-3" />
              </Button>
            </div>
            <div class="flex-1 min-h-0 overflow-y-auto font-mono text-xs px-2 py-1.5">
              {#if run?.loadingText}
                <div class="bg-secondary/40 dark:bg-white/5 text-muted-foreground rounded-md px-2 py-1 mb-2 whitespace-pre-wrap">{run.loadingText.trim()}</div>
              {/if}
              {#if run?.reasoningContent}
                <div class="text-purple-700 dark:text-purple-300 whitespace-pre-wrap">{run.reasoningContent}</div>
              {/if}
              {#if run?.content}
                <div class="whitespace-pre-wrap {run.reasoningContent ? 'mt-2' : ''}">{run.content}</div>
              {/if}
              {#if run?.status === "error" && run?.error}
                <div class="text-red-500 mt-2">[error] {run.error}</div>
              {/if}
            </div>
          </div>
        {/each}
      </div>
    {/if}
  </div>
</div>
