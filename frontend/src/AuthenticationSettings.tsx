import { useEffect, useRef, useState, type FormEvent } from "react";
import { Check, Pencil, Plus, RefreshCw, Server, ShieldCheck, X } from "lucide-react";
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
    skip_certificate_verification: boolean;
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
    ca_certificate_pem: "",
    skip_certificate_verification: false
};

// Lists and configures local or LDAP login realms without returning stored credentials.
export function AuthenticationSettings({ request, onError, onNotice }: Props) {
    const [realms, setRealms] = useState<Realm[]>([]);
    const [encryptionReady, setEncryptionReady] = useState(false);
    const [form, setForm] = useState<RealmForm>(newRealm);
    const [loading, setLoading] = useState(true);
    const [saving, setSaving] = useState(false);
    const [formOpen, setFormOpen] = useState(false);
    const [editingAlias, setEditingAlias] = useState<string | null>(null);
    const formDialog = useRef<HTMLDialogElement>(null);

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

    useEffect(() => {
        const dialog = formDialog.current;
        if (formOpen && dialog && !dialog.open) {
            dialog.showModal();
        } else if (!formOpen && dialog?.open) {
            dialog.close();
        }
    }, [formOpen]);

    function closeForm() {
        setFormOpen(false);
        setEditingAlias(null);
        setForm(newRealm);
    }

    function openCreateForm() {
        setEditingAlias(null);
        setForm(newRealm);
        setFormOpen(true);
    }

    function openEditForm(realm: Realm) {
        if (!realm.configuration) return;
        setEditingAlias(realm.alias);
        setForm({
            ...newRealm,
            ...realm.configuration,
            alias: realm.alias,
            enabled: realm.enabled,
            email_attribute: realm.configuration.email_attribute ?? "",
            ca_certificate_pem: realm.configuration.ca_certificate_pem ?? "",
            bind_password: ""
        });
        setFormOpen(true);
    }

    async function saveRealm(event: FormEvent<HTMLFormElement>) {
        event.preventDefault();
        const isEditing = editingAlias !== null;
        setSaving(true);
        try {
            if (editingAlias === null) {
                await request("/auth/admin/realms/ldap", "POST", form);
            } else {
                await request(`/auth/admin/realms/ldap/${encodeURIComponent(editingAlias)}`, "PUT", form);
            }
            closeForm();
            await loadRealms();
            onNotice(isEditing ? "LDAP realm updated." : "LDAP realm added.");
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
                <h2>Authentication</h2>
                <div className="auth-toolbar">
                    <button className="secondary-action" type="button" onClick={openCreateForm} disabled={!encryptionReady}><Plus size={15} />Add LDAP realm</button>
                    <button className="icon-button" type="button" aria-label="Refresh authentication realms" onClick={loadRealms} disabled={loading}><RefreshCw size={15} /></button>
                </div>
            </div>
            {!encryptionReady && <p className="realm-warning" role="note">Set <code>authentication.encryption_key</code> in <code>backend/config.toml</code> to add LDAP realms.</p>}
            {loading ? <p className="settings-empty">Loading…</p> : <div className="realm-list">
                <div className="realm-list-heading"><span>Realm</span><span>Source</span><span>Status</span><span>Actions</span></div>
                {realms.map((realm) => <article className="realm-card" key={realm.alias}>
                    <div className="realm-card-heading">
                        <span className="panel-icon">{realm.kind === "local" ? <ShieldCheck size={17} /> : <Server size={17} />}</span>
                        <div className="realm-name"><h3>{realm.alias}</h3><p>{realm.kind === "local" ? "Local accounts" : realm.configuration?.url ?? "LDAP"}</p></div>
                        <span className="realm-source">{realm.kind === "local" ? "Local" : "LDAP"}</span>
                        <span className={`realm-state${realm.enabled ? " is-enabled" : ""}`}>{realm.enabled ? "Enabled" : "Disabled"}</span>
                        <div className="realm-actions">
                            {realm.kind === "ldap" && <button className="secondary-action" type="button" disabled={saving} onClick={() => testRealm(realm)}><Check size={14} />Test</button>}
                            {realm.kind === "ldap" && <button className="icon-button" type="button" aria-label={`Edit ${realm.alias} realm`} title="Edit realm" disabled={saving} onClick={() => openEditForm(realm)}><Pencil size={14} /></button>}
                            <button className="text-action" type="button" disabled={saving} onClick={() => setEnabled(realm, !realm.enabled)}>{realm.enabled ? "Disable" : "Enable"}</button>
                        </div>
                    </div>
                </article>)}
                {realms.length === 0 && <p className="settings-empty">No authentication realms configured.</p>}
            </div>}

            <dialog className="realm-form-dialog" ref={formDialog} onClose={closeForm} onClick={(event) => {
                if (event.target === event.currentTarget) closeForm();
            }}>
                <form className="realm-form" onSubmit={saveRealm}>
                    <div className="realm-form-heading"><div><p className="eyebrow">Authentication</p><h3>{editingAlias === null ? "Add LDAP realm" : `Edit ${editingAlias}`}</h3></div><button className="icon-button" type="button" aria-label="Close" onClick={closeForm}><X size={16} /></button></div>
                    <div className="realm-form-grid">
                        <label>Realm name<input autoComplete="off" value={form.alias} onChange={(event) => updateForm("alias", event.target.value)} placeholder="cyber" readOnly={editingAlias !== null} required /></label>
                        <label>LDAP URL<input value={form.url} onChange={(event) => updateForm("url", event.target.value)} placeholder="ldaps://ipa.example.org:636" required /></label>
                        <label>Base DN<input value={form.base_dn} onChange={(event) => updateForm("base_dn", event.target.value)} placeholder="cn=users,dc=example,dc=org" required /></label>
                        <label>User search filter<input value={form.user_filter} onChange={(event) => updateForm("user_filter", event.target.value)} placeholder="(uid={username})" required /></label>
                        <label>Username attribute<input value={form.username_attribute} onChange={(event) => updateForm("username_attribute", event.target.value)} required /></label>
                        <label>Display name attribute<input value={form.display_name_attribute} onChange={(event) => updateForm("display_name_attribute", event.target.value)} required /></label>
                        <label>Email attribute<input value={form.email_attribute} onChange={(event) => updateForm("email_attribute", event.target.value)} /></label>
                        <label>Service bind DN<input value={form.bind_dn} onChange={(event) => updateForm("bind_dn", event.target.value)} required /></label>
                        <label>{editingAlias === null ? "Service bind password" : "New service bind password (optional)"}<input autoComplete="new-password" type="password" value={form.bind_password} onChange={(event) => updateForm("bind_password", event.target.value)} required={editingAlias === null} /></label>
                        <label className="realm-tls-option"><span><input type="checkbox" checked={form.skip_certificate_verification} onChange={(event) => updateForm("skip_certificate_verification", event.target.checked)} />Skip certificate verification</span><small>Use only for lab certificates. LDAP traffic remains encrypted.</small></label>
                        <label className="realm-ca-field">Custom CA certificate<textarea rows={4} value={form.ca_certificate_pem} onChange={(event) => updateForm("ca_certificate_pem", event.target.value)} placeholder="Optional; system trust is used otherwise." /></label>
                    </div>
                    <div className="realm-form-footer"><label className="realm-enabled"><input type="checkbox" checked={form.enabled} onChange={(event) => updateForm("enabled", event.target.checked)} />{editingAlias === null ? "Enable after adding" : "Enabled"}</label><div><button className="secondary-action" type="button" onClick={closeForm} disabled={saving}>Cancel</button><button className="primary-action" type="submit" disabled={saving || (editingAlias === null && !encryptionReady)}>{saving ? "Saving…" : editingAlias === null ? "Add realm" : "Save changes"}</button></div></div>
                </form>
            </dialog>
        </section>
    );
}
