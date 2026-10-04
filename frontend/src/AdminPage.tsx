import { useState } from "react";
import { Copy, KeyRound } from "lucide-react";
import { TemplateCatalog } from "./TemplateCatalog";
import type { ApiRequest } from "./api";
import { ProxmoxResourcePolicy } from "./ProxmoxResourcePolicy";
import { AuthenticationSettings } from "./AuthenticationSettings";
import { UserDirectory } from "./UserDirectory";

type Props = {
    request: ApiRequest;
    onError: (message: string) => void;
    onNotice: (message: string) => void;
};

// Groups platform-wide configuration and credential management away from deployments.
export function AdminPage({ request, onError, onNotice }: Props) {
    const [section, setSection] = useState<"capacity" | "networks" | "templates" | "authentication" | "users" | "access">("capacity");
    const [token, setToken] = useState("");
    const [tokenId, setTokenId] = useState<number | null>(null);
    const [busy, setBusy] = useState(false);

    async function createProviderToken() {
        setBusy(true);
        try {
            const result = await request<{ id: number; token: string }>("/auth/api-tokens", "POST", {
                name: "OpenTofu provider",
                lifetime_days: 30
            });
            setToken(result.token);
            setTokenId(result.id);
            onNotice("Copy this token now. Organesson will not show it again.");
        } catch (requestError) {
            onError((requestError as Error).message);
        } finally {
            setBusy(false);
        }
    }

    async function revokeProviderToken() {
        if (tokenId === null) {
            return;
        }
        setBusy(true);
        try {
            await request(`/auth/api-tokens/${tokenId}`, "DELETE", {});
            setToken("");
            setTokenId(null);
            onNotice("Provider token revoked.");
        } catch (requestError) {
            onError((requestError as Error).message);
        } finally {
            setBusy(false);
        }
    }

    async function copyToken() {
        try {
            await navigator.clipboard.writeText(token);
            onNotice("Provider token copied to clipboard.");
        } catch {
            onError("Clipboard access was unavailable. Select and copy the token manually.");
        }
    }

    return (
        <div className="admin-page">
            <div className="admin-settings-layout">
                <nav className="admin-settings-sidebar" aria-label="Administration settings">
                    <p className="eyebrow">Settings</p>
                    <button type="button" aria-current={section === "capacity" ? "page" : undefined} onClick={() => setSection("capacity")}>Quotas & placement</button>
                    <button type="button" aria-current={section === "networks" ? "page" : undefined} onClick={() => setSection("networks")}>Networks</button>
                    <button type="button" aria-current={section === "templates" ? "page" : undefined} onClick={() => setSection("templates")}>Source VMs</button>
                    <button type="button" aria-current={section === "authentication" ? "page" : undefined} onClick={() => setSection("authentication")}>Authentication</button>
                    <button type="button" aria-current={section === "users" ? "page" : undefined} onClick={() => setSection("users")}>Users</button>
                    <button type="button" aria-current={section === "access" ? "page" : undefined} onClick={() => setSection("access")}>Provider access</button>
                </nav>
                <div className="admin-settings-content">
                    {(section === "capacity" || section === "networks") && <ProxmoxResourcePolicy request={request} section={section} onError={onError} onNotice={onNotice} />}
                    {section === "templates" && <TemplateCatalog request={request} onError={onError} onNotice={onNotice} />}
                    {section === "authentication" && <AuthenticationSettings request={request} onError={onError} onNotice={onNotice} />}
                    {section === "users" && <UserDirectory request={request} onError={onError} onNotice={onNotice} />}
                    {section === "access" && <section className="panel token-panel" aria-labelledby="provider-token-heading">
                <div className="token-copy"><span className="panel-icon"><KeyRound size={17} /></span><div><p className="eyebrow">OpenTofu access</p><h2 id="provider-token-heading">Provider API token</h2><p>Create a short-lived token for the local provider smoke example. It belongs to your account and can be revoked by signing in again.</p></div></div>
                <button className="primary-action" type="button" disabled={busy || tokenId !== null} onClick={createProviderToken}>{busy ? "Creating…" : tokenId !== null ? "Token created in this tab" : "Create 30-day token"}</button>
                {tokenId !== null && <div className="token-result">{token && <code>{token}</code>}<div className="token-actions">{token && <button className="secondary-action" type="button" onClick={copyToken}><Copy size={15} /> Copy token</button>}<button className="secondary-action" type="button" disabled={busy} onClick={revokeProviderToken}>Revoke token</button></div></div>}
                    </section>}
                </div>
            </div>
        </div>
    );
}
