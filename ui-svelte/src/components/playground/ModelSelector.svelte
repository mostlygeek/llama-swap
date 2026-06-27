<script lang="ts">
  import { models } from "../../stores/api";
  import { groupModels } from "../../lib/modelUtils";

  interface Props {
    value: string;
    placeholder?: string;
    disabled?: boolean;
    capabilities?: string[];
    matchAny?: boolean;
  }

  let { value = $bindable(), placeholder = "Select a model...", disabled = false, capabilities, matchAny = false }: Props = $props();

  let grouped = $derived(groupModels($models, capabilities, matchAny));
  let hasMatching = $derived(grouped.localMatching.length > 0);
  let hasModels = $derived(hasMatching || grouped.local.length > 0 || Object.keys(grouped.peersByProvider).length > 0);
</script>

{#if hasModels}
  <select
    class="border-input bg-background focus-visible:border-ring focus-visible:ring-ring/50 dark:bg-input/30 min-w-0 flex-1 basis-48 rounded-md border px-3 py-2 text-sm shadow-xs transition-[color,box-shadow] outline-none focus-visible:ring-[3px] disabled:cursor-not-allowed disabled:opacity-50"
    bind:value
    {disabled}
  >
    <option value="">{placeholder}</option>
    {#if hasMatching}
      <optgroup label="Matching Capabilities">
        {#each grouped.localMatching as model (model.id)}
          <option value={model.id}>{model.id}</option>
          {#if model.aliases}
            {#each model.aliases as alias (alias)}
              <option value={alias}>  ↳ {alias}</option>
            {/each}
          {/if}
        {/each}
      </optgroup>
    {/if}
    {#if grouped.local.length > 0}
      <optgroup label="Local">
        {#each grouped.local as model (model.id)}
          <option value={model.id}>{model.id}</option>
          {#if model.aliases}
            {#each model.aliases as alias (alias)}
              <option value={alias}>  ↳ {alias}</option>
            {/each}
          {/if}
        {/each}
      </optgroup>
    {/if}
    {#each Object.entries(grouped.peersByProvider).sort(([a], [b]) => a.localeCompare(b)) as [peerId, peerModels] (peerId)}
      <optgroup label="Peer: {peerId}">
        {#each peerModels as model (model.id)}
          <option value={model.id}>{model.id}</option>
        {/each}
      </optgroup>
    {/each}
  </select>
{/if}
