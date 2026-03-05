import { describe, it, expect } from "vitest";
import { renderMarkdown, escapeHtml, splitCompleteBlocks, closePendingBlock, normalizeLatexDelimiters, renderStreamingMarkdown, createStreamingCache } from "./markdown";

describe("renderMarkdown", () => {
  describe("basic markdown", () => {
    it("renders plain text", () => {
      const result = renderMarkdown("Hello world");
      expect(result).toContain("Hello world");
    });

    it("renders bold text", () => {
      const result = renderMarkdown("**bold**");
      expect(result).toContain("<strong>bold</strong>");
    });

    it("renders italic text", () => {
      const result = renderMarkdown("*italic*");
      expect(result).toContain("<em>italic</em>");
    });

    it("renders code blocks", () => {
      const result = renderMarkdown("```js\nconst x = 1;\n```");
      expect(result).toContain("hljs");
      expect(result).toContain("const");
    });

    it("returns empty string for empty content", () => {
      const result = renderMarkdown("");
      expect(result).toBe("");
    });

    it("returns empty string for null/undefined content", () => {
      // @ts-expect-error - testing null input
      expect(renderMarkdown(null)).toBe("");
      // @ts-expect-error - testing undefined input
      expect(renderMarkdown(undefined)).toBe("");
    });
  });

  describe("KaTeX math rendering", () => {
    it("renders inline math with $...$ syntax", () => {
      const result = renderMarkdown("The equation $E = mc^2$ is famous.");
      // KaTeX should convert this to HTML with katex class
      expect(result).toContain("katex");
      expect(result).toContain("E");
      expect(result).toContain("=");
      expect(result).toContain("mc");
    });

    it("renders display math with $$...$$ syntax", () => {
      const result = renderMarkdown("$$\\int_{a}^{b} f(x) dx$$");
      // Math should be rendered with KaTeX
      expect(result).toContain("katex");
      expect(result).toContain("∫");
      expect(result).toContain("f(x)");
    });

    it("renders complex LaTeX expressions", () => {
      const result = renderMarkdown("$$\\sum_{i=1}^{n} x_i = \\frac{1}{n}\\sum_{i=1}^{n} x_i$$");
      expect(result).toContain("katex");
      expect(result).toContain("∑"); // or the MathML equivalent
    });

    it("renders LaTeX with Greek letters", () => {
      const result = renderMarkdown("$\\alpha + \\beta = \\gamma$");
      expect(result).toContain("katex");
      // Greek letters should be rendered
      expect(result).toMatch(/[αβγ]|alpha|beta|gamma/);
    });

    it("renders LaTeX with fractions", () => {
      const result = renderMarkdown("$\\frac{a}{b}$");
      expect(result).toContain("katex");
      expect(result).toContain("frac");
    });

    it("renders LaTeX with subscripts and superscripts", () => {
      const result = renderMarkdown("$x^2 + y_3$");
      expect(result).toContain("katex");
      expect(result).toContain("sup"); // superscript
      expect(result).toContain("sub"); // subscript
    });

    it("renders multiple inline math expressions in one paragraph", () => {
      const result = renderMarkdown("First $x = 1$ and then $y = 2$.");
      // Should contain multiple katex spans
      const katexMatches = result.match(/katex/g);
      expect(katexMatches?.length).toBeGreaterThanOrEqual(2);
    });

    it("renders math within a larger markdown document", () => {
      const markdown = `# Heading

This is a paragraph with $E = mc^2$ inline math.

$$\\int_0^\\infty e^{-x} dx = 1$$

More text here.
`;
      const result = renderMarkdown(markdown);
      expect(result).toContain("<h1>Heading</h1>");
      expect(result).toContain("katex");
      // Both inline and display math should be rendered
      expect(result).toContain("E = mc");
      expect(result).toContain("∫");
      expect(result).toContain("∞");
    });

    it("handles escaped dollar signs", () => {
      const result = renderMarkdown("This costs \\$5 and $x = 1$.");
      // Should render the escaped $5 as text and the math
      expect(result).toContain("katex");
      expect(result).toContain("$5");
    });

    it("handles empty math expressions gracefully", () => {
      // Empty math should not break the renderer
      const result = renderMarkdown("$$$");
      expect(result).toBeTruthy();
    });

    it("renders LaTeX matrices", () => {
      const result = renderMarkdown("$$\\begin{pmatrix} a & b \\\\ c & d \\end{pmatrix}$$");
      expect(result).toContain("katex");
      expect(result).toContain("pmatrix");
    });

    it("renders LaTeX square roots", () => {
      const result = renderMarkdown("$\\sqrt{x^2 + y^2}$");
      expect(result).toContain("katex");
      expect(result).toContain("sqrt");
    });

    it("renders \\[...\\] display math", () => {
      const result = renderMarkdown("\\[\nx^2 + y^2 = z^2\n\\]");
      expect(result).toContain("katex");
    });

    it("renders \\(...\\) inline math", () => {
      const result = renderMarkdown("The equation \\(E = mc^2\\) is famous.");
      expect(result).toContain("katex");
    });
  });

  describe("normalizeLatexDelimiters", () => {
    it("converts \\[...\\] to $$...$$", () => {
      expect(normalizeLatexDelimiters("\\[\nx^2\n\\]")).toBe("$$\nx^2\n$$");
    });

    it("converts \\(...\\) to $...$", () => {
      expect(normalizeLatexDelimiters("\\(x^2\\)")).toBe("$x^2$");
    });

    it("leaves $$ and $ delimiters unchanged", () => {
      const text = "$$x^2$$ and $y$";
      expect(normalizeLatexDelimiters(text)).toBe(text);
    });

    it("handles multiple occurrences", () => {
      expect(normalizeLatexDelimiters("\\(a\\) and \\(b\\)")).toBe("$a$ and $b$");
    });
  });

  describe("escapeHtml", () => {
    it("escapes HTML entities", () => {
      expect(escapeHtml("<script>")).toBe("&lt;script&gt;");
      expect(escapeHtml('"quoted"')).toBe("&quot;quoted&quot;");
      expect(escapeHtml("'single'")).toBe("&#39;single&#39;");
      expect(escapeHtml("a & b")).toBe("a &amp; b");
    });

    it("handles empty string", () => {
      expect(escapeHtml("")).toBe("");
    });
  });

  describe("error handling", () => {
    it("does not throw on invalid LaTeX syntax", () => {
      // Invalid LaTeX should not crash the renderer
      expect(() => renderMarkdown("$\\invalidcommand{")).not.toThrow();
    });

    it("returns fallback HTML on processing errors", () => {
      // Very large or malformed input should be handled
      const result = renderMarkdown("$" + "a".repeat(10000) + "$");
      expect(result).toBeTruthy();
    });
  });
});

describe("splitCompleteBlocks", () => {
  it("returns everything as pending when no blank line", () => {
    const result = splitCompleteBlocks("Hello world");
    expect(result.complete).toBe("");
    expect(result.pending).toBe("Hello world");
  });

  it("returns empty for empty input", () => {
    const result = splitCompleteBlocks("");
    expect(result.complete).toBe("");
    expect(result.pending).toBe("");
  });

  it("splits on blank line between paragraphs", () => {
    const result = splitCompleteBlocks("First paragraph.\n\nSecond paragraph");
    expect(result.complete).toBe("First paragraph.\n");
    expect(result.pending).toBe("Second paragraph");
  });

  it("splits multiple paragraphs at last blank line", () => {
    const result = splitCompleteBlocks("Para 1.\n\nPara 2.\n\nPara 3");
    expect(result.complete).toBe("Para 1.\n\nPara 2.\n");
    expect(result.pending).toBe("Para 3");
  });

  it("treats closed code fence as complete boundary", () => {
    const text = "```js\nconst x = 1;\n```\nMore text";
    const result = splitCompleteBlocks(text);
    expect(result.complete).toBe("```js\nconst x = 1;\n```");
    expect(result.pending).toBe("More text");
  });

  it("treats unclosed code fence as pending", () => {
    const text = "Done paragraph.\n\n```js\nconst x = 1;";
    const result = splitCompleteBlocks(text);
    expect(result.complete).toBe("Done paragraph.\n");
    expect(result.pending).toBe("```js\nconst x = 1;");
  });

  it("does not split on blank lines inside code fences", () => {
    const text = "```\nline1\n\nline2\n```";
    const result = splitCompleteBlocks(text);
    expect(result.complete).toBe("```\nline1\n\nline2\n```");
    expect(result.pending).toBe("");
  });

  it("handles tilde fences", () => {
    const text = "~~~py\nprint('hi')\n~~~\nAfter";
    const result = splitCompleteBlocks(text);
    expect(result.complete).toBe("~~~py\nprint('hi')\n~~~");
    expect(result.pending).toBe("After");
  });

  it("does not close backtick fence with tilde fence", () => {
    const text = "```\ncode\n~~~\nstill code";
    const result = splitCompleteBlocks(text);
    // The ~~~ should not close a backtick fence, so everything from ``` onward is pending
    expect(result.complete).toBe("");
    expect(result.pending).toBe("```\ncode\n~~~\nstill code");
  });

  it("treats closed math block as complete boundary", () => {
    const text = "$$\nx^2\n$$\nAfter";
    const result = splitCompleteBlocks(text);
    expect(result.complete).toBe("$$\nx^2\n$$");
    expect(result.pending).toBe("After");
  });

  it("treats unclosed math block as pending", () => {
    const text = "Before.\n\n$$\nx^2";
    const result = splitCompleteBlocks(text);
    expect(result.complete).toBe("Before.\n");
    expect(result.pending).toBe("$$\nx^2");
  });

  it("treats closed \\[...\\] math block as complete boundary", () => {
    const text = "\\[\nx^2\n\\]\nAfter";
    const result = splitCompleteBlocks(text);
    expect(result.complete).toBe("\\[\nx^2\n\\]");
    expect(result.pending).toBe("After");
  });

  it("treats unclosed \\[ math block as pending", () => {
    const text = "Before.\n\n\\[\nx^2";
    const result = splitCompleteBlocks(text);
    expect(result.complete).toBe("Before.\n");
    expect(result.pending).toBe("\\[\nx^2");
  });

  it("handles trailing blank line making everything complete", () => {
    const text = "Hello world.\n";
    const result = splitCompleteBlocks(text);
    // Last line is empty string after split, which is a blank line
    expect(result.complete).toBe("Hello world.\n");
    expect(result.pending).toBe("");
  });
});

describe("closePendingBlock", () => {
  it("returns empty string for empty input", () => {
    expect(closePendingBlock("")).toBe("");
  });

  it("returns plain text unchanged", () => {
    expect(closePendingBlock("Hello world")).toBe("Hello world");
  });

  it("closes an open backtick code fence", () => {
    const result = closePendingBlock("```python\nprint('hi')");
    expect(result).toBe("```python\nprint('hi')\n```");
  });

  it("closes an open tilde code fence", () => {
    const result = closePendingBlock("~~~js\nconst x = 1;");
    expect(result).toBe("~~~js\nconst x = 1;\n~~~");
  });

  it("does not modify already-closed code fence", () => {
    const text = "```py\ncode\n```";
    expect(closePendingBlock(text)).toBe(text);
  });

  it("closes an open math block", () => {
    const result = closePendingBlock("$$\nx^2 + y^2");
    expect(result).toBe("$$\nx^2 + y^2\n$$");
  });

  it("does not modify already-closed math block", () => {
    const text = "$$\nx^2\n$$";
    expect(closePendingBlock(text)).toBe(text);
  });

  it("closes an open \\[ math block with \\]", () => {
    const result = closePendingBlock("\\[\nx^2 + y^2");
    expect(result).toBe("\\[\nx^2 + y^2\n\\]");
  });

  it("does not modify already-closed \\[...\\] math block", () => {
    const text = "\\[\nx^2\n\\]";
    expect(closePendingBlock(text)).toBe(text);
  });

  it("closes code fence when preceded by regular text", () => {
    const result = closePendingBlock("Some text\n```\ncode");
    expect(result).toBe("Some text\n```\ncode\n```");
  });

  it("leaves headers unchanged", () => {
    expect(closePendingBlock("## Hello")).toBe("## Hello");
  });

  it("leaves tables unchanged", () => {
    const table = "| a | b |\n| --- | --- |\n| 1 | 2 |";
    expect(closePendingBlock(table)).toBe(table);
  });

  it("leaves lists unchanged", () => {
    expect(closePendingBlock("- item 1\n- item 2")).toBe("- item 1\n- item 2");
  });
});

describe("renderStreamingMarkdown", () => {
  it("renders complete blocks and pending as markdown", () => {
    const cache = createStreamingCache();
    const text = "# Hello\n\nWorld";
    const { blocks, pendingHtml } = renderStreamingMarkdown(text, cache);
    expect(blocks).toHaveLength(1);
    expect(blocks[0].html).toContain("<h1>Hello</h1>");
    expect(pendingHtml).toContain("World");
    expect(pendingHtml).toContain("<p>");
  });

  it("preserves existing blocks when complete portion is unchanged", () => {
    const cache = createStreamingCache();
    renderStreamingMarkdown("# Hello\n\nWor", cache);
    const firstBlocks = cache.blocks;

    const { blocks } = renderStreamingMarkdown("# Hello\n\nWorld", cache);
    // Same block array reference — nothing changed in the complete section
    expect(blocks).toBe(firstBlocks);
    expect(cache.completeKey).toBe("# Hello\n");
  });

  it("appends a new block when a new section completes", () => {
    const cache = createStreamingCache();
    renderStreamingMarkdown("# Hello\n\nParagraph", cache);
    expect(cache.blocks).toHaveLength(1);
    const firstBlock = cache.blocks[0];

    renderStreamingMarkdown("# Hello\n\nParagraph.\n\nMore", cache);
    expect(cache.blocks).toHaveLength(2);
    // First block is preserved with the same id and html
    expect(cache.blocks[0].id).toBe(firstBlock.id);
    expect(cache.blocks[0].html).toBe(firstBlock.html);
    // Second block contains the new paragraph
    expect(cache.blocks[1].html).toContain("Paragraph.");
  });

  it("assigns unique stable ids to each block", () => {
    const cache = createStreamingCache();
    renderStreamingMarkdown("A.\n\nB.\n\nC", cache);
    expect(cache.blocks).toHaveLength(1);
    const id0 = cache.blocks[0].id;

    renderStreamingMarkdown("A.\n\nB.\n\nC.\n\nD", cache);
    expect(cache.blocks).toHaveLength(2);
    expect(cache.blocks[0].id).toBe(id0);
    expect(cache.blocks[1].id).toBe(id0 + 1);
  });

  it("renders pending code block with syntax highlighting", () => {
    const cache = createStreamingCache();
    const text = "Done.\n\n```python\nprint('hello')";
    const { pendingHtml } = renderStreamingMarkdown(text, cache);
    expect(pendingHtml).toContain("<code");
    expect(pendingHtml).toContain("hljs");
  });

  it("renders pending table as markdown", () => {
    const cache = createStreamingCache();
    const text = "Done.\n\n| a | b |\n| --- | --- |\n| 1 | 2 |";
    const { pendingHtml } = renderStreamingMarkdown(text, cache);
    expect(pendingHtml).toContain("<table>");
    expect(pendingHtml).toContain("<td>");
  });

  it("renders pending portion through markdown pipeline", () => {
    const cache = createStreamingCache();
    const text = "Done.\n\nSome **bold** text";
    const { pendingHtml } = renderStreamingMarkdown(text, cache);
    expect(pendingHtml).toContain("<strong>bold</strong>");
  });
});
