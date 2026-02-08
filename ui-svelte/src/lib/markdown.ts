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

// Regex patterns for block detection
const PATTERNS = {
  // Fenced code block start: ``` or ~~~ followed by optional language
  fencedCodeStart: /^(```+|~~~+)(.*)$/,
  // Header: 1-6 # followed by space
  header: /^(#{1,6})\s/,
  // Blockquote: > followed by optional space
  blockquote: /^>\s?/,
  // Unordered list: -, *, or + followed by space
  unorderedList: /^(\s*)([-*+])\s/,
  // Ordered list: number followed by . and space
  orderedList: /^(\s*)(\d+)\.\s/,
  // Horizontal rule: ---, ***, or ___ on their own line
  horizontalRule: /^(---+|\*\*\*+|___+)\s*$/,
  // Math block: $$ on its own line
  mathBlockStart: /^\$\$\s*$/,
  mathBlockEnd: /^\$\$\s*$/,
  // Table row: | ... | with optional trailing |
  tableRow: /^\|.*\|$/,
  // Table separator: | --- | --- | etc
  tableSeparator: /^\|?(\s*:?-+:?\s*\|)+\s*:?-+:?\s*\|?$/,
  // Empty line
  emptyLine: /^\s*$/,
};

/**
 * Render markdown incrementally during streaming.
 * Complete blocks are rendered as markdown, incomplete blocks are escaped.
 *
 * Algorithm:
 * 1. Parse content line by line
 * 2. Track current block type and buffer
 * 3. For each line, determine if it continues current block or starts new block
 * 4. When a block is determined to be complete, render it via renderMarkdown()
 * 5. Remaining buffer (incomplete block) is escaped and added to output
 */
export function renderStreamingMarkdown(content: string): string {
  if (!content) {
    return "";
  }

  const lines = content.split("\n");
  const result: string[] = [];

  // Block state tracking
  type BlockType =
    | "none"
    | "fenced_code"
    | "math_block"
    | "header"
    | "blockquote"
    | "unordered_list"
    | "ordered_list"
    | "horizontal_rule"
    | "table"
    | "paragraph";

  interface BlockState {
    type: BlockType;
    lines: string[];
    fenceChar?: string;
    fenceLength?: number;
  }

  let state: BlockState | null = null;

  // Helper to check if a line is a fence matching the opening fence
  const isClosingFence = (line: string, fenceChar: string, fenceLength: number): boolean => {
    const trimmed = line.trim();
    if (!trimmed.startsWith(fenceChar)) return false;
    // Must be only fence chars
    if (!new RegExp(`^[${fenceChar}]+$`).test(trimmed)) return false;
    // Must be at least as long as opening fence
    return trimmed.length >= fenceLength;
  };

  // Helper to detect block type from line
  const detectBlockType = (line: string): BlockType => {
    if (PATTERNS.fencedCodeStart.test(line)) return "fenced_code";
    if (PATTERNS.mathBlockStart.test(line)) return "math_block";
    if (PATTERNS.horizontalRule.test(line)) return "horizontal_rule";
    if (PATTERNS.header.test(line)) return "header";
    if (PATTERNS.blockquote.test(line)) return "blockquote";
    if (PATTERNS.unorderedList.test(line)) return "unordered_list";
    if (PATTERNS.orderedList.test(line)) return "ordered_list";
    if (PATTERNS.tableRow.test(line)) return "table";
    if (PATTERNS.emptyLine.test(line)) return "none";
    return "paragraph";
  };

  // Helper to render a block and add to result
  const renderBlock = (blockLines: string[]) => {
    if (blockLines.length === 0) return;
    const blockContent = blockLines.join("\n");
    try {
      result.push(renderMarkdown(blockContent));
    } catch {
      result.push(`<p>${escapeHtml(blockContent)}</p>`);
    }
  };

  // Helper to escape incomplete content
  const escapeBlock = (blockLines: string[]) => {
    if (blockLines.length === 0) return;
    const escaped = escapeHtml(blockLines.join("\n")).replace(/\n/g, "<br>");
    result.push(escaped);
  };

  for (let i = 0; i < lines.length; i++) {
    const line = lines[i];

    if (state === null) {
      // Start a new block
      const blockType = detectBlockType(line);

      if (blockType === "none") {
        // Empty line - add spacing if we have content
        if (result.length > 0) {
          result.push("\n");
        }
        continue;
      }

      state = { type: blockType, lines: [line] };

      // Extract fence info for fenced code
      if (blockType === "fenced_code") {
        const match = line.match(PATTERNS.fencedCodeStart);
        if (match) {
          state.fenceChar = match[1][0];
          state.fenceLength = match[1].length;
        }
      }

      // Headers and horizontal rules are immediately complete
      if (blockType === "header" || blockType === "horizontal_rule") {
        renderBlock(state.lines);
        state = null;
      }
    } else {
      // We have an active block - check if this line continues or ends it
      const blockType = state.type;

      switch (blockType) {
        case "fenced_code": {
          state.lines.push(line);
          // Check if this is the closing fence
          if (state.fenceChar && state.fenceLength &&
              isClosingFence(line, state.fenceChar, state.fenceLength)) {
            renderBlock(state.lines);
            state = null;
          }
          break;
        }

        case "math_block": {
          state.lines.push(line);
          if (PATTERNS.mathBlockEnd.test(line)) {
            renderBlock(state.lines);
            state = null;
          }
          break;
        }

        case "blockquote": {
          // Blockquote continues if line starts with > or is empty (lazy continuation)
          if (PATTERNS.blockquote.test(line) || PATTERNS.emptyLine.test(line)) {
            state.lines.push(line);
          } else {
            // Line doesn't continue blockquote - render the blockquote since it's complete
            // (followed by non-blockquote content)
            renderBlock(state.lines);
            state = null;
            i--; // Reprocess this line
          }
          break;
        }

        case "unordered_list":
        case "ordered_list": {
          const listPattern = blockType === "unordered_list" ?
            PATTERNS.unorderedList : PATTERNS.orderedList;

          if (PATTERNS.emptyLine.test(line)) {
            state.lines.push(line);
          } else if (listPattern.test(line) || /^\s+/.test(line)) {
            // Continues list (new item or indented content)
            state.lines.push(line);
          } else {
            // New block starts - render list and reprocess
            renderBlock(state.lines);
            state = null;
            i--; // Reprocess this line
          }
          break;
        }

        case "table": {
          if (PATTERNS.tableRow.test(line) || PATTERNS.tableSeparator.test(line)) {
            state.lines.push(line);
          } else if (PATTERNS.emptyLine.test(line)) {
            // Empty line ends table
            renderBlock(state.lines);
            state = null;
          } else {
            // Non-table line - render and reprocess
            renderBlock(state.lines);
            state = null;
            i--; // Reprocess this line
          }
          break;
        }

        case "paragraph": {
          // Paragraph ends on empty line or start of new block
          if (PATTERNS.emptyLine.test(line)) {
            // Empty line ends paragraph
            renderBlock(state.lines);
            state = null;
          } else {
            const newBlockType = detectBlockType(line);
            if (newBlockType !== "paragraph" && newBlockType !== "none") {
              // New block type starts - render paragraph and reprocess
              renderBlock(state.lines);
              state = null;
              i--; // Reprocess this line
            } else {
              // Continue paragraph
              state.lines.push(line);
            }
          }
          break;
        }

        default:
          // Unknown state - escape and reset
          escapeBlock(state.lines);
          state = null;
      }
    }
  }

  // Handle any remaining incomplete block
  if (state !== null) {
    escapeBlock(state.lines);
  }

  return result.join("");
}
