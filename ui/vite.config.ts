import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";
import tailwindcss from "@tailwindcss/vite";

// https://vite.dev/config/
export default defineConfig({
  plugins: [react(), tailwindcss()],
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
    },
  },
});
