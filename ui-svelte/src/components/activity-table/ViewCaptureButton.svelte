<script lang="ts">
  import { Button } from "$lib/components/ui/button/index.js";

  interface Props {
    hasCapture: boolean;
    loading: boolean;
    onclick: () => void;
  }

  let { hasCapture, loading, onclick }: Props = $props();
</script>

{#if hasCapture}
  <Button variant="outline" size="xs" {onclick} disabled={loading}>
    {loading ? "..." : "View"}
  </Button>
{:else}
  <span class="text-muted-foreground">-</span>
{/if}
