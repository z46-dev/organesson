import { useEffect, useRef, useState, type FormEvent } from "react";
import { Copy, Plus, RefreshCw, ShieldCheck, UserRound, X } from "lucide-react";
import type { ApiRequest } from "./api";

type UserIdentity = { qualified_name: string; realm: string; kind: string };
type UserRecord = { id: number; display_name: string; platform_administrator: boolean; can_create_deployments: boolean; disabled: boolean; identities: UserIdentity[] };
type Props = { request: ApiRequest; onError: (message: string) => void; onNotice: (message: string) => void };

// Shows local Organesson users and the identity sources linked to each account.
export function UserDirectory({ request, onError, onNotice }: Props) {
    const [users, setUsers] = useState<UserRecord[]>([]);
    const [loading, setLoading] = useState(true);
    const [savingUserID, setSavingUserID] = useState<number | null>(null);
    const [resetLinks, setResetLinks] = useState<Record<number, string>>({});
    const [createOpen, setCreateOpen] = useState(false);
    const [creating, setCreating] = useState(false);
    const [username, setUsername] = useState("");
    const [displayName, setDisplayName] = useState("");
    const [createdAccount, setCreatedAccount] = useState<{ id: number; qualified_name: string; display_name: string; setup_token: string } | null>(null);
    const createDialog = useRef<HTMLDialogElement>(null);

    useEffect(() => {
        const dialog = createDialog.current;
        if (createOpen && dialog && !dialog.open) {
            dialog.showModal();
        } else if (!createOpen && dialog?.open) {
            dialog.close();
        }
    }, [createOpen]);

    async function loadUsers() {
        setLoading(true);
        try {
            const result = await request<{ users: UserRecord[] }>("/auth/admin/users");
            setUsers(result.users ?? []);
        } catch (error) {
            onError((error as Error).message);
        } finally {
            setLoading(false);
        }
    }

    useEffect(() => {
        loadUsers();
    }, [request, onError]);

    async function setAdministrator(user: UserRecord, enabled: boolean) {
        setSavingUserID(user.id);
        try {
            await request(`/auth/admin/users/${user.id}/platform-administrator`, "PUT", { enabled });
            await loadUsers();
            onNotice(`${user.display_name} ${enabled ? "is now" : "is no longer"} a platform administrator.`);
        } catch (error) {
            onError((error as Error).message);
        } finally {
            setSavingUserID(null);
        }
    }

    async function setDeploymentCreation(user: UserRecord, enabled: boolean) {
        setSavingUserID(user.id);
        try {
            await request(`/auth/admin/users/${user.id}/deployment-creation`, "PUT", { enabled });
            await loadUsers();
            onNotice(`${user.display_name} ${enabled ? "can now" : "can no longer"} create deployments.`);
        } catch (error) {
            onError((error as Error).message);
        } finally {
            setSavingUserID(null);
        }
    }

    async function createPasswordResetLink(user: UserRecord) {
        setSavingUserID(user.id);
        try {
            const result = await request<{ setup_token: string }>(`/admin/accounts/local/${user.id}/password-link`, "POST", {});
            const resetURL = new URL(window.location.href);
            resetURL.hash = new URLSearchParams({ "password-reset": result.setup_token }).toString();
            setResetLinks((current) => ({ ...current, [user.id]: resetURL.toString() }));
            onNotice(`One-time password reset link created for ${user.display_name}.`);
        } catch (error) {
            onError((error as Error).message);
        } finally {
            setSavingUserID(null);
        }
    }

    async function copyPasswordResetLink(user: UserRecord) {
        const resetLink = resetLinks[user.id];
        if (!resetLink) {
            return;
        }
        try {
            await navigator.clipboard.writeText(resetLink);
            onNotice("Password reset link copied.");
        } catch {
            onError("Could not copy the reset link. Select and copy it manually.");
        }
    }

    function closeCreateDialog() {
        if (!creating) {
            createDialog.current?.close();
        }
    }

    function resetCreateDialog() {
        if (creating) {
            return;
        }
        setCreateOpen(false);
        setCreatedAccount(null);
        setUsername("");
        setDisplayName("");
    }

    async function createLocalAccount(event: FormEvent<HTMLFormElement>) {
        event.preventDefault();
        setCreating(true);
        try {
            const result = await request<{ account: { id: number; qualified_name: string; display_name: string }; setup_token: string }>("/admin/accounts/local", "POST", {
                username,
                display_name: displayName
            });
            setCreatedAccount({ ...result.account, setup_token: result.setup_token });
            await loadUsers();
            onNotice(`Created ${result.account.qualified_name}. Copy the one-time activation link before closing.`);
        } catch (error) {
            onError((error as Error).message);
        } finally {
            setCreating(false);
        }
    }

    function activationLink(token: string) {
        const activationURL = new URL(window.location.href);
        activationURL.pathname = "/";
        activationURL.search = "";
        activationURL.hash = new URLSearchParams({ activation: token }).toString();
        return activationURL.toString();
    }

    async function copyActivationLink() {
        if (!createdAccount) {
            return;
        }
        try {
            await navigator.clipboard.writeText(activationLink(createdAccount.setup_token));
            onNotice("One-time activation link copied.");
        } catch {
            onError("Could not copy the activation link. Select and copy it manually.");
        }
    }

    return (
        <section className="user-directory" aria-label="Organesson users">
            <div className="settings-section-heading">
                <div><p className="eyebrow">Identity directory</p><h2>Users</h2></div>
                <div className="user-directory-actions">
                    <button className="secondary-action" type="button" onClick={() => setCreateOpen(true)}><Plus size={15} />Create user</button>
                    <button className="icon-button" type="button" aria-label="Refresh users" onClick={loadUsers} disabled={loading}><RefreshCw size={15} /></button>
                </div>
            </div>
            <p className="settings-description">Each Organesson account stays local to this platform. Identity providers are listed as sign-in sources. Deployment creators can create deployments and manage only the ones they create.</p>
            {loading ? <p className="settings-empty">Loading users…</p> : users.length === 0 ? <p className="settings-empty">No users.</p> : (
                <div className="user-list">
                    {users.map((user) => <article className="user-row" key={user.id}>
                        <span className="panel-icon"><UserRound size={17} /></span>
                        <div className="user-primary"><strong>{user.display_name}</strong><div className="user-identities">{user.identities.map((identity) => <span key={`${identity.realm}:${identity.qualified_name}`}><span className={`source-dot source-${identity.kind}`} />{identity.qualified_name}<small>{identity.kind}</small></span>)}</div></div>
                        {user.platform_administrator && <span className="admin-user-badge"><ShieldCheck size={14} />Administrator</span>}
                        <label className="admin-user-toggle"><input type="checkbox" checked={user.platform_administrator} disabled={savingUserID !== null || user.disabled} onChange={(event) => setAdministrator(user, event.target.checked)} />Platform administrator</label>
                        <label className="admin-user-toggle"><input type="checkbox" checked={user.can_create_deployments} disabled={savingUserID !== null || user.disabled || user.platform_administrator} onChange={(event) => setDeploymentCreation(user, event.target.checked)} />Create deployments</label>
                        {user.identities.some((identity) => identity.kind === "local") && <div className="user-password-actions">
                            <button className="secondary-action" type="button" disabled={savingUserID !== null || user.disabled} onClick={() => createPasswordResetLink(user)}>Reset password</button>
                            {resetLinks[user.id] && <div className="user-reset-link">
                                <a href={resetLinks[user.id]} target="_blank" rel="noopener noreferrer">Open reset link</a>
                                <input aria-label={`${user.display_name} one-time password reset link`} readOnly value={resetLinks[user.id]} onFocus={(event) => event.currentTarget.select()} />
                                <button className="icon-button" type="button" aria-label={`Copy ${user.display_name} password reset link`} onClick={() => copyPasswordResetLink(user)}><Copy size={14} /></button>
                            </div>}
                        </div>}
                    </article>)}
                </div>
            )}
            <dialog className="realm-form-dialog local-account-dialog" ref={createDialog} onClose={resetCreateDialog} onCancel={(event) => {
                if (creating) event.preventDefault();
            }} onClick={(event) => {
                if (event.target === event.currentTarget) closeCreateDialog();
            }}>
                <form className="realm-form" onSubmit={createLocalAccount}>
                    <div className="realm-form-heading"><div><p className="eyebrow">Identity directory</p><h3>{createdAccount ? "User created" : "Create local user"}</h3></div><button className="icon-button" type="button" aria-label="Close" onClick={closeCreateDialog} disabled={creating}><X size={16} /></button></div>
                    {createdAccount ? <>
                        <p className="settings-description">{createdAccount.qualified_name} can activate their account and choose a password with this one-time link. It will not be shown again after this dialog closes.</p>
                        <label className="activation-link-field">Activation link<input readOnly value={activationLink(createdAccount.setup_token)} onFocus={(event) => event.currentTarget.select()} /></label>
                        <div className="realm-form-footer"><span /><div><button className="secondary-action" type="button" onClick={copyActivationLink}><Copy size={14} />Copy link</button><button className="primary-action" type="button" onClick={closeCreateDialog}>Done</button></div></div>
                    </> : <>
                        <div className="realm-form-grid local-account-grid">
                            <label>Username<input autoComplete="off" value={username} onChange={(event) => setUsername(event.target.value)} placeholder="jane.smith" required /></label>
                            <label>Display name<input autoComplete="name" value={displayName} onChange={(event) => setDisplayName(event.target.value)} placeholder="Jane Smith" required /></label>
                        </div>
                        <p className="settings-description">The account will use the local Organesson realm. The user sets their own password using a one-time activation link.</p>
                        <div className="realm-form-footer"><span /><div><button className="secondary-action" type="button" onClick={closeCreateDialog} disabled={creating}>Cancel</button><button className="primary-action" type="submit" disabled={creating}>{creating ? "Creating…" : "Create user"}</button></div></div>
                    </>}
                </form>
            </dialog>
        </section>
    );
}
