import { useEffect, useState, type FormEvent } from "react";
import type { ApiRequest } from "./api";

type AddressPool = {
    name: string;
    prefix: string;
    allocation_prefix: string;
    start: string;
    end: string;
    gateway: string;
    dns: string[];
};

type PolicyNetwork = {
    name: string;
    kind: "bridge" | "vnet";
    pve_name: string;
    address_pools: AddressPool[];
};

type Policy = {
    limits: { virtual_cpus: number; memory_mib: number; storage_gib: number; snapshot_storage_gib: number };
    resource_pools: string[];
    storages: string[];
    networks: PolicyNetwork[];
    allow_isolated_sdn_networks: boolean;
};

type Inventory = { pools: string[]; storages: string[]; bridges: string[]; vnets: string[] };
type Props = { request: ApiRequest; section: "capacity" | "resources" | "networks"; onError: (message: string) => void; onNotice: (message: string) => void };

const emptyPolicy: Policy = { limits: { virtual_cpus: 0, memory_mib: 0, storage_gib: 0, snapshot_storage_gib: 0 }, resource_pools: [], storages: [], networks: [], allow_isolated_sdn_networks: false };

// Edits platform-wide resource limits and PVE allowlists, with validation performed before saving.
export function ProxmoxResourcePolicy({ request, section, onError, onNotice }: Props) {
    const [policy, setPolicy] = useState<Policy>(emptyPolicy);
    const [inventory, setInventory] = useState<Inventory | null>(null);
    const [configured, setConfigured] = useState(false);
    const [validatedAt, setValidatedAt] = useState<string | null>(null);
    const [issues, setIssues] = useState<string[]>([]);
    const [inventoryError, setInventoryError] = useState("");
    const [busy, setBusy] = useState(false);

    useEffect(() => {
        request<{ policy?: Policy; inventory?: Inventory; configured: boolean; validated_at?: string; inventory_error?: string }>("/admin/proxmox/resources")
            .then((result) => {
                setPolicy({ ...emptyPolicy, ...result.policy, limits: { ...emptyPolicy.limits, ...result.policy?.limits } });
                setInventory(result.inventory ?? null);
                setConfigured(result.configured);
                setValidatedAt(result.validated_at ?? null);
                setInventoryError(result.inventory_error ?? "");
            })
            .catch((error: Error) => onError(error.message));
    }, [request, onError]);

    function changeLimit(field: keyof Policy["limits"], value: string) {
        setPolicy((current) => ({ ...current, limits: { ...current.limits, [field]: Number(value) || 0 } }));
    }

    function changeNames(field: "resource_pools" | "storages", value: string) {
        setPolicy((current) => ({ ...current, [field]: value.split(",").map((part) => part.trim()).filter(Boolean) }));
    }

    function changeNetwork(index: number, field: keyof PolicyNetwork, value: string) {
        setPolicy((current) => ({ ...current, networks: current.networks.map((network, itemIndex) => itemIndex === index ? { ...network, [field]: value } : network) }));
    }

    function changePool(networkIndex: number, poolIndex: number, field: keyof AddressPool, value: string) {
        setPolicy((current) => ({ ...current, networks: current.networks.map((network, itemIndex) => itemIndex !== networkIndex ? network : {
            ...network,
            address_pools: network.address_pools.map((pool, addressIndex) => addressIndex !== poolIndex ? pool : { ...pool, [field]: field === "dns" ? value.split(",").map((item) => item.trim()).filter(Boolean) : value })
        }) }));
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

    const addNetwork = () => setPolicy((current) => ({ ...current, networks: [...current.networks, { name: "", kind: "bridge", pve_name: "", address_pools: [] }] }));
    const removeNetwork = (index: number) => setPolicy((current) => ({ ...current, networks: current.networks.filter((_, itemIndex) => index !== itemIndex) }));
    const addPool = (index: number) => setPolicy((current) => ({ ...current, networks: current.networks.map((network, itemIndex) => itemIndex === index ? { ...network, address_pools: [...network.address_pools, { name: "", prefix: "", allocation_prefix: "", start: "", end: "", gateway: "", dns: [] }] } : network) }));
    const removePool = (networkIndex: number, poolIndex: number) => setPolicy((current) => ({ ...current, networks: current.networks.map((network, itemIndex) => itemIndex === networkIndex ? { ...network, address_pools: network.address_pools.filter((_, addressIndex) => addressIndex !== poolIndex) } : network) }));

    return (
        <form className="panel resource-policy" aria-labelledby="resource-policy-heading" onSubmit={save}>
            <div className="policy-heading"><div><p className="eyebrow">Platform resource policy</p><h2 id="resource-policy-heading">{section === "capacity" ? "Capacity limits" : section === "resources" ? "Proxmox resources" : "Networks and address pools"}</h2></div><span className={validatedAt ? "policy-status is-ready" : "policy-status"}>{validatedAt ? "Validated" : "Not validated"}</span></div>
            <p className="policy-help">Capacity limits are enforced by provisioning. Address ranges are reserved for Organesson deployments as requests are applied; external DHCP and network infrastructure remain independently managed.</p>
            {!configured && <p className="policy-note">Configure the read-only Proxmox API connection in backend/config.toml before validating this policy.</p>}
            {inventoryError && <p className="policy-note policy-warning">{inventoryError}</p>}
            {section === "capacity" && <div className="policy-fields">
                <p className="policy-help">Leave a limit at 0 to let Organesson use the capacity available to its authorized resources.</p>
                <label>Virtual CPU count<input type="number" min="0" value={policy.limits.virtual_cpus} onChange={(event) => changeLimit("virtual_cpus", event.target.value)} /></label>
                <label>Memory limit (MiB)<input type="number" min="0" value={policy.limits.memory_mib} onChange={(event) => changeLimit("memory_mib", event.target.value)} /></label>
                <label>Storage limit (GiB)<input type="number" min="0" value={policy.limits.storage_gib} onChange={(event) => changeLimit("storage_gib", event.target.value)} /></label>
                <label>Snapshot reservation limit (GiB)<input type="number" min="0" value={policy.limits.snapshot_storage_gib} onChange={(event) => changeLimit("snapshot_storage_gib", event.target.value)} /></label>
                <p className="policy-help">Each managed snapshot reserves the VM’s full declared boot-disk size. Zero means unlimited.</p>
            </div>}
            {section === "resources" && <div className="policy-fields">
                <p className="policy-help">At least one pool and storage are required. Names must be visible in Proxmox inventory; this read-only check does not verify future resource-creation permissions.</p>
                <label>Authorized resource pools<input value={policy.resource_pools.join(", ")} onChange={(event) => changeNames("resource_pools", event.target.value)} placeholder={inventory?.pools.join(", ") || "pool names, separated by commas"} /></label>
                <label>Authorized storages<input value={policy.storages.join(", ")} onChange={(event) => changeNames("storages", event.target.value)} placeholder={inventory?.storages.join(", ") || "storage names, separated by commas"} /></label>
                {inventory && <p className="policy-help">Available pools: {inventory.pools.join(", ") || "none"}<br />Available storages: {inventory.storages.join(", ") || "none"}</p>}
            </div>}
            {section === "networks" && <div className="policy-network-list">
                <p className="policy-help">Bridges and SDN VNets are checked in Proxmox. Address ranges are reserved between Organesson deployments, but must also be excluded from external DHCP and other address managers.</p>
                <label className="policy-checkbox"><input type="checkbox" checked={policy.allow_isolated_sdn_networks} onChange={(event) => setPolicy((current) => ({ ...current, allow_isolated_sdn_networks: event.target.checked }))} />Allow deployments to create isolated Proxmox SDN Simple-zone networks</label>
                {policy.networks.map((network, networkIndex) => <fieldset className="policy-network" key={networkIndex}>
                    <legend>Network {networkIndex + 1}</legend>
                    <div className="policy-fields policy-network-fields">
                        <label>Organesson name<input value={network.name} onChange={(event) => changeNetwork(networkIndex, "name", event.target.value)} /></label>
                        <label>Type<select value={network.kind} onChange={(event) => changeNetwork(networkIndex, "kind", event.target.value)}><option value="bridge">Linux bridge</option><option value="vnet">SDN VNet</option></select></label>
                        <label>Proxmox name<input value={network.pve_name} list={`pve-networks-${networkIndex}`} onChange={(event) => changeNetwork(networkIndex, "pve_name", event.target.value)} />{inventory && <datalist id={`pve-networks-${networkIndex}`}>{(network.kind === "bridge" ? inventory.bridges : inventory.vnets).map((name) => <option key={name} value={name} />)}</datalist>}</label>
                    </div>
                    {network.address_pools.map((pool, poolIndex) => <div className="address-pool-fields" key={poolIndex}>
                        <label>Pool name<input value={pool.name} onChange={(event) => changePool(networkIndex, poolIndex, "name", event.target.value)} /></label>
                        <label>Guest network prefix<input value={pool.prefix} placeholder="10.0.0.0/8" onChange={(event) => changePool(networkIndex, poolIndex, "prefix", event.target.value)} /></label>
                        <label>Allocation subnet<input value={pool.allocation_prefix ?? ""} placeholder="10.192.0.0/12" onChange={(event) => changePool(networkIndex, poolIndex, "allocation_prefix", event.target.value)} /></label>
                        <label>First address<input value={pool.start} onChange={(event) => changePool(networkIndex, poolIndex, "start", event.target.value)} /></label>
                        <label>Last address<input value={pool.end} onChange={(event) => changePool(networkIndex, poolIndex, "end", event.target.value)} /></label>
                        <label>Gateway<input value={pool.gateway} onChange={(event) => changePool(networkIndex, poolIndex, "gateway", event.target.value)} /></label>
                        <label>DNS (comma-separated)<input value={pool.dns.join(", ")} onChange={(event) => changePool(networkIndex, poolIndex, "dns", event.target.value)} /></label>
                        <button className="text-action" type="button" onClick={() => removePool(networkIndex, poolIndex)}>Remove address pool</button>
                    </div>)}
                    <div className="policy-row-actions"><button className="secondary-action" type="button" onClick={() => addPool(networkIndex)}>Add address pool</button><button className="text-action" type="button" onClick={() => removeNetwork(networkIndex)}>Remove network</button></div>
                </fieldset>)}
                <button className="secondary-action" type="button" onClick={addNetwork}>Add network</button>
            </div>}
            {issues.length > 0 && <ul className="policy-issues">{issues.map((issue) => <li key={issue}>{issue}</li>)}</ul>}
            <div className="policy-footer"><span>{validatedAt ? `Last verified ${new Date(validatedAt).toLocaleString()}` : "Policy must pass PVE validation before it can be used."}</span><button className="primary-action" type="submit" disabled={busy || !configured}>{busy ? "Checking…" : "Validate and save"}</button></div>
        </form>
    );
}
