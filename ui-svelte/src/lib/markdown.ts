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
      if (trimmed === "$$" || trimmed === "\\]") {
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
    if (trimmed === "$$" || trimmed === "\\[") {
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

export function closePendingBlock(pending: string): string {
  if (!pending) return "";

  const lines = pending.split("\n");
  let inFence = false;
  let fenceStr = "";
  let inMathBlock = false;
  let mathClose = "";

  for (const line of lines) {
    const trimmed = line.trimEnd();

    if (inFence) {
      if (new RegExp(`^\\s*${fenceStr[0] === "~" ? "~~~" : "\\`\\`\\`"}\\s*$`).test(trimmed)) {
        inFence = false;
        fenceStr = "";
      }
      continue;
    }

    if (inMathBlock) {
      if (trimmed === "$$" || trimmed === "\\]") {
        inMathBlock = false;
        mathClose = "";
      }
      continue;
    }

    const fenceMatch = trimmed.match(/^(\s*)(```|~~~)/);
    if (fenceMatch) {
      fenceStr = fenceMatch[2];
      inFence = true;
      continue;
    }

    if (trimmed === "$$") {
      inMathBlock = true;
      mathClose = "$$";
      continue;
    }

    if (trimmed === "\\[") {
      inMathBlock = true;
      mathClose = "\\]";
      continue;
    }
  }

  if (inFence) return pending + "\n" + fenceStr;
  if (inMathBlock) return pending + "\n" + mathClose;
  return pending;
}

export interface RenderedBlock {
  id: number;
  html: string;
}

export interface StreamingCache {
  blocks: RenderedBlock[];
  nextId: number;
  completeKey: string;
}

export function createStreamingCache(): StreamingCache {
  return { blocks: [], nextId: 0, completeKey: "" };
}

export function renderStreamingMarkdown(
  text: string,
  cache: StreamingCache,
): { blocks: RenderedBlock[]; pendingHtml: string } {
  const { complete, pending } = splitCompleteBlocks(text);

  if (complete) {
    if (cache.completeKey !== complete) {
      if (complete.startsWith(cache.completeKey) && cache.completeKey.length > 0) {
        // Complete section grew — render only the new part as a new block
        const newPart = complete.slice(cache.completeKey.length);
        cache.blocks = [...cache.blocks, { id: cache.nextId++, html: renderMarkdown(newPart) }];
      } else {
        // Complete section changed unexpectedly — re-render as single block
        cache.blocks = [{ id: cache.nextId++, html: renderMarkdown(complete) }];
      }
      cache.completeKey = complete;
    }
  } else if (cache.blocks.length > 0) {
    cache.blocks = [];
    cache.completeKey = "";
  }

  let pendingHtml = "";
  if (pending) {
    const closed = closePendingBlock(pending);
    pendingHtml = renderMarkdown(closed);
  }

  return { blocks: cache.blocks, pendingHtml };
}

// Convert \[...\] to $$...$$ and \(...\) to $...$
export function normalizeLatexDelimiters(text: string): string {
  // Display math: \[...\] → $$...$$  (may span multiple lines)
  text = text.replace(/\\\[([\s\S]*?)\\\]/g, (_match, inner) => `$$${inner}$$`);
  // Inline math: \(...\) → $...$
  text = text.replace(/\\\(([\s\S]*?)\\\)/g, (_match, inner) => `$${inner}$`);
  return text;
}

export function renderMarkdown(content: string): string {
  if (!content) {
    return "";
  }

  try {
    const result = processor.processSync(normalizeLatexDelimiters(content));
    return String(result);
  } catch {
    // Fallback to escaped plain text if markdown parsing fails
    return `<p>${escapeHtml(content)}</p>`;
  }
}
