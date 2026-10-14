<script lang="ts">
  import { tick } from "svelte";
  import { playgroundModels, profileModels, selectorModels } from "../../stores/api";
  import {
    buildModelOptions,
    filterModelOptions,
    type ModelMatcher,
    type ModelOption,
  } from "../../lib/modelUtils";
  import * as Combobox from "$lib/components/ui/combobox/index.js";

  interface Props {
    value: string;
    placeholder?: string;
    disabled?: boolean;
    /** What the tab needs from a model; matches are listed first. */
    match?: ModelMatcher;
  }

  let { value = $bindable(), placeholder = "Select a model...", disabled = false, match }: Props = $props();

  let open = $state(false);
  // What has been typed into the search box. Cleared whenever the list opens,
  // so opening it on an already selected model still shows every model.
  let query = $state("");
  let inputRef = $state<HTMLInputElement | null>(null);

  let options = $derived(buildModelOptions({
    models: $playgroundModels.filter((model) => model.playgroundType === "model" || model.playgroundType === "peer"),
    profiles: $profileModels,
    selectors: $selectorModels,
    matcher: match,
  }));
  let visible = $derived(filterModelOptions(options, query));
  let hasModels = $derived(options.length > 0);
  // Aliases sit indented under their model, so they only need to name it when
  // the filter has left them on their own.
  let visibleValues = $derived(new Set(visible.map((option) => option.value)));
  // Every id/alias actually offered, regardless of the search box's current
  // filter. A value survives a server switch in storage (it's keyed per
  // server), but the model it named may not exist on whichever server is now
  // connected, in which case it should not keep showing as selected.
  let optionValues = $derived(new Set(options.map((option) => option.value)));

  // Rows in dropdown order, split into their group headings.
  let sections = $derived.by(() => {
    const out: { group: string; options: ModelOption[] }[] = [];
    for (const option of visible) {
      const last = out[out.length - 1];
      if (last && last.group === option.group) {
        last.options.push(option);
      } else {
        out.push({ group: option.group, options: [option] });
      }
    }
    return out;
  });

  // Searching while open, showing the selection while closed. The selected
  // model moves to the placeholder while searching so it stays visible.
  let text = $derived(open ? query : value);
  let hint = $derived(open && value ? value : placeholder);

  // Bits UI keeps its own copy of the input text (it writes the label into it
  // on selection, for instance), so put ours back after every render that
  // changes it. Without this the box keeps whatever was typed instead of
  // showing the model that got picked.
  $effect(() => {
    const el = inputRef;
    const next = text;
    if (el && el.value !== next) el.value = next;
  });

  $effect(() => {
    if (value && hasModels && !optionValues.has(value)) value = "";
  });

  function openList() {
    query = "";
    open = true;
  }

  function handleOpenChange(next: boolean) {
    open = next;
    if (!next) query = "";
  }

  function handleValueChange(next: string) {
    value = next;
    query = "";
  }

  function handleKeyDown(event: KeyboardEvent) {
    // Typing on a closed list starts a new search rather than editing the
    // selected model's name.
    const printable = event.key.length === 1 && !event.ctrlKey && !event.metaKey && !event.altKey;
    if (!open && printable) inputRef?.select();
  }

  let rehighlighting = false;

  async function handleInput(event: Event) {
    query = (event.target as HTMLInputElement).value;
    open = true;

    // Bits UI highlights the first row on input, but it does that before the
    // filtered list has rendered, so it lands on a row that is about to
    // disappear and Enter then selects nothing. Replaying the event once the
    // new rows are in the DOM highlights the first row that actually matches.
    if (rehighlighting) return;
    rehighlighting = true;
    await tick();
    inputRef?.dispatchEvent(new Event("input", { bubbles: true }));
    rehighlighting = false;
  }
</script>

{#if hasModels}
  <div class="relative min-w-0 flex-1 basis-48">
    <Combobox.Root
      type="single"
      bind:value
      bind:open
      {disabled}
      inputValue={text}
      allowDeselect={false}
      onValueChange={handleValueChange}
      onOpenChange={handleOpenChange}
      items={visible.map((option) => ({ value: option.value, label: option.value }))}
    >
      <Combobox.Input
        bind:ref={inputRef}
        placeholder={hint}
        {disabled}
        aria-label="Model"
        autocomplete="off"
        spellcheck={false}
        onfocus={openList}
        onclick={openList}
        onkeydown={handleKeyDown}
        oninput={handleInput}
      />
      <Combobox.Trigger {disabled} aria-label="Show models" />
      <Combobox.Content class="max-h-[60vh]">
        {#if visible.length === 0}
          <div class="text-muted-foreground px-3 py-3 text-sm">No models match &quot;{query}&quot;</div>
        {/if}
        {#each sections as section, i (section.group + i)}
          {#if i > 0}
            <Combobox.Separator />
          {/if}
          <Combobox.Group>
            <Combobox.GroupHeading>{section.group}</Combobox.GroupHeading>
            {#each section.options as option (option.group + option.value)}
              {@const nested = !!option.aliasOf && visibleValues.has(option.aliasOf)}
              <Combobox.Item value={option.value} label={option.value} class={nested ? "pl-5" : undefined}>
                <span class="flex min-w-0 flex-1 items-baseline gap-1.5">
                  {#if nested}
                    <span class="text-muted-foreground shrink-0" aria-hidden="true">↳</span>
                  {/if}
                  <span class="truncate">{option.value}</span>
                  {#if option.aliasOf && !nested}
                    <span class="text-muted-foreground shrink-0 pl-0.5 text-xs">alias of {option.aliasOf}</span>
                  {:else if option.name}
                    <span class="text-muted-foreground shrink-0 truncate pl-0.5 text-xs">{option.name}</span>
                  {/if}
                </span>
              </Combobox.Item>
            {/each}
          </Combobox.Group>
        {/each}
      </Combobox.Content>
    </Combobox.Root>
  </div>
{/if}
