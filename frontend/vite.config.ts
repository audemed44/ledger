import preact from "@preact/preset-vite";
import { defineConfig } from "vitest/config";

export default defineConfig({
  plugins: [preact()],
  build: { outDir: "../web/dist", emptyOutDir: true, assetsInlineLimit: 0 },
  server: {
    // `npm run dev` proxies the API to a local `ledger` binary (or LEDGER_URL).
    // Keep the Host header: Ledger refuses writes whose Origin doesn't match it.
    proxy: {
      "/api": { target: process.env.LEDGER_URL ?? "http://localhost:8080", changeOrigin: false },
    },
  },
  test: { environment: "jsdom" },
});
