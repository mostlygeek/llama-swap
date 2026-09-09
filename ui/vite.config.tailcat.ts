import { defineConfig } from "vite";
import { svelte } from "@sveltejs/vite-plugin-svelte";
import tailwindcss from "@tailwindcss/vite";
import { inlineSingleFile, stripKatexFontFallbacks } from "./vite-plugins";
import path from "node:path";

// The standalone Tailcat Playground page, built from ui/tailcat.html.
//
// This is a second config rather than a second rollupOptions.input on the main
// one because almost nothing is shared: that build serves from /ui/ inside the
// Go binary and emits brotli siblings, this one has to work as a lone file on
// disk with every asset folded in. The main `npm run build` also passes
// --emptyOutDir against internal/server/ui_dist, so the two must not share an
// output directory either.
//
// See `make tailcat-playground`, which builds the wasm module this page loads
// and then combines the two.
export default defineConfig({
  plugins: [svelte(), tailwindcss(), stripKatexFontFallbacks(), inlineSingleFile()],
  // Relative, so the page works from file:// as well as from a web server.
  base: "./",
  resolve: {
    alias: {
      $lib: path.resolve(__dirname, "./src/lib"),
    },
  },
  build: {
    outDir: "../build/tailcat-playground",
    emptyOutDir: false, // main.wasm.gz is written here before this build runs
    // ui/public holds favicons the main UI serves from /; this page inlines
    // everything it uses and references none of them.
    copyPublicDir: false,
    rollupOptions: {
      input: path.resolve(__dirname, "tailcat.html"),
      // One chunk, so the page has exactly one script for inlineSingleFile to
      // fold in and no runtime import to fetch.
      output: { codeSplitting: false },
    },
    cssCodeSplit: false,
    // Inline every asset, KaTeX's woff2 faces included: a separate file is a
    // request a file:// page cannot reliably make.
    assetsInlineLimit: () => true,
    // The whole page is one chunk by design; the size that matters is reported
    // by the build script instead.
    chunkSizeWarningLimit: 100_000,
  },
});
