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
    outDir: "../proxy/ui_dist",
    assetsDir: "assets",
  },
  server: {
    proxy: {
      "/api": "http://localhost:8080", // Proxy API calls to Go backend during development
      "/logs": "http://localhost:8080",
      "/upstream": "http://localhost:8080",
      "/unload": "http://localhost:8080",
      "/v1": "http://localhost:8080",
    },
  },
});
