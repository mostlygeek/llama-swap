<script lang="ts">
  interface Props {
    value: string;
    tailLength?: number;
    className?: string;
  }

  let { value, tailLength = 10, className = "" }: Props = $props();

  let displayValue = $derived(value || "-");
  let head = $derived(
    displayValue.length > tailLength ? displayValue.slice(0, -tailLength) : displayValue
  );
  let tail = $derived(
    displayValue.length > tailLength ? displayValue.slice(-tailLength) : ""
  );
</script>

<span class="flex min-w-0 items-baseline {className}" title={displayValue}>
  <span class="min-w-0 overflow-hidden text-ellipsis whitespace-nowrap">{head}</span>
  {#if tail}
    <span class="shrink-0 whitespace-nowrap">{tail}</span>
  {/if}
</span>
