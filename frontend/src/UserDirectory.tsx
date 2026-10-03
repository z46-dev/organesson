import { useEffect, useState } from "react";
import { RefreshCw, ShieldCheck, UserRound } from "lucide-react";
import type { ApiRequest } from "./api";

type UserIdentity = { qualified_name: string; realm: string; kind: string };
type UserRecord = { id: number; display_name: string; platform_administrator: boolean; disabled: boolean; identities: UserIdentity[] };
type Props = { request: ApiRequest; onError: (message: string) => void; onNotice: (message: string) => void };

// Shows local Organesson users and the identity sources linked to each account.
export function UserDirectory({ request, onError, onNotice }: Props) {
    const [users, setUsers] = useState<UserRecord[]>([]);
    const [loading, setLoading] = useState(true);
    const [savingUserID, setSavingUserID] = useState<number | null>(null);

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

    return (
        <section className="user-directory" aria-label="Organesson users">
            <div className="settings-section-heading">
                <div><p className="eyebrow">Identity directory</p><h2>Users</h2></div>
                <button className="icon-button" type="button" aria-label="Refresh users" onClick={loadUsers} disabled={loading}><RefreshCw size={15} /></button>
            </div>
            <p className="settings-description">Each Organesson account stays local to this platform. Identity providers are listed as sign-in sources.</p>
            {loading ? <p className="settings-empty">Loading users…</p> : users.length === 0 ? <p className="settings-empty">No users.</p> : (
                <div className="user-list">
                    {users.map((user) => <article className="user-row" key={user.id}>
                        <span className="panel-icon"><UserRound size={17} /></span>
                        <div className="user-primary"><strong>{user.display_name}</strong><div className="user-identities">{user.identities.map((identity) => <span key={`${identity.realm}:${identity.qualified_name}`}><span className={`source-dot source-${identity.kind}`} />{identity.qualified_name}<small>{identity.kind}</small></span>)}</div></div>
                        {user.platform_administrator && <span className="admin-user-badge"><ShieldCheck size={14} />Administrator</span>}
                        <label className="admin-user-toggle"><input type="checkbox" checked={user.platform_administrator} disabled={savingUserID !== null || user.disabled} onChange={(event) => setAdministrator(user, event.target.checked)} />Platform administrator</label>
                    </article>)}
                </div>
            )}
        </section>
    );
}
