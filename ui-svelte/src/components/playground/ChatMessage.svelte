<script lang="ts">
  import { renderMarkdown, escapeHtml, renderStreamingMarkdown, createStreamingCache } from "../../lib/markdown";
  import type { RenderedBlock } from "../../lib/markdown";
  import { Copy, Check, Pencil, X, Save, RefreshCw, ChevronDown, ChevronRight, Brain, Code } from "lucide-svelte";
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
    class="relative group rounded-lg px-4 py-2 {role === 'user'
      ? 'max-w-[85%] bg-primary text-btn-primary-text'
      : 'w-full sm:w-4/5 bg-surface border border-gray-200 dark:border-white/10'}"
  >
    {#if role === "assistant"}
      {#if reasoning_content || isReasoning}
        <div class="mb-3 border border-gray-200 dark:border-white/10 rounded overflow-hidden">
          <button
            class="w-full flex items-center gap-2 px-3 py-2 bg-gray-50 dark:bg-white/5 hover:bg-gray-100 dark:hover:bg-white/10 transition-colors text-sm"
            onclick={() => showReasoning = !showReasoning}
          >
            {#if showReasoning}
              <ChevronDown class="w-4 h-4" />
            {:else}
              <ChevronRight class="w-4 h-4" />
            {/if}
            <Brain class="w-4 h-4" />
            <span class="font-medium">Reasoning</span>
            <span class="text-txtsecondary ml-2">
              ({reasoning_content.length} chars{#if !isReasoning && reasoningTimeMs > 0}, {formatDuration(reasoningTimeMs)}{/if})
            </span>
            {#if isReasoning}
              <span class="ml-auto flex items-center gap-1 text-txtsecondary">
                <span class="w-1.5 h-1.5 bg-primary rounded-full animate-pulse"></span>
                reasoning...
              </span>
            {/if}
          </button>
          {#if showReasoning}
            <div class="px-3 py-2 bg-gray-50/50 dark:bg-white/[0.02] text-sm text-txtsecondary whitespace-pre-wrap font-mono">
              {reasoning_content}{#if isReasoning}<span class="inline-block w-1.5 h-4 bg-current animate-pulse ml-0.5"></span>{/if}
            </div>
          {/if}
        </div>
      {/if}
      {#if hasImages}
        <div class="mb-3 flex flex-wrap gap-2">
          {#each imageUrls as imageUrl, idx (idx)}
            <button
              onclick={() => openModal(imageUrl)}
              class="cursor-pointer rounded border border-gray-200 dark:border-white/10 hover:opacity-80 transition-opacity"
            >
              <img
                src={imageUrl}
                alt="Image {idx + 1}"
                class="max-h-64 rounded"
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
        <div class="flex gap-1 mt-2 pt-1 border-t border-gray-200 dark:border-white/10">
          {#if onRegenerate}
            <button
              class="p-1 rounded hover:bg-black/10 dark:hover:bg-white/10 text-txtsecondary"
              onclick={onRegenerate}
              title="Regenerate response"
            >
              <RefreshCw class="w-4 h-4" />
            </button>
          {/if}
          <button
            class="p-1 rounded hover:bg-black/10 dark:hover:bg-white/10 text-txtsecondary"
            onclick={copyToClipboard}
            title={copied ? "Copied!" : "Copy to clipboard"}
          >
            {#if copied}
              <Check class="w-4 h-4 text-green-500" />
            {:else}
              <Copy class="w-4 h-4" />
            {/if}
          </button>
          <button
            class="p-1 rounded hover:bg-black/10 dark:hover:bg-white/10 {showRaw ? 'text-primary' : 'text-txtsecondary'}"
            onclick={() => showRaw = !showRaw}
            title={showRaw ? "Show rendered" : "Show raw"}
          >
            <Code class="w-4 h-4" />
          </button>
        </div>
      {/if}
    {:else}
      {#if isEditing}
        <div class="flex flex-col gap-2 min-w-[300px]">
          <textarea
            class="w-full px-3 py-2 rounded border border-gray-200 dark:border-white/10 bg-surface text-txtmain focus:outline-none focus:ring-2 focus:ring-primary resize-none"
            rows="3"
            bind:value={editContent}
            onkeydown={handleKeyDown}
          ></textarea>
          <div class="flex justify-end gap-2">
            <button
              class="p-1.5 rounded hover:bg-white/20"
              onclick={cancelEdit}
              title="Cancel"
            >
              <X class="w-4 h-4" />
            </button>
            <button
              class="p-1.5 rounded hover:bg-white/20"
              onclick={saveEdit}
              title="Save"
            >
              <Save class="w-4 h-4" />
            </button>
          </div>
        </div>
      {:else}
        {#if hasImages}
          <div class="mb-2 flex flex-wrap gap-2">
            {#each imageUrls as imageUrl, idx (idx)}
              <button
                onclick={() => openModal(imageUrl)}
                class="cursor-pointer rounded border border-white/20 hover:opacity-80 transition-opacity"
              >
                <img
                  src={imageUrl}
                  alt="Image {idx + 1}"
                  class="max-w-[200px] rounded"
                />
              </button>
            {/each}
          </div>
        {/if}
        <div class="whitespace-pre-wrap pr-8">{textContent}</div>
        {#if canEdit}
          <button
            class="absolute top-2 right-2 p-1.5 rounded-lg opacity-0 group-hover:opacity-100 transition-opacity bg-white/20 hover:bg-white/30 shadow-sm"
            onclick={startEdit}
            title="Edit message"
          >
            <Pencil class="w-4 h-4" />
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
      class="absolute top-4 right-4 p-2 rounded-lg bg-white/10 hover:bg-white/20 text-white transition-colors"
      onclick={() => closeModal()}
      title="Close"
    >
      <X class="w-6 h-6" />
    </button>
    <img
      src={modalImageUrl}
      alt=""
      class="max-w-full max-h-full rounded pointer-events-none"
    />
  </div>
{/if}

<style>
  .prose :global(pre) {
    position: relative;
    background-color: var(--color-surface);
    border: 1px solid var(--color-border, rgba(128, 128, 128, 0.2));
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
    border: 1px solid var(--color-border);
    background: var(--color-surface);
    color: var(--color-txtsecondary);
    cursor: pointer;
    transition: background-color 0.15s;
    line-height: 0;
  }

  .prose :global(.code-copy-btn:hover) {
    background: var(--color-secondary);
  }

  .prose :global(.code-copy-btn.copied) {
    color: var(--color-success);
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
    background-color: var(--color-surface);
    padding: 0.125rem 0.25rem;
    border-radius: 0.25rem;
    border: 1px solid var(--color-border, rgba(128, 128, 128, 0.2));
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
    border-left: 3px solid var(--color-primary);
    padding-left: 1rem;
    margin: 0.5rem 0;
    font-style: italic;
  }

  .prose :global(a) {
    color: var(--color-primary);
    text-decoration: underline;
  }

  .prose :global(table) {
    width: 100%;
    border-collapse: collapse;
    margin: 0.5rem 0;
  }

  .prose :global(th),
  .prose :global(td) {
    border: 1px solid var(--color-border, rgba(128, 128, 128, 0.2));
    padding: 0.5rem;
    text-align: left;
  }

  .prose :global(th) {
    background-color: var(--color-surface);
    font-weight: 600;
  }

  /* Highlight.js theme overrides for dark mode */
  :global(.dark) .prose :global(.hljs) {
    background: transparent;
  }
</style>
