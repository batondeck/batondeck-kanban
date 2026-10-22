/// <reference types="vitest/config" />
import { defineConfig } from "vitest/config";
import react from "@vitejs/plugin-react";

// The build lands in dist/, which go:embed compiles into the binary (embed.go).
// The dev server proxies the data plane to a locally running `batondeck-kanban serve`,
// so `npm run dev` gives hot reload against real endpoints.
export default defineConfig({
  plugins: [react()],
  build: { outDir: "dist", assetsDir: "assets", sourcemap: false, target: "es2022" },
  test: {
    environment: "jsdom",
    globals: true,
    setupFiles: ["./src/test-setup.ts"],
    // Vitest stubs CSS imports to '' by default, which makes `index.css?raw` come back EMPTY, and a
    // token guard reading an empty stylesheet passes vacuously; tokens.test.ts asserts a non-empty read.
    css: true,
  },
  server: {
    proxy: Object.fromEntries(
      ["/api", "/healthz"].map((p) => [
        p, { target: "http://127.0.0.1:8080", changeOrigin: false },
      ]),
    ),
  },
});
