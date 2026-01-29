<script lang="ts">
  import { renderMarkdown } from "../../lib/markdown";

  interface Props {
    role: "user" | "assistant" | "system";
    content: string;
    isStreaming?: boolean;
  }

  let { role, content, isStreaming = false }: Props = $props();

  let renderedContent = $derived(
    role === "assistant" ? renderMarkdown(content) : content
  );
</script>

<div class="flex {role === 'user' ? 'justify-end' : 'justify-start'} mb-4">
  <div
    class="max-w-[85%] rounded-lg px-4 py-2 {role === 'user'
      ? 'bg-primary text-btn-primary-text'
      : 'bg-surface border border-gray-200 dark:border-white/10'}"
  >
    {#if role === "assistant"}
      <div class="prose prose-sm dark:prose-invert max-w-none">
        {@html renderedContent}
        {#if isStreaming}
          <span class="inline-block w-2 h-4 bg-current animate-pulse ml-0.5"></span>
        {/if}
      </div>
    {:else}
      <div class="whitespace-pre-wrap">{content}</div>
    {/if}
  </div>
</div>

<style>
  .prose :global(pre) {
    background-color: var(--color-surface);
    border: 1px solid var(--color-border, rgba(128, 128, 128, 0.2));
    border-radius: 0.375rem;
    padding: 0.75rem;
    overflow-x: auto;
    margin: 0.5rem 0;
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
