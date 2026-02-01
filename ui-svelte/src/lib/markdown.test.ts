import { describe, it, expect } from "vitest";
import { renderMarkdown, escapeHtml } from "./markdown";

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
