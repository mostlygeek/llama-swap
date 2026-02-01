<script lang="ts">
  import { models } from "../../stores/api";
  import { groupModels } from "../../lib/modelUtils";

  interface Props {
    value: string;
    placeholder?: string;
    disabled?: boolean;
  }

  let { value = $bindable(), placeholder = "Select a model...", disabled = false }: Props = $props();

  let grouped = $derived(groupModels($models));
  let hasModels = $derived(grouped.local.length > 0 || Object.keys(grouped.peersByProvider).length > 0);
</script>

{#if hasModels}
  <select
    class="min-w-0 flex-1 basis-48 px-3 py-2 rounded border border-gray-200 dark:border-white/10 bg-surface focus:outline-none focus:ring-2 focus:ring-primary"
    bind:value
    {disabled}
  >
    <option value="">{placeholder}</option>
    {#if grouped.local.length > 0}
      <optgroup label="Local">
        {#each grouped.local as model (model.id)}
          <option value={model.id}>{model.id}</option>
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
