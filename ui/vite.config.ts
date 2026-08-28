import { defineConfig, type Plugin } from "vite";
import { svelte } from "@sveltejs/vite-plugin-svelte";
import tailwindcss from "@tailwindcss/vite";
import { compression } from "vite-plugin-compression2";
import path from "node:path";

// KaTeX's CSS lists woff2/woff/ttf fallbacks per font-face, but browsers only
// ever fetch the first format they support (woff2, universally today) - the
// other two formats are never downloaded, they just bloat the build output
// (and the Go binary it's embedded into). Strip them at build time.
function stripKatexFontFallbacks(): Plugin {
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

// Already-compressed formats: re-compressing them wastes build time and only
// bloats the embedded assets with .gz/.br copies nobody benefits from.
// Already-compressed formats gain nothing from gzip/brotli, they just add dead
// weight to the Go binary these assets are embedded into.
const compressionExclude = [/\.(br|gz)$/, /\.(png|jpe?g|gif|webp|avif|ico)$/, /\.(woff2?)$/];

// https://vite.dev/config/
export default defineConfig({
  plugins: [
    svelte(),
    tailwindcss(),
    stripKatexFontFallbacks(),
    // The option is `algorithms` (plural) - the plugin ignores unknown keys and
    // silently runs both gzip and brotli, whose second pass also compresses
    // files the exclude filter already rejected.
    compression({
      algorithms: ["brotliCompress"],
      exclude: compressionExclude,
      threshold: 1024,
    }),
  ],
  base: "/ui/",
  resolve: {
    alias: {
      $lib: path.resolve(__dirname, "./src/lib"),
    },
  },
  build: {
    outDir: "../internal/server/ui_dist",
    assetsDir: "assets",
    // The playground chunk (markdown/KaTeX/highlight.js) is deferred and
    // loaded only after initial mount, so its size doesn't affect first paint.
    chunkSizeWarningLimit: 700,
  },
  server: {
    // yes very insecure but who's running this thing
    // on the public internet for dev?! haha.
    host: "0.0.0.0",
    allowedHosts: true,
    proxy: Object.fromEntries(
      ["/api", "/logs", "/upstream", "/unload", "/v1", "/sdapi"].map((path) => [
        path,
        process.env.LLAMA_SWAP_URL ?? "http://localhost:8080",
      ]),
    ),
  },
});
