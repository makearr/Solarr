/// <reference types="vitest/config" />
import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";
import tailwindcss from "@tailwindcss/vite";
import { fileURLToPath, URL } from "node:url";

// outDir sits beside the Go package that embeds it, and emptyOutDir is off so
// the committed .gitkeep survives. See documentation/web-interface.md.
export default defineConfig({
  plugins: [react(), tailwindcss()],
  resolve: {
    alias: { "@": fileURLToPath(new URL("./src", import.meta.url)) },
  },
  test: {
    // The list hook's wiring -- URL binding, page accumulation, SSE invalidation --
    // is only testable with a DOM. The pure logic beside it does not need one and
    // does not mind having one.
    environment: "jsdom",
    setupFiles: ["./src/test-setup.ts"],
    // e2e/ is the Playwright smoke suite (make e2e). It needs a real browser and
    // a running instance, so vitest must not try to collect it.
    exclude: ["node_modules/**", "e2e/**"],
  },
  build: {
    outDir: "../internal/api/webui/dist",
    emptyOutDir: false,
    sourcemap: true,
  },
  server: {
    port: 5173,
    // Production serves both from one origin, so the app uses relative URLs.
    proxy: {
      "/api": {
        target: "http://localhost:8080",
        changeOrigin: true,
        // Unbuffered, or the stream appears dead.
        configure: (proxy) => {
          proxy.on("proxyRes", (proxyRes) => {
            if (proxyRes.headers["content-type"]?.includes("text/event-stream")) {
              delete proxyRes.headers["content-length"];
            }
          });
        },
      },
    },
  },
});
