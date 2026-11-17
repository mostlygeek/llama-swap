<script lang="ts">
  import type { Model } from "../lib/types";
  import { pendingLoads, onToggleLoad, canStop } from "../stores/modelLoad";
  import { inferencePaused } from "../stores/api";
  import { Play, PowerOff, Loader2, Square } from "@lucide/svelte";

  interface Props {
    model: Model;
    /** "md" for list rows (size-7), "sm" for the detail header (size-5). */
    size?: "md" | "sm";
  }

  let { model, size = "md" }: Props = $props();

  let btnSize = $derived(size === "sm" ? "size-5 rounded-sm" : "size-7 rounded-md");
  let iconSize = $derived(size === "sm" ? "size-3.5" : "size-4");
  let transitioning = $derived(
    model.state === "starting" || model.state === "waking" || model.state === "going-to-sleep"
  );
  let loaded = $derived(model.state === "ready" || model.state === "sleeping");
  // Loading is rejected while inference is paused; unloading and stopping still work.
  let pausedLoad = $derived($inferencePaused && !canStop(model));

  function buttonTitle(): string {
    if (pausedLoad) return "Inference is paused";
    if (loaded) return "Unload";
    if (transitioning) return "Stop";
    if (model.state === "stopping") return "Stopping";
    return $pendingLoads[model.id] ? "Cancel" : "Load";
  }
</script>

<button
  type="button"
  class="group text-muted-foreground hover:bg-accent hover:text-accent-foreground flex {btnSize} shrink-0 items-center justify-center disabled:opacity-50"
  title={buttonTitle()}
  aria-label={canStop(model) ? "Stop model" : "Load model"}
  disabled={model.state === "stopping" || pausedLoad}
  onclick={() => onToggleLoad(model)}
>
  {#if $pendingLoads[model.id] && model.state === "stopped"}
    <Loader2 class="{iconSize} animate-spin" />
  {:else if loaded}
    <PowerOff class={iconSize} />
  {:else if transitioning}
    <Loader2 class="{iconSize} animate-spin group-hover:hidden" />
    <Square class="{iconSize} hidden group-hover:block" />
  {:else if model.state === "stopping"}
    <Loader2 class="{iconSize} animate-spin" />
  {:else}
    <Play class={iconSize} />
  {/if}
</button>
