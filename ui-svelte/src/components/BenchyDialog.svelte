<script lang="ts">
  import { persistentStore } from "../stores/persistent";
  import type { BenchyJob, BenchyStartOptions } from "../lib/types";

  interface Props {
    model: string | null;
    job: BenchyJob | null;
    open: boolean;
    canStart: boolean;
    starting: boolean;
    error: string | null;
    onclose: () => void;
    oncancel: () => void;
    onstart: (opts: BenchyStartOptions) => void;
  }

  let { model, job, open, canStart, starting, error, onclose, oncancel, onstart }: Props = $props();

  let dialogEl: HTMLDialogElement | undefined = $state();
  let optionsError: string | null = $state(null);

  const baseUrlStore = persistentStore<string>("benchy-options-base-url", "");
  const tokenizerStore = persistentStore<string>("benchy-options-tokenizer", "");
  const ppStore = persistentStore<string>("benchy-options-pp", "512, 2048, 8192");
  const tgStore = persistentStore<string>("benchy-options-tg", "32, 128");
  const depthStore = persistentStore<string>("benchy-options-depth", "");
  const concurrencyStore = persistentStore<string>("benchy-options-concurrency", "");
  const runsStore = persistentStore<string>("benchy-options-runs", "5");
  const latencyModeStore = persistentStore<"" | "api" | "generation" | "none">("benchy-options-latency-mode", "");
  const noCacheStore = persistentStore<boolean>("benchy-options-no-cache", false);
  const noWarmupStore = persistentStore<boolean>("benchy-options-no-warmup", false);
  const adaptPromptStore = persistentStore<"auto" | "true" | "false">("benchy-options-adapt-prompt", "auto");
  const enablePrefixCachingStore = persistentStore<boolean>("benchy-options-enable-prefix-caching", false);
  const trustRemoteCodeStore = persistentStore<"auto" | "true" | "false">("benchy-options-trust-remote-code", "auto");

  $effect(() => {
    if (open && dialogEl) {
      dialogEl.showModal();
    } else if (!open && dialogEl) {
      dialogEl.close();
    }
  });

  function handleDialogClose() {
    optionsError = null;
    onclose();
  }

  function parseNumberList(raw: string, field: string, minValue: number): number[] | undefined {
    const cleaned = raw.trim();
    if (!cleaned) return undefined;

    const tokens = cleaned.split(/[,\s]+/).filter(Boolean);
    if (tokens.length === 0) return undefined;

    const values: number[] = [];
    for (const token of tokens) {
      const n = Number(token);
      if (!Number.isInteger(n)) {
        throw new Error(`${field} must contain integers`);
      }
      if (n < minValue) {
        throw new Error(`${field} values must be >= ${minValue}`);
      }
      values.push(n);
    }
    return values;
  }

  function parsePositiveNumber(raw: string, field: string): number | undefined {
    const cleaned = raw.trim();
    if (!cleaned) return undefined;
    const n = Number(cleaned);
    if (!Number.isInteger(n) || n <= 0) {
      throw new Error(`${field} must be a positive integer`);
    }
    return n;
  }

  function parseTriState(value: "auto" | "true" | "false"): boolean | undefined {
    if (value === "auto") return undefined;
    return value === "true";
  }

  function handleStart(): void {
    if (!canStart || starting || job?.status === "running") return;
    optionsError = null;

    try {
      const opts: BenchyStartOptions = {};
      const baseUrl = $baseUrlStore.trim();
      const tokenizer = $tokenizerStore.trim();
      const runs = parsePositiveNumber($runsStore, "runs");
      const pp = parseNumberList($ppStore, "pp", 1);
      const tg = parseNumberList($tgStore, "tg", 1);
      const depth = parseNumberList($depthStore, "depth", 0);
      const concurrency = parseNumberList($concurrencyStore, "concurrency", 1);

      if (baseUrl) opts.baseUrl = baseUrl;
      if (tokenizer) opts.tokenizer = tokenizer;
      if (pp?.length) opts.pp = pp;
      if (tg?.length) opts.tg = tg;
      if (depth?.length) opts.depth = depth;
      if (concurrency?.length) opts.concurrency = concurrency;
      if (runs !== undefined) opts.runs = runs;
      if ($latencyModeStore) opts.latencyMode = $latencyModeStore;
      if ($noCacheStore) opts.noCache = true;
      if ($noWarmupStore) opts.noWarmup = true;
      if ($enablePrefixCachingStore) opts.enablePrefixCaching = true;

      const adaptPrompt = parseTriState($adaptPromptStore);
      if (adaptPrompt !== undefined) {
        opts.adaptPrompt = adaptPrompt;
      }

      const trustRemoteCode = parseTriState($trustRemoteCodeStore);
      if (trustRemoteCode !== undefined) {
        opts.trustRemoteCode = trustRemoteCode;
      }

      onstart(opts);
    } catch (e) {
      optionsError = e instanceof Error ? e.message : String(e);
    }
  }
</script>

<dialog
  bind:this={dialogEl}
  onclose={handleDialogClose}
  class="bg-surface text-txtmain rounded-lg shadow-xl max-w-5xl w-full max-h-[90vh] p-0 backdrop:bg-black/50 m-auto"
>
  <div class="flex flex-col max-h-[90vh]">
    <div class="flex justify-between items-center p-4 border-b border-card-border">
      <h2 class="text-xl font-bold pb-0">llama-benchy</h2>
      <button
        onclick={() => dialogEl?.close()}
        class="text-txtsecondary hover:text-txtmain text-2xl leading-none"
        aria-label="Close"
      >
        &times;
      </button>
    </div>

    <div class="overflow-y-auto flex-1 p-4 space-y-4">
      <div class="p-3 border border-card-border rounded bg-background/40 space-y-3">
        <div class="text-sm text-txtsecondary">
          Model: <span class="font-mono text-txtmain break-all">{model || "n/a"}</span>
        </div>

        <div class="grid grid-cols-1 md:grid-cols-2 gap-3">
          <label class="text-sm">
            <div class="text-txtsecondary mb-1">Tokenizer (optional)</div>
            <input class="w-full px-2 py-1 rounded border border-card-border bg-background" bind:value={$tokenizerStore} placeholder="auto from metadata/model" />
          </label>
          <label class="text-sm">
            <div class="text-txtsecondary mb-1">Base URL (optional)</div>
            <input class="w-full px-2 py-1 rounded border border-card-border bg-background" bind:value={$baseUrlStore} placeholder="http://127.0.0.1:8000/v1" />
          </label>
          <label class="text-sm">
            <div class="text-txtsecondary mb-1">pp tokens</div>
            <input class="w-full px-2 py-1 rounded border border-card-border bg-background font-mono" bind:value={$ppStore} placeholder="512, 2048, 8192" />
          </label>
          <label class="text-sm">
            <div class="text-txtsecondary mb-1">tg tokens</div>
            <input class="w-full px-2 py-1 rounded border border-card-border bg-background font-mono" bind:value={$tgStore} placeholder="32, 128" />
          </label>
          <label class="text-sm">
            <div class="text-txtsecondary mb-1">depth</div>
            <input class="w-full px-2 py-1 rounded border border-card-border bg-background font-mono" bind:value={$depthStore} placeholder="0, 512, 2048" />
          </label>
          <label class="text-sm">
            <div class="text-txtsecondary mb-1">concurrency</div>
            <input class="w-full px-2 py-1 rounded border border-card-border bg-background font-mono" bind:value={$concurrencyStore} placeholder="1, 2, 4, 8" />
          </label>
          <label class="text-sm">
            <div class="text-txtsecondary mb-1">runs</div>
            <input class="w-full px-2 py-1 rounded border border-card-border bg-background font-mono" bind:value={$runsStore} placeholder="5" />
          </label>
          <label class="text-sm">
            <div class="text-txtsecondary mb-1">latency mode</div>
            <select class="w-full px-2 py-1 rounded border border-card-border bg-background" bind:value={$latencyModeStore}>
              <option value="">default</option>
              <option value="api">api</option>
              <option value="generation">generation</option>
              <option value="none">none</option>
            </select>
          </label>
          <label class="text-sm">
            <div class="text-txtsecondary mb-1">adapt prompt</div>
            <select class="w-full px-2 py-1 rounded border border-card-border bg-background" bind:value={$adaptPromptStore}>
              <option value="auto">auto (benchy default)</option>
              <option value="true">true (--adapt-prompt)</option>
              <option value="false">false (--no-adapt-prompt)</option>
            </select>
          </label>
          <label class="text-sm">
            <div class="text-txtsecondary mb-1">trust remote code</div>
            <select class="w-full px-2 py-1 rounded border border-card-border bg-background" bind:value={$trustRemoteCodeStore}>
              <option value="auto">auto (model metadata)</option>
              <option value="true">true</option>
              <option value="false">false</option>
            </select>
          </label>
        </div>

        <div class="flex flex-wrap gap-4 text-sm">
          <label class="flex items-center gap-2">
            <input type="checkbox" bind:checked={$noCacheStore} />
            no-cache
          </label>
          <label class="flex items-center gap-2">
            <input type="checkbox" bind:checked={$noWarmupStore} />
            no-warmup
          </label>
          <label class="flex items-center gap-2">
            <input type="checkbox" bind:checked={$enablePrefixCachingStore} />
            enable-prefix-caching
          </label>
        </div>
      </div>

      {#if starting}
        <div class="text-sm text-txtsecondary">Starting benchmark...</div>
      {/if}

      {#if error}
        <div class="p-2 border border-error/40 bg-error/10 rounded text-sm text-error break-words">
          {error}
        </div>
      {/if}

      {#if optionsError}
        <div class="p-2 border border-error/40 bg-error/10 rounded text-sm text-error break-words">
          {optionsError}
        </div>
      {/if}

      {#if job}
        <div class="text-sm text-txtsecondary space-y-1">
          <div>
            Model: <span class="font-mono break-all text-txtmain">{job.model}</span>
          </div>
          <div>
            Tokenizer: <span class="font-mono break-all text-txtmain">{job.tokenizer}</span>
          </div>
          <div>
            Base URL: <span class="font-mono break-all text-txtmain">{job.baseUrl}</span>
          </div>
          <div>
            pp: <span class="font-mono text-txtmain">{job.pp.join(" ")}</span> | tg:
            <span class="font-mono text-txtmain">{job.tg.join(" ")}</span> | runs:
            <span class="font-mono text-txtmain">{job.runs}</span>
          </div>
          {#if job.depth?.length}
            <div>
              depth: <span class="font-mono text-txtmain">{job.depth.join(" ")}</span>
            </div>
          {/if}
          {#if job.concurrency?.length}
            <div>
              concurrency: <span class="font-mono text-txtmain">{job.concurrency.join(" ")}</span>
            </div>
          {/if}
          <div>
            latency mode: <span class="font-mono text-txtmain">{job.latencyMode || "default"}</span>
          </div>
          <div>
            flags:
            <span class="font-mono text-txtmain">
              {job.noCache ? " no-cache" : ""}{job.noWarmup ? " no-warmup" : ""}{job.enablePrefixCaching ? " enable-prefix-caching" : ""}
              {job.adaptPrompt === true ? " adapt-prompt" : job.adaptPrompt === false ? " no-adapt-prompt" : ""}
              {job.trustRemoteCode ? " trust-remote-code" : ""}
              {!(job.noCache || job.noWarmup || job.enablePrefixCaching || job.adaptPrompt !== undefined || job.trustRemoteCode) ? " none" : ""}
            </span>
          </div>
          <div>
            Status: <span class="font-mono text-txtmain">{job.status}</span>
          </div>
        </div>

        <div>
          <h3 class="text-base font-semibold pb-2">Output</h3>
          <pre class="whitespace-pre-wrap text-xs bg-background rounded p-3 border border-border">{job.stdout || ""}</pre>
        </div>

        {#if job.stderr}
          <div>
            <h3 class="text-base font-semibold pb-2">Stderr</h3>
            <pre class="whitespace-pre-wrap text-xs bg-background rounded p-3 border border-border">{job.stderr}</pre>
          </div>
        {/if}
      {/if}
    </div>

    <div class="p-4 border-t border-card-border flex justify-end gap-2">
      <button
        onclick={handleStart}
        class="btn btn--sm"
        disabled={!canStart || starting || job?.status === "running"}
      >
        {job ? "Run Again" : "Run"}
      </button>
      {#if job?.status === "running"}
        <button onclick={oncancel} class="btn btn--sm">Cancel</button>
      {/if}
      <button onclick={() => dialogEl?.close()} class="btn">Close</button>
    </div>
  </div>
</dialog>
