import { useCallback, useEffect, useRef, useState, type FormEvent, type MouseEvent } from "react";
import { AdminPage } from "./AdminPage";
import { apiRequest } from "./api";
import { Dashboard } from "./Dashboard";
import { VMConsolePage } from "./VMConsolePage";
import { ToastViewport, type ToastNotice } from "./Toasts";
import { resolveRoute } from "./routes.js";
import type { AuthStatus } from "./types";
import "./index.css";

type ViewMode = "login" | "bootstrap" | "activate";
type Theme = "light" | "dark";

// Owns authentication, shared application chrome, and top-level navigation.
export function App() {
    const [status, setStatus] = useState<AuthStatus | null>(null);
    const [mode, setMode] = useState<ViewMode>("login");
    const [pathname, setPathname] = useState(window.location.pathname);
    const [toasts, setToasts] = useState<ToastNotice[]>([]);
    const [busy, setBusy] = useState(false);
    const [loginName, setLoginName] = useState("");
    const [realm, setRealm] = useState("");
    const [password, setPassword] = useState("");
    const [oneTimeToken, setOneTimeToken] = useState("");
    const [newPassword, setNewPassword] = useState("");
    const [theme, setTheme] = useState<Theme>(() => localStorage.getItem("organesson-theme") === "dark" ? "dark" : "light");
    const nextToastID = useRef(0);

    const pushToast = useCallback((kind: ToastNotice["kind"], message: string) => {
        nextToastID.current += 1;
        setToasts((current) => [...current.slice(-3), { id: nextToastID.current, kind, message }]);
    }, []);
    const dismissToast = useCallback((id: number) => setToasts((current) => current.filter((toast) => toast.id !== id)), []);
    const onError = useCallback((message: string) => pushToast("error", message), [pushToast]);
    const onNotice = useCallback((message: string) => pushToast("success", message), [pushToast]);

    useEffect(() => {
        let active = true;
        apiRequest<AuthStatus>("/auth/status")
            .then((result) => {
                if (active) {
                    setStatus(result);
                    if (result.setup_required) {
                        setMode("bootstrap");
                    }
                }
            })
            .catch((requestError: Error) => active && onError(requestError.message));
        return () => {
            active = false;
        };
    }, [onError]);

    useEffect(() => {
        const realms = status?.realms ?? [];
        if (realms.length > 0 && !realms.includes(realm)) {
            const firstRealm = realms[0];
            if (firstRealm) {
                setRealm(firstRealm);
            }
        }
    }, [realm, status?.realms]);

    useEffect(() => {
        document.documentElement.dataset.theme = theme;
        localStorage.setItem("organesson-theme", theme);
    }, [theme]);

    useEffect(() => {
        function updatePath() {
            setPathname(window.location.pathname);
        }
        window.addEventListener("popstate", updatePath);
        return () => window.removeEventListener("popstate", updatePath);
    }, []);

    function navigate(path: string) {
        if (path === pathname) {
            return;
        }
        window.history.pushState({}, "", path);
        setPathname(path);
    }

    function handleNavigation(event: MouseEvent<HTMLAnchorElement>, path: string) {
        if (event.button !== 0 || event.metaKey || event.ctrlKey || event.shiftKey || event.altKey) {
            return;
        }
        event.preventDefault();
        navigate(path);
    }

    async function submitAuth(event: FormEvent<HTMLFormElement>) {
        event.preventDefault();
        setBusy(true);
        try {
            if (mode === "activate") {
                await apiRequest("/auth/password/redeem", "POST", { token: oneTimeToken, password: newPassword });
                setMode("login");
                setLoginName("");
                setPassword("");
                setOneTimeToken("");
                setNewPassword("");
                onNotice("Account activated. Sign in with the username and password you just set.");
                return;
            }
            if (mode === "bootstrap") {
                await apiRequest("/auth/bootstrap/redeem", "POST", { token: oneTimeToken, password: newPassword });
                setOneTimeToken("");
                setNewPassword("");
                onNotice("Administrator account is ready.");
            } else {
                await apiRequest("/auth/login", "POST", { username: loginName, realm, password });
                setPassword("");
            }
            setStatus(await apiRequest<AuthStatus>("/auth/status"));
        } catch (requestError) {
            onError((requestError as Error).message);
        } finally {
            setBusy(false);
        }
    }

    async function signOut() {
        try {
            await apiRequest("/auth/logout", "POST", {});
            setStatus({ setup_required: false, authenticated: false, realms: status?.realms });
            onNotice("Signed out.");
            navigate("/");
        } catch (requestError) {
            onError((requestError as Error).message);
        }
    }

    const account = status?.authenticated ? status.account : undefined;
    const route = resolveRoute(pathname, account?.platform_administrator === true);

    return (
        <div className="app-shell">
            <header className="site-header">
                <a className="wordmark" href="/" aria-label="Organesson home" onClick={(event) => handleNavigation(event, "/")}>
                    <img className="wordmark-mark" src="/organesson-mark.svg" alt="" />
                    <span>organesson</span>
                </a>
                {account ? (
                    <div className="header-actions">
                        <nav className="primary-nav" aria-label="Main navigation">
                            <a className="nav-link" href="/" aria-current={route === "dashboard" ? "page" : undefined} onClick={(event) => handleNavigation(event, "/")}>Dashboard</a>
                            {account.platform_administrator && <a className="nav-link" href="/admin" aria-current={route === "admin" ? "page" : undefined} onClick={(event) => handleNavigation(event, "/admin")}>Administration</a>}
                        </nav>
                        <details className="profile-menu">
                            <summary><span className="profile-icon" aria-hidden="true">{account.display_name.slice(0, 1).toUpperCase()}</span>{account.qualified_name}</summary>
                            <div className="profile-popover">
                                <span>{account.qualified_name}</span>
                                <div className="theme-control"><span>Theme</span><div role="group" aria-label="Color theme"><button type="button" aria-pressed={theme === "light"} onClick={() => setTheme("light")}>Light</button><button type="button" aria-pressed={theme === "dark"} onClick={() => setTheme("dark")}>Dark</button></div></div>
                                <button className="quiet-button" type="button" onClick={signOut}>Sign out</button>
                            </div>
                        </details>
                    </div>
                ) : <span className="environment-label">Proxmox resource management</span>}
            </header>

            <main className={`main-content${account && route === "dashboard" ? " dashboard-main" : account && route === "admin" ? " admin-main" : ""}`}>
                {!status ? <section className="auth-card"><p className="eyebrow">Connecting</p><h1>Loading Organesson…</h1></section> : account ? (
                    route === "dashboard" ? <Dashboard request={apiRequest} onError={onError} />
                        : route === "admin" ? <AdminPage request={apiRequest} onError={onError} onNotice={onNotice} />
                            : route === "console" ? <VMConsolePage resourceID={Number(pathname.split("/").filter(Boolean)[1])} request={apiRequest} onError={onError} />
                            : route === "forbidden" ? <section className="panel route-message"><p className="eyebrow">Platform administration</p><h1>Access restricted.</h1><p>Your account is not a platform administrator. Deployment roles do not grant platform-wide administration.</p><a className="primary-action" href="/" onClick={(event) => handleNavigation(event, "/")}>Return to dashboard</a></section>
                                : <section className="panel route-message"><p className="eyebrow">Not found</p><h1>That page doesn’t exist.</h1><a className="primary-action" href="/" onClick={(event) => handleNavigation(event, "/")}>Return to dashboard</a></section>
                ) : (
                    <section className="auth-layout">
                        <div className="auth-intro"><p className="eyebrow">Resource ownership, made clear</p><h1>{mode === "bootstrap" ? "Set up the administrator." : mode === "activate" ? "Activate your account." : "Sign in to Organesson."}</h1></div>
                        <form className="panel auth-card" onSubmit={submitAuth}>
                            {mode === "login" ? <>
                                <label>Realm<select value={realm} onChange={(event) => setRealm(event.target.value)} required disabled={(status.realms ?? []).length === 0}><option value="" disabled>{(status.realms ?? []).length === 0 ? "No realms available" : "Select realm"}</option>{(status.realms ?? []).map((name) => <option key={name} value={name}>{name}</option>)}</select></label>
                                <label>Username<input autoComplete="username" value={loginName} onChange={(event) => setLoginName(event.target.value)} placeholder="username" required /></label>
                                <label>Password<input autoComplete="current-password" type="password" value={password} onChange={(event) => setPassword(event.target.value)} required /></label>
                                <button className="primary-action full-width" type="submit" disabled={busy || !realm}>{busy ? "Signing in…" : "Sign in"}</button>
                                <button className="text-action" type="button" onClick={() => setMode("activate")}>Have a one-time activation token?</button>
                            </> : <>
                                <p className="form-intro">{mode === "bootstrap" ? "Paste the one-time setup token printed by the backend, then choose a strong password." : "Paste the one-time token from the local fixture seeding output, then choose a password for this account."}</p>
                                <label>One-time token<input autoComplete="one-time-code" value={oneTimeToken} onChange={(event) => setOneTimeToken(event.target.value)} required /></label>
                                <label>{mode === "bootstrap" ? "Administrator password" : "New password"}<input autoComplete="new-password" type="password" minLength={12} value={newPassword} onChange={(event) => setNewPassword(event.target.value)} required /></label>
                                <button className="primary-action full-width" type="submit" disabled={busy}>{busy ? "Saving…" : mode === "bootstrap" ? "Activate administrator" : "Activate account"}</button>
                                {mode === "activate" && <button className="text-action" type="button" onClick={() => setMode("login")}>Back to sign in</button>}
                            </>}
                        </form>
                    </section>
                )}
            </main>
            <ToastViewport toasts={toasts} onDismiss={dismissToast} />
        </div>
    );
}

export default App;
