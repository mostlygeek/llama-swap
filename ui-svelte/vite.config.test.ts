import { describe, it, expect } from "vitest";
import type { Plugin } from "vite";
import config from "./vite.config";

// Some plugins (e.g. @sveltejs/vite-plugin-svelte) return an array of
// several internal plugins rather than a single plugin object, so the raw
// `plugins` array can contain nested arrays. Flatten it before searching.
function flattenPlugins(plugins: unknown): Plugin[] {
  const result: Plugin[] = [];
  for (const p of (plugins as unknown[]) ?? []) {
    if (Array.isArray(p)) {
      result.push(...flattenPlugins(p));
    } else if (p) {
      result.push(p as Plugin);
    }
  }
  return result;
}

function findPlugin(name: string): Plugin {
  const plugin = flattenPlugins(config.plugins).find((p) => p.name === name);
  if (!plugin) throw new Error(`Plugin "${name}" not found in vite config`);
  return plugin;
}

function callTransform(plugin: Plugin, code: string, id: string): string | null {
  const transform = plugin.transform as unknown as (code: string, id: string) => string | null;
  return transform(code, id);
}

describe("stripKatexFontFallbacks vite plugin", () => {
  it("is registered in the plugins list", () => {
    expect(() => findPlugin("strip-katex-font-fallbacks")).not.toThrow();
  });

  it("runs in the 'pre' phase, before other transforms", () => {
    const plugin = findPlugin("strip-katex-font-fallbacks");
    expect(plugin.enforce).toBe("pre");
  });

  it("strips the woff and ttf fallbacks from a katex.min.css font-face rule", () => {
    const plugin = findPlugin("strip-katex-font-fallbacks");
    const css =
      '@font-face{font-family:KaTeX_Main;src:url(fonts/a.woff2) format("woff2"),url(fonts/a.woff) format("woff"),url(fonts/a.ttf) format("truetype");}';
    const result = callTransform(plugin, css, "/some/path/katex.min.css");
    expect(result).toBe(
      '@font-face{font-family:KaTeX_Main;src:url(fonts/a.woff2) format("woff2");}',
    );
  });

  it("strips fallbacks from multiple font-face declarations in the same file", () => {
    const plugin = findPlugin("strip-katex-font-fallbacks");
    const css = [
      'url(a.woff2) format("woff2"),url(a.woff) format("woff"),url(a.ttf) format("truetype")',
      'url(b.woff2) format("woff2"),url(b.woff) format("woff"),url(b.ttf) format("truetype")',
    ].join(";");
    const result = callTransform(plugin, css, "katex.min.css");
    expect(result).toBe('url(a.woff2) format("woff2");url(b.woff2) format("woff2")');
  });

  it("returns null for files that are not katex.min.css, leaving them untouched", () => {
    const plugin = findPlugin("strip-katex-font-fallbacks");
    const css =
      'url(a.woff2) format("woff2"),url(a.woff) format("woff"),url(a.ttf) format("truetype")';
    expect(callTransform(plugin, css, "/some/path/other.css")).toBeNull();
  });

  it("matches by suffix, regardless of the directory the file lives in", () => {
    const plugin = findPlugin("strip-katex-font-fallbacks");
    const css =
      'url(a.woff2) format("woff2"),url(a.woff) format("woff"),url(a.ttf) format("truetype")';
    const result = callTransform(
      plugin,
      css,
      "/home/user/project/node_modules/katex/dist/katex.min.css",
    );
    expect(result).toBe('url(a.woff2) format("woff2")');
  });

  it("leaves content unchanged when the triple-format pattern is absent", () => {
    const plugin = findPlugin("strip-katex-font-fallbacks");
    const css = "body { color: red; }";
    expect(callTransform(plugin, css, "katex.min.css")).toBe(css);
  });

  it("does not match when formats are separated with extra whitespace after commas", () => {
    const plugin = findPlugin("strip-katex-font-fallbacks");
    // Some CSS formatters would emit ", " (comma + space) here; the plugin's
    // regex has no `\s*` allowance for that, so it's a known limitation this
    // test documents rather than silently losing the fallback fonts.
    const css =
      'src:url(a.woff2) format("woff2"), url(a.woff) format("woff"), url(a.ttf) format("truetype");';
    expect(callTransform(plugin, css, "katex.min.css")).toBe(css);
  });

  it("does not touch a lone woff2 format() declaration with no fallbacks to strip", () => {
    const plugin = findPlugin("strip-katex-font-fallbacks");
    const css = 'src:url(a.woff2) format("woff2");';
    expect(callTransform(plugin, css, "katex.min.css")).toBe(css);
  });
});

describe("vite build config", () => {
  it("raises the chunk size warning limit to accommodate the deferred playground chunk", () => {
    expect(config.build?.chunkSizeWarningLimit).toBe(700);
  });
});