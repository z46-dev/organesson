import { useEffect, useState, type FormEvent } from "react";
import { ChevronDown, Pencil, Plus, Trash2, X } from "lucide-react";
import type { ApiRequest } from "./api";

type AddressPool = {
    name: string;
    prefix: string;
    allocation_prefix: string;
    gateway: string;
    dns: string[];
};

type PolicySubnet = { prefix: string; gateway: string; dhcp_enabled: boolean };

type PolicyNetwork = {
    name: string;
    kind: "bridge" | "vnet";
    target_mode: "existing" | "create";
    pve_name: string;
    subnets: PolicySubnet[];
    address_pools: AddressPool[];
};

type Policy = {
    limits: { max_deployments: number; max_sdn_networks: number; virtual_cpus: number; memory_mib: number; storage_gib: number; snapshot_storage_gib: number };
    deployment_limits: { max_resources: number; max_sdn_networks: number; virtual_cpus: number; memory_mib: number; storage_gib: number; snapshot_storage_gib: number };
    vm_limits: { virtual_cpus: number; memory_mib: number; storage_gib: number; snapshot_storage_gib: number };
    resource_pools: string[];
    storages: string[];
    networks: PolicyNetwork[];
};

type Inventory = { pools: string[]; storages: string[]; bridges: string[]; vnets: string[]; vnet_subnets?: Record<string, PolicySubnet[]> };
type Props = { request: ApiRequest; section: "capacity" | "networks"; onError: (message: string) => void; onNotice: (message: string) => void };

const emptyPolicy: Policy = {
    limits: { max_deployments: 0, max_sdn_networks: 0, virtual_cpus: 0, memory_mib: 0, storage_gib: 0, snapshot_storage_gib: 0 },
    deployment_limits: { max_resources: 0, max_sdn_networks: 0, virtual_cpus: 0, memory_mib: 0, storage_gib: 0, snapshot_storage_gib: 0 },
    vm_limits: { virtual_cpus: 0, memory_mib: 0, storage_gib: 0, snapshot_storage_gib: 0 },
    resource_pools: [], storages: [], networks: []
};

type QuotaScope = "limits" | "deployment_limits" | "vm_limits";
type QuotaRow = { field: string; label: string; unit: string; value: number; scale?: number; inactiveText?: string };

function ResourceTargetSelect({ label, options, selected, onChange }: { label: string; options: string[]; selected: string[]; onChange: (values: string[]) => void }) {
    const choices = [...new Set([...options, ...selected])].sort((left, right) => left.localeCompare(right));
    return <div className="placement-select-field">
        <span>{label}</span>
        <details className="placement-select">
            <summary><span>{selected.length === 0 ? `Select ${label.toLowerCase()}` : selected.join(", ")}</span><ChevronDown size={15} /></summary>
            <div className="placement-select-options" role="group" aria-label={label}>
                {choices.length === 0 ? <p>No Proxmox targets available</p> : choices.map((choice) => {
                    const available = options.includes(choice);
                    return <label className={!available ? "is-unavailable" : ""} key={choice}>
                        <input type="checkbox" checked={selected.includes(choice)} onChange={(event) => onChange(event.target.checked ? [...selected, choice] : selected.filter((value) => value !== choice))} />
                        <span>{choice}{!available && <small>Unavailable</small>}</span>
                    </label>;
                })}
            </div>
        </details>
    </div>;
}

function QuotaTable({ title, rows, onChange }: { title: string; rows: QuotaRow[]; onChange: (field: string, value: number) => void }) {
    return <section className="quota-column" aria-label={title}>
        <h3>{title}</h3>
        <table className="quota-table"><tbody>{rows.map((row) => {
            const scale = row.scale ?? 1;
            const active = row.value > 0;
            return <tr key={row.field}>
                <th scope="row">{row.label}</th>
                <td><input type="checkbox" aria-label={`Enable ${row.label}`} checked={active} onChange={(event) => onChange(row.field, event.target.checked ? scale : 0)} /></td>
                <td><input type="number" min="0" step={scale === 1 ? 1 : 0.5} aria-label={`${row.label} limit`} value={active ? row.value / scale : ""} placeholder={row.inactiveText ?? "Unlimited"} disabled={!active} onChange={(event) => onChange(row.field, (Number(event.target.value) || 0) * scale)} /></td>
                <td>{row.unit}</td>
            </tr>;
        })}</tbody></table>
    </section>;
}

// Edits platform-wide resource limits and PVE allowlists, with validation performed before saving.
export function ProxmoxResourcePolicy({ request, section, onError, onNotice }: Props) {
    const [policy, setPolicy] = useState<Policy>(emptyPolicy);
    const [inventory, setInventory] = useState<Inventory | null>(null);
    const [configured, setConfigured] = useState(false);
    const [validatedAt, setValidatedAt] = useState<string | null>(null);
    const [issues, setIssues] = useState<string[]>([]);
    const [inventoryError, setInventoryError] = useState("");
    const [busy, setBusy] = useState(false);
    const [networkEditor, setNetworkEditor] = useState<{ networkIndex: number | null; value: PolicyNetwork } | null>(null);
    const [poolEditor, setPoolEditor] = useState<{ poolIndex: number | null; value: AddressPool } | null>(null);
    const [poolIssue, setPoolIssue] = useState("");

    useEffect(() => {
        request<{ policy?: Policy; inventory?: Inventory; configured: boolean; validated_at?: string; inventory_error?: string }>("/admin/proxmox/resources")
            .then((result) => {
                setPolicy({
                    ...emptyPolicy,
                    ...result.policy,
                    limits: { ...emptyPolicy.limits, ...result.policy?.limits },
                    deployment_limits: { ...emptyPolicy.deployment_limits, ...result.policy?.deployment_limits },
                    vm_limits: { ...emptyPolicy.vm_limits, ...result.policy?.vm_limits },
                    networks: (result.policy?.networks ?? []).map((network) => ({
                        ...network,
                        target_mode: network.target_mode ?? "existing",
                        subnets: network.subnets ?? [],
                        address_pools: network.address_pools ?? []
                    }))
                });
                setInventory(result.inventory ?? null);
                setConfigured(result.configured);
                setValidatedAt(result.validated_at ?? null);
                setInventoryError(result.inventory_error ?? "");
            })
            .catch((error: Error) => onError(error.message));
    }, [request, onError]);

    function changeQuota(field: string, value: number) {
        const [scope, name] = field.split(".") as [QuotaScope, string];
        setPolicy((current) => ({ ...current, [scope]: { ...current[scope], [name]: value } }));
    }

    function changeNetwork(_index: number, field: keyof PolicyNetwork, value: string | PolicySubnet[]) {
        setNetworkEditor((current) => current ? { ...current, value: { ...current.value, [field]: value } } : current);
    }

    function savePool() {
        if (!poolEditor) return;
        const { value, poolIndex } = poolEditor;
        if (!networkEditor) return;
        const prefix = value.prefix.trim();
        const allocationPrefix = value.allocation_prefix.trim();
        const cidrPattern = /^(\d{1,3}\.){3}\d{1,3}\/\d{1,2}$|^[0-9a-fA-F:]+\/\d{1,3}$/;
        if (!value.name.trim() || !cidrPattern.test(prefix) || (allocationPrefix && !cidrPattern.test(allocationPrefix))) {
            setPoolIssue("Enter a pool name and valid network prefixes.");
            return;
        }
        if (value.gateway && !/^[0-9a-fA-F:.]+$/.test(value.gateway.trim())) {
            setPoolIssue("Enter a valid gateway address.");
            return;
        }
        const pools = networkEditor.value.address_pools;
        if (pools.some((pool, index) => index !== poolIndex && pool.name.trim() === value.name.trim())) {
            setPoolIssue("Pool names must be unique within a network.");
            return;
        }
        setNetworkEditor((current) => current ? { ...current, value: {
            ...current.value,
            address_pools: poolIndex === null ? [...current.value.address_pools, value] : current.value.address_pools.map((pool, index) => index === poolIndex ? value : pool)
        } } : current);
        setPoolIssue("");
        setPoolEditor(null);
    }

    async function save(event: FormEvent<HTMLFormElement>) {
        event.preventDefault();
        setBusy(true);
        setIssues([]);
        try {
            const result = await request<{ validated_at: string }>("/admin/proxmox/resources", "PUT", policy);
            setValidatedAt(result.validated_at);
            onNotice("Resource policy validated against the current Proxmox inventory and saved.");
        } catch (error) {
            const message = (error as Error).message;
            onError(message);
            setIssues([message]);
        } finally {
            setBusy(false);
        }
    }

    const blankNetwork: PolicyNetwork = { name: "", kind: "bridge", target_mode: "existing", pve_name: "", subnets: [], address_pools: [] };
    const openNetworkEditor = (networkIndex: number | null) => {
        const selectedNetwork = networkIndex === null ? blankNetwork : policy.networks[networkIndex] ?? blankNetwork;
        const value: PolicyNetwork = { ...selectedNetwork, subnets: [...selectedNetwork.subnets], address_pools: [...selectedNetwork.address_pools] };
        setPoolEditor(null);
        setNetworkEditor({ networkIndex, value });
    };
    const saveNetwork = () => {
        if (!networkEditor) return;
        setPolicy((current) => ({ ...current, networks: networkEditor.networkIndex === null
            ? [...current.networks, networkEditor.value]
            : current.networks.map((network, index) => index === networkEditor.networkIndex ? networkEditor.value : network) }));
        setNetworkEditor(null);
    };
    const removeNetwork = (index: number) => setPolicy((current) => ({ ...current, networks: current.networks.filter((_, itemIndex) => index !== itemIndex) }));
    const openPoolEditor = (poolIndex: number | null) => {
        const emptyPool: AddressPool = { name: "", prefix: "", allocation_prefix: "", gateway: "", dns: [] };
        const value: AddressPool = poolIndex === null ? emptyPool : { ...(networkEditor?.value.address_pools[poolIndex] ?? emptyPool) };
        setPoolIssue("");
        setPoolEditor({ poolIndex, value });
    };
    const removePool = (poolIndex: number) => setNetworkEditor((current) => current ? { ...current, value: { ...current.value, address_pools: current.value.address_pools.filter((_, index) => index !== poolIndex) } } : current);

    return (
        <form className="panel resource-policy" aria-labelledby="resource-policy-heading" onSubmit={save}>
            <div className="policy-heading"><div><p className="eyebrow">Platform resource policy</p><h2 id="resource-policy-heading">{section === "capacity" ? "Quotas & placement" : "Networks and address pools"}</h2></div>{section === "networks" ? <button className="icon-action" type="button" aria-label="Add network" title="Add network" onClick={() => openNetworkEditor(null)}><Plus size={17} /></button> : <span className={validatedAt ? "policy-status is-ready" : "policy-status"}>{validatedAt ? "Validated" : "Not validated"}</span>}</div>
            {!configured && <p className="policy-note">Configure the read-only Proxmox API connection in backend/config.toml before validating this policy.</p>}
            {inventoryError && <p className="policy-note policy-warning">{inventoryError}</p>}
            {section === "capacity" && <div className="quota-layout">
                <div className="quota-columns">
                    <QuotaTable title="Organesson" onChange={changeQuota} rows={[
                        { field: "limits.max_deployments", label: "Deployments", unit: "max", value: policy.limits.max_deployments },
                        { field: "limits.max_sdn_networks", label: "SDN networks", unit: "max", value: policy.limits.max_sdn_networks, inactiveText: "Unlimited" },
                        { field: "limits.virtual_cpus", label: "vCPU", unit: "cores", value: policy.limits.virtual_cpus },
                        { field: "limits.memory_mib", label: "RAM", unit: "GiB", value: policy.limits.memory_mib, scale: 1024 },
                        { field: "limits.storage_gib", label: "Storage", unit: "GiB", value: policy.limits.storage_gib },
                        { field: "limits.snapshot_storage_gib", label: "Snapshots", unit: "GiB", value: policy.limits.snapshot_storage_gib }
                    ]} />
                    <QuotaTable title="Per deployment" onChange={changeQuota} rows={[
                        { field: "deployment_limits.max_resources", label: "Resources", unit: "max", value: policy.deployment_limits.max_resources },
                        { field: "deployment_limits.max_sdn_networks", label: "SDN networks", unit: "max", value: policy.deployment_limits.max_sdn_networks, inactiveText: "Unlimited" },
                        { field: "deployment_limits.virtual_cpus", label: "vCPU", unit: "cores", value: policy.deployment_limits.virtual_cpus },
                        { field: "deployment_limits.memory_mib", label: "RAM", unit: "GiB", value: policy.deployment_limits.memory_mib, scale: 1024 },
                        { field: "deployment_limits.storage_gib", label: "Storage", unit: "GiB", value: policy.deployment_limits.storage_gib },
                        { field: "deployment_limits.snapshot_storage_gib", label: "Snapshots", unit: "GiB", value: policy.deployment_limits.snapshot_storage_gib }
                    ]} />
                    <QuotaTable title="Per VM" onChange={changeQuota} rows={[
                        { field: "vm_limits.virtual_cpus", label: "vCPU", unit: "cores", value: policy.vm_limits.virtual_cpus },
                        { field: "vm_limits.memory_mib", label: "RAM", unit: "GiB", value: policy.vm_limits.memory_mib, scale: 1024 },
                        { field: "vm_limits.storage_gib", label: "Active storage", unit: "GiB", value: policy.vm_limits.storage_gib },
                        { field: "vm_limits.snapshot_storage_gib", label: "Snapshots", unit: "GiB", value: policy.vm_limits.snapshot_storage_gib }
                    ]} />
                </div>
                <section className="placement-targets" aria-label="Proxmox placement targets">
                    <div className="placement-targets-heading"><h3>Proxmox placement targets</h3><span>Only selected targets are authorized.</span></div>
                    <div className="policy-fields">
                        <ResourceTargetSelect label="Resource pools" options={inventory?.pools ?? []} selected={policy.resource_pools} onChange={(resource_pools) => setPolicy((current) => ({ ...current, resource_pools }))} />
                        <ResourceTargetSelect label="Storages" options={inventory?.storages ?? []} selected={policy.storages} onChange={(storages) => setPolicy((current) => ({ ...current, storages }))} />
                    </div>
                </section>
            </div>}
            {section === "networks" && <div className="policy-network-list">
                {policy.networks.map((network, networkIndex) => <fieldset className="policy-network" key={networkIndex}>
                    <legend className="policy-network-title"><span>{network.name || `Network ${networkIndex + 1}`}</span><span className="policy-network-actions"><button className="icon-action" type="button" aria-label={`Edit ${network.name || `network ${networkIndex + 1}`}`} onClick={() => openNetworkEditor(networkIndex)}><Pencil size={14} /></button><button className="icon-action danger-action" type="button" aria-label={`Remove ${network.name || `network ${networkIndex + 1}`}`} onClick={() => removeNetwork(networkIndex)}><Trash2 size={14} /></button></span></legend>
                    <div className="policy-network-columns">
                        <div className="network-summary">
                            <div className="network-summary-item"><span>Type</span><strong>{network.kind === "bridge" ? "Linux bridge" : "SDN VNet"}</strong></div>
                            <div className="network-summary-item"><span>Proxmox target</span><strong>{network.target_mode === "create" ? "Created per deployment" : network.pve_name || "Not selected"}</strong></div>
                            {network.kind === "vnet" && <div className="network-summary-subnets"><span>{network.target_mode === "create" ? "Configured subnets" : "Proxmox subnets"}</span>{(network.target_mode === "create" ? network.subnets : inventory?.vnet_subnets?.[network.pve_name] ?? []).length === 0 ? <small>No subnets defined</small> : (network.target_mode === "create" ? network.subnets : inventory?.vnet_subnets?.[network.pve_name] ?? []).map((subnet, subnetIndex) => <small key={`${subnet.prefix}-${subnetIndex}`}><strong>{subnet.prefix}</strong>{subnet.gateway ? ` · gateway ${subnet.gateway}` : ""}{subnet.dhcp_enabled ? " · DHCP" : ""}</small>)}</div>}
                        </div>
                        <section className="address-pool-list" aria-label={`${network.name} address pools`}>
                            <div className="pool-list-heading"><h4>Address pools</h4></div>
                            {network.address_pools.length === 0 ? <p className="empty-pool-list">No address pools</p> : <div className="address-pool-table-wrap"><table className="address-pool-table"><thead><tr><th scope="col">Pool</th><th scope="col">Guest prefix</th><th scope="col">Allocation subnet</th><th scope="col">Gateway</th><th scope="col">DNS</th></tr></thead><tbody>{network.address_pools.map((pool, poolIndex) => <tr key={poolIndex}><th scope="row">{pool.name}</th><td>{pool.prefix || "—"}</td><td>{pool.allocation_prefix || "Same as guest prefix"}</td><td>{pool.gateway || "—"}</td><td>{pool.dns.length > 0 ? pool.dns.join(", ") : "—"}</td></tr>)}</tbody></table></div>}
                        </section>
                    </div>
                </fieldset>)}
            </div>}
            {issues.length > 0 && <ul className="policy-issues">{issues.map((issue) => <li key={issue}>{issue}</li>)}</ul>}
            <div className="policy-footer"><span>{validatedAt ? `Last verified ${new Date(validatedAt).toLocaleString()}` : "Policy must pass PVE validation before it can be used."}</span><button className="primary-action" type="submit" disabled={busy || !configured}>{busy ? "Checking…" : "Validate and save"}</button></div>
            {networkEditor && <div className="modal-backdrop" role="presentation" onMouseDown={(event) => { if (event.target === event.currentTarget) setNetworkEditor(null); }}><section className="pool-editor-modal network-editor-modal" role="dialog" aria-modal="true" aria-labelledby="network-editor-title"><header><h3 id="network-editor-title">{networkEditor.networkIndex === null ? "Add network" : "Edit network"}</h3><button className="icon-action" type="button" aria-label="Close" onClick={() => setNetworkEditor(null)}><X size={17} /></button></header><div><table className="policy-config-table"><tbody>
                <tr><th scope="row">Organesson name</th><td><input autoFocus value={networkEditor.value.name} onChange={(event) => changeNetwork(networkEditor.networkIndex ?? -1, "name", event.target.value)} /></td></tr>
                <tr><th scope="row">Type</th><td><select value={networkEditor.value.kind} onChange={(event) => {
                    const kind = event.target.value as PolicyNetwork["kind"];
                    setNetworkEditor((current) => current ? { ...current, value: { ...current.value, kind, target_mode: "existing", pve_name: kind === current.value.kind ? current.value.pve_name : "", subnets: kind === "bridge" ? [] : current.value.subnets } } : current);
                }}><option value="bridge">Linux bridge</option><option value="vnet">SDN VNet</option></select></td></tr>
                {networkEditor.value.kind === "vnet" && <tr><th scope="row">VNet source</th><td><select value={networkEditor.value.target_mode} onChange={(event) => setNetworkEditor((current) => current ? { ...current, value: { ...current.value, target_mode: event.target.value as PolicyNetwork["target_mode"], pve_name: event.target.value === "create" ? "" : current.value.pve_name } } : current)}><option value="existing">Existing Proxmox VNet</option><option value="create">Create our own</option></select></td></tr>}
                {(networkEditor.value.kind === "bridge" || networkEditor.value.target_mode === "existing") && <tr><th scope="row">Proxmox target</th><td><select value={networkEditor.value.pve_name} onChange={(event) => changeNetwork(networkEditor.networkIndex ?? -1, "pve_name", event.target.value)}><option value="">Select target</option>{(networkEditor.value.kind === "bridge" ? inventory?.bridges ?? [] : inventory?.vnets ?? []).map((name) => <option key={name} value={name}>{name}</option>)}</select></td></tr>}
            </tbody></table>
            {networkEditor.value.kind === "vnet" && networkEditor.value.target_mode === "create" && <div className="network-subnets"><div className="pool-list-heading"><h4>VNet subnets</h4><button className="icon-action" type="button" aria-label="Add VNet subnet" onClick={() => changeNetwork(networkEditor.networkIndex ?? -1, "subnets", [...networkEditor.value.subnets, { prefix: "", gateway: "", dhcp_enabled: false }])}><Plus size={15} /></button></div>{networkEditor.value.subnets.map((subnet, subnetIndex) => <div className="network-subnet-row" key={subnetIndex}><input aria-label={`VNet subnet ${subnetIndex + 1} prefix`} placeholder="192.0.2.0/24" value={subnet.prefix} onChange={(event) => changeNetwork(networkEditor.networkIndex ?? -1, "subnets", networkEditor.value.subnets.map((item, index) => index === subnetIndex ? { ...item, prefix: event.target.value } : item))} /><input aria-label={`VNet subnet ${subnetIndex + 1} gateway`} placeholder="Gateway" value={subnet.gateway} onChange={(event) => changeNetwork(networkEditor.networkIndex ?? -1, "subnets", networkEditor.value.subnets.map((item, index) => index === subnetIndex ? { ...item, gateway: event.target.value } : item))} /><label className="subnet-dhcp"><input type="checkbox" checked={subnet.dhcp_enabled} onChange={(event) => changeNetwork(networkEditor.networkIndex ?? -1, "subnets", networkEditor.value.subnets.map((item, index) => index === subnetIndex ? { ...item, dhcp_enabled: event.target.checked } : item))} /> DHCP</label><button className="icon-action danger-action" type="button" aria-label="Remove VNet subnet" onClick={() => changeNetwork(networkEditor.networkIndex ?? -1, "subnets", networkEditor.value.subnets.filter((_, index) => index !== subnetIndex))}><Trash2 size={14} /></button></div>)}</div>}
            {networkEditor.value.kind === "vnet" && networkEditor.value.target_mode === "existing" && networkEditor.value.pve_name && (inventory?.vnet_subnets?.[networkEditor.value.pve_name] ?? []).length > 0 && <div className="network-subnets"><div className="pool-list-heading"><h4>Proxmox subnets</h4></div>{inventory?.vnet_subnets?.[networkEditor.value.pve_name]?.map((subnet) => <div className="existing-subnet-row" key={`${networkEditor.value.pve_name}-${subnet.prefix}`}><span>{subnet.prefix}</span>{subnet.gateway && <span>Gateway {subnet.gateway}</span>}{subnet.dhcp_enabled && <span>DHCP</span>}</div>)}</div>}
            <div className="network-editor-pools"><div className="pool-list-heading"><h4>Address pools</h4><button className="icon-action" type="button" aria-label="Add address pool" onClick={() => openPoolEditor(null)}><Plus size={15} /></button></div>{networkEditor.value.address_pools.length === 0 ? <p className="empty-pool-list">No address pools</p> : networkEditor.value.address_pools.map((pool, poolIndex) => <div className="address-pool-row" key={poolIndex}><span className="pool-summary"><strong>{pool.name}</strong><span>{pool.prefix}{pool.allocation_prefix ? ` · ${pool.allocation_prefix}` : ""}</span></span><button className="icon-action" type="button" aria-label={`Edit ${pool.name} address pool`} onClick={() => openPoolEditor(poolIndex)}><Pencil size={14} /></button><button className="icon-action danger-action" type="button" aria-label={`Remove ${pool.name} address pool`} onClick={() => removePool(poolIndex)}><Trash2 size={14} /></button></div>)}</div>
            <footer><button className="secondary-action" type="button" onClick={() => setNetworkEditor(null)}>Cancel</button><button className="primary-action" type="button" onClick={saveNetwork}>Save network</button></footer></div></section></div>}
            {poolEditor && <div className="modal-backdrop pool-modal-backdrop" role="presentation" onMouseDown={(event) => { if (event.target === event.currentTarget) setPoolEditor(null); }}><section className="pool-editor-modal" role="dialog" aria-modal="true" aria-labelledby="pool-editor-title"><header><h3 id="pool-editor-title">{poolEditor.poolIndex === null ? "New address pool" : "Edit address pool"}</h3><button className="icon-action" type="button" aria-label="Close" onClick={() => setPoolEditor(null)}><X size={17} /></button></header><div><table className="policy-config-table"><tbody>
                <tr><th scope="row">Name</th><td><input autoFocus value={poolEditor.value.name} onChange={(event) => setPoolEditor({ ...poolEditor, value: { ...poolEditor.value, name: event.target.value } })} /></td></tr>
                <tr><th scope="row">Network prefix</th><td><input placeholder="10.0.0.0/8" value={poolEditor.value.prefix} onChange={(event) => setPoolEditor({ ...poolEditor, value: { ...poolEditor.value, prefix: event.target.value } })} /></td></tr>
                <tr><th scope="row">Allocation subnet</th><td><input placeholder="Same as network prefix" value={poolEditor.value.allocation_prefix} onChange={(event) => setPoolEditor({ ...poolEditor, value: { ...poolEditor.value, allocation_prefix: event.target.value } })} /></td></tr>
                <tr><th scope="row">Gateway</th><td><input value={poolEditor.value.gateway} onChange={(event) => setPoolEditor({ ...poolEditor, value: { ...poolEditor.value, gateway: event.target.value } })} /></td></tr>
                <tr><th scope="row">DNS</th><td><input placeholder="Comma-separated addresses" value={poolEditor.value.dns.join(", ")} onChange={(event) => setPoolEditor({ ...poolEditor, value: { ...poolEditor.value, dns: event.target.value.split(",").map((item) => item.trim()).filter(Boolean) } })} /></td></tr>
            </tbody></table>{poolIssue && <p className="policy-warning">{poolIssue}</p>}<footer><button className="secondary-action" type="button" onClick={() => setPoolEditor(null)}>Cancel</button><button className="primary-action" type="button" onClick={savePool}>Save pool</button></footer></div></section></div>}
        </form>
    );
}
