// Markdown rendering for chat. Streaming helpers are ported verbatim from
// lib/markdown.ts (pure string logic); renderMarkdown is reworked to drive the
// vendored marked v15 + KaTeX + highlight.js globals (loaded as classic <script>
// tags in index.html) instead of the unified/remark/rehype pipeline.

export function escapeHtml(text) {
  const htmlEntities = {
    "&": "&amp;",
    "<": "&lt;",
    ">": "&gt;",
    '"': "&quot;",
    "'": "&#39;",
  };
  return String(text).replace(/[&<>"']/g, (char) => htmlEntities[char]);
}

// ---- streaming block splitting (ported verbatim from markdown.ts) ----

export function splitCompleteBlocks(text) {
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
      // Check for closing fence: same character, at least 3, no other content.
      // Hardcoded regexes (selected by fence char) avoid a dynamic RegExp.
      const closeFenceRe = fenceChar === "~" ? /^\s*~{3,}\s*$/ : /^\s*`{3,}\s*$/;
      if (closeFenceRe.test(trimmed)) {
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

export function closePendingBlock(pending) {
  if (!pending) return "";

  const lines = pending.split("\n");
  let inFence = false;
  let fenceStr = "";
  let inMathBlock = false;
  let mathClose = "";

  for (const line of lines) {
    const trimmed = line.trimEnd();

    if (inFence) {
      // Hardcoded regexes (selected by fence char) avoid a dynamic RegExp.
      const closeRe = fenceStr[0] === "~" ? /^\s*~~~\s*$/ : /^\s*```\s*$/;
      if (closeRe.test(trimmed)) {
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

export function createStreamingCache() {
  return { blocks: [], nextId: 0, completeKey: "" };
}

export function renderStreamingMarkdown(text, cache) {
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

// Convert \[...\] to $$...$$ and \(...\) to $...$ (ported verbatim from markdown.ts)
export function normalizeLatexDelimiters(text) {
  // Display math: \[...\] → $$...$$  (may span multiple lines)
  text = text.replace(/\\\[([\s\S]*?)\\\]/g, (_match, inner) => `$$${inner}$$`);
  // Inline math: \(...\) → $...$
  text = text.replace(/\\\(([\s\S]*?)\\\)/g, (_match, inner) => `$${inner}$`);
  return text;
}

// ---- marked + KaTeX + highlight.js wiring ----

// KaTeX delimiter rules mirroring remark-math (and the marked-katex-extension):
// math is tokenized at the lexer level so $...$ inside code spans/fences is left
// alone. Inline closing must be followed by whitespace/punctuation/end of input,
// which keeps prices like "$5 and $x$" from being mis-parsed.
const INLINE_MATH_RULE =
  /^(\${1,2})(?!\$)((?:\\.|[^\\\n])*?(?:\\.|[^\\\n$]))\1(?=[\s?!.,:？！。，：]|$)/;
const BLOCK_MATH_RULE = /^(\${1,2})\n((?:\\[^]|[^\\])+?)\n\1(?:\n|$)/;

function renderKatex(token) {
  try {
    return globalThis.katex.renderToString(token.text, {
      throwOnError: false,
      displayMode: token.displayMode,
    });
  } catch {
    // KaTeX failed even with throwOnError:false — fall back to the raw source.
    return escapeHtml(token.raw);
  }
}

const inlineMathExtension = {
  name: "inlineMath",
  level: "inline",
  start(src) {
    let index;
    let indexSrc = src;
    while (indexSrc) {
      index = indexSrc.indexOf("$");
      if (index === -1) return undefined;
      const possible = indexSrc.substring(index);
      if (INLINE_MATH_RULE.test(possible)) {
        return src.length - indexSrc.length + index;
      }
      indexSrc = indexSrc.substring(index + 1).replace(/^\$+/, "");
    }
    return undefined;
  },
  tokenizer(src) {
    const match = INLINE_MATH_RULE.exec(src);
    if (match) {
      return {
        type: "inlineMath",
        raw: match[0],
        text: match[2].trim(),
        displayMode: match[1].length === 2,
      };
    }
    return undefined;
  },
  renderer: renderKatex,
};

const blockMathExtension = {
  name: "blockMath",
  level: "block",
  start(src) {
    // Only a genuine multiline block opener — "$$" at the start of a line,
    // immediately followed by a newline — should truncate a paragraph here;
    // single-line "$$...$$" is handled by the inline extension.
    const m = src.match(/(?:^|\n)(\$\$\n)/);
    return m ? m.index + (m[0].length - m[1].length) : undefined;
  },
  tokenizer(src) {
    const match = BLOCK_MATH_RULE.exec(src);
    if (match) {
      return {
        type: "blockMath",
        raw: match[0],
        text: match[2].trim(),
        displayMode: match[1].length === 2,
      };
    }
    return undefined;
  },
  renderer: renderKatex,
};

// Custom fenced-code renderer running highlight.js, mirroring the old
// rehypeHighlight plugin: `hljs language-xxx` classes, plaintext fallback.
function renderCode(token, infostring) {
  // marked v15 passes a token object; tolerate the older (code, lang) signature.
  let text;
  let lang;
  if (token && typeof token === "object") {
    text = token.text;
    lang = token.lang;
  } else {
    text = token;
    lang = infostring;
  }
  const langId = (lang || "").match(/\S*/)[0];
  const hljs = globalThis.hljs;
  const language = langId && hljs.getLanguage(langId) ? langId : "plaintext";
  let highlighted;
  try {
    highlighted = hljs.highlight(text, { language }).value;
  } catch {
    highlighted = escapeHtml(text);
  }
  return `<pre><code class="hljs language-${language}">${highlighted}</code></pre>`;
}

let markedInstance = null;
function getMarked() {
  if (markedInstance) return markedInstance;
  const { Marked } = globalThis.marked;
  markedInstance = new Marked({ gfm: true });
  markedInstance.use({
    extensions: [blockMathExtension, inlineMathExtension],
    renderer: { code: renderCode },
  });
  return markedInstance;
}

export function renderMarkdown(content) {
  if (!content) {
    return "";
  }
  try {
    return getMarked().parse(normalizeLatexDelimiters(content));
  } catch {
    // Fallback to escaped plain text if markdown parsing fails
    return `<p>${escapeHtml(content)}</p>`;
  }
}
