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
  },
  build: {
    outDir: "../internal/webui/dist",
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
