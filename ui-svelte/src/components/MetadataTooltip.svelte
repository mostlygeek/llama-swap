<script lang="ts">
  import type { Snippet } from "svelte";

  interface Props {
    metadata: Record<string, string> | undefined;
    children: Snippet;
  }

  let { metadata, children }: Props = $props();

  let entries = $derived(Object.entries(metadata || {}));
</script>

<div class="relative group inline-flex">
  {@render children()}
  {#if entries.length > 0}
    <div
      class="absolute top-full left-0 mt-2 px-3 py-2 bg-gray-900 text-white text-sm rounded-md
             opacity-0 group-hover:opacity-100 transition-opacity duration-200
             pointer-events-none z-50 normal-case min-w-[12rem] max-w-[24rem] whitespace-normal shadow-lg"
    >
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
      <div
        class="absolute bottom-full left-4 transform -translate-x-1/2 border-4 border-transparent border-b-gray-900"
      ></div>
    </div>
  {/if}
</div>
