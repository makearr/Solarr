import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";
import tailwindcss from "@tailwindcss/vite";
import { fileURLToPath, URL } from "node:url";

// The bundle is written next to the Go package that embeds it, rather than into
// web/dist, so `go:embed` can reach it -- embed patterns cannot climb out of the
// embedding package's directory.
//
// emptyOutDir is off on purpose: the directory carries a committed .gitkeep so
// the embed pattern always matches, and emptying would delete it and leave the
// tree dirty, which `make image` refuses.
export default defineConfig({
  plugins: [react(), tailwindcss()],
  resolve: {
    alias: { "@": fileURLToPath(new URL("./src", import.meta.url)) },
  },
  build: {
    outDir: "../internal/webui/dist",
    emptyOutDir: false,
    // Fail the build rather than shipping a bundle nobody can debug.
    sourcemap: true,
  },
  server: {
    port: 5173,
    // Dev mode talks to a real backend; production serves both from one origin,
    // so the app only ever uses same-origin relative URLs (ADR-19).
    proxy: {
      "/api": {
        target: "http://localhost:8080",
        changeOrigin: true,
        // SSE must not be buffered by the proxy or the stream appears dead.
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
