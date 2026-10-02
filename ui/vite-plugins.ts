import type { Plugin } from "vite";

// KaTeX's CSS lists woff2/woff/ttf fallbacks per font-face, but browsers only
// ever fetch the first format they support (woff2, universally today) - the
// other two formats are never downloaded, they just bloat the build output
// (and the Go binary it's embedded into). Strip them at build time.
//
// The single-file Tailcat build needs this more than the main one: there every
// surviving font URL is base64-inlined into the page, so three formats per
// face would be three copies of every glyph in the HTML.
export function stripKatexFontFallbacks(): Plugin {
  return {
    name: "strip-katex-font-fallbacks",
    enforce: "pre",
    transform(code, id) {
      if (!id.endsWith("katex.min.css")) return null;
      return code.replace(
        /url\(([^)]+\.woff2)\) format\("woff2"\),url\([^)]+\.woff\) format\("woff"\),url\([^)]+\.ttf\) format\("truetype"\)/g,
        'url($1) format("woff2")',
      );
    },
  };
}

/** Escapes a closing tag so inlined content cannot terminate its own element. */
function escapeForTag(source: string, tag: string): string {
  return source.replace(new RegExp(`</${tag}`, "gi"), `<\\/${tag}`);
}

// Folds every emitted script and stylesheet into the HTML so the build is one
// file that works from file://, with no sibling requests a local page cannot
// make.
//
// Written here rather than pulled in as a dependency because the project is on
// Vite 8, which bundles with Rolldown: generateBundle is common ground, while a
// third-party single-file plugin reaching into Rollup internals is a bet.
export function inlineSingleFile(): Plugin {
  return {
    name: "inline-single-file",
    enforce: "post",
    generateBundle(_options, bundle) {
      const inlined = new Set<string>();

      const scriptFor = (fileName: string): string | null => {
        const item = bundle[fileName];
        if (!item || item.type !== "chunk") return null;
        inlined.add(fileName);
        return `<script type="module">${escapeForTag(item.code, "script")}</script>`;
      };

      const styleFor = (fileName: string): string | null => {
        const item = bundle[fileName];
        if (!item || item.type !== "asset") return null;
        inlined.add(fileName);
        return `<style>${escapeForTag(String(item.source), "style")}</style>`;
      };

      for (const item of Object.values(bundle)) {
        if (item.type !== "asset" || !item.fileName.endsWith(".html")) continue;

        let html = String(item.source);
        // Preloads only make sense for assets fetched over the network.
        html = html.replace(/<link[^>]+rel="modulepreload"[^>]*>/gi, "");
        html = html.replace(
          /<script[^>]*\ssrc="([^"]+)"[^>]*><\/script>/gi,
          (match, src: string) => scriptFor(src.replace(/^\.?\//, "")) ?? match,
        );
        html = html.replace(
          /<link[^>]+rel="stylesheet"[^>]*\shref="([^"]+)"[^>]*>/gi,
          (match, href: string) => styleFor(href.replace(/^\.?\//, "")) ?? match,
        );
        item.source = html;
      }

      for (const fileName of inlined) {
        delete bundle[fileName];
      }
    },
  };
}
