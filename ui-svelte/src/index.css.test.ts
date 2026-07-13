import { describe, it, expect } from "vitest";
import { readFileSync } from "node:fs";
import { fileURLToPath } from "node:url";

const css = readFileSync(fileURLToPath(new URL("./index.css", import.meta.url)), "utf-8");

describe("index.css", () => {
  it("does not import katex's stylesheet globally", () => {
    // katex.min.css is now imported directly from lib/markdown.ts so it
    // ships with that module's lazy-loaded chunk instead of the global
    // stylesheet, which is downloaded on every page regardless of whether
    // math rendering is ever used.
    expect(css).not.toMatch(/@import\s+["']katex\/dist\/katex\.min\.css["'];?/);
    expect(css).not.toContain("katex");
  });

  it("still imports the base tailwindcss and tw-animate-css stylesheets", () => {
    expect(css).toContain('@import "tailwindcss";');
    expect(css).toContain('@import "tw-animate-css";');
  });
});