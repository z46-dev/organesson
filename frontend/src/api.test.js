import { describe, expect, test } from "bun:test";
import { apiRequest } from "./api.js";

describe("API request security", () => {
    test("includes a CSRF token on bodyless mutations", async () => {
        const originalFetch = globalThis.fetch;
        const calls = [];
        globalThis.fetch = async (input, options) => {
            calls.push({ input, options });
            if (String(input).endsWith("/auth/csrf")) {
                return new Response(JSON.stringify({ csrf_token: "session-token" }), { status: 200 });
            }
            return new Response(null, { status: 204 });
        };

        try {
            await apiRequest("/vm-snapshots/12", "DELETE");
            expect(calls).toHaveLength(2);
            expect(calls[1].options.method).toBe("DELETE");
            expect(calls[1].options.headers.get("X-Csrf-Token")).toBe("session-token");
        } finally {
            globalThis.fetch = originalFetch;
        }
    });
});
