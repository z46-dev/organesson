import { describe, expect, test } from "bun:test";
import { resolveRoute } from "./routes.js";

describe("application route access", () => {
    test("shows the dashboard to authenticated users", () => {
        expect(resolveRoute("/", false)).toBe("dashboard");
    });

    test("allows only platform administrators into administration", () => {
        expect(resolveRoute("/admin", true)).toBe("admin");
        expect(resolveRoute("/admin", false)).toBe("forbidden");
    });

    test("normalizes trailing slashes and rejects unknown paths", () => {
        expect(resolveRoute("/admin/", true)).toBe("admin");
        expect(resolveRoute("/deployments", true)).toBe("not-found");
    });
});
