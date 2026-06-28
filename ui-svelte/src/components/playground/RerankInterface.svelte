<script lang="ts">
  import { models } from "../../stores/api";
  import { persistentStore } from "../../stores/persistent";
  import { rerank } from "../../lib/rerankApi";
  import { playgroundStores } from "../../stores/playgroundActivity";
  import ModelSelector from "./ModelSelector.svelte";
  import { Button } from "$lib/components/ui/button/index.js";
  import { Input } from "$lib/components/ui/input/index.js";
  import { Textarea } from "$lib/components/ui/textarea/index.js";
  import * as ToggleGroup from "$lib/components/ui/toggle-group/index.js";

  type RerankRow = { doc: string; score: number | null };
  type SortOrder = "none" | "asc" | "desc";
  type EditorMode = "table" | "json";

  const selectedModelStore = persistentStore<string>("playground-rerank-model", "");

  const defaultQuery = "How do LLM's work?";
  const defaultDocs = [
    "Large language models (LLMs) use transformer architectures to predict the next token in a sequence based on massive amounts of text data.",
    "LLMs are trained on diverse internet text, learning statistical patterns of language that allow them to generate coherent responses.",
    "During training, LLMs minimize a loss function that measures the difference between predicted and actual tokens across billions of examples.",
    "Attention mechanisms in transformers enable LLMs to weigh the importance of different words when generating output.",
    "Fine\u2011tuning allows a pre\u2011trained LLM to adapt to a specific downstream task with a smaller dataset.",
    "Neural networks consist of layers of interconnected neurons that adjust their weights during back\u2011propagation.",
    "The history of the Roman Empire spanned over a thousand years.",
    "Soccer is the most popular sport in many countries around the world.",
    "Quantum computing uses qubits to perform calculations that are intractable for classical computers.",
  ];

  let query = $state(defaultQuery);
  let rows = $state<RerankRow[]>([
    ...defaultDocs.map((doc) => ({ doc, score: null })),
    { doc: "", score: null },
  ]);
  let isLoading = $state(false);
  let error = $state<string | null>(null);
  let usage = $state<{ prompt_tokens: number; total_tokens: number } | null>(null);
  let abortController: AbortController | null = null;
  let sortOrder = $state<SortOrder>("desc");
  let editorMode = $state<EditorMode>("table");
  let jsonText = $state("");
  let jsonError = $state<string | null>(null);

  let hasModels = $derived($models.some((m) => !m.unlisted));

  let canSubmit = $derived((() => {
    if (!$selectedModelStore || isLoading) return false;
    if (editorMode === "json") {
      try {
        const parsed = JSON.parse(jsonText) as Record<string, unknown>;
        return (
          typeof parsed.query === "string" &&
          parsed.query.trim() !== "" &&
          Array.isArray(parsed.documents) &&
          (parsed.documents as unknown[]).some(
            (d) => typeof d === "string" && (d as string).trim() !== ""
          )
        );
      } catch {
        return false;
      }
    }
    return query.trim() !== "" && rows.some((r) => r.doc.trim() !== "");
  })());

  // Display rows with sort applied (display-only transform, rows[] is never mutated by sorting)
  let displayRows = $derived((() => {
    const indexed = rows.map((row, i) => ({ row, i }));
    if (sortOrder === "none") return indexed;
    return [...indexed].sort((a, b) => {
      if (a.row.score === null && b.row.score === null) return 0;
      if (a.row.score === null) return 1;
      if (b.row.score === null) return -1;
      return sortOrder === "desc"
        ? b.row.score - a.row.score
        : a.row.score - b.row.score;
    });
  })());

  // Auto-add a new empty row when the last row gets content (table mode only)
  $effect(() => {
    if (editorMode === "table" && rows[rows.length - 1]?.doc.trim() !== "") {
      rows = [...rows, { doc: "", score: null }];
    }
  });

  // Sync loading state to activity store
  $effect(() => {
    playgroundStores.rerankLoading.set(isLoading);
  });

  function switchToJson() {
    if (editorMode === "json") return;
    const docs = rows.filter((r) => r.doc.trim() !== "").map((r) => r.doc);
    jsonText = JSON.stringify({ query, documents: docs }, null, 2);
    jsonError = null;
    editorMode = "json";
  }

  function switchToTable() {
    if (editorMode === "table") return;
    if (jsonText.trim() === "") {
      query = "";
      rows = [{ doc: "", score: null }];
      jsonError = null;
      editorMode = "table";
      return;
    }
    try {
      const parsed = JSON.parse(jsonText) as unknown;
      if (typeof parsed !== "object" || parsed === null || Array.isArray(parsed)) {
        throw new Error("Expected a JSON object");
      }
      const obj = parsed as Record<string, unknown>;
      if (typeof obj.query !== "string") throw new Error('"query" must be a string');
      if (!Array.isArray(obj.documents)) throw new Error('"documents" must be an array');
      query = obj.query;
      const newRows: RerankRow[] = (obj.documents as unknown[]).map((d) => ({
        doc: typeof d === "string" ? d : String(d),
        score: null,
      }));
      if (newRows.length === 0 || newRows[newRows.length - 1].doc.trim() !== "") {
        newRows.push({ doc: "", score: null });
      }
      rows = newRows;
      jsonError = null;
      editorMode = "table";
    } catch (err) {
      jsonError = err instanceof Error ? err.message : "Invalid JSON";
    }
  }

  function cycleSortOrder() {
    sortOrder = sortOrder === "none" ? "desc" : sortOrder === "desc" ? "asc" : "none";
  }

  function sortIndicator(): string {
    if (sortOrder === "desc") return " ↓";
    if (sortOrder === "asc") return " ↑";
    return "";
  }

  async function submit() {
    if (!canSubmit) return;

    let submitQuery: string;
    let nonEmptyEntries: { originalIndex: number; doc: string }[];

    if (editorMode === "json") {
      // Parse JSON, sync state to table, then submit
      try {
        const parsed = JSON.parse(jsonText) as Record<string, unknown>;
        submitQuery = parsed.query as string;
        const docs = (parsed.documents as string[]).filter((d) => d.trim() !== "");
        const newRows: RerankRow[] = docs.map((d) => ({ doc: d, score: null }));
        newRows.push({ doc: "", score: null });
        rows = newRows;
        query = submitQuery;
        editorMode = "table";
      } catch {
        error = "Invalid JSON — fix before submitting";
        return;
      }
      nonEmptyEntries = rows
        .map((r, i) => ({ originalIndex: i, doc: r.doc }))
        .filter((e) => e.doc.trim() !== "");
    } else {
      submitQuery = query;
      nonEmptyEntries = rows
        .map((r, i) => ({ originalIndex: i, doc: r.doc }))
        .filter((e) => e.doc.trim() !== "");
    }

    isLoading = true;
    error = null;
    usage = null;

    // Clear previous scores
    rows = rows.map((r) => ({ ...r, score: null }));

    abortController = new AbortController();

    try {
      const response = await rerank(
        $selectedModelStore,
        submitQuery,
        nonEmptyEntries.map((e) => e.doc),
        abortController.signal
      );

      usage = response.usage;

      // Map result.index (position in submitted docs array) back to original rows[] index
      const updated = rows.map((r) => ({ ...r }));
      for (const result of response.results) {
        const entry = nonEmptyEntries[result.index];
        if (entry !== undefined) {
          updated[entry.originalIndex].score = result.relevance_score;
        }
      }
      rows = updated;
    } catch (err) {
      if (err instanceof Error && err.name === "AbortError") {
        // User cancelled
      } else {
        error = err instanceof Error ? err.message : "An error occurred";
      }
    } finally {
      isLoading = false;
      abortController = null;
    }
  }

  function cancel() {
    abortController?.abort();
  }

  function clear() {
    query = defaultQuery;
    rows = [...defaultDocs.map((doc) => ({ doc, score: null })), { doc: "", score: null }];
    error = null;
    usage = null;
    sortOrder = "desc";
    jsonText = "";
    jsonError = null;
  }

  function deleteRow(originalIndex: number) {
    if (rows.length <= 1) return;
    rows = rows.filter((_, i) => i !== originalIndex);
  }

  function updateDoc(originalIndex: number, value: string) {
    const updated = rows.map((r) => ({ ...r }));
    updated[originalIndex].doc = value;
    rows = updated;
  }

  function scoreColor(score: number | null): string {
    if (score === null) return "text-muted-foreground";
    if (score > 0) return "text-green-600 dark:text-green-400";
    return "text-destructive";
  }

  function formatScore(score: number | null): string {
    if (score === null) return "—";
    return score.toFixed(3);
  }

  function handleKeyDown(e: KeyboardEvent) {
    if (e.key === "Enter" && !e.shiftKey) {
      e.preventDefault();
      submit();
    }
  }

  let isCleared = $derived(
    query === defaultQuery &&
    rows.every((r, i) => r.score === null && r.doc === (defaultDocs[i] ?? "")) &&
    rows.length === defaultDocs.length + 1 &&
    !jsonText.trim() &&
    !error &&
    !usage
  );
</script>

<div class="flex flex-col h-full">
  <!-- Top bar: model selector + query input (table mode) + mode toggle -->
  <div class="shrink-0 flex flex-wrap gap-2 mb-4">
    <ModelSelector bind:value={$selectedModelStore} placeholder="Select a rerank model..." disabled={isLoading} capabilities={["reranker"]} />
    {#if editorMode === "table"}
      <Input
        type="text"
        class="min-w-0 flex-1 basis-48"
        placeholder="Query..."
        bind:value={query}
        disabled={isLoading}
        onkeydown={handleKeyDown}
      />
    {/if}
    <!-- Table / JSON toggle -->
    <ToggleGroup.Root
      type="single"
      variant="outline"
      value={editorMode}
      onValueChange={(v) => v && (v === "table" ? switchToTable() : switchToJson())}
      class="shrink-0"
    >
      <ToggleGroup.Item value="table" disabled={isLoading}>Table</ToggleGroup.Item>
      <ToggleGroup.Item value="json" disabled={isLoading}>JSON</ToggleGroup.Item>
    </ToggleGroup.Root>
  </div>

  {#if !hasModels}
    <div class="text-muted-foreground flex flex-1 items-center justify-center">
      <p>No models configured. Add models to your configuration to use reranking.</p>
    </div>
  {:else if editorMode === "json"}
    <!-- JSON editor -->
    <div class="mb-4 flex min-h-0 flex-1 flex-col">
      <Textarea
        class="w-full flex-1 resize-none font-mono text-sm"
        bind:value={jsonText}
        disabled={isLoading}
        placeholder={'{\n  "query": "your search query",\n  "documents": [\n    "document one",\n    "document two"\n  ]\n}'}
        spellcheck={false}
      />
      {#if jsonError}
        <p class="text-destructive mt-1 text-sm">{jsonError}</p>
      {/if}
    </div>
  {:else}
    <!-- Document table -->
    <div class="mb-4 flex-1 overflow-y-auto rounded-lg border">
      <table class="w-full table-fixed border-collapse">
        <colgroup>
          <col class="w-auto" />
          <col style="width: 120px" />
          <col style="width: 40px" />
        </colgroup>
        <thead class="bg-card sticky top-0 border-b">
          <tr>
            <th class="text-muted-foreground px-3 py-2 text-left text-sm font-medium">Document</th>
            <th
              class="text-muted-foreground hover:text-foreground cursor-pointer select-none px-3 py-2 text-right text-sm font-medium transition-colors"
              onclick={cycleSortOrder}
            >
              Score{sortIndicator()}
            </th>
            <th class="px-2 py-2"></th>
          </tr>
        </thead>
        <tbody>
          {#each displayRows as { row, i } (i)}
            <tr class="border-b last:border-0">
              <td class="px-3 py-1.5">
                <Input
                  type="text"
                  class="border-0 focus-visible:ring-1 h-7 px-1 py-0.5 bg-transparent"
                  placeholder={i === rows.length - 1 ? "Add document..." : "Document text..."}
                  value={row.doc}
                  oninput={(e) => updateDoc(i, (e.target as HTMLInputElement).value)}
                  disabled={isLoading}
                  onkeydown={handleKeyDown}
                />
              </td>
              <td class="px-3 py-1.5 text-right font-mono text-sm {scoreColor(row.score)}">
                {#if isLoading && row.score === null && row.doc.trim() !== ""}
                  <span class="inline-block h-4 w-4 animate-spin rounded-full border-2 border-current border-t-transparent align-middle"></span>
                {:else}
                  {formatScore(row.score)}
                {/if}
              </td>
              <td class="px-2 py-1.5 text-center">
                <Button
                  variant="ghost"
                  size="icon-sm"
                  class="h-7 w-7 text-muted-foreground hover:text-destructive"
                  onclick={() => deleteRow(i)}
                  disabled={rows.length <= 1}
                  tabindex={-1}
                  aria-label="Remove row"
                >
                  ×
                </Button>
              </td>
            </tr>
          {/each}
        </tbody>
      </table>
    </div>
  {/if}

  <!-- Bottom toolbar -->
  {#if hasModels}
    <div class="flex shrink-0 flex-wrap items-center gap-2">
      {#if isLoading}
        <Button variant="destructive" onclick={cancel}>Cancel</Button>
      {:else}
        <Button onclick={submit} disabled={!canSubmit}>Rerank</Button>
        <Button variant="outline" onclick={clear} disabled={isCleared}>Clear</Button>
      {/if}

      {#if error}
        <span class="text-destructive ml-2 text-sm">{error}</span>
      {:else if usage}
        <span class="text-muted-foreground ml-2 text-sm">{usage.total_tokens} tokens</span>
      {/if}
    </div>
  {/if}
</div>
