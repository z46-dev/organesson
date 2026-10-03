import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";
import tailwindcss from "@tailwindcss/vite";
import { existsSync, readFileSync } from "node:fs";
import { resolve } from "node:path";
import { fileURLToPath } from "node:url";

const localAPITarget = process.env.ORGANESSON_API_TARGET ?? "https://127.0.0.1:6800";
const usesLocalSelfSignedAPI = process.env.ORGANESSON_API_TARGET === undefined;
const defaultTLSDirectory = fileURLToPath(new URL("../backend/tls", import.meta.url));

export default defineConfig(({ command }) => {
    const serverConfiguration = {
        allowedHosts: true as const,
        host: "::",
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
    };

    if (command === "serve") {
        const tlsDirectory = resolve(process.env.ORGANESSON_TLS_DIR ?? defaultTLSDirectory);
        const certificatePath = resolve(tlsDirectory, "server.crt");
        const keyPath = resolve(tlsDirectory, "server.key");

        if (!existsSync(certificatePath) || !existsSync(keyPath)) {
            throw new Error(`Vite development TLS files were not found in ${tlsDirectory}. Set ORGANESSON_TLS_DIR to the directory containing server.crt and server.key.`);
        }

        Object.assign(serverConfiguration, {
            https: {
                cert: readFileSync(certificatePath),
                key: readFileSync(keyPath)
            }
        });
    }

    return {
        plugins: [react(), tailwindcss()],
        server: serverConfiguration
    }
});
