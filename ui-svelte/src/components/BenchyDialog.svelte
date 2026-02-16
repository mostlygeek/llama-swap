<script lang="ts">
  import type { BenchyJob } from "../lib/types";

  interface Props {
    job: BenchyJob | null;
    open: boolean;
    starting: boolean;
    error: string | null;
    onclose: () => void;
    oncancel: () => void;
  }

  let { job, open, starting, error, onclose, oncancel }: Props = $props();

  let dialogEl: HTMLDialogElement | undefined = $state();

  $effect(() => {
    if (open && dialogEl) {
      dialogEl.showModal();
    } else if (!open && dialogEl) {
      dialogEl.close();
    }
  });

  function handleDialogClose() {
    onclose();
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
      {#if starting}
        <div class="text-sm text-txtsecondary">Starting benchmark...</div>
      {/if}

      {#if error}
        <div class="p-2 border border-error/40 bg-error/10 rounded text-sm text-error break-words">
          {error}
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
      {#if job?.status === "running"}
        <button onclick={oncancel} class="btn btn--sm">Cancel</button>
      {/if}
      <button onclick={() => dialogEl?.close()} class="btn">Close</button>
    </div>
  </div>
</dialog>

