const apiRoot = "/api/v1";

export type ApiRequest = <T>(path: string, method?: string, body?: unknown) => Promise<T>;

// Makes an API request and includes the session-bound CSRF token for mutations.
export async function apiRequest<T>(path: string, method = "GET", body?: unknown): Promise<T> {
    const headers = new Headers({ Accept: "application/json" });
    const options: RequestInit = {
        method,
        credentials: "same-origin",
        headers
    };

    if (body !== undefined) {
        headers.set("Content-Type", "application/json");
        const csrfResponse = await fetch(`${apiRoot}/auth/csrf`, { credentials: "same-origin" });
        if (!csrfResponse.ok) {
            throw new Error("Could not initialize browser security. Refresh and try again.");
        }
        const csrfResult = await csrfResponse.json() as { csrf_token: string };
        headers.set("X-Csrf-Token", csrfResult.csrf_token);
        options.body = JSON.stringify(body);
    }

    const response = await fetch(`${apiRoot}${path}`, options);
    if (!response.ok) {
        let detail = `Request failed (${response.status}).`;
        try {
            const result = await response.json() as { error?: string };
            if (result.error) {
                detail = result.error;
            }
        } catch {
            // Keep the status-based message when an API response has no JSON body.
        }
        throw new Error(detail);
    }
    if (response.status === 204) {
        return undefined as T;
    }
    return response.json() as Promise<T>;
}
