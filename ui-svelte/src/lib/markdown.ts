import { unified } from "unified";
import remarkParse from "remark-parse";
import remarkGfm from "remark-gfm";
import remarkMath from "remark-math";
import remarkRehype from "remark-rehype";
import rehypeKatex from "rehype-katex";
import rehypeStringify from "rehype-stringify";
// Scoped here (rather than in the global stylesheet) so it ships with this
// module's chunk instead of loading on every page.
import "katex/dist/katex.min.css";
import hljs from "highlight.js/lib/core";
import bash from "highlight.js/lib/languages/bash";
import c from "highlight.js/lib/languages/c";
import cpp from "highlight.js/lib/languages/cpp";
import csharp from "highlight.js/lib/languages/csharp";
import css from "highlight.js/lib/languages/css";
import diff from "highlight.js/lib/languages/diff";
import go from "highlight.js/lib/languages/go";
import graphql from "highlight.js/lib/languages/graphql";
import ini from "highlight.js/lib/languages/ini";
import java from "highlight.js/lib/languages/java";
import javascript from "highlight.js/lib/languages/javascript";
import json from "highlight.js/lib/languages/json";
import kotlin from "highlight.js/lib/languages/kotlin";
import less from "highlight.js/lib/languages/less";
import lua from "highlight.js/lib/languages/lua";
import makefile from "highlight.js/lib/languages/makefile";
import markdown from "highlight.js/lib/languages/markdown";
import objectivec from "highlight.js/lib/languages/objectivec";
import perl from "highlight.js/lib/languages/perl";
import php from "highlight.js/lib/languages/php";
import phpTemplate from "highlight.js/lib/languages/php-template";
import plaintext from "highlight.js/lib/languages/plaintext";
import python from "highlight.js/lib/languages/python";
import pythonRepl from "highlight.js/lib/languages/python-repl";
import r from "highlight.js/lib/languages/r";
import ruby from "highlight.js/lib/languages/ruby";
import rust from "highlight.js/lib/languages/rust";
import scss from "highlight.js/lib/languages/scss";
import shell from "highlight.js/lib/languages/shell";
import sql from "highlight.js/lib/languages/sql";
import swift from "highlight.js/lib/languages/swift";
import typescript from "highlight.js/lib/languages/typescript";
import vbnet from "highlight.js/lib/languages/vbnet";
import wasm from "highlight.js/lib/languages/wasm";
import xml from "highlight.js/lib/languages/xml";
import yaml from "highlight.js/lib/languages/yaml";
import { visit } from "unist-util-visit";
import type { Element, Root } from "hast";

// Curated language subset (~35 langs) instead of highlight.js's full ~190
// language bundle, which was the single largest contributor to bundle size.
hljs.registerLanguage("bash", bash);
hljs.registerLanguage("c", c);
hljs.registerLanguage("cpp", cpp);
hljs.registerLanguage("csharp", csharp);
hljs.registerLanguage("css", css);
hljs.registerLanguage("diff", diff);
hljs.registerLanguage("go", go);
hljs.registerLanguage("graphql", graphql);
hljs.registerLanguage("ini", ini);
hljs.registerLanguage("java", java);
hljs.registerLanguage("javascript", javascript);
hljs.registerLanguage("json", json);
hljs.registerLanguage("kotlin", kotlin);
hljs.registerLanguage("less", less);
hljs.registerLanguage("lua", lua);
hljs.registerLanguage("makefile", makefile);
hljs.registerLanguage("markdown", markdown);
hljs.registerLanguage("objectivec", objectivec);
hljs.registerLanguage("perl", perl);
hljs.registerLanguage("php", php);
hljs.registerLanguage("php-template", phpTemplate);
hljs.registerLanguage("plaintext", plaintext);
hljs.registerLanguage("python", python);
hljs.registerLanguage("python-repl", pythonRepl);
hljs.registerLanguage("r", r);
hljs.registerLanguage("ruby", ruby);
hljs.registerLanguage("rust", rust);
hljs.registerLanguage("scss", scss);
hljs.registerLanguage("shell", shell);
hljs.registerLanguage("sql", sql);
hljs.registerLanguage("swift", swift);
hljs.registerLanguage("typescript", typescript);
hljs.registerLanguage("vbnet", vbnet);
hljs.registerLanguage("wasm", wasm);
hljs.registerLanguage("xml", xml);
hljs.registerLanguage("yaml", yaml);

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
