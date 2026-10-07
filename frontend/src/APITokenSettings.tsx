import { useEffect, useRef, useState, type FormEvent } from "react";
import { Check, Copy, KeyRound, Plus, RefreshCw, RotateCw, Trash2, X } from "lucide-react";
import type { ApiRequest } from "./api";

type UserIdentity = { qualified_name: string; realm: string; kind: string };
type UserRecord = { id: number; display_name: string; active: boolean; identities: UserIdentity[] };
type APIToken = {
    id: number;
    account_id: number;
    owner_display_name: string;
    owner_qualified_name: string;
    name: string;
    created_at: string;
    expires_at: string | null;
    last_used_at: string | null;
    revoked_at: string | null;
};
type SecretResult = { id: number; account_id: number; name: string; expires_at: string; token: string };
type Props = { request: ApiRequest; onError: (message: string) => void; onNotice: (message: string) => void };

function tokenStatus(token: APIToken): string {
    if (!token.expires_at || new Date(token.expires_at).getTime() <= Date.now()) return "Expired";
    if (token.revoked_at) return "Revoked";
    return "Active";
}

function formatDate(value: string | null): string {
    return value ? new Date(value).toLocaleString() : "—";
}

// Lets platform administrators issue, rotate, and expire user-owned provider tokens.
export function APITokenSettings({ request, onError, onNotice }: Props) {
    const [tokens, setTokens] = useState<APIToken[]>([]);
    const [users, setUsers] = useState<UserRecord[]>([]);
    const [loading, setLoading] = useState(true);
    const [busy, setBusy] = useState(false);
    const [accountID, setAccountID] = useState("");
    const [name, setName] = useState("OpenTofu provider");
    const [lifetimeDays, setLifetimeDays] = useState("90");
    const [modalView, setModalView] = useState<"create" | "secret" | null>(null);
    const [secretResult, setSecretResult] = useState<SecretResult | null>(null);
    const dialog = useRef<HTMLDialogElement>(null);
    const expiredTokenCount = tokens.filter((token) => tokenStatus(token) === "Expired").length;
    const orderedTokens = [...tokens].sort((left, right) => {
        const leftIsActive = tokenStatus(left) === "Active";
        const rightIsActive = tokenStatus(right) === "Active";
        if (leftIsActive !== rightIsActive) return leftIsActive ? -1 : 1;
        return new Date(right.created_at).getTime() - new Date(left.created_at).getTime();
    });

    async function refresh() {
        setLoading(true);
        try {
            const [tokenResult, userResult] = await Promise.all([
                request<{ tokens: APIToken[] }>("/auth/admin/api-tokens"),
                request<{ users: UserRecord[] }>("/auth/admin/users")
            ]);
            setTokens(tokenResult.tokens ?? []);
            setUsers(userResult.users ?? []);
            setAccountID((current) => {
                const eligibleUsers = (userResult.users ?? []).filter((user) => user.active);
                return eligibleUsers.some((user) => String(user.id) === current) ? current : String(eligibleUsers[0]?.id ?? "");
            });
        } catch (error) {
            onError((error as Error).message);
        } finally {
            setLoading(false);
        }
    }

    useEffect(() => {
        refresh();
    }, [request, onError]);

    const eligibleUsers = users.filter((user) => user.active);

    useEffect(() => {
        if (modalView && dialog.current && !dialog.current.open) {
            dialog.current.showModal();
        } else if (!modalView && dialog.current?.open) {
            dialog.current.close();
        }
    }, [modalView]);

    function closeModal() {
        setModalView(null);
        setSecretResult(null);
        setName("OpenTofu provider");
    }

    async function createToken(event: FormEvent<HTMLFormElement>) {
        event.preventDefault();
        setBusy(true);
        try {
            const created = await request<SecretResult>("/auth/admin/api-tokens", "POST", {
                account_id: Number(accountID),
                name,
                lifetime_days: Number(lifetimeDays)
            });
            setSecretResult(created);
            setModalView("secret");
            await refresh();
            onNotice("Token created. Copy the secret now; it will not be shown again.");
        } catch (error) {
            onError((error as Error).message);
        } finally {
            setBusy(false);
        }
    }

    async function renewToken(token: APIToken) {
        if (!window.confirm(`Renew “${token.name}” for ${token.owner_display_name} for 90 days? The current secret will stop working.`)) return;
        setBusy(true);
        try {
            const renewed = await request<SecretResult>(`/auth/admin/api-tokens/${token.id}/renew`, "POST", { lifetime_days: 90 });
            setSecretResult(renewed);
            setModalView("secret");
            await refresh();
            onNotice("Token renewed. Copy the new secret now; it will not be shown again.");
        } catch (error) {
            onError((error as Error).message);
        } finally {
            setBusy(false);
        }
    }

    async function expireToken(token: APIToken) {
        if (!window.confirm(`Expire “${token.name}” for ${token.owner_display_name}?`)) return;
        setBusy(true);
        try {
            await request(`/auth/admin/api-tokens/${token.id}`, "DELETE", {});
            await refresh();
            onNotice("API token expired.");
        } catch (error) {
            onError((error as Error).message);
        } finally {
            setBusy(false);
        }
    }

    async function pruneExpiredTokens() {
        if (expiredTokenCount === 0 || !window.confirm(`Permanently remove ${expiredTokenCount} expired API token${expiredTokenCount === 1 ? "" : "s"}?`)) return;
        setBusy(true);
        try {
            const result = await request<{ pruned_count: number }>("/auth/admin/api-tokens/prune-expired", "POST", {});
            await refresh();
            onNotice(`${result.pruned_count} expired API token${result.pruned_count === 1 ? "" : "s"} pruned.`);
        } catch (error) {
            onError((error as Error).message);
        } finally {
            setBusy(false);
        }
    }

    async function copySecret() {
        if (!secretResult) return;
        try {
            await navigator.clipboard.writeText(secretResult.token);
            onNotice("Token copied to clipboard.");
        } catch {
            onError("Clipboard access was unavailable. Select and copy the token manually.");
        }
    }

    return (
        <section className="api-token-settings" aria-label="API tokens">
            <div className="settings-section-heading">
                <h2>API tokens</h2>
                <div className="auth-toolbar">
                    <button className="secondary-action" type="button" onClick={pruneExpiredTokens} disabled={busy || expiredTokenCount === 0}><Trash2 size={14} />Prune expired{expiredTokenCount > 0 ? ` (${expiredTokenCount})` : ""}</button>
                    <button className="secondary-action" type="button" onClick={() => setModalView("create")} disabled={eligibleUsers.length === 0}><Plus size={15} />Create token</button>
                    <button className="icon-button" type="button" aria-label="Refresh API tokens" onClick={refresh} disabled={loading}><RefreshCw size={15} /></button>
                </div>
            </div>
            {loading ? <p className="settings-empty">Loading…</p> : tokens.length === 0 ? <div className="api-token-empty"><KeyRound size={18} /><span>No API tokens.</span></div> : (
                <div className="api-token-table-scroll">
                    <table className="api-token-table">
                        <thead><tr><th>Token</th><th>User</th><th>Created</th><th>Last used</th><th>Expires</th><th>Status</th><th></th></tr></thead>
                        <tbody>{orderedTokens.map((token) => <tr key={token.id}>
                            <td><strong>{token.name}</strong><small>Secret hidden</small></td>
                            <td><strong>{token.owner_display_name || `Account ${token.account_id}`}</strong><small>{token.owner_qualified_name}</small></td>
                            <td>{formatDate(token.created_at)}</td>
                            <td>{formatDate(token.last_used_at)}</td>
                            <td>{formatDate(token.expires_at)}</td>
                            <td><span className={`api-token-state${tokenStatus(token) === "Active" ? " is-active" : ""}`}>{tokenStatus(token)}</span></td>
                            <td className="api-token-actions">{tokenStatus(token) === "Active" && <><button className="icon-button" type="button" aria-label={`Renew ${token.name}`} title="Renew token" disabled={busy} onClick={() => renewToken(token)}><RotateCw size={14} /></button><button className="icon-button danger-icon-button" type="button" aria-label={`Expire ${token.name}`} title="Expire token" disabled={busy} onClick={() => expireToken(token)}><Trash2 size={14} /></button></>}</td>
                        </tr>)}</tbody>
                    </table>
                </div>
            )}

            <dialog className="api-token-dialog" ref={dialog} onClose={closeModal} onClick={(event) => {
                if (event.target === event.currentTarget) closeModal();
            }}>
                {modalView === "create" && <form className="api-token-form" onSubmit={createToken}>
                    <header><div><p className="eyebrow">Platform credentials</p><h3>Create API token</h3></div><button className="icon-button" type="button" aria-label="Close" onClick={closeModal}><X size={16} /></button></header>
                    <label>Owner<select value={accountID} onChange={(event) => setAccountID(event.target.value)} required>{eligibleUsers.map((user) => <option key={user.id} value={user.id}>{user.display_name} · {user.identities[0]?.qualified_name ?? `Account ${user.id}`}</option>)}</select></label>
                    <label>Name<input value={name} onChange={(event) => setName(event.target.value)} maxLength={100} required /></label>
                    <label>Expires after<select value={lifetimeDays} onChange={(event) => setLifetimeDays(event.target.value)}><option value="30">30 days</option><option value="90">90 days</option><option value="180">180 days</option><option value="365">365 days</option></select></label>
                    <footer><button className="secondary-action" type="button" onClick={closeModal} disabled={busy}>Cancel</button><button className="primary-action" type="submit" disabled={busy || !accountID}>{busy ? "Creating…" : "Create token"}</button></footer>
                </form>}
                {modalView === "secret" && secretResult && <div className="api-token-secret">
                    <header><div><p className="eyebrow">Copy once</p><h3>{secretResult.name}</h3></div><button className="icon-button" type="button" aria-label="Close" onClick={closeModal}><X size={16} /></button></header>
                    <code>{secretResult.token}</code>
                    <p>Owned by {users.find((user) => user.id === secretResult.account_id)?.display_name ?? `account ${secretResult.account_id}`} · expires {formatDate(secretResult.expires_at)}</p>
                    <footer><button className="secondary-action" type="button" onClick={closeModal}>Done</button><button className="primary-action" type="button" onClick={copySecret}><Copy size={15} />Copy token</button></footer>
                </div>}
            </dialog>
        </section>
    );
}
