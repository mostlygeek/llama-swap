<script lang="ts">
  import { renderMarkdown, escapeHtml, renderStreamingMarkdown, createStreamingCache } from "../../lib/markdown";
  import type { RenderedBlock } from "../../lib/markdown";
  import { Copy, Check, Pencil, X, Save, RefreshCw, ChevronDown, ChevronRight, Brain, Code } from "@lucide/svelte";
  import { Button } from "$lib/components/ui/button/index.js";
  import { Textarea } from "$lib/components/ui/textarea/index.js";
  import { getTextContent, getImageUrls } from "../../lib/types";
  import type { ContentPart, GenerationStats } from "../../lib/types";
  import { formatDuration } from "../../lib/format";
  import { copyText } from "../../lib/clipboard";
  import { isSubmitEnter } from "../../lib/ime";
  import ToolCallCard from "./ToolCallCard.svelte";
  import AgentWork from "./AgentWork.svelte";
  import MessageStats from "./MessageStats.svelte";
  import StatsBreakdown from "./StatsBreakdown.svelte";
  import TextEditDialog from "./TextEditDialog.svelte";
  import { screenWidth } from "../../stores/theme";
  import type { WorkItem } from "./AgentWork.svelte";

  interface Props {
    role: "user" | "assistant" | "system";
    content: string | ContentPart[];
    reasoning_content?: string;
    reasoningTimeMs?: number;
    isStreaming?: boolean;
    isReasoning?: boolean;
    toolResults?: ToolWorkResult[];
    workItems?: WorkItem[];
    /**
     * Stats of the request this message belongs to: for an assistant message
     * its own turn, for a user message the turn it prompted. Prompt processing
     * is shown under the user message, reasoning in the Reasoning header, and
     * the response under the reply.
     */
    stats?: GenerationStats;
    /** That request is still in flight, so the stats are updating. */
    statsLive?: boolean;
    /** Tool calls included in an aggregated agent response. */
    toolCallCount?: number;
    onEdit?: (newContent: string) => void;
    onRegenerate?: () => void;
  }

  interface ToolWorkResult {
    name: string;
    label: string;
    args: string;
    content: string;
    ok?: boolean;
    durationMs?: number;
    running?: boolean;
  }

  let {
    role,
    content,
    reasoning_content = "",
    reasoningTimeMs = 0,
    isStreaming = false,
    isReasoning = false,
    toolResults = [],
    workItems = [],
    stats,
    statsLive = false,
    toolCallCount = 0,
    onEdit,
    onRegenerate,
  }: Props = $props();

  let textContent = $derived(getTextContent(content));
  let imageUrls = $derived(getImageUrls(content));
  let hasImages = $derived(imageUrls.length > 0);
  let hasMessageText = $derived(Boolean(textContent.trim()));
  let isReasoningOnly = $derived(
    role === "assistant" && !hasMessageText && !hasImages && Boolean(reasoning_content || workItems.length)
  );
  let canEdit = $derived(onEdit !== undefined && !hasImages);
  let showActions = $derived(!isStreaming && hasMessageText);
  // The footer shows the whole turn in one line; a chevron expands it into
  // the detailed table. While the model is still thinking the footer would
  // only repeat the live Reasoning header, so it waits for the answer; once
  // the turn is over it shows even for a thinking-only turn, since that is
  // where the stop reason and the details live.
  let showGenerationStats = $derived(
    stats?.generation?.tokens !== undefined && !(isStreaming && stats?.reasoning && !stats?.answer)
  );
  // Per message and deliberately not persisted: every new reply starts
  // collapsed. The chat keys its list by position, so regenerating a reply
  // reuses this component and keeps the choice made for it.
  let statsExpanded = $state(false);
  // The user message's prompt line pulses until the first generated token.
  let waitingForFirstToken = $derived(statsLive && stats?.generation?.tokens === undefined);

  let streamingCache = createStreamingCache();
  let renderedParts = $derived.by(() => {
    if (role !== "assistant") {
      return { blocks: [{ id: -1, html: escapeHtml(textContent).replace(/\n/g, '<br>') }] as RenderedBlock[], pendingHtml: "" };
    }
    if (!isStreaming) {
      streamingCache = createStreamingCache();
      return { blocks: [{ id: -1, html: renderMarkdown(textContent) }] as RenderedBlock[], pendingHtml: "" };
    }
    return renderStreamingMarkdown(textContent, streamingCache);
  });
  let copied = $state(false);
  let showRaw = $state(false);
  let isEditing = $state(false);
  let editContent = $state("");
  // In-place editing suits a mouse and a wide page. On a phone the on-screen
  // keyboard covers half the screen, so the edit opens in a sheet that
  // tracks the keyboard instead.
  let editInDialog = $derived($screenWidth === "xs" || $screenWidth === "sm");
  let showEditDialog = $state(false);
  let showReasoning = $state(false);
  let modalImageUrl = $state<string | null>(null);

  async function copyToClipboard() {
    if (await copyText(textContent)) {
      copied = true;
      setTimeout(() => (copied = false), 2000);
    }
  }

  function startEdit() {
    if (editInDialog) {
      showEditDialog = true;
      return;
    }
    editContent = textContent;
    isEditing = true;
  }

  function submitEdit(text: string) {
    const trimmed = text.trim();
    if (onEdit && trimmed && trimmed !== textContent) {
      onEdit(trimmed);
    }
  }

  function cancelEdit() {
    isEditing = false;
    editContent = "";
  }

  function saveEdit() {
    submitEdit(editContent);
    isEditing = false;
    editContent = "";
  }

  function saveDialogEdit(text: string) {
    showEditDialog = false;
    submitEdit(text);
  }

  function openModal(imageUrl: string) {
    modalImageUrl = imageUrl;
    document.body.style.overflow = "hidden";
  }

  function closeModal(event?: MouseEvent) {
    // Only close if clicking the background, not the image
    if (event && event.target !== event.currentTarget) {
      return;
    }
    modalImageUrl = null;
    document.body.style.overflow = "";
  }

  function handleModalKeyDown(event: KeyboardEvent) {
    if (event.key === "Escape") {
      closeModal();
    }
  }

  function handleKeyDown(event: KeyboardEvent) {
    if (isSubmitEnter(event)) {
      event.preventDefault();
      saveEdit();
    } else if (event.key === "Escape") {
      cancelEdit();
    }
  }

  const COPY_SVG = `<svg xmlns="http://www.w3.org/2000/svg" width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><rect width="14" height="14" x="8" y="8" rx="2" ry="2"/><path d="M4 16c-1.1 0-2-.9-2-2V4c0-1.1.9-2 2-2h10c1.1 0 2 .9 2 2"/></svg>`;
  const CHECK_SVG = `<svg xmlns="http://www.w3.org/2000/svg" width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M20 6 9 17l-5-5"/></svg>`;

  function codeBlockCopy(node: HTMLElement) {
    function attachButtons() {
      node.querySelectorAll<HTMLPreElement>('pre:not([data-copy-btn])').forEach(pre => {
        pre.setAttribute('data-copy-btn', 'true');
        const btn = document.createElement('button');
        btn.className = 'code-copy-btn';
        btn.title = 'Copy code';
        btn.innerHTML = COPY_SVG;
        btn.addEventListener('click', async () => {
          const text = pre.querySelector('code')?.textContent ?? pre.textContent ?? '';
          if (await copyText(text)) {
            btn.innerHTML = CHECK_SVG;
            btn.classList.add('copied');
            setTimeout(() => { btn.innerHTML = COPY_SVG; btn.classList.remove('copied'); }, 2000);
          }
        });
        pre.appendChild(btn);
      });
    }
    attachButtons();
    const mo = new MutationObserver(attachButtons);
    mo.observe(node, { childList: true, subtree: true });
    return { destroy: () => mo.disconnect() };
  }
</script>

<div class="flex min-w-0 flex-col {role === 'user' ? 'items-end' : 'items-start'}" class:mb-4={!isReasoningOnly}>
  {#if role === "assistant"}
    <!-- Replies sit directly on the page: no box, full width, so a phone
         gets the whole line for the text. -->
    <div class="group w-full min-w-0">
      {#if workItems.length}
        <AgentWork items={workItems} running={isStreaming} />
      {:else if reasoning_content || isReasoning}
        <div class:mb-2={!isReasoningOnly}>
          <button
            class="text-muted-foreground hover:text-foreground -ml-1 flex max-w-full flex-wrap items-center gap-x-2 gap-y-1 rounded-md px-1 py-1 text-xs transition-colors"
            onclick={() => showReasoning = !showReasoning}
            aria-expanded={showReasoning}
          >
            <Brain class="size-3.5 shrink-0" />
            <span class="font-medium">Reasoning</span>
            {#if stats?.reasoning}
              <MessageStats phase={stats.reasoning} kind="reasoning" />
            {:else}
              <span>
                {reasoning_content.length} chars{#if !isReasoning && reasoningTimeMs > 0}, {formatDuration(reasoningTimeMs, { precision: 1, subSecondMs: true })}{/if}
              </span>
            {/if}
            {#if isReasoning}
              <span class="flex items-center gap-1">
                <span class="bg-primary h-1.5 w-1.5 animate-pulse rounded-full"></span>
                thinking…
              </span>
            {/if}
            {#if showReasoning}
              <ChevronDown class="size-3.5 shrink-0" />
            {:else}
              <ChevronRight class="size-3.5 shrink-0" />
            {/if}
          </button>
          {#if showReasoning}
            <div class="border-border text-muted-foreground mt-1 mb-2 whitespace-pre-wrap border-l-2 pl-3 text-sm break-words">
              {reasoning_content}{#if isReasoning}<span class="ml-0.5 inline-block h-4 w-1.5 animate-pulse bg-current"></span>{/if}
            </div>
          {/if}
        </div>
      {/if}
      {#if hasImages}
        <div class="mb-3 flex flex-wrap gap-2">
          {#each imageUrls as imageUrl, idx (idx)}
            <button
              onclick={() => openModal(imageUrl)}
              class="cursor-pointer overflow-hidden rounded-lg transition-opacity hover:opacity-80"
            >
              <img
                src={imageUrl}
                alt="Image {idx + 1}"
                class="max-h-64"
              />
            </button>
          {/each}
        </div>
      {/if}
      {#if !workItems.length && toolResults.length}
        <div class="mb-3">
          {#each toolResults as tool, idx (idx)}
            <ToolCallCard
              name={tool.name}
              label={tool.label}
              args={tool.args}
              content={tool.content}
              ok={tool.ok}
              durationMs={tool.durationMs}
              running={tool.running}
            />
          {/each}
        </div>
      {/if}
      {#if showRaw}
        <div class="whitespace-pre-wrap font-mono text-sm break-words">{textContent}</div>
      {:else}
        <div class="prose prose-sm dark:prose-invert max-w-none min-w-0 break-words" use:codeBlockCopy>
          {#each renderedParts.blocks as block (block.id)}
            {@html block.html}
          {/each}
          {@html renderedParts.pendingHtml}
          {#if isStreaming && !isReasoning}
            <span class="inline-block w-2 h-4 bg-current animate-pulse ml-0.5"></span>
          {/if}
        </div>
      {/if}
      {#if showActions || showGenerationStats}
        <!-- Touch screens have no hover, so the buttons stay visible there;
             on larger screens they fade in on hover to keep the page quiet. -->
        <div class="mt-1 flex flex-wrap items-center gap-x-2 gap-y-1">
          {#if showActions}
            <div
              class="-ml-1 flex items-center gap-0.5 transition-opacity md:focus-within:opacity-100 md:group-hover:opacity-100 {showRaw || copied ? 'md:opacity-100' : 'md:opacity-0'}"
            >
              {#if onRegenerate}
                <Button variant="ghost" size="icon-xs" class="text-muted-foreground" onclick={onRegenerate} title="Regenerate response">
                  <RefreshCw />
                </Button>
              {/if}
              <Button
                variant="ghost"
                size="icon-xs"
                class="text-muted-foreground"
                onclick={copyToClipboard}
                title={copied ? "Copied!" : "Copy to clipboard"}
              >
                {#if copied}
                  <Check class="text-success" />
                {:else}
                  <Copy />
                {/if}
              </Button>
              <Button
                variant="ghost"
                size="icon-xs"
                class={showRaw ? "text-primary" : "text-muted-foreground"}
                onclick={() => showRaw = !showRaw}
                title={showRaw ? "Show rendered" : "Show raw"}
              >
                <Code />
              </Button>
            </div>
          {/if}
          {#if showGenerationStats && stats}
            <button
              class="text-muted-foreground hover:text-foreground ml-auto inline-flex items-center gap-1 rounded-md px-1 py-0.5 transition-colors"
              onclick={() => statsExpanded = !statsExpanded}
              title={statsExpanded ? "Hide detailed stats" : "Show detailed stats"}
              aria-expanded={statsExpanded}
            >
              {#if statsExpanded}
                <ChevronDown class="size-3.5" />
              {:else}
                <ChevronRight class="size-3.5" />
              {/if}
              <MessageStats phase={stats.generation} kind="generation" />
            </button>
          {/if}
        </div>
        {#if showGenerationStats && stats && statsExpanded}
          <StatsBreakdown {stats} {toolCallCount} />
        {/if}
      {/if}
    </div>
  {:else}
    <div class="group flex min-w-0 flex-col items-end {isEditing ? 'w-full sm:w-4/5' : 'max-w-[85%]'}">
      {#if isEditing}
        <div class="flex w-full flex-col gap-2">
          <Textarea class="resize-none" rows={3} bind:value={editContent} onkeydown={handleKeyDown} />
          <div class="flex justify-end gap-1">
            <Button variant="ghost" size="sm" onclick={cancelEdit} title="Cancel">
              <X />
              Cancel
            </Button>
            <Button size="sm" onclick={saveEdit} title="Save and resend">
              <Save />
              Send
            </Button>
          </div>
        </div>
      {:else}
        <div class="bg-primary text-primary-foreground rounded-2xl rounded-br-md px-3.5 py-2 sm:px-4">
          {#if hasImages}
            <div class="flex flex-wrap gap-2" class:mb-2={hasMessageText}>
              {#each imageUrls as imageUrl, idx (idx)}
                <button
                  onclick={() => openModal(imageUrl)}
                  class="cursor-pointer overflow-hidden rounded-lg transition-opacity hover:opacity-80"
                >
                  <img
                    src={imageUrl}
                    alt="Image {idx + 1}"
                    class="max-w-[200px]"
                  />
                </button>
              {/each}
            </div>
          {/if}
          {#if hasMessageText}
            <div class="whitespace-pre-wrap break-words">{textContent}</div>
          {/if}
        </div>
      {/if}
      {#if !isEditing && (stats?.prompt || waitingForFirstToken || hasMessageText || canEdit)}
        <div class="mt-1 flex flex-wrap items-center justify-end gap-x-2 gap-y-1">
          <MessageStats
            phase={stats?.prompt}
            kind="prompt"
            waiting={waitingForFirstToken}
            cached={stats?.cachedTokens}
          />
          <div
            class="-mr-1 flex items-center gap-0.5 transition-opacity md:focus-within:opacity-100 md:group-hover:opacity-100 {copied ? 'md:opacity-100' : 'md:opacity-0'}"
          >
            {#if hasMessageText}
              <Button
                variant="ghost"
                size="icon-xs"
                class="text-muted-foreground"
                onclick={copyToClipboard}
                title={copied ? "Copied!" : "Copy to clipboard"}
              >
                {#if copied}
                  <Check class="text-success" />
                {:else}
                  <Copy />
                {/if}
              </Button>
            {/if}
            {#if canEdit}
              <Button variant="ghost" size="icon-xs" class="text-muted-foreground" onclick={startEdit} title="Edit message">
                <Pencil />
              </Button>
            {/if}
          </div>
        </div>
      {/if}
    </div>
  {/if}
</div>

{#if showEditDialog}
  <TextEditDialog
    value={textContent}
    title="Edit message"
    saveLabel="Send"
    onsave={saveDialogEdit}
    oncancel={() => (showEditDialog = false)}
  />
{/if}

<!-- Full-size image modal -->
{#if modalImageUrl}
  <div
    class="fixed inset-0 z-50 flex items-center justify-center bg-black/80 p-4"
    onclick={(e) => closeModal(e)}
    onkeydown={handleModalKeyDown}
    role="button"
    tabindex="-1"
  >
    <button
      class="absolute right-4 top-4 rounded-lg bg-white/10 p-2 text-white transition-colors hover:bg-white/20"
      onclick={() => closeModal()}
      title="Close"
    >
      <X class="size-6" />
    </button>
    <img
      src={modalImageUrl}
      alt=""
      class="max-w-full max-h-full rounded-md pointer-events-none"
    />
  </div>
{/if}

<style>
  .prose :global(pre) {
    position: relative;
    background-color: var(--muted);
    border: 1px solid var(--border);
    border-radius: 0.375rem;
    padding: 0.75rem;
    padding-right: 2.5rem;
    overflow-x: auto;
    margin: 0.5rem 0;
  }

  .prose :global(.code-copy-btn) {
    position: absolute;
    top: 0.375rem;
    right: 0.375rem;
    display: flex;
    align-items: center;
    justify-content: center;
    padding: 0.25rem;
    border-radius: 0.25rem;
    border: 1px solid var(--border);
    background: var(--muted);
    color: var(--muted-foreground);
    cursor: pointer;
    transition: background-color 0.15s;
    line-height: 0;
  }

  .prose :global(.code-copy-btn:hover) {
    background: var(--accent);
  }

  .prose :global(.code-copy-btn.copied) {
    color: var(--success);
    opacity: 1;
  }

  .prose :global(code) {
    font-family: ui-monospace, SFMono-Regular, Menlo, Monaco, Consolas, monospace;
    font-size: 0.875em;
  }

  .prose :global(pre code) {
    background: none;
    padding: 0;
  }

  .prose :global(code:not(pre code)) {
    background-color: var(--muted);
    padding: 0.125rem 0.25rem;
    border-radius: 0.25rem;
    border: 1px solid var(--border);
  }

  .prose :global(p) {
    margin: 0.5rem 0;
  }

  .prose :global(p:first-child) {
    margin-top: 0;
  }

  .prose :global(p:last-child) {
    margin-bottom: 0;
  }

  .prose :global(ul),
  .prose :global(ol) {
    margin: 0.5rem 0;
    padding-left: 1.5rem;
  }

  .prose :global(ul) {
    list-style: disc;
  }

  .prose :global(ol) {
    list-style: decimal;
  }

  .prose :global(li) {
    margin: 0.25rem 0;
  }

  .prose :global(h1),
  .prose :global(h2),
  .prose :global(h3),
  .prose :global(h4) {
    margin: 1rem 0 0.5rem 0;
    font-weight: 600;
  }

  .prose :global(h1:first-child),
  .prose :global(h2:first-child),
  .prose :global(h3:first-child),
  .prose :global(h4:first-child) {
    margin-top: 0;
  }

  .prose :global(blockquote) {
    border-left: 3px solid var(--primary);
    padding-left: 1rem;
    margin: 0.5rem 0;
    font-style: italic;
  }

  .prose :global(a) {
    color: var(--primary);
    text-decoration: underline;
  }

  .prose :global(table) {
    width: 100%;
    border-collapse: collapse;
    margin: 0.5rem 0;
  }

  .prose :global(th),
  .prose :global(td) {
    border: 1px solid var(--border);
    padding: 0.5rem;
    text-align: left;
  }

  .prose :global(th) {
    background-color: var(--muted);
    font-weight: 600;
  }

  /* Highlight.js theme overrides for dark mode */
  :global(.dark) .prose :global(.hljs) {
    background: transparent;
  }
</style>
