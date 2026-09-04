<script lang="ts">
  import DocsInterface from "../components/playground/DocsInterface.svelte";
  import * as Card from "$lib/components/ui/card/index.js";
  import { refreshPlaygroundModels } from "$lib/hooks/playground-models.svelte";

  /**
   * Help: ask llama-swap's own documentation a question.
   *
   * It was a Playground tab until it became its own page. The Playground is
   * for trying a model out; this is for finding out how something works, which
   * is what someone reaching for "Help" is after -- and it is one click from
   * the bottom of the sidebar rather than three tabs deep.
   */
  refreshPlaygroundModels();
</script>

<Card.Root class="flex h-full flex-col gap-0 overflow-hidden p-4">
  <div class="relative flex-1 overflow-hidden">
    <DocsInterface />
  </div>
</Card.Root>
