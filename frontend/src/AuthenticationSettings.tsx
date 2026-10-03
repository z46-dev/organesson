import { useEffect, useState, type FormEvent } from "react";
import { Check, RefreshCw, Server, ShieldCheck } from "lucide-react";
import type { ApiRequest } from "./api";

type LDAPConfiguration = {
    url: string;
    base_dn: string;
    user_filter: string;
    username_attribute: string;
    display_name_attribute: string;
    email_attribute?: string;
    bind_dn: string;
    ca_certificate_pem?: string;
};

type Realm = {
    alias: string;
    kind: "local" | "ldap";
    enabled: boolean;
    system_managed: boolean;
    has_bind_password: boolean;
    configuration?: LDAPConfiguration;
};

type RealmForm = LDAPConfiguration & { alias: string; enabled: boolean; bind_password: string };

type Props = { request: ApiRequest; onError: (message: string) => void; onNotice: (message: string) => void };

const newRealm: RealmForm = {
    alias: "",
    enabled: true,
    url: "ldaps://directory.example.org:636",
    base_dn: "",
    user_filter: "(uid={username})",
    username_attribute: "uid",
    display_name_attribute: "cn",
    email_attribute: "mail",
    bind_dn: "",
    bind_password: "",
    ca_certificate_pem: ""
};

// Lists and configures local or LDAP login realms without returning stored credentials.
export function AuthenticationSettings({ request, onError, onNotice }: Props) {
    const [realms, setRealms] = useState<Realm[]>([]);
    const [encryptionReady, setEncryptionReady] = useState(false);
    const [form, setForm] = useState<RealmForm>(newRealm);
    const [loading, setLoading] = useState(true);
    const [saving, setSaving] = useState(false);

    async function loadRealms() {
        setLoading(true);
        try {
            const result = await request<{ realms: Realm[]; encryption_ready: boolean }>("/auth/admin/realms");
            setRealms(result.realms ?? []);
            setEncryptionReady(result.encryption_ready);
        } catch (error) {
            onError((error as Error).message);
        } finally {
            setLoading(false);
        }
    }

    useEffect(() => {
        loadRealms();
    }, [request, onError]);

    async function saveRealm(event: FormEvent<HTMLFormElement>) {
        event.preventDefault();
        setSaving(true);
        try {
            await request("/auth/admin/realms/ldap", "POST", form);
            setForm(newRealm);
            await loadRealms();
            onNotice("LDAP realm added.");
        } catch (error) {
            onError((error as Error).message);
        } finally {
            setSaving(false);
        }
    }

    async function setEnabled(realm: Realm, enabled: boolean) {
        setSaving(true);
        try {
            if (realm.kind === "local") {
                await request("/auth/admin/realms/local", "PUT", { enabled });
            } else if (realm.configuration) {
                await request(`/auth/admin/realms/ldap/${encodeURIComponent(realm.alias)}`, "PUT", {
                    alias: realm.alias,
                    enabled,
                    ...realm.configuration
                });
            }
            await loadRealms();
            onNotice(`${realm.alias} realm ${enabled ? "enabled" : "disabled"}.`);
        } catch (error) {
            onError((error as Error).message);
        } finally {
            setSaving(false);
        }
    }

    async function testRealm(realm: Realm) {
        setSaving(true);
        try {
            await request(`/auth/admin/realms/ldap/${encodeURIComponent(realm.alias)}/test`, "POST", {});
            onNotice(`${realm.alias} LDAP connection passed.`);
        } catch (error) {
            onError((error as Error).message);
        } finally {
            setSaving(false);
        }
    }

    function updateForm(field: keyof RealmForm, value: string | boolean) {
        setForm((current) => ({ ...current, [field]: value }));
    }

    return (
        <section className="auth-settings" aria-label="Authentication realms">
            <div className="settings-section-heading">
                <div><p className="eyebrow">Identity</p><h2>Authentication realms</h2></div>
                <button className="icon-button" type="button" aria-label="Refresh authentication realms" onClick={loadRealms} disabled={loading}><RefreshCw size={15} /></button>
            </div>
            <p className="settings-description">Choose which identity sources can sign in. Directory users are mapped into Organesson the first time they authenticate.</p>
            {loading ? <p className="settings-empty">Loading realms…</p> : <div className="realm-list">
                {realms.map((realm) => <article className="realm-card" key={realm.alias}>
                    <div className="realm-card-heading">
                        <span className="panel-icon">{realm.kind === "local" ? <ShieldCheck size={17} /> : <Server size={17} />}</span>
                        <div><h3>{realm.alias}</h3><p>{realm.kind === "local" ? "Organesson local passwords" : `${realm.configuration?.url ?? "LDAP"} · ${realm.configuration?.base_dn ?? ""}`}</p></div>
                        <span className={`realm-state${realm.enabled ? " is-enabled" : ""}`}>{realm.enabled ? "Enabled" : "Disabled"}</span>
                    </div>
                    {realm.kind === "ldap" && <div className="realm-card-actions">
                        <button className="secondary-action" type="button" disabled={saving} onClick={() => testRealm(realm)}><Check size={14} />Test connection</button>
                        <button className="text-action" type="button" disabled={saving} onClick={() => setEnabled(realm, !realm.enabled)}>{realm.enabled ? "Disable" : "Enable"}</button>
                        {realm.has_bind_password && <span>Bind credential stored</span>}
                    </div>}
                    {realm.kind === "local" && <div className="realm-card-actions"><span>Local users and passwords are managed separately.</span><button className="text-action" type="button" disabled={saving} onClick={() => setEnabled(realm, !realm.enabled)}>{realm.enabled ? "Disable local login" : "Enable local login"}</button></div>}
                </article>)}
            </div>}

            <form className="panel realm-form" onSubmit={saveRealm}>
                <div><p className="eyebrow">Add identity source</p><h3>LDAP realm</h3></div>
                {!encryptionReady && <p className="realm-warning" role="note">Set <code>ORGANESSON_AUTH_ENCRYPTION_KEY</code> before adding a realm. Use a base64-encoded 32-byte key and keep it stable across restarts.</p>}
                <div className="realm-form-grid">
                    <label>Realm name<input autoComplete="off" value={form.alias} onChange={(event) => updateForm("alias", event.target.value)} placeholder="cyber" required /></label>
                    <label>LDAP URL<input value={form.url} onChange={(event) => updateForm("url", event.target.value)} placeholder="ldaps://ipa.example.org:636" required /></label>
                    <label>Base DN<input value={form.base_dn} onChange={(event) => updateForm("base_dn", event.target.value)} placeholder="cn=users,dc=example,dc=org" required /></label>
                    <label>User search filter<input value={form.user_filter} onChange={(event) => updateForm("user_filter", event.target.value)} placeholder="(uid={username})" required /></label>
                    <label>Username attribute<input value={form.username_attribute} onChange={(event) => updateForm("username_attribute", event.target.value)} required /></label>
                    <label>Display name attribute<input value={form.display_name_attribute} onChange={(event) => updateForm("display_name_attribute", event.target.value)} required /></label>
                    <label>Email attribute<input value={form.email_attribute} onChange={(event) => updateForm("email_attribute", event.target.value)} /></label>
                    <label>Service bind DN<input value={form.bind_dn} onChange={(event) => updateForm("bind_dn", event.target.value)} required /></label>
                    <label>Service bind password<input autoComplete="new-password" type="password" value={form.bind_password} onChange={(event) => updateForm("bind_password", event.target.value)} required /></label>
                    <label className="realm-ca-field">Custom CA certificate (PEM)<textarea rows={4} value={form.ca_certificate_pem} onChange={(event) => updateForm("ca_certificate_pem", event.target.value)} placeholder="Optional; system trust is used otherwise." /></label>
                </div>
                <div className="realm-form-footer"><label className="realm-enabled"><input type="checkbox" checked={form.enabled} onChange={(event) => updateForm("enabled", event.target.checked)} />Enable after adding</label><button className="primary-action" type="submit" disabled={saving || !encryptionReady}>{saving ? "Saving…" : "Add LDAP realm"}</button></div>
            </form>
        </section>
    );
}
