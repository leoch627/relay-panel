import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";
import path from "node:path";

// 构建产物直接输出到 panel/web/，由 Go 二进制 embed（Go 源码零改动）
export default defineConfig({
  plugins: [react()],
  resolve: {
    alias: { "@": path.resolve(__dirname, "./src") },
  },
  build: {
    outDir: path.resolve(__dirname, "../web"),
    emptyOutDir: true,
    chunkSizeWarningLimit: 1024,
  },
  server: {
    proxy: {
      "/api": "http://127.0.0.1:8081",
    },
  },
});
