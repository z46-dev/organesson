// Resolves the browser path to an authenticated application destination.
export function resolveRoute(pathname, isPlatformAdministrator) {
    const normalizedPath = pathname.length > 1 ? pathname.replace(/\/+$/, "") : pathname;
    if (normalizedPath === "/") {
        return "dashboard";
    }
    if (normalizedPath === "/admin") {
        return isPlatformAdministrator ? "admin" : "forbidden";
    }
    if (/^\/console\/\d+$/.test(normalizedPath)) {
        return "console";
    }
    return "not-found";
}
