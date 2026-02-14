import { unified } from "unified";
import remarkParse from "remark-parse";
import remarkGfm from "remark-gfm";
import remarkMath from "remark-math";
import remarkRehype from "remark-rehype";
import rehypeKatex from "rehype-katex";
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


export function escapeHtml(text: string): string {
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
  .use(remarkMath)
  .use(remarkRehype, { allowDangerousHtml: true })
  .use(rehypeKatex)
  .use(rehypeHighlight)
  .use(rehypeStringify, { allowDangerousHtml: true });

export function splitCompleteBlocks(text: string): { complete: string; pending: string } {
  if (!text) {
    return { complete: "", pending: "" };
  }

  const lines = text.split("\n");
  let lastCompleteBoundary = -1; // index of last line that ends a complete block
  let inFence = false;
  let fenceChar = "";
  let inMathBlock = false;

  for (let i = 0; i < lines.length; i++) {
    const trimmed = lines[i].trimEnd();

    if (inFence) {
      // Check for closing fence: same character, at least 3, no other content
      if (new RegExp(`^\\s*${fenceChar.replace(/~/g, "\\~")}{3,}\\s*$`).test(trimmed)) {
        inFence = false;
        fenceChar = "";
        lastCompleteBoundary = i;
      }
      continue;
    }

    if (inMathBlock) {
      if (trimmed === "$$") {
        inMathBlock = false;
        lastCompleteBoundary = i;
      }
      continue;
    }

    // Check for opening fence
    const fenceMatch = trimmed.match(/^(\s*)(```|~~~)/);
    if (fenceMatch) {
      // Check if it's an opening fence (may have language info after)
      // A line with just ``` or ~~~ could be opening or closing, but since we're not in a fence it's opening
      fenceChar = fenceMatch[2][0]; // '`' or '~'
      inFence = true;
      continue;
    }

    // Check for opening math block
    if (trimmed === "$$") {
      inMathBlock = true;
      continue;
    }

    // Outside fences/math: blank line marks a complete boundary
    if (trimmed === "") {
      lastCompleteBoundary = i;
    }
  }

  if (lastCompleteBoundary < 0) {
    return { complete: "", pending: text };
  }

  const completeLines = lines.slice(0, lastCompleteBoundary + 1);
  const pendingLines = lines.slice(lastCompleteBoundary + 1);

  return {
    complete: completeLines.join("\n"),
    pending: pendingLines.join("\n"),
  };
}

export function renderStreamingMarkdown(
  text: string,
  cache: { key: string; html: string },
): { completeHtml: string; pendingHtml: string } {
  const { complete, pending } = splitCompleteBlocks(text);

  let completeHtml = "";
  if (complete) {
    if (cache.key === complete) {
      // Complete section unchanged — reuse cached HTML
      completeHtml = cache.html;
    } else if (complete.startsWith(cache.key) && cache.key.length > 0) {
      // Complete section grew — only render the new blocks and append
      const newPart = complete.slice(cache.key.length);
      completeHtml = cache.html + renderMarkdown(newPart);
      cache.key = complete;
      cache.html = completeHtml;
    } else {
      completeHtml = renderMarkdown(complete);
      cache.key = complete;
      cache.html = completeHtml;
    }
  }

  let pendingHtml = "";
  if (pending) {
    pendingHtml = escapeHtml(pending).replace(/\n/g, "<br>");
  }

  return { completeHtml, pendingHtml };
}

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
