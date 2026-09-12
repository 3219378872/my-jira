import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";
import tailwindcss from "@tailwindcss/vite";

export default defineConfig({
  plugins: [react(), tailwindcss()],
  build: {
    rollupOptions: {
      output: {
        manualChunks(id) {
          if (
            /\/node_modules\/(?:react|react-dom|scheduler|react-router|react-router-dom)\//.test(
              id,
            )
          )
            return "react-runtime";
          if (
            id.includes("/node_modules/@radix-ui/") ||
            id.includes("/node_modules/@floating-ui/")
          )
            return "ui-primitives";
        },
      },
    },
  },
  server: {
    host: "127.0.0.1",
    port: 4173,
    strictPort: true,
    proxy: {
      "/api": {
        target: process.env.VITE_API_PROXY_URL ?? "http://127.0.0.1:8088",
        changeOrigin: true,
      },
      "/live": {
        target: process.env.VITE_LIVE_PROXY_URL ?? "http://127.0.0.1:3101",
        ws: true,
        changeOrigin: true,
        rewrite: (path) => path.replace(/^\/live/, "") || "/",
      },
    },
  },
});
