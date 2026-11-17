<script lang="ts">
  import { Button } from "$lib/components/ui/button/index.js";
  import { Input } from "$lib/components/ui/input/index.js";
  import { Label } from "$lib/components/ui/label/index.js";
  import type { ChatHistory } from "../../stores/chatHistory";

  /** The history controls for one chat page: how many to keep, and clearing. */
  let {
    history,
    disabled = false,
    onCleared,
  }: {
    history: ChatHistory<any>;
    disabled?: boolean;
    /** Called after everything was erased. */
    onCleared?: () => void;
  } = $props();

  // The history instance is created once per page and never swapped.
  // svelte-ignore state_referenced_locally
  const limit = history.limit;
  // svelte-ignore state_referenced_locally
  const available = history.available;
  // svelte-ignore state_referenced_locally
  const saveError = history.saveError;

  let confirming = $state(false);

  function onInput(event: Event) {
    const raw = (event.target as HTMLInputElement).value;
    if (raw === "") return;
    void history.setLimit(Number(raw));
  }

  async function clear() {
    confirming = false;
    await history.clear();
    onCleared?.();
  }
</script>

<div class="space-y-4">
  <div>
    <Label class="mb-1" for="history-limit">Conversations to keep</Label>
    <Input id="history-limit" type="number" min="0" max="100" step="1" value={$limit} oninput={onInput} />
    <p class="text-muted-foreground mt-1 text-xs">
      1 keeps the last conversation. 0 saves nothing, so every visit starts a new chat. Images count toward
      browser storage.
    </p>
  </div>

  {#if !$available}
    <p class="text-destructive text-xs">This browser does not allow storing history, so nothing is saved.</p>
  {:else if $saveError}
    <p class="text-destructive text-xs">The last conversation did not fit in browser storage and was not saved.</p>
  {/if}

  <div class="flex items-center gap-2">
    {#if confirming}
      <Button variant="destructive" onclick={clear}>Erase all history</Button>
      <Button variant="outline" onclick={() => (confirming = false)}>Cancel</Button>
    {:else}
      <Button variant="outline" {disabled} onclick={() => (confirming = true)}>Clear history</Button>
    {/if}
  </div>
</div>
