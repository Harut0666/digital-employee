import react from "@vitejs/plugin-react";
import { defineConfig } from "vite";

// 开发时把 /api 转给 T3 的 Elysia 服务；VITE_MOCK=1 时不会发出任何 /api 请求。
export default defineConfig({
  plugins: [react()],
  server: {
    port: 5173,
    proxy: { "/api": { target: process.env.VITE_API_TARGET ?? "http://localhost:3000", changeOrigin: true } },
  },
  build: { chunkSizeWarningLimit: 1800 },
});
