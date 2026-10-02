import { useEffect, useState, type FormEvent, type MouseEvent } from "react";
import { CircleAlert, LogOut, ShieldCheck, UserRound } from "lucide-react";
import { AdminPage } from "./AdminPage";
import { apiRequest } from "./api";
import { Dashboard } from "./Dashboard";
import { VMConsolePage } from "./VMConsolePage";
import { resolveRoute } from "./routes.js";
import type { AuthStatus } from "./types";
import "./index.css";

type ViewMode = "login" | "bootstrap" | "activate";

// Owns authentication, shared application chrome, and top-level navigation.
export function App() {
    const [status, setStatus] = useState<AuthStatus | null>(null);
    const [mode, setMode] = useState<ViewMode>("login");
    const [pathname, setPathname] = useState(window.location.pathname);
    const [error, setError] = useState("");
    const [notice, setNotice] = useState("");
    const [busy, setBusy] = useState(false);
    const [loginName, setLoginName] = useState("");
    const [password, setPassword] = useState("");
    const [oneTimeToken, setOneTimeToken] = useState("");
    const [newPassword, setNewPassword] = useState("");

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
            .catch((requestError: Error) => active && setError(requestError.message));
        return () => {
            active = false;
        };
    }, []);

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
        setError("");
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
        setError("");
        setNotice("");
        try {
            if (mode === "activate") {
                await apiRequest("/auth/password/redeem", "POST", { token: oneTimeToken, password: newPassword });
                setMode("login");
                setLoginName("");
                setPassword("");
                setOneTimeToken("");
                setNewPassword("");
                setNotice("Account activated. Sign in with the username and password you just set.");
                return;
            }
            if (mode === "bootstrap") {
                await apiRequest("/auth/bootstrap/redeem", "POST", { token: oneTimeToken, password: newPassword });
                setOneTimeToken("");
                setNewPassword("");
                setNotice("Administrator account is ready.");
            } else {
                await apiRequest("/auth/login", "POST", { qualified_name: loginName, password });
                setPassword("");
            }
            setStatus(await apiRequest<AuthStatus>("/auth/status"));
        } catch (requestError) {
            setError((requestError as Error).message);
        } finally {
            setBusy(false);
        }
    }

    async function signOut() {
        setError("");
        try {
            await apiRequest("/auth/logout", "POST", {});
            setStatus({ setup_required: false, authenticated: false });
            setNotice("Signed out.");
            navigate("/");
        } catch (requestError) {
            setError((requestError as Error).message);
        }
    }

    const account = status?.authenticated ? status.account : undefined;
    const route = resolveRoute(pathname, account?.platform_administrator === true);

    return (
        <div className="app-shell">
            <header className="site-header">
                <a className="wordmark" href="/" aria-label="Organesson home" onClick={(event) => handleNavigation(event, "/")}>
                    <span className="wordmark-mark" aria-hidden="true">O</span>
                    <span>organesson</span>
                </a>
                {account ? (
                    <div className="header-actions">
                        <nav className="primary-nav" aria-label="Main navigation">
                            <a className="nav-link" href="/" aria-current={route === "dashboard" ? "page" : undefined} onClick={(event) => handleNavigation(event, "/")}>Dashboard</a>
                            {account.platform_administrator && <a className="nav-link" href="/admin" aria-current={route === "admin" ? "page" : undefined} onClick={(event) => handleNavigation(event, "/admin")}>Administration</a>}
                        </nav>
                        <div className="account-menu">
                            <span className="account-name"><UserRound size={15} />{account.qualified_name}</span>
                            <button className="quiet-button" type="button" onClick={signOut}><LogOut size={15} /> Sign out</button>
                        </div>
                    </div>
                ) : <span className="environment-label">Proxmox resource management</span>}
            </header>

            <main className={`main-content${account && route === "dashboard" ? " dashboard-main" : account && route === "admin" ? " admin-main" : ""}`}>
                {error && <p className="message message-error" role="alert"><CircleAlert size={17} />{error}</p>}
                {notice && <p className="message message-success" role="status"><ShieldCheck size={17} />{notice}</p>}
                {!status ? <section className="auth-card"><p className="eyebrow">Connecting</p><h1>Loading Organesson…</h1></section> : account ? (
                    route === "dashboard" ? <Dashboard request={apiRequest} onError={setError} />
                        : route === "admin" ? <AdminPage request={apiRequest} onError={setError} onNotice={setNotice} />
                            : route === "console" ? <VMConsolePage resourceID={Number(pathname.split("/").filter(Boolean)[1])} request={apiRequest} onError={setError} />
                            : route === "forbidden" ? <section className="panel route-message"><p className="eyebrow">Platform administration</p><h1>Access restricted.</h1><p>Your account is not a platform administrator. Deployment roles do not grant platform-wide administration.</p><a className="primary-action" href="/" onClick={(event) => handleNavigation(event, "/")}>Return to dashboard</a></section>
                                : <section className="panel route-message"><p className="eyebrow">Not found</p><h1>That page doesn’t exist.</h1><a className="primary-action" href="/" onClick={(event) => handleNavigation(event, "/")}>Return to dashboard</a></section>
                ) : (
                    <section className="auth-layout">
                        <div className="auth-intro"><p className="eyebrow">Resource ownership, made clear</p><h1>{mode === "bootstrap" ? "Set up the administrator." : mode === "activate" ? "Activate your account." : "Sign in to Organesson."}</h1><p className="lede">Organesson keeps infrastructure and access organized around the people and teams who own it.</p></div>
                        <form className="panel auth-card" onSubmit={submitAuth}>
                            {mode === "login" ? <>
                                <label>Username<input autoComplete="username" value={loginName} onChange={(event) => setLoginName(event.target.value)} placeholder="name@organesson" required /></label>
                                <label>Password<input autoComplete="current-password" type="password" value={password} onChange={(event) => setPassword(event.target.value)} required /></label>
                                <button className="primary-action full-width" type="submit" disabled={busy}>{busy ? "Signing in…" : "Sign in"}</button>
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
        </div>
    );
}

export default App;
