<script lang="ts">
  import { models } from "../../stores/api";
  import { persistentStore } from "../../stores/persistent";
  import { streamChatCompletion } from "../../lib/chatApi";

  type Status = "waiting" | "streaming" | "done" | "error";
  type RunState = {
    status: Status;
    content: string;
    elapsedMs: number;
    error?: string;
  };
  type TestEntry = { id: string; model: string };

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

  function clearTestList() {
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

  async function runOne(entry: TestEntry, signal: AbortSignal) {
    const start = performance.now();
    runs[entry.id] = { status: "streaming", content: "", elapsedMs: 0 };

    const ticker = window.setInterval(() => {
      const prev = runs[entry.id];
      if (!prev || prev.status !== "streaming") return;
      runs[entry.id] = { ...prev, elapsedMs: performance.now() - start };
    }, 50);

    try {
      const stream = streamChatCompletion(
        entry.model,
        [{ role: "user", content: $promptStore }],
        signal,
        { endpoint: "v1/chat/completions", max_tokens: $maxTokensStore }
      );
      for await (const chunk of stream) {
        if (chunk.done) break;
        const prev = runs[entry.id];
        if (!prev) break;
        runs[entry.id] = {
          ...prev,
          content: prev.content + (chunk.reasoning_content ?? "") + (chunk.content ?? ""),
          elapsedMs: performance.now() - start,
        };
      }
      const prev = runs[entry.id];
      if (prev) {
        runs[entry.id] = { ...prev, status: "done", elapsedMs: performance.now() - start };
      }
    } catch (err) {
      const prev = runs[entry.id] ?? { status: "waiting", content: "", elapsedMs: 0 };
      const aborted = err instanceof Error && err.name === "AbortError";
      runs[entry.id] = {
        ...prev,
        status: "error",
        elapsedMs: performance.now() - start,
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
      initial[e.id] = { status: "waiting", content: "", elapsedMs: 0 };
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
  <!-- Left column: model picker, test list, settings -->
  <div class="md:w-80 shrink-0 flex flex-col gap-3 min-h-0">
    <!-- Run controls -->
    <div class="flex items-center gap-2">
      {#if isRunning}
        <button class="btn bg-red-500 hover:bg-red-600 text-white border-red-500" onclick={stop}>
          <span class="inline-block w-3 h-3 bg-white align-middle mr-2"></span>Stop
        </button>
      {:else}
        <button
          class="btn bg-primary text-btn-primary-text hover:opacity-90"
          onclick={run}
          disabled={!canRun}
          title={$testListStore.length === 0 ? "Add models to the test list first" : "Run concurrent requests"}
        >
          <span class="inline-block align-middle mr-2" aria-hidden="true">▶</span>Go
        </button>
      {/if}
      <button class="btn btn--sm" onclick={clearTestList} disabled={isRunning || $testListStore.length === 0}>
        Clear
      </button>
    </div>

    <!-- Test list -->
    <div class="flex flex-col min-h-0">
      <div class="text-xs font-medium text-txtsecondary mb-1 flex items-center justify-between">
        <span>Test list ({$testListStore.length})</span>
        <span class="text-[10px]">drag to reorder</span>
      </div>
      <div class="border border-gray-200 dark:border-white/10 rounded overflow-y-auto" style="max-height: 240px;">
        {#if $testListStore.length === 0}
          <div class="p-3 text-sm text-txtsecondary text-center">
            Click a model below to add it
          </div>
        {:else}
          <ul class="divide-y divide-gray-100 dark:divide-white/5">
            {#each $testListStore as entry, i (entry.id)}
              <li
                class="flex items-center gap-2 px-2 py-1.5 text-sm {dragOverIndex === i && dragIndex !== i ? 'bg-primary/10' : ''} {dragIndex === i ? 'opacity-40' : ''}"
                draggable={!isRunning}
                ondragstart={(e) => onDragStart(i, e)}
                ondragover={(e) => onDragOver(i, e)}
                ondrop={(e) => onDrop(i, e)}
                ondragend={onDragEnd}
              >
                <span class="text-txtsecondary cursor-grab select-none" aria-hidden="true">⋮⋮</span>
                <span class="text-txtsecondary tabular-nums w-5 text-right">{i + 1}.</span>
                <span class="flex-1 truncate" title={entry.model}>{entry.model}</span>
                <button
                  class="w-6 h-6 flex items-center justify-center text-txtsecondary hover:text-red-500 transition-colors rounded disabled:opacity-30"
                  onclick={() => removeEntry(entry.id)}
                  disabled={isRunning}
                  aria-label="Remove from test list"
                  tabindex="-1"
                >
                  ×
                </button>
              </li>
            {/each}
          </ul>
        {/if}
      </div>
    </div>

    <!-- Available models -->
    <div class="flex flex-col min-h-0 flex-1">
      <div class="text-xs font-medium text-txtsecondary mb-1">Available models</div>
      <div class="flex-1 border border-gray-200 dark:border-white/10 rounded overflow-y-auto min-h-0">
        {#if !hasModels}
          <div class="p-3 text-sm text-txtsecondary text-center">No models configured.</div>
        {:else}
          <ul class="divide-y divide-gray-100 dark:divide-white/5">
            {#each availableModels as m (m.id)}
              <li>
                <button
                  class="w-full text-left px-2 py-1.5 text-sm hover:bg-secondary-hover transition-colors disabled:opacity-50 disabled:cursor-not-allowed"
                  onclick={() => addModel(m.id)}
                  disabled={isRunning}
                  title="Add {m.id} to test list"
                >
                  <span class="truncate inline-block w-full">{m.id}</span>
                </button>
              </li>
            {/each}
          </ul>
        {/if}
      </div>
    </div>

    <!-- Settings -->
    <div class="flex flex-col gap-2 border-t border-gray-200 dark:border-white/10 pt-3">
      <div class="flex items-center justify-between">
        <label for="concurrency-prompt" class="text-xs font-medium text-txtsecondary">Prompt</label>
        <button
          class="text-[10px] text-txtsecondary hover:text-txtmain underline"
          onclick={resetDefaults}
          disabled={isRunning}
        >
          reset defaults
        </button>
      </div>
      <textarea
        id="concurrency-prompt"
        class="w-full px-2 py-1.5 text-sm rounded border border-gray-200 dark:border-white/10 bg-surface focus:outline-none focus:ring-2 focus:ring-primary resize-none"
        rows="3"
        bind:value={$promptStore}
        disabled={isRunning}
      ></textarea>
      <label for="concurrency-max-tokens" class="text-xs font-medium text-txtsecondary">max_tokens</label>
      <input
        id="concurrency-max-tokens"
        type="number"
        min="1"
        class="w-full px-2 py-1.5 text-sm rounded border border-gray-200 dark:border-white/10 bg-surface focus:outline-none focus:ring-2 focus:ring-primary"
        bind:value={$maxTokensStore}
        disabled={isRunning}
      />
    </div>
  </div>

  <!-- Right column: result panels -->
  <div class="flex-1 min-w-0 min-h-0 overflow-y-auto">
    {#if $testListStore.length === 0}
      <div class="h-full flex items-center justify-center text-txtsecondary text-sm">
        Add models to the test list, then press Go.
      </div>
    {:else}
      <div class="grid grid-cols-1 lg:grid-cols-2 xl:grid-cols-3 gap-3">
        {#each $testListStore as entry, i (entry.id)}
          {@const run = runs[entry.id]}
          {@const status = run?.status ?? "waiting"}
          <div class="border border-gray-200 dark:border-white/10 rounded flex flex-col min-h-0" style="height: 280px;">
            <div class="shrink-0 flex items-center gap-2 px-2 py-1.5 border-b border-gray-200 dark:border-white/10 bg-secondary/40">
              <span class="text-txtsecondary tabular-nums text-xs w-5 text-right">{i + 1}.</span>
              <span class="flex-1 truncate text-sm font-medium" title={entry.model}>{entry.model}</span>
              <span class="text-xs tabular-nums text-txtsecondary">
                {run ? formatElapsed(run.elapsedMs) : "—"}
              </span>
              <span class="status text-[10px] {statusBadgeClass(status)}">{status}</span>
            </div>
            <div class="flex-1 min-h-0 overflow-y-auto font-mono text-xs whitespace-pre-wrap px-2 py-1.5">
              {#if run}
                {run.content}
                {#if run.status === "error" && run.error}
                  <span class="text-red-500">{run.content ? "\n\n" : ""}[error] {run.error}</span>
                {/if}
              {/if}
            </div>
          </div>
        {/each}
      </div>
    {/if}
  </div>
</div>
