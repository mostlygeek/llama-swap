import { defineConfig } from "vite";
import { svelte } from "@sveltejs/vite-plugin-svelte";
import tailwindcss from "@tailwindcss/vite";
import { compression } from "vite-plugin-compression2";

// https://vite.dev/config/
export default defineConfig({
  plugins: [
    svelte(),
    tailwindcss(),
    compression({
      algorithm: "gzip",
      exclude: [/\.(br)$/, /\.(gz)$/],
      threshold: 1024,
    }),
    compression({
      algorithm: "brotliCompress",
      exclude: [/\.(br)$/, /\.(gz)$/],
      threshold: 1024,
      filename: "[path][base].br",
    }),
  ],
  base: "/ui/",
  build: {
    outDir: "../internal/server/ui_dist",
    assetsDir: "assets",
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
