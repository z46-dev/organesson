import { useCallback, useEffect, useRef, useState, type FormEvent } from "react";
import { Check, ChevronDown, Pencil, Plus, RefreshCw, Search, ShieldCheck, Trash2, X } from "lucide-react";
import "./template-catalog.css";

type VMTemplate = {
    id: number;
    display_name: string;
    description: string;
    source_platform: string;
    source_id: string;
    guest_os: string;
    guest_os_name: string;
    guest_os_version: string;
    edition: string;
    architecture: string;
    system_only: boolean;
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
    guest_os_version?: string;
    guest_architecture?: string;
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
    guest_os_name: string;
    guest_os_version: string;
    edition: string;
    architecture: string;
    system_only: boolean;
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
    guest_os: "linux",
    guest_os_name: "",
    guest_os_version: "",
    edition: "",
    architecture: "",
    system_only: false
};

const guestTypes = ["linux", "windows", "bsd"];
const knownLinuxDistributions = ["almalinux", "alpine", "amzn", "arch", "centos", "debian", "endeavouros", "fedora", "linuxmint", "manjaro", "ol", "opensuse", "opensuse-leap", "opensuse-tumbleweed", "oracle", "pop", "rhel", "rocky", "sled", "sles", "ubuntu"];

function preparationFamily(template: VMTemplate | undefined) {
    const guestOS = template?.guest_os.toLowerCase() ?? "";
    if (guestOS === "linux" || knownLinuxDistributions.includes(guestOS)) {
        return "linux";
    }
    if (guestOS === "windows" || guestOS.startsWith("windows-")) {
        return "windows";
    }
    if (guestOS === "bsd" || guestOS === "freebsd") {
        return "freebsd";
    }
    return "";
}

function preflightFor(template: VMTemplate): PreflightResult | null {
    try {
        const result = JSON.parse(template.last_preflight_json) as PreflightResult;
        return result.checked_at ? result : null;
    } catch {
        return null;
    }
}

function templateErrorMessage(error: unknown) {
    return error instanceof Error && error.message.trim() ? error.message : "Template operation failed.";
}

export function TemplateCatalog({ request, onError, onNotice }: Props) {
    const [templates, setTemplates] = useState<VMTemplateRecord[]>([]);
    const [form, setForm] = useState<TemplateForm>(emptyForm);
    const [aliasesText, setAliasesText] = useState("");
    const [editingId, setEditingId] = useState<number | null>(null);
    const [aliasDrafts, setAliasDrafts] = useState<Record<number, string>>({});
    const [preparingTemplateId, setPreparingTemplateId] = useState<number | null>(null);
    const [usernamesText, setUsernamesText] = useState("");
    const [proxmoxConfigured, setProxmoxConfigured] = useState(false);
    const [busy, setBusy] = useState(false);
    const [formOpen, setFormOpen] = useState(false);
    const [searchText, setSearchText] = useState("");
    const [readinessFilter, setReadinessFilter] = useState<"all" | "ready" | "not_ready">("all");
    const formDialog = useRef<HTMLDialogElement>(null);
    const prepareDialog = useRef<HTMLDialogElement>(null);

    const refresh = useCallback(async () => {
        const [catalog, connection] = await Promise.all([
            request<{ templates: VMTemplateRecord[] }>("/admin/vm-templates"),
            request<{ configured: boolean }>("/admin/proxmox/status")
        ]);
        setTemplates(catalog.templates ?? []);
        setProxmoxConfigured(connection.configured);
    }, [request]);

    useEffect(() => {
        refresh().catch((error: Error) => onError(error.message));
    }, [refresh, onError]);

    useEffect(() => {
        const dialog = formDialog.current;
        if (formOpen && dialog && !dialog.open) {
            dialog.showModal();
        } else if (!formOpen && dialog?.open) {
            dialog.close();
        }
    }, [formOpen]);

    useEffect(() => {
        const dialog = prepareDialog.current;
        if (preparingTemplateId !== null && dialog && !dialog.open) {
            dialog.showModal();
        } else if (preparingTemplateId === null && dialog?.open) {
            dialog.close();
        }
    }, [preparingTemplateId]);

    function closeTemplateForm() {
        setFormOpen(false);
        setEditingId(null);
        setForm(emptyForm);
        setAliasesText("");
    }

    function changeForm<Field extends keyof TemplateForm>(field: Field, value: TemplateForm[Field]) {
        setForm((current) => ({ ...current, [field]: value }));
    }

    async function saveTemplate(event: FormEvent<HTMLFormElement>) {
        event.preventDefault();
        setBusy(true);
        try {
            if (editingId === null) {
                const result = await request<{ detection: PreflightResult }>("/admin/vm-templates", "POST", {
                    ...form,
                    execution_method: "qemu_guest_agent",
                    aliases: aliasesText.split(",").map((alias) => alias.trim()).filter(Boolean)
                });
                setForm(emptyForm);
                setAliasesText("");
                setFormOpen(false);
                onNotice(`Source added: ${result.detection.guest_os_name} ${result.detection.guest_os_version} · ${result.detection.guest_architecture}. Prepare it before provisioning.`);
            } else {
                await request(`/admin/vm-templates/${editingId}`, "PUT", {
                    ...form,
                    execution_method: "qemu_guest_agent"
                });
                setEditingId(null);
                setForm(emptyForm);
                setFormOpen(false);
                onNotice("Template metadata saved. Run preparation again before provisioning from this source.");
            }
            await refresh();
        } catch (error) {
            onError(templateErrorMessage(error));
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
            guest_os: knownLinuxDistributions.includes(record.template.guest_os) ? "linux" : record.template.guest_os === "freebsd" ? "bsd" : record.template.guest_os,
            guest_os_name: record.template.guest_os_name,
            guest_os_version: record.template.guest_os_version,
            edition: record.template.edition,
            architecture: record.template.architecture,
            system_only: record.template.system_only
        });
        setFormOpen(true);
    }

    async function addAlias(templateId: number) {
        const alias = aliasDrafts[templateId]?.trim();
        if (!alias) {
            return;
        }
        setBusy(true);
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

    async function deleteTemplate(template: VMTemplate) {
        if (!window.confirm(`Remove ${template.display_name} (VMID ${template.source_id}) from the Organesson catalog? The Proxmox VM will not be deleted, but its aliases will no longer be available for provisioning.`)) {
            return;
        }
        setBusy(true);
        try {
            await request(`/admin/vm-templates/${template.id}`, "DELETE");
            await refresh();
            onNotice("Source VM removed from the Organesson catalog.");
        } catch (error) {
            onError((error as Error).message);
        } finally {
            setBusy(false);
        }
    }

    async function runPreflight(templateId: number) {
        setBusy(true);
        try {
            const result = await request<{ preflight: PreflightResult }>(`/admin/vm-templates/${templateId}/preflight`, "POST", {});
            await refresh();
            onNotice(result.preflight.passed ? "Source checks passed." : "Source checks found issues that need attention.");
        } catch (error) {
            onError((error as Error).message);
        } finally {
            setBusy(false);
        }
    }

    async function prepareTemplate(event: FormEvent<HTMLFormElement>) {
        event.preventDefault();
        if (preparingTemplateId === null) {
            return;
        }
        const template = templates.find(({ template: source }) => source.id === preparingTemplateId)?.template;
        const family = preparationFamily(template);
        const scripts: Record<string, string> = {};
        setBusy(true);
        try {
            const loadScript = async (key: string, path: string) => {
                const response = await fetch(path);
                if (!response.ok) {
                    throw new Error(`Could not load the ${key} preparation script from the frontend.`);
                }
                scripts[key] = await response.text();
            };
            if (family === "linux") {
                await loadScript("linux", "/scripts/template-prep/linux/og-prep-linux.sh");
            } else if (family === "windows") {
                const detectedWindows = `${template?.guest_os_name} ${template?.guest_os_version}`.toLowerCase();
                const windowsServer = detectedWindows.includes("server") && detectedWindows.includes("2025");
                await Promise.all([
                    loadScript("windows-common", "/scripts/template-prep/windows/og-prep-windows-common.ps1"),
                    loadScript("windows-entry", windowsServer ? "/scripts/template-prep/windows/og-prep-windows-server2025.ps1" : "/scripts/template-prep/windows/og-prep-windows11.ps1")
                ]);
            } else if (family === "freebsd") {
                await loadScript("freebsd", "/scripts/template-prep/freebsd/og-prep-freebsd.sh");
            } else {
                throw new Error("Select a supported guest OS family before preparation.");
            }
            const result = await request<{ template: VMTemplateRecord }>(`/admin/vm-templates/${preparingTemplateId}/prepare`, "POST", {
                scripts,
                usernames: usernamesText.split(/[\s,]+/).map((username) => username.trim()).filter(Boolean)
            });
            await refresh();
            setPreparingTemplateId(null);
            setUsernamesText("");
            onNotice(result.template.template.provisioning_ready ? "Source preparation completed and template is ready." : "Source preparation completed, but the template is not ready.");
        } catch (error) {
            onError(templateErrorMessage(error));
        } finally {
            setBusy(false);
        }
    }

    const query = searchText.trim().toLowerCase();
    const preparingTemplate = templates.find(({ template }) => template.id === preparingTemplateId)?.template;
    const preparingFamily = preparationFamily(preparingTemplate);
    const visibleTemplates = templates.filter(({ template, aliases }) => {
        const matchesQuery = !query || [
            template.display_name,
            template.description,
            template.source_id,
            template.guest_os,
            template.guest_os_name,
            template.guest_os_version,
            template.edition,
            template.architecture,
            ...aliases.map((alias) => alias.alias)
        ].some((value) => value.toLowerCase().includes(query));
        const matchesReadiness = readinessFilter === "all" || (readinessFilter === "ready") === template.provisioning_ready;
        return matchesQuery && matchesReadiness;
    });

    return (
        <section className="panel template-catalog" aria-labelledby="template-catalog-heading">
            <div className="template-catalog-heading">
                <div>
                    <p className="eyebrow">Platform administration</p>
                    <h2 id="template-catalog-heading">Source VM catalog</h2>
                    <p className="template-catalog-intro">Proxmox sources and their OpenTofu aliases.</p>
                </div>
                <div className="template-catalog-actions">
                    <button className="icon-button" type="button" onClick={() => refresh().catch((error: Error) => onError(error.message))} aria-label="Refresh source VM catalog"><RefreshCw size={16} /></button>
                    <button className="primary-action" type="button" onClick={() => { setEditingId(null); setForm(emptyForm); setAliasesText(""); setFormOpen(true); }}><Plus size={15} />Add source VM</button>
                </div>
            </div>

            <dialog className="template-form-dialog" ref={formDialog} onClose={closeTemplateForm} onClick={(event) => {
                if (event.target === event.currentTarget) {
                    closeTemplateForm();
                }
            }}>
                <form className="template-form" onSubmit={saveTemplate}>
                    <div className="template-form-heading">
                        <h3>{editingId === null ? "Register a source VM" : "Edit source metadata"}</h3>
                        <button className="icon-button" type="button" aria-label="Close" onClick={closeTemplateForm}><X size={16} /></button>
                    </div>
                    <div className="template-fields">
                        <label>Display name<input value={form.display_name} onChange={(event) => changeForm("display_name", event.target.value)} required maxLength={128} placeholder="Fedora Workstation" /></label>
                        <label>Proxmox VMID<input inputMode="numeric" value={form.source_id} onChange={(event) => changeForm("source_id", event.target.value)} required placeholder="156" disabled={editingId !== null} /></label>
                        <label>Guest type<select value={form.guest_os} onChange={(event) => changeForm("guest_os", event.target.value)} required disabled={editingId !== null}>{guestTypes.map((guestType) => <option value={guestType} key={guestType}>{guestType === "bsd" ? "BSD (FreeBSD)" : guestType === "linux" ? "Linux" : "Windows"}</option>)}</select></label>
                        {editingId === null && <p className="template-detection-note">Registration briefly starts the VM to detect its OS and test QEMU Guest Agent, then shuts it down.</p>}
                        <label className="template-description">Description<textarea value={form.description} onChange={(event) => changeForm("description", event.target.value)} maxLength={2048} rows={2} /></label>
                        <label className="template-system-only"><input type="checkbox" checked={form.system_only} onChange={(event) => changeForm("system_only", event.target.checked)} /> System only — available to platform-managed resources, not deployment VM requests</label>
                        {editingId === null && <label className="template-alias-entry">Aliases<input value={aliasesText} onChange={(event) => setAliasesText(event.target.value)} required placeholder="og-template-fedora-workstation-latest, fedora-workstation" /><small>Comma-separated. Each alias must be unique.</small></label>}
                    </div>
                    <div className="template-form-actions"><button className="secondary-action" type="button" onClick={closeTemplateForm} disabled={busy}>Cancel</button><button className="primary-action" type="submit" disabled={busy}><Plus size={15} />{busy ? "Saving…" : editingId === null ? "Add source VM" : "Save metadata"}</button></div>
                </form>
            </dialog>

            <dialog className="template-form-dialog" ref={prepareDialog} onClose={() => { setPreparingTemplateId(null); setUsernamesText(""); }} onClick={(event) => {
                if (event.target === event.currentTarget) {
                    setPreparingTemplateId(null);
                    setUsernamesText("");
                }
            }}>
                <form className="template-form" onSubmit={prepareTemplate}>
                    <div className="template-form-heading"><h3>Prepare source VM</h3><button className="icon-button" type="button" aria-label="Close" disabled={busy} onClick={() => setPreparingTemplateId(null)}><X size={16} /></button></div>
                    <p>The source will be powered on if needed, prepared through QEMU Guest Agent, then shut down. Readiness is removed before the run and restored only after every check passes.</p>
                    <label>Accounts to remove<input value={usernamesText} onChange={(event) => setUsernamesText(event.target.value)} placeholder="administrator, temporary-user" /><small>Optional. {preparingFamily === "windows" ? "Windows local account names" : preparingFamily === "freebsd" ? "FreeBSD account names" : "Linux account names"} separated by commas or spaces. Active and system accounts are protected; account profiles and home directories are removed.</small></label>
                    <div className="template-form-actions"><button className="secondary-action" type="button" disabled={busy} onClick={() => setPreparingTemplateId(null)}>Cancel</button><button className="primary-action" type="submit" disabled={busy || preparingTemplateId === null}><ShieldCheck size={15} />{busy ? "Preparing…" : "Run preparation"}</button></div>
                </form>
            </dialog>

            {templates.length > 0 && <div className="template-browser">
                <div className="template-browser-toolbar">
                    <label className="template-search"><Search size={15} /><input type="search" value={searchText} onChange={(event) => setSearchText(event.target.value)} placeholder="Search source VMs" aria-label="Search source VMs" /></label>
                    <select value={readinessFilter} onChange={(event) => setReadinessFilter(event.target.value as typeof readinessFilter)} aria-label="Filter templates by readiness"><option value="all">All sources</option><option value="ready">Ready</option><option value="not_ready">Not ready</option></select>
                    <span>{visibleTemplates.length} / {templates.length}</span>
                </div>
                {visibleTemplates.length === 0 ? <div className="template-empty">No source VMs match.</div> : <div className="template-list">
                    {visibleTemplates.map((record) => {
                        const { template } = record;
                        const preflight = preflightFor(template);
                        return (
                            <article className="template-card" key={template.id}>
                                <div className="template-card-heading">
                                    <div><p className="eyebrow">{template.guest_os_name || template.guest_os}</p><h3>{template.display_name}</h3><p className="template-source-id">VMID {template.source_id} · {template.guest_os_version} · {template.architecture} · {record.aliases.length} aliases</p></div>
                                    <div className="template-status-stack">{template.system_only && <span className="template-status">System only</span>}<span className={`template-status${template.provisioning_ready ? " is-ready" : ""}`}>{template.provisioning_ready ? <><Check size={13} /> Ready</> : "Not ready"}</span></div>
                                </div>
                                <details className="template-management">
                                    <summary><span>Manage source</span><ChevronDown size={15} /></summary>
                                    <div className="template-management-content">
                                        {template.description && <p className="template-description-copy">{template.description}</p>}
                                        <div className="template-aliases" aria-label="Template aliases">
                                            {record.aliases.map((alias) => <span className="template-alias" key={alias.id}><code>{alias.alias}</code><button type="button" disabled={busy || record.aliases.length < 2} onClick={() => removeAlias(template.id, alias.id)} aria-label={`Remove alias ${alias.alias}`}><Trash2 size={13} /></button></span>)}
                                        </div>
                                        <div className="template-alias-add"><input aria-label={`New alias for ${template.display_name}`} value={aliasDrafts[template.id] ?? ""} onChange={(event) => setAliasDrafts((current) => ({ ...current, [template.id]: event.target.value }))} placeholder="Add another alias" /><button className="secondary-action" type="button" disabled={busy || !aliasDrafts[template.id]?.trim()} onClick={() => addAlias(template.id)}><Plus size={14} /> Add alias</button></div>
                                        <div className="template-card-actions"><button className="secondary-action" type="button" disabled={busy} onClick={() => startEditing(record)}><Pencil size={14} /> Edit metadata</button><button className="secondary-action" type="button" disabled={busy || !proxmoxConfigured} onClick={() => runPreflight(template.id)}><RefreshCw size={14} /> Check source</button><button className="secondary-action danger-action" type="button" disabled={busy} onClick={() => deleteTemplate(template)}><Trash2 size={14} /> Delete catalog entry</button></div>
                                        {preflight && <div className="preflight-results"><div className="preflight-summary"><strong>{preflight.passed ? "Preflight passed" : "Preflight needs attention"}</strong><time dateTime={preflight.checked_at}>{new Date(preflight.checked_at).toLocaleString()}</time></div>{preflight.checks?.map((check) => <p className={`preflight-check${check.passed ? " is-passed" : ""}`} key={check.name}><span>{check.passed ? "✓" : check.required ? "!" : "·"}</span>{check.details}</p>)}</div>}
                                        <div className="template-readiness"><p className="template-readiness-status">{template.provisioning_ready ? "Prepared source is stopped and ready to clone." : "Preparation runs the OS script, removes only accounts listed, then shuts down and validates the source."}</p><button className="secondary-action" type="button" disabled={busy || !proxmoxConfigured || !preparationFamily(template)} onClick={() => { setUsernamesText(""); setPreparingTemplateId(template.id); }}><ShieldCheck size={14} /> {template.provisioning_ready ? "Prepare / update" : "Prepare source"}</button></div>
                                    </div>
                                </details>
                            </article>
                        );
                    })}
                </div>}
            </div>}
            {templates.length === 0 && <div className="template-empty">No source VMs are registered.</div>}
        </section>
    );
}
