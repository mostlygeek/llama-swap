<script lang="ts">
  import { persistentStore } from "../stores/persistent";
  import { Type, WrapText, Search, SearchX, CircleX } from "@lucide/svelte";
  import { Button } from "$lib/components/ui/button/index.js";
  import { Input } from "$lib/components/ui/input/index.js";

  interface Props {
    id: string;
    title: string;
    logData: string;
  }

  let { id, title, logData }: Props = $props();

  let filterRegex = $state("");

  // Create persistent stores for this panel (id is intentionally captured at init time)
  // svelte-ignore state_referenced_locally
  const fontSizeStore = persistentStore<"xxs" | "xs" | "small" | "normal">(`logPanel-${id}-fontSize`, "normal");
  // svelte-ignore state_referenced_locally
  const wrapTextStore = persistentStore<boolean>(`logPanel-${id}-wrapText`, false);
  // svelte-ignore state_referenced_locally
  const showFilterStore = persistentStore<boolean>(`logPanel-${id}-showFilter`, false);

  let textWrapClass = $derived($wrapTextStore ? "whitespace-pre-wrap" : "whitespace-pre");

  function toggleFontSize(): void {
    fontSizeStore.update((prev) => {
      switch (prev) {
        case "xxs": return "xs";
        case "xs": return "small";
        case "small": return "normal";
        case "normal": return "xxs";
      }
    });
  }

  function toggleWrapText(): void {
    wrapTextStore.update((prev) => !prev);
  }

  function toggleFilter(): void {
    if ($showFilterStore) {
      showFilterStore.set(false);
      filterRegex = "";
    } else {
      showFilterStore.set(true);
    }
  }

  let fontSizeClass = $derived.by(() => {
    switch ($fontSizeStore) {
      case "xxs": return "text-[0.5rem]";
      case "xs": return "text-[0.75rem]";
      case "small": return "text-[0.875rem]";
      case "normal": return "text-base";
    }
  });

  let filteredLogs = $derived.by(() => {
    if (!filterRegex) return logData;
    try {
      const regex = new RegExp(filterRegex, "i");
      return logData.split("\n").filter((line) => regex.test(line)).join("\n");
    } catch {
      return logData;
    }
  });

  let preElement: HTMLPreElement;
  let userScrolledUp = $state(false);

  function handleScroll() {
    if (!preElement) return;
    const { scrollTop, scrollHeight, clientHeight } = preElement;
    userScrolledUp = scrollHeight - scrollTop - clientHeight > 40;
  }

  // Auto scroll to bottom when logs change, unless user has scrolled up
  $effect(() => {
    if (preElement && filteredLogs && !userScrolledUp) {
      preElement.scrollTop = preElement.scrollHeight;
    }
  });
</script>

<div class="bg-muted/50 flex h-full w-full flex-col overflow-hidden rounded-xl border p-1">
  <div class="p-3">
    <div class="flex items-center justify-between">
      <h3 class="m-0 p-0 text-lg">{title}</h3>

      <div class="flex items-center gap-1">
        <Button variant="ghost" size="icon-sm" onclick={toggleFontSize} title="Change font size">
          <Type />
        </Button>
        <Button variant="ghost" size="icon-sm" onclick={toggleWrapText} title="Toggle text wrap">
          <WrapText class={$wrapTextStore ? "text-primary" : ""} />
        </Button>
        <Button variant="ghost" size="icon-sm" onclick={toggleFilter} title="Toggle filter">
          {#if $showFilterStore}<SearchX />{:else}<Search />{/if}
        </Button>
      </div>
    </div>

    {#if $showFilterStore}
      <div class="mt-2 flex w-full items-center gap-2">
        <Input type="text" class="h-8" placeholder="Filter logs (regex)..." bind:value={filterRegex} />
        <Button variant="ghost" size="icon-sm" onclick={() => (filterRegex = "")} aria-label="Clear filter">
          <CircleX />
        </Button>
      </div>
    {/if}
  </div>
  <div class="bg-background flex-1 overflow-hidden rounded-lg font-mono text-sm">
    <pre bind:this={preElement} onscroll={handleScroll} class="{textWrapClass} {fontSizeClass} h-full overflow-auto p-4">{filteredLogs}</pre>
  </div>
</div>
