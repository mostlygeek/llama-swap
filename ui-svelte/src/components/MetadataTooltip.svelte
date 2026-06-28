<script lang="ts">
  import type { Snippet } from "svelte";
  import * as Tooltip from "$lib/components/ui/tooltip/index.js";

  interface Props {
    metadata: Record<string, string> | undefined;
    children: Snippet;
  }

  let { metadata, children }: Props = $props();

  let entries = $derived(Object.entries(metadata || {}));
</script>

{#if entries.length > 0}
  <Tooltip.Provider>
    <Tooltip.Root>
      <Tooltip.Trigger>
        {@render children()}
      </Tooltip.Trigger>
      <Tooltip.Content class="min-w-[12rem] max-w-[24rem] normal-case">
        <table class="w-full text-left">
          <tbody>
            {#each entries as [key, value]}
              <tr class="border-b border-white/10 last:border-0">
                <td class="py-1 pr-3 font-medium whitespace-nowrap text-primary">{key}</td>
                <td class="py-1 break-all">{value}</td>
              </tr>
            {/each}
          </tbody>
        </table>
      </Tooltip.Content>
    </Tooltip.Root>
  </Tooltip.Provider>
{:else}
  {@render children()}
{/if}
