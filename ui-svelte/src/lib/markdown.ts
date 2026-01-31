import { unified } from "unified";
import remarkParse from "remark-parse";
import remarkGfm from "remark-gfm";
import remarkRehype from "remark-rehype";
import rehypeStringify from "rehype-stringify";
import hljs from "highlight.js";
import { visit } from "unist-util-visit";
import type { Element, Root } from "hast";

// Custom plugin to highlight code blocks with highlight.js
function rehypeHighlight() {
  return (tree: Root) => {
    visit(tree, "element", (node: Element) => {
      if (node.tagName === "code" && node.properties) {
        const className = node.properties.className;
        const classes = Array.isArray(className)
          ? className.filter((c): c is string => typeof c === "string")
          : [];
        const lang = classes
          .find((c) => c.startsWith("language-"))
          ?.replace("language-", "");

        const text = node.children
          .filter((child): child is { type: "text"; value: string } => child.type === "text")
          .map((child) => child.value)
          .join("");

        if (text) {
          const language = lang && hljs.getLanguage(lang) ? lang : "plaintext";
          const highlighted = hljs.highlight(text, { language }).value;

          // Replace the text node with raw HTML
          node.properties.className = [
            "hljs",
            `language-${language}`,
            ...classes.filter((c) => !c.startsWith("language-")),
          ];
          // Use type assertion since we're modifying the tree structure
          (node.children as unknown) = [
            { type: "raw", value: highlighted },
          ];
        }
      }
    });
  };
}

// Custom plugin to escape raw HTML for XSS protection
function rehypeEscapeHtml() {
  return (tree: Root) => {
    visit(tree, "raw", (node: { type: "raw"; value: string }, _index, parent) => {
      // Only escape raw nodes that aren't from our highlighting plugin
      // (highlight.js output is safe)
      if (parent && "tagName" in parent && parent.tagName !== "code") {
        node.value = escapeHtml(node.value);
      }
    });
  };
}

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

// Create the unified processor
const processor = unified()
  .use(remarkParse)
  .use(remarkGfm)
  .use(remarkRehype, { allowDangerousHtml: true })
  .use(rehypeHighlight)
  .use(rehypeEscapeHtml)
  .use(rehypeStringify, { allowDangerousHtml: true });

export function renderMarkdown(content: string): string {
  if (!content) {
    return "";
  }

  try {
    const result = processor.processSync(content);
    return String(result);
  } catch {
    // Fallback to escaped plain text if markdown parsing fails
    return `<p>${escapeHtml(content)}</p>`;
  }
}
