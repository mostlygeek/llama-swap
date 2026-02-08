import { describe, it, expect } from "vitest";
import { renderMarkdown, renderStreamingMarkdown, escapeHtml } from "./markdown";

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

describe("renderStreamingMarkdown", () => {
  describe("fenced code blocks", () => {
    it("renders complete fenced code blocks with highlighting", () => {
      const content = "```js\nconst x = 1;\n```";
      const result = renderStreamingMarkdown(content);
      expect(result).toContain("hljs");
      expect(result).toContain("const");
      expect(result).toContain("<pre");
    });

    it("renders complete fenced code blocks with tildes", () => {
      const content = "~~~python\nprint('hello')\n~~~";
      const result = renderStreamingMarkdown(content);
      expect(result).toContain("hljs");
      expect(result).toContain("print");
    });

    it("escapes incomplete fenced code blocks", () => {
      const content = "```js\nconst x = 1;\n// still typing";
      const result = renderStreamingMarkdown(content);
      // Incomplete code block should not be rendered with highlighting
      expect(result).not.toContain("hljs");
      expect(result).not.toContain("<pre");
      // Should contain the raw content with line breaks
      expect(result).toContain("<br>");
      expect(result).toContain("const x = 1");
    });

    it("renders multiple complete code blocks", () => {
      const content = "```js\nconst a = 1;\n```\n\n```py\nb = 2\n```";
      const result = renderStreamingMarkdown(content);
      expect(result).toContain("hljs");
      // Should have two code blocks
      const preMatches = result.match(/<pre/g);
      expect(preMatches?.length).toBe(2);
    });

    it("handles code block with backticks inside", () => {
      const content = "```\nconst s = `template`;\n```";
      const result = renderStreamingMarkdown(content);
      expect(result).toContain("hljs");
    });
  });

  describe("headers", () => {
    it("renders headers immediately at end of line", () => {
      const content = "# Heading 1\nSome text";
      const result = renderStreamingMarkdown(content);
      expect(result).toContain("<h1>Heading 1</h1>");
    });

    it("renders all header levels", () => {
      for (let i = 1; i <= 6; i++) {
        const content = `${"#".repeat(i)} Heading ${i}\n\nNext paragraph`;
        const result = renderStreamingMarkdown(content);
        expect(result).toContain(`<h${i}>Heading ${i}</h${i}>`);
      }
    });

    it("renders multiple headers", () => {
      const content = "# First\n\n## Second\n\n### Third";
      const result = renderStreamingMarkdown(content);
      expect(result).toContain("<h1>First</h1>");
      expect(result).toContain("<h2>Second</h2>");
      expect(result).toContain("<h3>Third</h3>");
    });
  });

  describe("paragraphs", () => {
    it("renders paragraphs when followed by empty line", () => {
      const content = "First paragraph\n\nSecond paragraph";
      const result = renderStreamingMarkdown(content);
      // First paragraph should be rendered (followed by empty line)
      expect(result).toContain("<p>First paragraph</p>");
      // Second paragraph is incomplete (at end of content), so escaped
      expect(result).toContain("Second paragraph");
    });

    it("renders paragraphs when followed by new block", () => {
      const content = "First paragraph\n# Header";
      const result = renderStreamingMarkdown(content);
      expect(result).toContain("<p>First paragraph</p>");
      expect(result).toContain("<h1>Header</h1>");
    });

    it("escapes incomplete paragraphs", () => {
      const content = "This is an incomplete paragraph still typing";
      const result = renderStreamingMarkdown(content);
      expect(result).not.toContain("<p>");
      expect(result).toContain("incomplete paragraph");
    });
  });

  describe("blockquotes", () => {
    it("renders complete blockquotes", () => {
      const content = "> Line 1\n> Line 2\n\nNext paragraph";
      const result = renderStreamingMarkdown(content);
      expect(result).toContain("<blockquote>");
      expect(result).toContain("</blockquote>");
    });

    it("escapes incomplete blockquotes", () => {
      const content = "> Line 1\n> Line 2";
      const result = renderStreamingMarkdown(content);
      // Blockquote at end of content should be escaped (incomplete)
      expect(result).toContain("&gt;");
    });
  });

  describe("unordered lists", () => {
    it("renders complete unordered lists", () => {
      const content = "- Item 1\n- Item 2\n\nNext paragraph";
      const result = renderStreamingMarkdown(content);
      expect(result).toContain("<ul>");
      expect(result).toContain("<li>Item 1</li>");
      expect(result).toContain("<li>Item 2</li>");
    });

    it("renders lists with asterisks", () => {
      const content = "* Item 1\n* Item 2\n\nDone";
      const result = renderStreamingMarkdown(content);
      expect(result).toContain("<ul>");
      expect(result).toContain("<li>Item 1</li>");
    });

    it("renders lists with plus signs", () => {
      const content = "+ Item 1\n+ Item 2\n\nDone";
      const result = renderStreamingMarkdown(content);
      expect(result).toContain("<ul>");
      expect(result).toContain("<li>Item 1</li>");
    });

    it("escapes incomplete lists", () => {
      const content = "- Item 1\n- Item 2";
      const result = renderStreamingMarkdown(content);
      // Should be escaped since list isn't complete
      expect(result).toContain("- Item");
      expect(result).not.toContain("<ul>");
    });
  });

  describe("ordered lists", () => {
    it("renders complete ordered lists", () => {
      const content = "1. First\n2. Second\n\nNext paragraph";
      const result = renderStreamingMarkdown(content);
      expect(result).toContain("<ol>");
      expect(result).toContain("<li>First</li>");
      expect(result).toContain("<li>Second</li>");
    });

    it("escapes incomplete ordered lists", () => {
      const content = "1. First\n2. Second";
      const result = renderStreamingMarkdown(content);
      // Should be escaped
      expect(result).toContain("1. First");
      expect(result).not.toContain("<ol>");
    });
  });

  describe("horizontal rules", () => {
    it("renders horizontal rules immediately", () => {
      const content = "---\n\nNext paragraph";
      const result = renderStreamingMarkdown(content);
      expect(result).toContain("<hr>");
    });

    it("renders horizontal rules with asterisks", () => {
      const content = "***\n\nNext";
      const result = renderStreamingMarkdown(content);
      expect(result).toContain("<hr>");
    });

    it("renders horizontal rules with underscores", () => {
      const content = "___\n\nNext";
      const result = renderStreamingMarkdown(content);
      expect(result).toContain("<hr>");
    });
  });

  describe("tables", () => {
    it("renders complete tables", () => {
      const content = "| Col1 | Col2 |\n|------|------|\n| A    | B    |\n\nNext";
      const result = renderStreamingMarkdown(content);
      expect(result).toContain("<table>");
      expect(result).toContain("<th>Col1</th>");
      expect(result).toContain("<td>A</td>");
    });

    it("escapes incomplete tables", () => {
      const content = "| Col1 | Col2 |\n|------|------|\n| A    | B    |";
      const result = renderStreamingMarkdown(content);
      // Should be escaped since table isn't complete
      expect(result).toContain("| Col1");
      expect(result).not.toContain("<table>");
    });
  });

  describe("math blocks", () => {
    it("renders complete math blocks", () => {
      const content = "$$\nE = mc^2\n$$";
      const result = renderStreamingMarkdown(content);
      expect(result).toContain("katex");
    });

    it("escapes incomplete math blocks", () => {
      const content = "$$\nE = mc^2";
      const result = renderStreamingMarkdown(content);
      // Should be escaped
      expect(result).toContain("$$");
      expect(result).toContain("<br>");
      expect(result).not.toContain("katex");
    });
  });

  describe("mixed content", () => {
    it("renders complete blocks and escapes incomplete ones", () => {
      const content = "# Complete Header\n\n```js\nconst x = 1;\n```\n\nIncomplete paragraph";
      const result = renderStreamingMarkdown(content);
      expect(result).toContain("<h1>Complete Header</h1>");
      expect(result).toContain("hljs");
      // Last part should be escaped
      expect(result).toContain("Incomplete paragraph");
    });

    it("handles streaming simulation - progressively completing content", () => {
      // Simulate streaming: incomplete -> complete
      const incomplete = "```js\nconst x = 1;";
      const complete = "```js\nconst x = 1;\n```";

      const incompleteResult = renderStreamingMarkdown(incomplete);
      const completeResult = renderStreamingMarkdown(complete);

      expect(incompleteResult).not.toContain("hljs");
      expect(completeResult).toContain("hljs");
    });

    it("handles complex mixed document", () => {
      const content = `# Document Title

This is a complete paragraph.

\`\`\`python
def hello():
    return "world"
\`\`\`

> A blockquote
> with two lines

- List item 1
- List item 2

Incomplete`;

      const result = renderStreamingMarkdown(content);

      // All complete blocks should be rendered
      expect(result).toContain("<h1>Document Title</h1>");
      expect(result).toContain("<p>This is a complete paragraph.</p>");
      expect(result).toContain("hljs");
      expect(result).toContain("<blockquote>");
      expect(result).toContain("<ul>");

      // Incomplete part should be escaped
      expect(result).toContain("Incomplete");
    });
  });

  describe("edge cases", () => {
    it("handles empty content", () => {
      const result = renderStreamingMarkdown("");
      expect(result).toBe("");
    });

    it("handles only whitespace", () => {
      const result = renderStreamingMarkdown("   \n\n   ");
      // Whitespace-only content should return empty or whitespace
      expect(result !== undefined).toBe(true);
    });

    it("handles single line", () => {
      const result = renderStreamingMarkdown("Just one line");
      expect(result).toContain("Just one line");
    });

    it("handles code block with language specifier", () => {
      const content = "```typescript\nconst x: number = 1;\n```";
      const result = renderStreamingMarkdown(content);
      expect(result).toContain("hljs");
    });

    it("handles nested markdown in blockquotes", () => {
      const content = "> # Header in quote\n> **bold text**\n\nDone";
      const result = renderStreamingMarkdown(content);
      expect(result).toContain("<blockquote>");
      expect(result).toContain("<h1>Header in quote</h1>");
      expect(result).toContain("<strong>bold text</strong>");
    });

    it("handles inline code in paragraphs", () => {
      const content = "Use `console.log()` for debugging.\n\nNext paragraph";
      const result = renderStreamingMarkdown(content);
      // Inline code gets highlight.js treatment
      expect(result).toContain("<code");
      expect(result).toContain("console.log()");
    });

    it("handles links in paragraphs", () => {
      const content = "Visit [example](https://example.com) for more.\n\nNext";
      const result = renderStreamingMarkdown(content);
      expect(result).toContain('<a href="https://example.com">example</a>');
    });
  });
});
