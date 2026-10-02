import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";
import tailwindcss from "@tailwindcss/vite";
import { readFileSync } from "node:fs";

const localAPITarget = process.env.ORGANESSON_API_TARGET ?? "https://127.0.0.1:6800";
const usesLocalSelfSignedAPI = process.env.ORGANESSON_API_TARGET === undefined;

export default defineConfig({
    plugins: [react(), tailwindcss()],
    server: {
        allowedHosts: true,
        host: "::",
        https: {
            cert: readFileSync(new URL("../backend/tls/server.crt", import.meta.url)),
            key: readFileSync(new URL("../backend/tls/server.key", import.meta.url))
        },
        port: 8080,
        strictPort: true,
        proxy: {
            "/api": {
                target: localAPITarget,
                secure: !usesLocalSelfSignedAPI,
                ws: true,
                // Preserve the browser Host so Fiber's CSRF origin check sees
                // the same origin the browser sends for the dev UI.
                changeOrigin: false
            }
        }
    }
});
