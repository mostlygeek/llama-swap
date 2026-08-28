<script lang="ts">
  import { link } from "svelte-spa-router";

  interface Props {
    model: string;
  }

  let { model }: Props = $props();

  let href = $derived(`/models/${encodeURIComponent(model)}`);
</script>

{#if model}
  <a href={href} use:link class="text-primary hover:underline">
    {model}
  </a>
{:else}
  <span class="text-muted-foreground">-</span>
{/if}
