import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";
import tailwindcss from "@tailwindcss/vite";

export default defineConfig({
    plugins: [react(), tailwindcss()],
    server: {
        allowedHosts: true,
        host: "0.0.0.0",
        port: 8080,
        strictPort: true,
        proxy: {
            "/api": {
                target: process.env.ORGANESSON_API_TARGET ?? "http://127.0.0.1:6800",
                // Preserve the browser Host so Fiber's CSRF origin check sees
                // the same origin the browser sends for the dev UI.
                changeOrigin: false
            }
        }
    }
});
