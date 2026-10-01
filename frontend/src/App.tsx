import { useCallback, useEffect, useState, type FormEvent } from "react";
import { ArrowRight, Boxes, CircleAlert, Copy, KeyRound, LogOut, RefreshCw, Server, ShieldCheck, UserRound } from "lucide-react";
import { TemplateCatalog } from "./TemplateCatalog";
import "./index.css";

type Account = {
    id: number;
    qualified_name: string;
    display_name: string;
    platform_administrator: boolean;
};

type Deployment = {
    id: number;
    name: string;
    description: string;
    root_node_id: number | null;
};

type Resource = {
    id: number;
    name: string;
    kind: string;
    power_state: string;
};

type DeploymentDetail = {
    deployment: Deployment;
    resources: Resource[];
};

type AuthStatus = {
    setup_required: boolean;
    authenticated: boolean;
    account?: Account;
};

type ViewMode = "login" | "bootstrap" | "activate";

const apiRoot = "/api/v1";

async function apiRequest<T>(path: string, method = "GET", body?: unknown): Promise<T> {
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

export function App() {
    const [status, setStatus] = useState<AuthStatus | null>(null);
    const [mode, setMode] = useState<ViewMode>("login");
    const [deployments, setDeployments] = useState<Deployment[]>([]);
    const [selected, setSelected] = useState<DeploymentDetail | null>(null);
    const [error, setError] = useState("");
    const [notice, setNotice] = useState("");
    const [busy, setBusy] = useState(false);
    const [token, setToken] = useState("");
    const [tokenId, setTokenId] = useState<number | null>(null);
    const [loginName, setLoginName] = useState("");
    const [password, setPassword] = useState("");
    const [oneTimeToken, setOneTimeToken] = useState("");
    const [newPassword, setNewPassword] = useState("");

    const loadStatus = useCallback(async () => {
        const result = await apiRequest<AuthStatus>("/auth/status");
        setStatus(result);
        if (result.setup_required) {
            setMode("bootstrap");
        }
        return result;
    }, []);

    const loadDeployments = useCallback(async () => {
        const result = await apiRequest<{ deployments: Deployment[] }>("/deployments");
        const visibleDeployments = result.deployments ?? [];
        setDeployments(visibleDeployments);
        setSelected((current) => current && !visibleDeployments.some((deployment) => deployment.id === current.deployment.id) ? null : current);
    }, []);

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
        if (!status?.authenticated) {
            setDeployments([]);
            setSelected(null);
            return;
        }
        loadDeployments().catch((requestError: Error) => setError(requestError.message));
    }, [loadDeployments, status?.authenticated]);

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
            await loadStatus();
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
            setToken("");
            setNotice("Signed out.");
        } catch (requestError) {
            setError((requestError as Error).message);
        }
    }

    async function createProviderToken() {
        setBusy(true);
        setError("");
        try {
            const result = await apiRequest<{ id: number; token: string }>("/auth/api-tokens", "POST", {
                name: "OpenTofu provider",
                lifetime_days: 30
            });
            setToken(result.token);
            setTokenId(result.id);
            setNotice("Copy this token now. Organesson will not show it again.");
        } catch (requestError) {
            setError((requestError as Error).message);
        } finally {
            setBusy(false);
        }
    }

    async function revokeProviderToken() {
        if (tokenId === null) {
            return;
        }
        setBusy(true);
        setError("");
        try {
            await apiRequest(`/auth/api-tokens/${tokenId}`, "DELETE", {});
            setToken("");
            setTokenId(null);
            setNotice("Provider token revoked.");
        } catch (requestError) {
            setError((requestError as Error).message);
        } finally {
            setBusy(false);
        }
    }

    async function openDeployment(deployment: Deployment) {
        setError("");
        try {
            setSelected(await apiRequest<DeploymentDetail>(`/deployments/${deployment.id}`));
        } catch (requestError) {
            setError((requestError as Error).message);
        }
    }

    async function changePower(resource: Resource) {
        setError("");
        try {
            await apiRequest(`/virtual-machines/${resource.id}/power`, "POST", {
                action: resource.power_state === "running" ? "stop" : "start"
            });
            if (selected) {
                setSelected(await apiRequest<DeploymentDetail>(`/deployments/${selected.deployment.id}`));
            }
        } catch (requestError) {
            setError((requestError as Error).message);
        }
    }

    async function copyToken() {
        await navigator.clipboard.writeText(token);
        setNotice("Provider token copied to clipboard.");
    }

    const account = status?.account;

    return (
        <div className="app-shell">
            <header className="site-header">
                <a className="wordmark" href="/" aria-label="Organesson home">
                    <span className="wordmark-mark" aria-hidden="true">O</span>
                    <span>organesson</span>
                </a>
                {account ? (
                    <div className="account-menu">
                        <span className="account-name"><UserRound size={15} />{account.qualified_name}</span>
                        <button className="quiet-button" type="button" onClick={signOut}><LogOut size={15} /> Sign out</button>
                    </div>
                ) : <span className="environment-label">Proxmox resource management</span>}
            </header>

            <main className="main-content">
                {error && <p className="message message-error" role="alert"><CircleAlert size={17} />{error}</p>}
                {notice && <p className="message message-success" role="status"><ShieldCheck size={17} />{notice}</p>}
                {!status ? <section className="auth-card"><p className="eyebrow">Connecting</p><h1>Loading Organesson…</h1></section> : account ? (
                    <>
                        <section className="page-introduction">
                            <p className="eyebrow">{account.platform_administrator ? "Platform overview" : "Your resources"}</p>
                            <h1>{account.platform_administrator ? "Infrastructure, with ownership in view." : `Welcome, ${account.display_name}.`}</h1>
                            <p className="lede">Resources appear here according to your access. Open a deployment to inspect what you can see and manage.</p>
                        </section>

                        <div className="dashboard-layout">
                            <section className="panel deployment-panel" aria-labelledby="deployments-heading">
                                <div className="panel-heading">
                                    <div><p className="eyebrow">Workspace</p><h2 id="deployments-heading">Deployments</h2></div>
                                    <button className="icon-button" type="button" onClick={() => loadDeployments().catch((requestError: Error) => setError(requestError.message))} aria-label="Refresh deployments"><RefreshCw size={16} /></button>
                                </div>
                                {deployments.length === 0 ? (
                                    <div className="empty-state"><Boxes size={22} /><p>No deployments are visible to this account yet.</p></div>
                                ) : (
                                    <ul className="deployment-list">
                                        {deployments.map((deployment) => (
                                            <li key={deployment.id}>
                                                <button className={`deployment-row${selected?.deployment.id === deployment.id ? " is-selected" : ""}`} type="button" onClick={() => openDeployment(deployment)}>
                                                    <span className="deployment-symbol"><Server size={17} /></span>
                                                    <span className="deployment-copy"><strong>{deployment.name}</strong><small>{deployment.description || "No description"}</small></span>
                                                    <ArrowRight size={16} />
                                                </button>
                                            </li>
                                        ))}
                                    </ul>
                                )}
                            </section>

                            <section className="panel detail-panel" aria-live="polite">
                                {selected ? (
                                    <>
                                        <div className="panel-heading"><div><p className="eyebrow">Deployment · {selected.deployment.id}</p><h2>{selected.deployment.name}</h2></div><span className="resource-count">{selected.resources.length} resources</span></div>
                                        {selected.resources.length === 0 ? <div className="empty-state"><Boxes size={22} /><p>No resources in this deployment are visible to you.</p></div> : (
                                            <ul className="resource-list">
                                                {selected.resources.map((resource) => (
                                                    <li className="resource-row" key={resource.id}>
                                                        <span className="resource-symbol"><Server size={17} /></span>
                                                        <span className="resource-copy"><strong>{resource.name}</strong><small>{resource.kind.replaceAll("_", " ")} · {resource.power_state}</small></span>
                                                        {resource.kind === "virtual_machine" && <button className="secondary-action" type="button" onClick={() => changePower(resource)}>{resource.power_state === "running" ? "Stop" : "Start"}</button>}
                                                    </li>
                                                ))}
                                            </ul>
                                        )}
                                    </>
                                ) : <div className="empty-state detail-placeholder"><Server size={24} /><p>Select a deployment to see its resources.</p></div>}
                            </section>
                        </div>

                        {account.platform_administrator && <TemplateCatalog request={apiRequest} onError={setError} onNotice={setNotice} />}

                        {account.platform_administrator && <section className="panel token-panel">
                            <div className="token-copy"><span className="panel-icon"><KeyRound size={17} /></span><div><p className="eyebrow">OpenTofu access</p><h2>Provider API token</h2><p>Create a short-lived token for the local provider smoke example. It belongs to your account and can be revoked by signing in again.</p></div></div>
                            <button className="primary-action" type="button" disabled={busy || tokenId !== null} onClick={createProviderToken}>{busy ? "Creating…" : tokenId !== null ? "Token created in this tab" : "Create 30-day token"}</button>
                            {tokenId !== null && <div className="token-result">{token && <code>{token}</code>}<div className="token-actions">{token && <button className="secondary-action" type="button" onClick={copyToken}><Copy size={15} /> Copy token</button>}<button className="secondary-action" type="button" disabled={busy} onClick={revokeProviderToken}>Revoke token</button></div></div>}
                        </section>}
                    </>
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
