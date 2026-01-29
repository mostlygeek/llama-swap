import { Marked } from "marked";
import hljs from "highlight.js";

const marked = new Marked({
  gfm: true,
  breaks: true,
});

// Custom renderer for code blocks with syntax highlighting and HTML sanitization
marked.use({
  renderer: {
    code({ text, lang }: { text: string; lang?: string }) {
      const language = lang && hljs.getLanguage(lang) ? lang : "plaintext";
      const highlighted = hljs.highlight(text, { language }).value;
      return `<pre><code class="hljs language-${language}">${highlighted}</code></pre>`;
    },
    // Escape HTML in inline code
    codespan({ text }: { text: string }) {
      return `<code>${escapeHtml(text)}</code>`;
    },
    // Escape raw HTML to prevent XSS
    html({ text }: { text: string }) {
      return escapeHtml(text);
    },
  },
});

function escapeHtml(text: string): string {
  const htmlEntities: Record<string, string> = {
    "&": "&amp;",
    "<": "&lt;",
    ">": "&gt;",
    '"': "&quot;",
    "'": "&#39;",
  };
  return text.replace(/[&<>"']/g, (char) => htmlEntities[char]);
}

export function renderMarkdown(content: string): string {
  if (!content) {
    return "";
  }

  try {
    const result = marked.parse(content);
    // marked.parse can return string or Promise<string>, but with our config it's sync
    return typeof result === "string" ? result : "";
  } catch {
    // Fallback to escaped plain text if markdown parsing fails
    return `<p>${escapeHtml(content)}</p>`;
  }
}
