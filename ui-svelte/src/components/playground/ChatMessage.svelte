<script lang="ts">
  import { renderMarkdown, escapeHtml, renderStreamingMarkdown, createStreamingCache } from "../../lib/markdown";
  import type { RenderedBlock } from "../../lib/markdown";
  import { Copy, Check, Pencil, X, Save, RefreshCw, ChevronDown, ChevronRight, Brain, Code } from "@lucide/svelte";
  import { Button } from "$lib/components/ui/button/index.js";
  import { Textarea } from "$lib/components/ui/textarea/index.js";
  import { getTextContent, getImageUrls } from "../../lib/types";
  import type { ContentPart } from "../../lib/types";

  interface Props {
    role: "user" | "assistant" | "system";
    content: string | ContentPart[];
    reasoning_content?: string;
    reasoningTimeMs?: number;
    isStreaming?: boolean;
    isReasoning?: boolean;
    onEdit?: (newContent: string) => void;
    onRegenerate?: () => void;
  }

  let { role, content, reasoning_content = "", reasoningTimeMs = 0, isStreaming = false, isReasoning = false, onEdit, onRegenerate }: Props = $props();

  let textContent = $derived(getTextContent(content));
  let imageUrls = $derived(getImageUrls(content));
  let hasImages = $derived(imageUrls.length > 0);
  let canEdit = $derived(onEdit !== undefined && !hasImages);

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
  let showReasoning = $state(false);
  let modalImageUrl = $state<string | null>(null);

  function formatDuration(ms: number): string {
    if (ms < 1000) {
      return `${ms.toFixed(0)}ms`;
    }
    return `${(ms / 1000).toFixed(1)}s`;
  }

  async function copyToClipboard() {
    try {
      if (navigator.clipboard && window.isSecureContext) {
        await navigator.clipboard.writeText(textContent);
      } else {
        // Fallback for non-secure contexts (HTTP)
        const textarea = document.createElement("textarea");
        textarea.value = textContent;
        textarea.style.position = "fixed";
        textarea.style.left = "-9999px";
        document.body.appendChild(textarea);
        textarea.select();
        document.execCommand("copy");
        document.body.removeChild(textarea);
      }
      copied = true;
      setTimeout(() => (copied = false), 2000);
    } catch (err) {
      console.error("Failed to copy:", err);
    }
  }

  function startEdit() {
    editContent = textContent;
    isEditing = true;
  }

  function cancelEdit() {
    isEditing = false;
    editContent = "";
  }

  function saveEdit() {
    if (onEdit && editContent.trim() !== textContent) {
      onEdit(editContent.trim());
    }
    isEditing = false;
    editContent = "";
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
    if (event.key === "Enter" && !event.shiftKey) {
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
          try {
            if (navigator.clipboard && window.isSecureContext) {
              await navigator.clipboard.writeText(text);
            } else {
              const ta = document.createElement('textarea');
              ta.value = text;
              ta.style.cssText = 'position:fixed;left:-9999px';
              document.body.appendChild(ta);
              ta.select();
              document.execCommand('copy');
              document.body.removeChild(ta);
            }
            btn.innerHTML = CHECK_SVG;
            btn.classList.add('copied');
            setTimeout(() => { btn.innerHTML = COPY_SVG; btn.classList.remove('copied'); }, 2000);
          } catch (e) {
            console.error('copy failed', e);
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

<div class="flex {role === 'user' ? 'justify-end' : 'justify-start'} mb-4">
  <div
    class="group relative rounded-lg px-4 py-2 {role === 'user'
      ? 'bg-primary text-primary-foreground max-w-[85%]'
      : 'bg-card w-full border sm:w-4/5'}"
  >
    {#if role === "assistant"}
      {#if reasoning_content || isReasoning}
        <div class="mb-3 overflow-hidden rounded-md border">
          <button
            class="bg-muted/50 hover:bg-muted flex w-full items-center gap-2 px-3 py-2 text-sm transition-colors"
            onclick={() => showReasoning = !showReasoning}
          >
            {#if showReasoning}
              <ChevronDown class="size-4" />
            {:else}
              <ChevronRight class="size-4" />
            {/if}
            <Brain class="size-4" />
            <span class="font-medium">Reasoning</span>
            <span class="text-muted-foreground ml-2">
              ({reasoning_content.length} chars{#if !isReasoning && reasoningTimeMs > 0}, {formatDuration(reasoningTimeMs)}{/if})
            </span>
            {#if isReasoning}
              <span class="text-muted-foreground ml-auto flex items-center gap-1">
                <span class="bg-primary h-1.5 w-1.5 animate-pulse rounded-full"></span>
                reasoning...
              </span>
            {/if}
          </button>
          {#if showReasoning}
            <div class="bg-muted/30 text-muted-foreground whitespace-pre-wrap px-3 py-2 font-mono text-sm">
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
              class="cursor-pointer rounded-md border transition-opacity hover:opacity-80"
            >
              <img
                src={imageUrl}
                alt="Image {idx + 1}"
                class="max-h-64 rounded-md"
              />
            </button>
          {/each}
        </div>
      {/if}
      {#if showRaw}
        <div class="whitespace-pre-wrap font-mono text-sm">{textContent}</div>
      {:else}
        <div class="prose prose-sm dark:prose-invert max-w-none" use:codeBlockCopy>
          {#each renderedParts.blocks as block (block.id)}
            {@html block.html}
          {/each}
          {@html renderedParts.pendingHtml}
          {#if isStreaming && !isReasoning}
            <span class="inline-block w-2 h-4 bg-current animate-pulse ml-0.5"></span>
          {/if}
        </div>
      {/if}
      {#if !isStreaming}
        <div class="mt-2 flex gap-1 border-t pt-1">
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
    {:else}
      {#if isEditing}
        <div class="flex min-w-[300px] flex-col gap-2">
          <Textarea class="resize-none" rows={3} bind:value={editContent} onkeydown={handleKeyDown} />
          <div class="flex justify-end gap-2">
            <Button variant="ghost" size="icon-sm" onclick={cancelEdit} title="Cancel">
              <X />
            </Button>
            <Button variant="ghost" size="icon-sm" onclick={saveEdit} title="Save">
              <Save />
            </Button>
          </div>
        </div>
      {:else}
        {#if hasImages}
          <div class="mb-2 flex flex-wrap gap-2">
            {#each imageUrls as imageUrl, idx (idx)}
              <button
                onclick={() => openModal(imageUrl)}
                class="cursor-pointer rounded-md border border-white/20 transition-opacity hover:opacity-80"
              >
                <img
                  src={imageUrl}
                  alt="Image {idx + 1}"
                  class="max-w-[200px] rounded-md"
                />
              </button>
            {/each}
          </div>
        {/if}
        <div class="whitespace-pre-wrap pr-8">{textContent}</div>
        {#if canEdit}
          <button
            class="absolute right-2 top-2 rounded-lg bg-white/20 p-1.5 opacity-0 shadow-sm transition-opacity hover:bg-white/30 group-hover:opacity-100"
            onclick={startEdit}
            title="Edit message"
          >
            <Pencil class="size-4" />
          </button>
        {/if}
      {/if}
    {/if}
  </div>
</div>

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
