<script lang="ts">
  import { models } from "../../stores/api";
  import { groupModels } from "../../lib/modelUtils";
  import * as Select from "$lib/components/ui/select/index.js";

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
  <Select.Root
    type="single"
    {value}
    onValueChange={(v) => v !== undefined && (value = v)}
    {disabled}
  >
    <Select.Trigger class="min-w-0 flex-1 basis-48">{value || placeholder}</Select.Trigger>
    <Select.Content class="max-h-[60vh]">
      <Select.Item value="">{placeholder}</Select.Item>
      {#if hasMatching}
        <Select.Group>
          <Select.Label>Matching Capabilities</Select.Label>
          {#each grouped.localMatching as model (model.id)}
            <Select.Item value={model.id}>{model.id}</Select.Item>
            {#if model.aliases}
              {#each model.aliases as alias (alias)}
                <Select.Item value={alias}>↳ {alias}</Select.Item>
              {/each}
            {/if}
          {/each}
        </Select.Group>
        <Select.Separator />
      {/if}
      {#if grouped.local.length > 0}
        <Select.Group>
          <Select.Label>Local</Select.Label>
          {#each grouped.local as model (model.id)}
            <Select.Item value={model.id}>{model.id}</Select.Item>
            {#if model.aliases}
              {#each model.aliases as alias (alias)}
                <Select.Item value={alias}>↳ {alias}</Select.Item>
              {/each}
            {/if}
          {/each}
        </Select.Group>
        <Select.Separator />
      {/if}
      {#each Object.entries(grouped.peersByProvider).sort(([a], [b]) => a.localeCompare(b)) as [peerId, peerModels] (peerId)}
        <Select.Group>
          <Select.Label>Peer: {peerId}</Select.Label>
          {#each peerModels as model (model.id)}
            <Select.Item value={model.id}>{model.id}</Select.Item>
          {/each}
        </Select.Group>
      {/each}
    </Select.Content>
  </Select.Root>
{/if}
