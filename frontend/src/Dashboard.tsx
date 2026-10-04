import { useEffect, useState } from "react";
import { Boxes, Cable, ChevronDown, ChevronRight, CircleUserRound, FolderTree, ListTree, Network, RefreshCw, Router, Server, Users } from "lucide-react";
import type { ApiRequest } from "./api";
import type { Deployment, DeploymentAccess, DeploymentDetail, Resource } from "./types";
import { ResourcePowerControl } from "./ResourcePowerControl";
import { DeploymentAccessPanel } from "./DeploymentAccessPanel";
import { VMSnapshotPanel } from "./VMSnapshotPanel";
import { VMConsolePanel } from "./VMConsolePanel";

type WorkspaceSection = "resources" | "users" | "groups";
type AddressPoolDetails = {
    deployment_id: number;
    name: string;
    environment_network: string;
    address_family: string;
    address_count: number;
    pool_name: string;
    prefix: string;
    gateway?: string;
    dns?: string[];
    addresses: string[];
    address_usage: { address: string; in_use: boolean; virtual_machine_id?: number; virtual_machine_name?: string }[];
};
type ResourceLiveState = "verified" | "missing" | "unknown" | "unavailable";
type SDNIPAMEntry = { ip: string; mac?: string; hostname?: string; subnet?: string; vmid?: string };
type VMSpecification = { template_alias: string; sockets: number; cores: number; architecture: string; cpu_model: string; memory_mib: number; boot_disk_gib: number; pool: string; storage: string };
type NetworkDetails = { request: { mode: string; subnet?: string; gateway?: string; dhcp_enabled: boolean; egress_policy: string }; placement: { zone: string; vnet: string } };
type AttachmentDetails = { request: { node: string; vmid: string; bridge: string }; placement: { device: string; mac: string }; addresses: string[]; environment_network?: string; logical_network_id?: number; address_pool_request_id?: number; requested_address_count?: number; address_prefix?: string; address_gateway?: string; address_dns?: string[]; guest_network?: { ipv4_method: string; ipv4_address?: string; ipv4_gateway?: string; ipv4_dns?: string[] } };
type SelectedResourceDetails = { allocation?: AddressPoolDetails; configuration?: NetworkDetails | AttachmentDetails; specification?: VMSpecification; live_state?: ResourceLiveState; ipam_state?: "available" | "unconfigured" | "unavailable"; ipam_entries?: SDNIPAMEntry[] };

function formatCPUSpecification(specification?: VMSpecification) {
    if (!specification) {
        return "—";
    }

    const socketCount = specification.sockets || 1;
    const coreCount = specification.cores || 0;
    const sockets = `${socketCount} ${socketCount === 1 ? "socket" : "sockets"}`;
    const cores = `${coreCount} ${coreCount === 1 ? "core" : "cores"}/socket`;
    const architecture = specification.architecture || "Unknown architecture";
    const model = specification.cpu_model || "Default";
    return `${sockets} · ${cores} · ${architecture} · ${model}`;
}

type Props = {
    request: ApiRequest;
    onError: (message: string) => void;
};

// Shows deployments in a resource tree and opens selected items in the workspace.
export function Dashboard({ request, onError }: Props) {
    const [deployments, setDeployments] = useState<Deployment[]>([]);
    const [selectedDeploymentID, setSelectedDeploymentID] = useState<number | null>(null);
    const [selectedDeployment, setSelectedDeployment] = useState<DeploymentDetail | null>(null);
    const [deploymentAccess, setDeploymentAccess] = useState<DeploymentAccess | null>(null);
    const [selectedResourceID, setSelectedResourceID] = useState<number | null>(null);
    const [selectedUserID, setSelectedUserID] = useState<number | null>(null);
    const [selectedGroupID, setSelectedGroupID] = useState<number | null>(null);
    const [selectedResourceDetails, setSelectedResourceDetails] = useState<SelectedResourceDetails | null>(null);
    const [resourceDetailsLoading, setResourceDetailsLoading] = useState(false);
    const [resourceDetailsError, setResourceDetailsError] = useState("");
    const [selectedSection, setSelectedSection] = useState<WorkspaceSection>("resources");
    const [collapsedDeployments, setCollapsedDeployments] = useState<Record<number, boolean>>({});
    const [collapsedGroups, setCollapsedGroups] = useState<Record<number, boolean>>({});
    const [collapsedBranches, setCollapsedBranches] = useState<Record<string, boolean>>({});
    const [loadingDeployments, setLoadingDeployments] = useState(true);
    const [loadingDetails, setLoadingDetails] = useState(false);

    async function loadDeployments() {
        setLoadingDeployments(true);
        try {
            const result = await request<{ deployments: Deployment[] }>("/deployments");
            const visibleDeployments = result.deployments ?? [];
            setDeployments(visibleDeployments);
            setSelectedDeploymentID((current) => current !== null && visibleDeployments.some((deployment) => deployment.id === current)
                ? current
                : visibleDeployments[0]?.id ?? null);
        } catch (requestError) {
            onError((requestError as Error).message);
        } finally {
            setLoadingDeployments(false);
        }
    }

    useEffect(() => {
        loadDeployments();
    }, [request, onError]);

    useEffect(() => {
        if (selectedDeploymentID === null) {
            setSelectedDeployment(null);
            setDeploymentAccess(null);
            return;
        }

        let active = true;
        setSelectedDeployment(null);
        setDeploymentAccess(null);
        setSelectedResourceID(null);
        setSelectedUserID(null);
        setSelectedGroupID(null);
        setLoadingDetails(true);
        request<DeploymentDetail>(`/deployments/${selectedDeploymentID}`)
            .then((result) => active && setSelectedDeployment(result))
            .catch((requestError: Error) => active && onError(requestError.message))
            .finally(() => active && setLoadingDetails(false));
        return () => {
            active = false;
        };
    }, [request, onError, selectedDeploymentID]);

    useEffect(() => {
        if (selectedDeploymentID === null || selectedDeployment?.deployment.id !== selectedDeploymentID || (!selectedDeployment.can_manage_groups && !selectedDeployment.can_manage_permissions)) {
            setDeploymentAccess(null);
            return;
        }
        let active = true;
        setDeploymentAccess(null);
        request<DeploymentAccess>(`/deployments/${selectedDeploymentID}/access`)
            .then((result) => active && setDeploymentAccess(result))
            .catch((requestError: Error) => active && onError(requestError.message));
        return () => {
            active = false;
        };
    }, [request, onError, selectedDeploymentID, selectedDeployment?.can_manage_groups, selectedDeployment?.can_manage_permissions]);

    useEffect(() => {
        const resource = selectedDeployment?.resources.find((item) => item.id === selectedResourceID);
        if (!resource || (resource.kind !== "virtual_machine" && resource.kind !== "virtual_network" && resource.kind !== "address_pool_request" && resource.kind !== "network_attachment")) {
            setSelectedResourceDetails(null);
            setResourceDetailsLoading(false);
            setResourceDetailsError("");
            return;
        }

        let active = true;
        setSelectedResourceDetails(null);
        setResourceDetailsError("");
        setResourceDetailsLoading(true);
        const path = resource.kind === "virtual_machine" ? `/virtual-machines/${resource.id}` : resource.kind === "virtual_network" ? `/networks/${resource.id}` : resource.kind === "network_attachment" ? `/network-attachments/${resource.id}` : `/address-pool-requests/${resource.id}`;
        request<{ configuration?: NetworkDetails | AttachmentDetails; allocation?: AddressPoolDetails; specification?: VMSpecification; live_state?: ResourceLiveState }>(path)
            .then((result) => active && setSelectedResourceDetails(result))
            .catch((requestError: Error) => {
                if (active) {
                    setResourceDetailsError(requestError.message);
                    onError(requestError.message);
                }
            })
            .finally(() => active && setResourceDetailsLoading(false));
        return () => {
            active = false;
        };
    }, [request, onError, selectedDeployment, selectedResourceID]);

    async function refreshSelectedDeployment() {
        if (selectedDeploymentID === null) {
            await loadDeployments();
            return;
        }
        setLoadingDetails(true);
        try {
            const updated = await request<DeploymentDetail>(`/deployments/${selectedDeploymentID}`);
            setSelectedDeployment(updated);
            if (updated.can_manage_groups || updated.can_manage_permissions) {
                setDeploymentAccess(await request<DeploymentAccess>(`/deployments/${selectedDeploymentID}/access`));
            }
        } catch (requestError) {
            onError((requestError as Error).message);
        } finally {
            setLoadingDetails(false);
        }
    }

    async function changePower(resource: Resource, action: "start" | "stop" | "restart") {
        try {
            await request(`/virtual-machines/${resource.id}/power`, "POST", {
                action
            });
            await refreshSelectedDeployment();
        } catch (requestError) {
            onError((requestError as Error).message);
        }
    }

    const selectedResource = selectedDeployment?.resources.find((resource) => resource.id === selectedResourceID);

    function selectSection(section: WorkspaceSection) {
        setSelectedResourceID(null);
        if (section !== "users") {
            setSelectedUserID(null);
        }
        if (section !== "groups") {
            setSelectedGroupID(null);
        }
        setSelectedSection(section);
    }

    function branchIsExpanded(key: string) {
        return collapsedBranches[key] !== true;
    }

    function toggleBranch(key: string) {
        const expanded = branchIsExpanded(key);
        setCollapsedBranches((current) => ({ ...current, [key]: expanded }));
    }

    function selectUser(accountID: number) {
        setSelectedSection("users");
        setSelectedResourceID(null);
        setSelectedGroupID(null);
        setSelectedUserID(accountID);
    }

    function selectGroup(groupID: number) {
        setSelectedSection("groups");
        setSelectedResourceID(null);
        setSelectedUserID(null);
        setSelectedGroupID(groupID);
        setCollapsedBranches((current) => ({ ...current, [`${selectedDeploymentID}:groups`]: false }));
    }

    function renderOwnershipNodes(parentNodeID: number): React.ReactNode {
        return selectedDeployment?.ownership_nodes.filter((node) => node.parent_id === parentNodeID).map((node) => {
            if (node.kind === 2) {
                const resource = selectedDeployment.resources.find((item) => item.ownership_id === node.id);
                if (!resource) {
                    return null;
                }
                const ResourceIcon = resource.kind === "virtual_machine" ? Server : resource.kind === "virtual_network" ? Router : resource.kind === "network_attachment" ? Cable : resource.kind === "address_pool_request" ? ListTree : Network;
                return <li key={node.id}><button className={`tree-resource${resource.id === selectedResourceID ? " is-current" : ""}`} type="button" onClick={() => { selectSection("resources"); setSelectedResourceID(resource.id); }}>
                    <span className="tree-resource-icon">{resource.kind === "virtual_machine" && <span className={`tree-resource-state${resource.power_state === "running" ? " is-running" : ""}`} aria-label={`VM ${resource.power_state}`} />}<ResourceIcon size={14} aria-hidden="true" /></span>
                    <span>{resource.name}</span>
                </button></li>;
            }
            const isExpanded = collapsedGroups[node.id] !== true;
            return <li className="tree-owner-group" key={node.id}>
                    <details className="tree-group-details" open={isExpanded}>
                        <summary className="tree-group-button" aria-expanded={isExpanded} onClick={(event) => {
                            event.preventDefault();
                            setCollapsedGroups((current) => ({ ...current, [node.id]: isExpanded }));
                        }}><span className="tree-group-disclosure"><ChevronRight size={13} /><ChevronDown size={13} /></span><FolderTree size={13} /><span>{node.name}</span></summary>
                        <ul className="tree-resources">{renderOwnershipNodes(node.id)}</ul>
                    </details>
                </li>;
        });
    }

    return (
        <section className="dashboard-page" aria-label="Deployment workspace">
            <div className="workspace-layout">
                <aside className="workspace-sidebar" aria-label="Deployment navigation">
                    <div className="sidebar-heading">
                        <div><p className="eyebrow">Workspace</p><h2>Deployments</h2></div>
                        <button className="icon-button" type="button" onClick={loadDeployments} aria-label="Refresh deployments" disabled={loadingDeployments}><RefreshCw size={15} /></button>
                    </div>
                    {loadingDeployments && deployments.length === 0 ? <p className="sidebar-hint">Loading deployments…</p> : deployments.length === 0 ? (
                        <div className="sidebar-empty"><Boxes size={18} /><p>No deployments are visible to this account.</p></div>
                    ) : (
                        <ul className="deployment-tree">
                            {deployments.map((deployment) => {
                                const isSelected = selectedDeploymentID === deployment.id;
                                const isExpanded = isSelected && collapsedDeployments[deployment.id] !== true;
                                const resources = isSelected ? selectedDeployment?.resources ?? [] : [];
                                return (
                                    <li className="tree-deployment" key={deployment.id}>
                                        <button className={`tree-deployment-button${isSelected ? " is-selected" : ""}`} type="button" aria-expanded={isExpanded} onClick={() => {
                                            if (isSelected) {
                                                setCollapsedDeployments((current) => ({ ...current, [deployment.id]: !isExpanded }));
                                            } else {
                                                setSelectedDeploymentID(deployment.id);
                                                setCollapsedDeployments((current) => ({ ...current, [deployment.id]: false }));
                                            }
                                            setSelectedResourceID(null);
                                            setSelectedSection("resources");
                                        }}>
                                            {isExpanded ? <ChevronDown size={15} /> : <ChevronRight size={15} />}
                                            <Boxes size={16} />
                                            <span>{deployment.name}</span>
                                        </button>
                                                {isExpanded && (
                                                    <ul className="tree-branches">
                                                        <li>
                                                    {(() => {
                                                        const key = `${deployment.id}:resources`;
                                                        const expanded = branchIsExpanded(key);
                                                        return <>
                                                            <button className={`tree-category-button${selectedSection === "resources" ? " is-active" : ""}`} type="button" aria-expanded={expanded} onClick={() => { toggleBranch(key); selectSection("resources"); }}><span className="tree-branch-disclosure">{expanded ? <ChevronDown size={13} /> : <ChevronRight size={13} />}</span><Server size={14} /><span>Resources</span><small>{selectedDeployment?.resources.length ?? "—"}</small></button>
                                                            {expanded && resources.length > 0 && selectedDeployment?.deployment.root_node_id !== null && selectedDeployment?.deployment.root_node_id !== undefined && <ul className="tree-resources">{renderOwnershipNodes(selectedDeployment.deployment.root_node_id)}</ul>}
                                                        </>;
                                                    })()}
                                                </li>
                                                {(selectedDeployment?.can_manage_groups || selectedDeployment?.can_manage_permissions) && <li>
                                                    {(() => {
                                                        const key = `${deployment.id}:users`;
                                                        const expanded = branchIsExpanded(key);
                                                        return <>
                                                            <button className={`tree-category-button${selectedSection === "users" ? " is-active" : ""}`} type="button" aria-expanded={expanded} onClick={() => { toggleBranch(key); selectSection("users"); }}><span className="tree-branch-disclosure">{expanded ? <ChevronDown size={13} /> : <ChevronRight size={13} />}</span><CircleUserRound size={14} /><span>Users</span><small>{deploymentAccess?.accounts.length ?? "—"}</small></button>
                                                            {expanded && <ul className="tree-resources">{deploymentAccess?.accounts.map((account) => <li key={account.id}><button className={`tree-resource tree-entity${selectedUserID === account.id ? " is-current" : ""}`} type="button" onClick={() => selectUser(account.id)}><CircleUserRound size={13} /><span>{account.display_name}</span></button></li>)}</ul>}
                                                        </>;
                                                    })()}
                                                </li>}
                                                {(selectedDeployment?.can_manage_groups || selectedDeployment?.can_manage_permissions) && <li>
                                                    {(() => {
                                                        const key = `${deployment.id}:groups`;
                                                        const expanded = branchIsExpanded(key);
                                                        return <>
                                                            <button className={`tree-category-button${selectedSection === "groups" ? " is-active" : ""}`} type="button" aria-expanded={expanded} onClick={() => { toggleBranch(key); selectSection("groups"); }}><span className="tree-branch-disclosure">{expanded ? <ChevronDown size={13} /> : <ChevronRight size={13} />}</span><Users size={14} /><span>Groups</span><small>{deploymentAccess?.groups.length ?? "—"}</small></button>
                                                            {expanded && <ul className="tree-resources">{deploymentAccess?.groups.map((group) => <li key={group.id}><button className={`tree-resource tree-entity${selectedGroupID === group.id ? " is-current" : ""}`} type="button" onClick={() => selectGroup(group.id)}><Users size={13} /><span>{group.name}</span></button></li>)}</ul>}
                                                        </>;
                                                    })()}
                                                </li>}
                                            </ul>
                                        )}
                                    </li>
                                );
                            })}
                        </ul>
                    )}
                </aside>

                <main className={`workspace-content${selectedResource?.kind === "virtual_machine" && selectedResource.can_console_control ? " has-vm-console" : ""}`}>
                    {selectedDeploymentID === null ? (
                        <div className="workspace-empty" aria-label={loadingDeployments ? "Loading workspace" : "No selection"}>
                            {loadingDeployments ? <p>Loading…</p> : <Boxes size={26} />}
                        </div>
                    ) : loadingDetails && !selectedDeployment ? (
                        <div className="workspace-empty panel" role="status"><p>Loading deployment details…</p></div>
                    ) : selectedDeployment ? (
                        <>
                            {selectedSection !== "resources" && deploymentAccess ? <DeploymentAccessPanel
                                section={selectedSection}
                                access={deploymentAccess}
                                selectedUserID={selectedUserID}
                                selectedGroupID={selectedGroupID}
                                onSelectUser={selectUser}
                                onSelectGroup={selectGroup}
                            /> : selectedSection !== "resources" ? <div className="workspace-empty panel" role="status"><p>Loading access…</p></div> : selectedResource ? (
                                <>
                                <section className={`resource-detail panel${selectedResource.kind === "virtual_machine" ? " vm-summary-panel" : ""}`} aria-labelledby="resource-detail-heading">
                                    <div className="resource-detail-heading">
                                        <div className="resource-detail-icon">{selectedResource.kind === "virtual_machine" ? <Server size={19} /> : <Network size={19} />}</div>
                                        <div><p className="eyebrow">{selectedResource.kind.replaceAll("_", " ")}</p><h3 id="resource-detail-heading">{selectedResource.name}</h3></div>
                                        {selectedResource.kind === "virtual_machine" ? <ResourcePowerControl resource={selectedResource} onPower={changePower} /> : selectedResource.kind !== "address_pool_request" && selectedResource.kind !== "virtual_network" && <span className={`power-state${selectedResource.power_state === "running" ? " is-running" : ""}`}>{selectedResource.power_state}</span>}
                                    </div>
                                    {selectedResource.kind !== "address_pool_request" && selectedResource.kind !== "virtual_network" && selectedResource.kind !== "virtual_machine" && <dl className="resource-facts">
                                        <div><dt>Resource ID</dt><dd>{selectedResource.id}</dd></div>
                                        <div><dt>Type</dt><dd>{selectedResource.kind.replaceAll("_", " ")}</dd></div>
                                        <div><dt>{selectedResource.kind === "virtual_machine" ? "Power state" : "Status"}</dt><dd>{selectedResource.power_state}</dd></div>
                                    </dl>}
                                    {selectedResource.kind === "virtual_machine" && <section className={`vm-details-grid${selectedResource.can_snapshot_control ? " has-snapshots" : ""}`}>
                                        <section className="vm-specs-panel" aria-label="Virtual machine specifications">
                                            <table className="address-properties-table"><tbody>
                                                <tr><th scope="row">Proxmox VMID</th><td>{selectedResource.external_id || "—"}</td></tr>
                                                <tr><th scope="row">Node</th><td>{selectedResource.external_node || "—"}</td></tr>
                                                <tr><th scope="row">Template</th><td>{selectedResourceDetails?.specification?.template_alias || "—"}</td></tr>
                                                <tr><th scope="row">CPU</th><td>{formatCPUSpecification(selectedResourceDetails?.specification)}</td></tr>
                                                <tr><th scope="row">Memory</th><td>{selectedResourceDetails?.specification?.memory_mib ? `${(selectedResourceDetails.specification.memory_mib / 1024).toLocaleString()} GiB` : "—"}</td></tr>
                                                <tr><th scope="row">Boot disk</th><td>{selectedResourceDetails?.specification?.boot_disk_gib ? `${selectedResourceDetails.specification.boot_disk_gib} GiB` : "—"}</td></tr>
                                            </tbody></table>
                                        </section>
                                        {selectedResource.can_snapshot_control && <VMSnapshotPanel resourceID={selectedResource.id} reservedGiB={selectedResourceDetails?.specification?.boot_disk_gib} request={request} onError={onError} />}
                                    </section>}
                                    {resourceDetailsLoading && <p className="resource-detail-loading" role="status">Loading resource details…</p>}
                                    {resourceDetailsError && <p className="resource-detail-error" role="alert">{resourceDetailsError}</p>}
                                    {selectedResource.kind === "virtual_network" && selectedResourceDetails?.configuration && "mode" in selectedResourceDetails.configuration.request && "vnet" in selectedResourceDetails.configuration.placement && <section className="typed-resource-details" aria-label="Virtual network details">
                                        <table className="address-properties-table"><tbody>
                                            <tr><th scope="row">Network mode</th><td>{selectedResourceDetails.configuration.request.mode === "managed" ? "Managed subnet" : "Unmanaged Layer 2"}</td></tr>
                                            {selectedResourceDetails.configuration.request.mode === "managed" && <>
                                                <tr><th scope="row">Subnet</th><td>{selectedResourceDetails.configuration.request.subnet || "—"}</td></tr>
                                                <tr><th scope="row">Gateway</th><td>{selectedResourceDetails.configuration.request.gateway || "—"}</td></tr>
                                                <tr><th scope="row">DHCP</th><td>{selectedResourceDetails.configuration.request.dhcp_enabled ? "Enabled" : "Disabled"}</td></tr>
                                            </>}
                                            <tr><th scope="row">Egress</th><td>{selectedResourceDetails.configuration.request.egress_policy}</td></tr>
                                            <tr><th scope="row">Proxmox VNet</th><td>{selectedResourceDetails.configuration.placement.vnet}</td></tr>
                                            <tr><th scope="row">SDN zone</th><td>{selectedResourceDetails.configuration.placement.zone}</td></tr>
                                            <tr><th scope="row">Proxmox state</th><td>{selectedResourceDetails.live_state ?? "unavailable"}</td></tr>
                                        </tbody></table>
                                        {selectedResourceDetails.configuration.request.mode === "managed" && <section className="address-usage-section ipam-section" aria-label="Virtual network IPAM">
                                            <h4>IPAM</h4>
                                            {selectedResourceDetails.live_state !== "verified" || selectedResourceDetails.ipam_state === "unavailable" ? <p className="ipam-empty">IPAM data unavailable</p> : selectedResourceDetails.ipam_state === "unconfigured" ? <p className="ipam-empty">No IPAM configured</p> : selectedResourceDetails.ipam_entries?.length ? <ul className="ipam-tree">
                                                <li><span>{selectedResourceDetails.configuration.placement.vnet}</span><ul>
                                                    {Array.from(new Set(selectedResourceDetails.ipam_entries.map((entry) => entry.subnet || "Unassigned"))).map((subnet) => <li key={subnet}><span>{subnet}</span><ul>
                                                        {selectedResourceDetails.ipam_entries?.filter((entry) => (entry.subnet || "Unassigned") === subnet).map((entry) => {
                                                            const guest = selectedDeployment?.resources.find((item) => item.kind === "virtual_machine" && item.external_id === entry.vmid);
                                                            return <li key={`${entry.ip}-${entry.mac ?? ""}`}><span><code>{entry.ip}</code>{entry.hostname ? ` · ${entry.hostname}` : ""}{entry.mac ? ` · ${entry.mac}` : ""}{guest ? <> · <button className="inline-link" type="button" onClick={() => setSelectedResourceID(guest.id)}>{guest.name} (Resource ID: {guest.id})</button></> : entry.vmid ? ` · VM ${entry.vmid}` : ""}</span></li>;
                                                        })}
                                                    </ul></li>)}
                                                </ul></li>
                                            </ul> : <p className="ipam-empty">No IPAM assignments</p>}
                                        </section>}
                                    </section>}
                                    {selectedResource.kind === "network_attachment" && selectedResourceDetails?.configuration && <section className="typed-resource-details" aria-label="Network attachment details">
                                        <h4>Network interface</h4>
                                        <p className={`resource-live-state is-${selectedResourceDetails.live_state ?? "unavailable"}`}>Proxmox state: {selectedResourceDetails.live_state ?? "unavailable"}</p>
                                        <dl className="resource-facts">
                                            {"device" in selectedResourceDetails.configuration.placement && <div><dt>Device</dt><dd>{selectedResourceDetails.configuration.placement.device}</dd></div>}
                                            {"mac" in selectedResourceDetails.configuration.placement && <div><dt>MAC address</dt><dd>{selectedResourceDetails.configuration.placement.mac}</dd></div>}
                                            {"bridge" in selectedResourceDetails.configuration.request && <div><dt>Network</dt><dd>{selectedResourceDetails.configuration.request.bridge}</dd></div>}
                                            {"addresses" in selectedResourceDetails.configuration && <div className="resource-address-list"><dt>Reserved addresses</dt><dd>{selectedResourceDetails.configuration.addresses.length ? selectedResourceDetails.configuration.addresses.map((address) => <code key={address}>{address}</code>) : "None"}</dd></div>}
                                            {"address_prefix" in selectedResourceDetails.configuration && <div><dt>Prefix</dt><dd>{selectedResourceDetails.configuration.address_prefix || "None"}</dd></div>}
                                            {"environment_network" in selectedResourceDetails.configuration && selectedResourceDetails.configuration.environment_network && <div><dt>Environment network</dt><dd>{selectedResourceDetails.configuration.environment_network}</dd></div>}
                                            {"requested_address_count" in selectedResourceDetails.configuration && selectedResourceDetails.configuration.requested_address_count ? <div><dt>Requested addresses</dt><dd>{selectedResourceDetails.configuration.requested_address_count}</dd></div> : null}
                                            {"address_gateway" in selectedResourceDetails.configuration && selectedResourceDetails.configuration.address_gateway && <div><dt>Gateway</dt><dd>{selectedResourceDetails.configuration.address_gateway}</dd></div>}
                                            {"address_dns" in selectedResourceDetails.configuration && selectedResourceDetails.configuration.address_dns?.length ? <div><dt>DNS</dt><dd>{selectedResourceDetails.configuration.address_dns.join(", ")}</dd></div> : null}
                                            {"guest_network" in selectedResourceDetails.configuration && <div><dt>Guest configuration</dt><dd>{selectedResourceDetails.configuration.guest_network?.ipv4_method ?? "Not configured"}</dd></div>}
                                        </dl>
                                    </section>}
                                    {selectedResource.kind === "address_pool_request" && selectedResourceDetails?.allocation && <section className="typed-resource-details" aria-label="Address allocation details">
                                        <table className="address-properties-table"><tbody>
                                            <tr><th scope="row">Environment Network</th><td>{selectedResourceDetails.allocation.environment_network}</td></tr>
                                            <tr><th scope="row">Pool</th><td>{selectedResourceDetails.allocation.pool_name}</td></tr>
                                            <tr><th scope="row">Address Family</th><td>{selectedResourceDetails.allocation.address_family}</td></tr>
                                            <tr><th scope="row">Guest Prefix</th><td>{selectedResourceDetails.allocation.prefix}</td></tr>
                                            <tr><th scope="row">Gateway</th><td>{selectedResourceDetails.allocation.gateway || "—"}</td></tr>
                                            <tr><th scope="row">DNS</th><td>{selectedResourceDetails.allocation.dns?.join(", ") || "—"}</td></tr>
                                            <tr><th scope="row">Requested Count</th><td>{selectedResourceDetails.allocation.address_count}</td></tr>
                                            <tr><th scope="row">Usable Range</th><td>{selectedResourceDetails.allocation.addresses[0]} – {selectedResourceDetails.allocation.addresses.at(-1)}</td></tr>
                                        </tbody></table>
                                        <section className="address-usage-section" aria-label="Allocated address usage">
                                            <h4>Addresses</h4>
                                            <div className="address-usage-scroll" tabIndex={0}>
                                                <table className="address-usage-table">
                                                    <thead><tr><th scope="col">Address</th><th scope="col">Use</th></tr></thead>
                                                    <tbody>{selectedResourceDetails.allocation.address_usage.map((usage) => <tr key={usage.address}>
                                                        <td><code>{usage.address}</code></td>
                                                        <td>{usage.in_use ? usage.virtual_machine_id ? <button className="inline-link" type="button" onClick={() => setSelectedResourceID(usage.virtual_machine_id ?? null)}>{usage.virtual_machine_name} (Resource ID: {usage.virtual_machine_id})</button> : "In use" : "Available"}</td>
                                                    </tr>)}</tbody>
                                                </table>
                                            </div>
                                        </section>
                                    </section>}
                                </section>
                                {selectedResource.kind === "virtual_machine" && selectedResource.can_console_control && <section className="vm-console-view panel" aria-label={`${selectedResource.name} console`}>
                                    <VMConsolePanel resourceID={selectedResource.id} resourceName={selectedResource.name} allowed={selectedResource.can_console_control} powerState={selectedResource.power_state} />
                                </section>}
                                </>
                            ) : (
                                <div className="workspace-empty" aria-label="No selection"><Boxes size={26} /></div>
                            )}
                        </>
                    ) : <div className="workspace-empty panel" role="status"><p>Deployment details could not be loaded.</p></div>}
                </main>
            </div>
        </section>
    );
}
