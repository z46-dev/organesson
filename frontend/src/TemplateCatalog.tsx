import { useCallback, useEffect, useState, type FormEvent } from "react";
import { Check, CircleAlert, Pencil, Plus, RefreshCw, ShieldCheck, Trash2 } from "lucide-react";
import "./template-catalog.css";

type VMTemplate = {
    id: number;
    display_name: string;
    description: string;
    source_platform: string;
    source_id: string;
    guest_os: string;
    guest_os_version: string;
    edition: string;
    architecture: string;
    execution_method: string;
    provisioning_ready: boolean;
    guest_agent_root_verified: boolean;
    provisioning_account_removed: boolean;
    last_preflight_at: string | null;
    last_preflight_json: string;
};

type VMTemplateAlias = {
    id: number;
    vm_template_id: number;
    alias: string;
};

type VMTemplateRecord = {
    template: VMTemplate;
    aliases: VMTemplateAlias[];
};

type PreflightCheck = {
    name: string;
    passed: boolean;
    required: boolean;
    details: string;
};

type PreflightResult = {
    passed: boolean;
    checks: PreflightCheck[];
    guest_os_id?: string;
    guest_os_name?: string;
    agent_reachable?: boolean;
    guest_agent_root_verified?: boolean;
    power_state?: string;
    checked_at: string;
};

type TemplateForm = {
    display_name: string;
    description: string;
    source_id: string;
    guest_os: string;
    guest_os_version: string;
    edition: string;
    architecture: string;
};

type Props = {
    request: <T>(path: string, method?: string, body?: unknown) => Promise<T>;
    onError: (message: string) => void;
    onNotice: (message: string) => void;
};

const emptyForm: TemplateForm = {
    display_name: "",
    description: "",
    source_id: "",
    guest_os: "fedora",
    guest_os_version: "",
    edition: "server",
    architecture: "x86_64"
};

function preflightFor(template: VMTemplate): PreflightResult | null {
    try {
        const result = JSON.parse(template.last_preflight_json) as PreflightResult;
        return result.checked_at ? result : null;
    } catch {
        return null;
    }
}

export function TemplateCatalog({ request, onError, onNotice }: Props) {
    const [templates, setTemplates] = useState<VMTemplateRecord[]>([]);
    const [form, setForm] = useState<TemplateForm>(emptyForm);
    const [aliasesText, setAliasesText] = useState("");
    const [editingId, setEditingId] = useState<number | null>(null);
    const [aliasDrafts, setAliasDrafts] = useState<Record<number, string>>({});
    const [rootVerified, setRootVerified] = useState<Record<number, boolean>>({});
    const [accountRemoved, setAccountRemoved] = useState<Record<number, boolean>>({});
    const [proxmoxConfigured, setProxmoxConfigured] = useState(false);
    const [insecureTLS, setInsecureTLS] = useState(false);
    const [busy, setBusy] = useState(false);

    const refresh = useCallback(async () => {
        const [catalog, connection] = await Promise.all([
            request<{ templates: VMTemplateRecord[] }>("/admin/vm-templates"),
            request<{ configured: boolean; insecure_tls: boolean }>("/admin/proxmox/status")
        ]);
        setTemplates(catalog.templates ?? []);
        setRootVerified(Object.fromEntries((catalog.templates ?? []).map(({ template }) => [template.id, template.guest_agent_root_verified])));
        setAccountRemoved(Object.fromEntries((catalog.templates ?? []).map(({ template }) => [template.id, template.provisioning_account_removed])));
        setProxmoxConfigured(connection.configured);
        setInsecureTLS(connection.insecure_tls);
    }, [request]);

    useEffect(() => {
        refresh().catch((error: Error) => onError(error.message));
    }, [refresh, onError]);

    function changeForm(field: keyof TemplateForm, value: string) {
        setForm((current) => ({ ...current, [field]: value }));
    }

    async function saveTemplate(event: FormEvent<HTMLFormElement>) {
        event.preventDefault();
        setBusy(true);
        onError("");
        try {
            if (editingId === null) {
                await request("/admin/vm-templates", "POST", {
                    ...form,
                    execution_method: "qemu_guest_agent",
                    aliases: aliasesText.split(",").map((alias) => alias.trim()).filter(Boolean)
                });
                setForm(emptyForm);
                setAliasesText("");
                onNotice("Source VM added to the catalog. It is not ready for provisioning yet.");
            } else {
                await request(`/admin/vm-templates/${editingId}`, "PUT", {
                    ...form,
                    execution_method: "qemu_guest_agent"
                });
                setEditingId(null);
                setForm(emptyForm);
                onNotice("Template metadata saved. Preflight and readiness checks need to be repeated.");
            }
            await refresh();
        } catch (error) {
            onError((error as Error).message);
        } finally {
            setBusy(false);
        }
    }

    function startEditing(record: VMTemplateRecord) {
        setEditingId(record.template.id);
        setForm({
            display_name: record.template.display_name,
            description: record.template.description,
            source_id: record.template.source_id,
            guest_os: record.template.guest_os,
            guest_os_version: record.template.guest_os_version,
            edition: record.template.edition,
            architecture: record.template.architecture
        });
    }

    async function addAlias(templateId: number) {
        const alias = aliasDrafts[templateId]?.trim();
        if (!alias) {
            return;
        }
        setBusy(true);
        onError("");
        try {
            await request(`/admin/vm-templates/${templateId}/aliases`, "POST", { alias });
            setAliasDrafts((current) => ({ ...current, [templateId]: "" }));
            await refresh();
            onNotice("Template alias added.");
        } catch (error) {
            onError((error as Error).message);
        } finally {
            setBusy(false);
        }
    }

    async function removeAlias(templateId: number, aliasId: number) {
        setBusy(true);
        onError("");
        try {
            await request(`/admin/vm-templates/${templateId}/aliases/${aliasId}`, "DELETE");
            await refresh();
            onNotice("Template alias removed.");
        } catch (error) {
            onError((error as Error).message);
        } finally {
            setBusy(false);
        }
    }

    async function runPreflight(templateId: number) {
        setBusy(true);
        onError("");
        try {
            const result = await request<{ preflight: PreflightResult }>(`/admin/vm-templates/${templateId}/preflight`, "POST", {});
            await refresh();
            onNotice(result.preflight.passed ? "Source checks passed. Complete the remaining readiness check." : "Source checks found issues that need attention.");
        } catch (error) {
            onError((error as Error).message);
        } finally {
            setBusy(false);
        }
    }

    async function updateReadiness(templateId: number) {
        setBusy(true);
        onError("");
        try {
            await request(`/admin/vm-templates/${templateId}/readiness`, "PUT", {
                guest_agent_root_verified: rootVerified[templateId] === true,
                provisioning_account_removed: accountRemoved[templateId] === true
            });
            await refresh();
            onNotice("Template readiness saved.");
        } catch (error) {
            onError((error as Error).message);
        } finally {
            setBusy(false);
        }
    }

    return (
        <section className="panel template-catalog" aria-labelledby="template-catalog-heading">
            <div className="template-catalog-heading">
                <div>
                    <p className="eyebrow">Platform administration</p>
                    <h2 id="template-catalog-heading">Source VM catalog</h2>
                    <p className="template-catalog-intro">Register ordinary Proxmox VMs and give each source one or more stable selectors for OpenTofu.</p>
                </div>
                <button className="icon-button" type="button" onClick={() => refresh().catch((error: Error) => onError(error.message))} aria-label="Refresh source VM catalog"><RefreshCw size={16} /></button>
            </div>

            <div className={`connection-note${proxmoxConfigured && !insecureTLS ? " is-connected" : ""}`}>
                {proxmoxConfigured && !insecureTLS ? <ShieldCheck size={17} /> : <CircleAlert size={17} />}
                <span>{!proxmoxConfigured
                    ? "Proxmox preflight is not configured. Add the API URL, token ID, and token secret to the backend config.toml, then restart the backend."
                    : insecureTLS
                        ? "Warning: Proxmox TLS certificate verification is disabled. Use only for isolated testing; a server impersonator could intercept credentials."
                        : "Proxmox read-only preflight is configured with TLS certificate verification."}</span>
            </div>

            <form className="template-form" onSubmit={saveTemplate}>
                <div className="template-form-heading">
                    <h3>{editingId === null ? "Register a source VM" : "Edit source metadata"}</h3>
                    {editingId !== null && <button className="text-action" type="button" onClick={() => { setEditingId(null); setForm(emptyForm); }}>Cancel</button>}
                </div>
                <div className="template-fields">
                    <label>Display name<input value={form.display_name} onChange={(event) => changeForm("display_name", event.target.value)} required maxLength={128} placeholder="Fedora Workstation" /></label>
                    <label>Proxmox VMID<input inputMode="numeric" value={form.source_id} onChange={(event) => changeForm("source_id", event.target.value)} required placeholder="156" /></label>
                    <label>Guest OS ID<input value={form.guest_os} onChange={(event) => changeForm("guest_os", event.target.value)} required placeholder="fedora" /></label>
                    <label>OS version<input value={form.guest_os_version} onChange={(event) => changeForm("guest_os_version", event.target.value)} required placeholder="44" /></label>
                    <label>Edition<input value={form.edition} onChange={(event) => changeForm("edition", event.target.value)} required placeholder="workstation or server" /></label>
                    <label>Architecture<select value={form.architecture} onChange={(event) => changeForm("architecture", event.target.value)}><option value="x86_64">x86_64</option><option value="aarch64">aarch64</option></select></label>
                    <label className="template-description">Description<textarea value={form.description} onChange={(event) => changeForm("description", event.target.value)} maxLength={2048} rows={2} /></label>
                    {editingId === null && <label className="template-alias-entry">Aliases<input value={aliasesText} onChange={(event) => setAliasesText(event.target.value)} required placeholder="og-template-fedora-workstation-latest, fedora-workstation" /><small>Comma-separated. Each alias must be unique.</small></label>}
                </div>
                <div className="template-form-actions"><button className="primary-action" type="submit" disabled={busy}><Plus size={15} />{busy ? "Saving…" : editingId === null ? "Add source VM" : "Save metadata"}</button></div>
            </form>

            {templates.length === 0 ? <div className="template-empty">No source VMs are registered yet. Add VMID 156 for Fedora Workstation and VMID 157 for Fedora Server.</div> : (
                <div className="template-list">
                    {templates.map((record) => {
                        const { template } = record;
                        const preflight = preflightFor(template);
                        const rootCheck = preflight?.checks?.find((check) => check.name === "guest_agent_root_execution");
                        const linuxRootCheck = !template.guest_os.toLowerCase().includes("windows");
                        const canConfirmReadiness = preflight?.passed && preflight.power_state === "running" && preflight.agent_reachable;
                        return (
                            <article className="template-card" key={template.id}>
                                <div className="template-card-heading">
                                    <div><p className="eyebrow">{template.guest_os} {template.guest_os_version} · {template.edition}</p><h3>{template.display_name}</h3><p className="template-source-id">Proxmox VMID {template.source_id} · {template.architecture}</p></div>
                                    <span className={`template-status${template.provisioning_ready ? " is-ready" : ""}`}>{template.provisioning_ready ? <><Check size={13} /> Ready</> : "Not ready"}</span>
                                </div>
                                {template.description && <p className="template-description-copy">{template.description}</p>}
                                <div className="template-aliases" aria-label="Template aliases">
                                    {record.aliases.map((alias) => <span className="template-alias" key={alias.id}><code>{alias.alias}</code><button type="button" disabled={busy || record.aliases.length < 2} onClick={() => removeAlias(template.id, alias.id)} aria-label={`Remove alias ${alias.alias}`} title={record.aliases.length < 2 ? "Every source needs at least one alias" : "Remove alias"}><Trash2 size={13} /></button></span>)}
                                </div>
                                <div className="template-alias-add"><input aria-label={`New alias for ${template.display_name}`} value={aliasDrafts[template.id] ?? ""} onChange={(event) => setAliasDrafts((current) => ({ ...current, [template.id]: event.target.value }))} placeholder="Add another alias" /><button className="secondary-action" type="button" disabled={busy || !aliasDrafts[template.id]?.trim()} onClick={() => addAlias(template.id)}><Plus size={14} /> Add alias</button></div>
                                <div className="template-card-actions"><button className="secondary-action" type="button" disabled={busy} onClick={() => startEditing(record)}><Pencil size={14} /> Edit metadata</button><button className="secondary-action" type="button" title="Checks VM configuration and guest OS; Linux also must pass the QEMU Guest Agent root and SELinux execution check." disabled={busy || !proxmoxConfigured} onClick={() => runPreflight(template.id)}><RefreshCw size={14} /> Check source</button></div>
                                {preflight && <div className="preflight-results"><div className="preflight-summary"><strong>{preflight.passed ? "Preflight passed" : "Preflight needs attention"}</strong><time dateTime={preflight.checked_at}>{new Date(preflight.checked_at).toLocaleString()}</time></div>{preflight.checks?.map((check) => <p className={`preflight-check${check.passed ? " is-passed" : ""}`} key={check.name}><span>{check.passed ? "✓" : check.required ? "!" : "·"}</span>{check.details}</p>)}</div>}
                                <div className="template-readiness">
                                    {linuxRootCheck ? <p className="template-readiness-status">Linux guest-agent system-level check: {rootCheck?.passed && preflight?.guest_agent_root_verified ? "verified" : "not verified"}</p> : <label><input type="checkbox" checked={rootVerified[template.id] ?? template.guest_agent_root_verified} onChange={(event) => setRootVerified((current) => ({ ...current, [template.id]: event.target.checked }))} /> I verified the guest agent executes as SYSTEM and the guest OS matches this record.</label>}
                                    <label><input type="checkbox" checked={accountRemoved[template.id] ?? template.provisioning_account_removed} onChange={(event) => setAccountRemoved((current) => ({ ...current, [template.id]: event.target.checked }))} /> The temporary provisioning account has been removed.</label>
                                    {!canConfirmReadiness && <p className="template-readiness-status">Start the source VM and rerun preflight to verify guest access.</p>}
                                    <button className="secondary-action" type="button" disabled={busy || !canConfirmReadiness || linuxRootCheck && (!rootCheck?.passed || !preflight?.guest_agent_root_verified) || !linuxRootCheck && rootVerified[template.id] !== true && !template.guest_agent_root_verified || accountRemoved[template.id] !== true && !template.provisioning_account_removed} onClick={() => updateReadiness(template.id)}><ShieldCheck size={14} /> Confirm readiness</button>
                                </div>
                            </article>
                        );
                    })}
                </div>
            )}
        </section>
    );
}
