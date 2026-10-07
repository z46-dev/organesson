import { useEffect, useRef, useState } from "react";
import { Boxes, ChevronDown, ChevronRight, CircleUserRound, FolderTree, ListTree, Network, RefreshCw, Router, Server, Users } from "lucide-react";
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
    logical_network_id?: number;
    range_start?: string;
    range_end?: string;
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
type ObservedRouterAddress = { address: string; mac?: string; hostname?: string; source?: string; last_seen?: string; lease_expires_at?: string; virtual_machine_id?: number; virtual_machine_name?: string };
type StaticNetworkAddress = { address: string; mac?: string; network_attachment_id: number; network_attachment: string; virtual_machine_id?: number; virtual_machine?: string };
type RouterPollingDetails = { state: "not_configured" | "available" | "unavailable"; router_vmid?: number; last_polled_at?: string; dhcp_range_start?: string; dhcp_range_end?: string; dhcpv6_range_start?: string; dhcpv6_range_end?: string; egress?: { interface: string; mac?: string; bridge?: string; addresses?: string[]; ipv6_addresses?: string[]; gateway?: string; ipv6_gateway?: string }; observed_addresses: ObservedRouterAddress[]; static_addresses: StaticNetworkAddress[] };
type NetworkAddressRow = { address: string; mac?: string; virtual_machine_id?: number; virtual_machine_name?: string; lease_expires_at?: string; is_dhcp: boolean };
type VMSpecification = { template_alias: string; sockets: number; cores: number; architecture: string; cpu_model: string; memory_mib: number; boot_disk_gib: number; pool: string; storage: string };
type NetworkDetails = { request: { mode: string; subnet?: string; gateway?: string; ipv6_subnet?: string; ipv6_gateway?: string; dhcp_enabled: boolean; egress_policy: string; router_vmid?: number }; placement: { zone: string; vnet: string } };
type AttachmentDetails = { request: { node: string; vmid: string; bridge: string }; placement: { device: string; mac: string }; addresses: string[]; ipv6_addresses?: string[]; environment_network?: string; logical_network_id?: number; address_pool_request_id?: number; requested_address_count?: number; ipv6_address_prefix?: string; ipv6_address_gateway?: string; ipv6_address_dns?: string[]; address_prefix?: string; address_gateway?: string; address_dns?: string[]; guest_network?: { ipv4_method: string; ipv4_address?: string; ipv4_gateway?: string; ipv4_dns?: string[]; ipv6_method?: string; ipv6_address?: string; ipv6_gateway?: string; ipv6_dns?: string[] } };
type SelectedResourceDetails = { allocation?: AddressPoolDetails; configuration?: NetworkDetails | AttachmentDetails; specification?: VMSpecification; live_state?: ResourceLiveState; router_polling?: RouterPollingDetails };

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

function networkAddressRows(polling?: RouterPollingDetails) {
    const rows = new Map<string, NetworkAddressRow>();

    for (const entry of polling?.static_addresses ?? []) {
        const address = entry.address.split("/")[0] ?? entry.address;
        rows.set(address, {
            address,
            mac: entry.mac,
            virtual_machine_id: entry.virtual_machine_id,
            virtual_machine_name: entry.virtual_machine,
            is_dhcp: false
        });
    }

    for (const entry of polling?.observed_addresses ?? []) {
        const address = entry.address.split("/")[0] ?? entry.address;
        const current = rows.get(address);
        rows.set(address, {
            address,
            mac: entry.mac || current?.mac,
            virtual_machine_id: entry.virtual_machine_id || current?.virtual_machine_id,
            virtual_machine_name: entry.virtual_machine_name || current?.virtual_machine_name,
            lease_expires_at: current && !current.is_dhcp ? undefined : entry.lease_expires_at,
            is_dhcp: current && !current.is_dhcp ? false : entry.source?.includes("lease") ?? false
        });
    }

    return [...rows.values()].sort((left, right) => {
        const leftOctets = left.address.split(".").map(Number);
        const rightOctets = right.address.split(".").map(Number);
        for (let index = 0; index < 4; index += 1) {
            const leftOctet = leftOctets[index] ?? 0;
            const rightOctet = rightOctets[index] ?? 0;
            if (leftOctet !== rightOctet) {
                return leftOctet - rightOctet;
            }
        }
        return 0;
    });
}

type Props = {
    request: ApiRequest;
    onError: (message: string) => void;
};

// Shows deployments in a resource tree and opens selected items in the workspace.
export function Dashboard({ request, onError }: Props) {
    const initialResourceLink = useRef<{ deploymentID: number; resourceID: number } | null>(null);
    if (initialResourceLink.current === null) {
        const parameters = new URLSearchParams(window.location.search);
        const deploymentID = Number(parameters.get("deployment"));
        const resourceID = Number(parameters.get("resource"));
        if (Number.isInteger(deploymentID) && deploymentID > 0 && Number.isInteger(resourceID) && resourceID > 0) {
            initialResourceLink.current = { deploymentID, resourceID };
        }
    }
    const [deployments, setDeployments] = useState<Deployment[]>([]);
    const [selectedDeploymentID, setSelectedDeploymentID] = useState<number | null>(() => {
        const requestedID = Number(new URLSearchParams(window.location.search).get("deployment"));
        return Number.isInteger(requestedID) && requestedID > 0 ? requestedID : null;
    });
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
            .then((result) => {
                if (!active) return;
                setSelectedDeployment(result);
                if (initialResourceLink.current?.deploymentID === selectedDeploymentID && result.resources.some((resource) => resource.id === initialResourceLink.current?.resourceID)) {
                    setSelectedResourceID(initialResourceLink.current.resourceID);
                    initialResourceLink.current = null;
                    window.history.replaceState({}, "", "/");
                }
            })
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
        async function loadDetails(reportErrors: boolean) {
            try {
                const result = await request<{ configuration?: NetworkDetails | AttachmentDetails; allocation?: AddressPoolDetails; specification?: VMSpecification; live_state?: ResourceLiveState; router_polling?: RouterPollingDetails }>(path);
                if (active) {
                    setSelectedResourceDetails(result);
                    setResourceDetailsError("");
                }
            } catch (requestError) {
                if (active && reportErrors) {
                    setResourceDetailsError((requestError as Error).message);
                    onError((requestError as Error).message);
                }
            } finally {
                if (active && reportErrors) {
                    setResourceDetailsLoading(false);
                }
            }
        }

        void loadDetails(true);
        const refreshTimer = resource.kind === "virtual_network" ? window.setInterval(() => void loadDetails(false), 30000) : undefined;
        return () => {
            active = false;
            if (refreshTimer !== undefined) {
                window.clearInterval(refreshTimer);
            }
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
    const selectedNetworkAddresses = networkAddressRows(selectedResourceDetails?.router_polling);
    const selectedAddressAllocation = selectedResourceDetails?.allocation;
    const selectedAddressSource = selectedAddressAllocation?.environment_network || selectedDeployment?.resources.find((resource) => resource.id === selectedAddressAllocation?.logical_network_id)?.name || `Resource ${selectedAddressAllocation?.logical_network_id ?? "—"}`;
    const selectedAddressPool = selectedAddressAllocation?.logical_network_id
        ? `${selectedAddressAllocation.range_start} – ${selectedAddressAllocation.range_end}`
        : selectedAddressAllocation?.pool_name ?? "—";
    const selectedAddressUsableRange = selectedAddressAllocation?.addresses.length
        ? `${selectedAddressAllocation.addresses[0]} – ${selectedAddressAllocation.addresses.at(-1)}`
        : "—";

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
        const ownershipNodes = selectedDeployment?.ownership_nodes
            .filter((node) => node.parent_id === parentNodeID)
            .map((node) => ({
                node,
                resource: node.kind === 2 ? selectedDeployment.resources.find((item) => item.ownership_id === node.id) : undefined
            })) ?? [];
        const sortedOwnershipNodes = ownershipNodes.sort((left, right) => {
            const leftOrder = left.node.kind !== 2 ? 0 : left.resource?.kind === "virtual_machine" ? 1 : 2;
            const rightOrder = right.node.kind !== 2 ? 0 : right.resource?.kind === "virtual_machine" ? 1 : 2;
            if (leftOrder !== rightOrder) {
                return leftOrder - rightOrder;
            }

            const leftName = left.resource?.name ?? left.node.name;
            const rightName = right.resource?.name ?? right.node.name;
            return leftName.localeCompare(rightName, undefined, { numeric: true, sensitivity: "base" });
        });

        return sortedOwnershipNodes.map(({ node, resource }) => {
            if (node.kind === 2) {
                // Network interfaces are VM configuration, not standalone tree resources.
                if (!resource || resource.kind === "network_attachment") {
                    return null;
                }
                const ResourceIcon = resource.kind === "virtual_machine" ? Server : resource.kind === "virtual_network" ? Router : resource.kind === "address_pool_request" ? ListTree : Network;
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
                                const resources = isSelected ? selectedDeployment?.resources.filter((resource) => resource.kind !== "network_attachment") ?? [] : [];
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
                                                            <button className={`tree-category-button${selectedSection === "resources" ? " is-active" : ""}`} type="button" aria-expanded={expanded} onClick={() => { toggleBranch(key); selectSection("resources"); }}><span className="tree-branch-disclosure">{expanded ? <ChevronDown size={13} /> : <ChevronRight size={13} />}</span><Server size={14} /><span>Resources</span><small>{selectedDeployment ? selectedDeployment.resources.filter((resource) => resource.kind !== "network_attachment").length : "—"}</small></button>
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
                                <section className={`resource-detail panel${selectedResource.kind === "virtual_machine" ? " vm-summary-panel" : ""}${selectedResource.kind === "virtual_network" && selectedResourceDetails?.configuration && "mode" in selectedResourceDetails.configuration.request && selectedResourceDetails.configuration.request.mode === "managed" ? " managed-network-panel" : ""}`} aria-labelledby="resource-detail-heading">
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
                                        {selectedResource.can_snapshot_control && <VMSnapshotPanel resourceID={selectedResource.id} request={request} onError={onError} />}
                                    </section>}
                                    {resourceDetailsLoading && <p className="resource-detail-loading" role="status">Loading resource details…</p>}
                                    {resourceDetailsError && <p className="resource-detail-error" role="alert">{resourceDetailsError}</p>}
                                    {selectedResource.kind === "virtual_network" && selectedResourceDetails?.configuration && "mode" in selectedResourceDetails.configuration.request && "vnet" in selectedResourceDetails.configuration.placement && <section className="typed-resource-details" aria-label="Virtual network details">
                                        <div className="network-property-columns">
                                            <table className="address-properties-table network-properties-table"><tbody>
                                                <tr><th scope="row">Network mode</th><td>{selectedResourceDetails.configuration.request.mode === "managed" ? "Managed" : "Unmanaged Layer 2"}</td></tr>
                                                {selectedResourceDetails.configuration.request.mode === "managed" && <>
                                                    {(selectedResourceDetails.configuration.request.subnet || selectedResourceDetails.configuration.request.ipv6_subnet) && <tr><th scope="row">Subnet</th><td>{[selectedResourceDetails.configuration.request.subnet, selectedResourceDetails.configuration.request.ipv6_subnet].filter(Boolean).map((subnet) => <code key={subnet}>{subnet}</code>)}</td></tr>}
                                                    {(selectedResourceDetails.configuration.request.gateway || selectedResourceDetails.configuration.request.ipv6_gateway) && <tr><th scope="row">Gateway</th><td>{[selectedResourceDetails.configuration.request.gateway, selectedResourceDetails.configuration.request.ipv6_gateway].filter(Boolean).map((gateway) => <code key={gateway}>{gateway}</code>)}</td></tr>}
                                                </>}
                                                <tr><th scope="row">Proxmox VNet</th><td>{selectedResourceDetails.configuration.placement.vnet}</td></tr>
                                                <tr><th scope="row">SDN zone</th><td>{selectedResourceDetails.configuration.placement.zone}</td></tr>
                                            </tbody></table>
                                            <table className="address-properties-table network-properties-table"><tbody>
                                                {selectedResourceDetails.configuration.request.mode === "managed" && <tr><th scope="row">DHCP</th><td><div className="network-dhcp-value"><span>{selectedResourceDetails.configuration.request.dhcp_enabled ? "Enabled" : "Disabled"}</span>{selectedResourceDetails.configuration.request.dhcp_enabled && selectedResourceDetails.router_polling?.dhcp_range_start && selectedResourceDetails.router_polling.dhcp_range_end ? <code>v4 {selectedResourceDetails.router_polling.dhcp_range_start}–{selectedResourceDetails.router_polling.dhcp_range_end}</code> : null}{selectedResourceDetails.router_polling?.dhcpv6_range_start && selectedResourceDetails.router_polling.dhcpv6_range_end ? <code>v6 {selectedResourceDetails.router_polling.dhcpv6_range_start}–{selectedResourceDetails.router_polling.dhcpv6_range_end}</code> : null}</div></td></tr>}
                                                <tr><th scope="row">Egress</th><td>{selectedResourceDetails.router_polling?.egress ? <table className="network-egress-table"><tbody>
                                                    <tr><th scope="row">SDN policy</th><td>{selectedResourceDetails.configuration.request.egress_policy === "isolated" ? "Isolated" : selectedResourceDetails.configuration.request.egress_policy}</td></tr>
                                                        <tr><th scope="row">NAT uplink</th><td>{[selectedResourceDetails.router_polling.egress.bridge, selectedResourceDetails.router_polling.egress.interface].filter(Boolean).join(" · ") || "Router interface"}</td></tr>
                                                        {selectedResourceDetails.router_polling.egress.addresses?.length || selectedResourceDetails.router_polling.egress.ipv6_addresses?.length ? <tr><th scope="row">Address</th><td>{[...(selectedResourceDetails.router_polling.egress.addresses ?? []), ...(selectedResourceDetails.router_polling.egress.ipv6_addresses ?? [])].map((address) => <code key={address}>{address}</code>)}</td></tr> : null}
                                                        {(selectedResourceDetails.router_polling.egress.gateway || selectedResourceDetails.router_polling.egress.ipv6_gateway) ? <tr><th scope="row">Gateway</th><td>{[selectedResourceDetails.router_polling.egress.gateway, selectedResourceDetails.router_polling.egress.ipv6_gateway].filter(Boolean).map((gateway) => <code key={gateway}>{gateway}</code>)}</td></tr> : null}
                                                </tbody></table> : selectedResourceDetails.configuration.request.egress_policy === "isolated" ? "Isolated" : selectedResourceDetails.configuration.request.egress_policy}</td></tr>
                                                <tr><th scope="row">Proxmox state</th><td>{selectedResourceDetails.live_state ?? "unavailable"}</td></tr>
                                                {selectedResourceDetails.configuration.request.router_vmid ? <tr><th scope="row">Router VM</th><td>{selectedResourceDetails.configuration.request.router_vmid}</td></tr> : null}
                                            </tbody></table>
                                        </div>
                                        {selectedResourceDetails.configuration.request.mode === "managed" && <section className="address-usage-section router-polling-section" aria-label="Router polling and configured static addresses">
                                            <div className="router-polling-heading"><h4><Router size={15} />Router Polling</h4><span className="router-polling-as-of">{selectedResourceDetails.router_polling?.state === "available" && selectedResourceDetails.router_polling.last_polled_at ? `As of ${new Date(selectedResourceDetails.router_polling.last_polled_at).toLocaleTimeString()}` : selectedResourceDetails.router_polling?.state === "unavailable" ? "Unavailable" : "Not configured"}</span></div>
                                            {selectedResourceDetails.router_polling?.state === "unavailable" ? <p className="router-polling-note">The configured router could not be queried.</p> : null}
                                            <div className="address-usage-scroll router-addresses">
                                                <table className="address-usage-table">
                                                    <thead><tr><th scope="col">Address</th><th scope="col">MAC</th><th scope="col">VM</th><th scope="col">Lease</th></tr></thead>
                                                    <tbody>
                                                        {selectedNetworkAddresses.length ? selectedNetworkAddresses.map((entry) => {
                                                            const guest = selectedDeployment?.resources.find((item) => item.id === entry.virtual_machine_id);
                                                            const virtualMachineName = entry.virtual_machine_name || guest?.name;
                                                            return <tr key={entry.address}>
                                                                <td><code>{entry.address}</code></td>
                                                                <td>{entry.mac || "—"}</td>
                                                                <td>{guest ? <button className="inline-link" type="button" onClick={() => { setSelectedSection("resources"); setSelectedResourceID(guest.id); }}>{virtualMachineName || guest.name} ({guest.id})</button> : virtualMachineName ? `${virtualMachineName}${entry.virtual_machine_id ? ` (${entry.virtual_machine_id})` : ""}` : "—"}</td>
                                                                <td>{entry.is_dhcp && entry.lease_expires_at ? new Date(entry.lease_expires_at).toLocaleString() : entry.is_dhcp ? "Active" : "—"}</td>
                                                            </tr>;
                                                        }) : <tr><td colSpan={4} className="router-polling-empty">No addresses reported or configured.</td></tr>}
                                                    </tbody>
                                                </table>
                                            </div>
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
                                            <tr><th scope="row">{selectedAddressAllocation?.environment_network ? "Environment Network" : "Managed VNet"}</th><td>{selectedAddressSource}</td></tr>
                                            <tr><th scope="row">Pool</th><td>{selectedAddressPool}</td></tr>
                                            <tr><th scope="row">Address Family</th><td>{selectedResourceDetails.allocation.address_family}</td></tr>
                                            <tr><th scope="row">Guest Prefix</th><td>{selectedResourceDetails.allocation.prefix}</td></tr>
                                            <tr><th scope="row">Gateway</th><td>{selectedResourceDetails.allocation.gateway || "—"}</td></tr>
                                            <tr><th scope="row">DNS</th><td>{selectedResourceDetails.allocation.dns?.join(", ") || "—"}</td></tr>
                                            <tr><th scope="row">Requested Count</th><td>{selectedResourceDetails.allocation.address_count}</td></tr>
                                            <tr><th scope="row">Usable Range</th><td>{selectedAddressUsableRange}</td></tr>
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
