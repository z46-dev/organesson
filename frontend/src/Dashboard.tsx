import { useEffect, useState } from "react";
import { Boxes, ChevronDown, ChevronRight, CircleUserRound, FolderTree, KeyRound, RefreshCw, Server, Users } from "lucide-react";
import type { ApiRequest } from "./api";
import type { Deployment, DeploymentAccess, DeploymentDetail, Resource } from "./types";
import { ResourcePowerControl } from "./ResourcePowerControl";
import { DeploymentAccessPanel } from "./DeploymentAccessPanel";
import { VMSnapshotPanel } from "./VMSnapshotPanel";

type WorkspaceSection = "resources" | "users" | "groups" | "permissions";

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
    const [selectedSection, setSelectedSection] = useState<WorkspaceSection>("resources");
    const [collapsedDeployments, setCollapsedDeployments] = useState<Record<number, boolean>>({});
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
    const runningCount = selectedDeployment?.resources.filter((resource) => resource.power_state === "running").length ?? 0;
    const stoppedCount = selectedDeployment?.resources.filter((resource) => resource.power_state === "stopped").length ?? 0;

    function selectSection(section: WorkspaceSection) {
        setSelectedResourceID(null);
        setSelectedSection(section);
    }

    function renderOwnershipNodes(parentNodeID: number): React.ReactNode {
        return selectedDeployment?.ownership_nodes.filter((node) => node.parent_id === parentNodeID).map((node) => {
            if (node.kind === 2) {
                const resource = selectedDeployment.resources.find((item) => item.ownership_id === node.id);
                if (!resource) {
                    return null;
                }
                return <li key={node.id}><button className={`tree-resource${resource.id === selectedResourceID ? " is-current" : ""}`} type="button" onClick={() => { selectSection("resources"); setSelectedResourceID(resource.id); }}><span className={`tree-resource-state${resource.power_state === "running" ? " is-running" : ""}`} /><span>{resource.name}</span></button></li>;
            }
            return <li className="tree-owner-group" key={node.id}><div><FolderTree size={13} /><span>{node.name}</span></div><ul className="tree-resources">{renderOwnershipNodes(node.id)}</ul></li>;
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
                                                    <button className={`tree-category-button${selectedSection === "resources" ? " is-active" : ""}`} type="button" onClick={() => selectSection("resources")}><Server size={14} /><span>Resources</span><small>{selectedDeployment?.resources.length ?? "—"}</small></button>
                                                    {resources.length > 0 && selectedDeployment?.deployment.root_node_id !== null && selectedDeployment?.deployment.root_node_id !== undefined && <ul className="tree-resources">{renderOwnershipNodes(selectedDeployment.deployment.root_node_id)}</ul>}
                                                </li>
                                                {selectedDeployment?.can_manage_groups && <li><button className={`tree-category-button${selectedSection === "users" ? " is-active" : ""}`} type="button" onClick={() => selectSection("users")}><CircleUserRound size={14} /><span>Users</span></button></li>}
                                                {selectedDeployment?.can_manage_groups && <li><button className={`tree-category-button${selectedSection === "groups" ? " is-active" : ""}`} type="button" onClick={() => selectSection("groups")}><Users size={14} /><span>Groups</span><small>{deploymentAccess?.groups.length ?? "—"}</small></button></li>}
                                                {selectedDeployment?.can_manage_permissions && <li><button className={`tree-category-button${selectedSection === "permissions" ? " is-active" : ""}`} type="button" onClick={() => selectSection("permissions")}><KeyRound size={14} /><span>Permissions</span><small>{deploymentAccess?.permission_grants.length ?? "—"}</small></button></li>}
                                            </ul>
                                        )}
                                    </li>
                                );
                            })}
                        </ul>
                    )}
                </aside>

                <main className="workspace-content">
                    {selectedDeploymentID === null ? (
                        <div className="workspace-empty panel">
                            <Boxes size={26} />
                            <h2>{loadingDeployments ? "Loading your workspace" : "No deployments yet"}</h2>
                            {loadingDeployments && <p>Loading your workspace…</p>}
                        </div>
                    ) : loadingDetails && !selectedDeployment ? (
                        <div className="workspace-empty panel" role="status"><p>Loading deployment details…</p></div>
                    ) : selectedDeployment ? (
                        <>
                            <div className="deployment-heading">
                                <div className="deployment-heading-copy">
                                    <p className="eyebrow">Deployment · {selectedDeployment.deployment.id}</p>
                                    <h2>{selectedResource?.name ?? selectedDeployment.deployment.name}</h2>
                                    <p>{selectedResource ? "Resource details and available controls." : selectedDeployment.deployment.description || "Deployment resources and access."}</p>
                                </div>
                                <button className="icon-button" type="button" onClick={refreshSelectedDeployment} aria-label="Refresh selected deployment" disabled={loadingDetails}><RefreshCw size={15} /></button>
                            </div>

                            <div className="workspace-tabs" aria-label="Deployment sections">
                                <button className={`workspace-tab${selectedSection === "resources" ? " is-current" : ""}`} type="button" onClick={() => selectSection("resources")}><Server size={14} /> Resources</button>
                                {selectedDeployment.can_manage_groups ? <button className={`workspace-tab${selectedSection === "users" ? " is-current" : ""}`} type="button" onClick={() => selectSection("users")}><CircleUserRound size={14} /> Users</button> : null}
                                {selectedDeployment.can_manage_groups ? <button className={`workspace-tab${selectedSection === "groups" ? " is-current" : ""}`} type="button" onClick={() => selectSection("groups")}><Users size={14} /> Groups</button> : null}
                                {selectedDeployment.can_manage_permissions ? <button className={`workspace-tab${selectedSection === "permissions" ? " is-current" : ""}`} type="button" onClick={() => selectSection("permissions")}><KeyRound size={14} /> Permissions</button> : null}
                            </div>

                            {selectedSection !== "resources" && deploymentAccess ? <DeploymentAccessPanel deploymentID={selectedDeploymentID} section={selectedSection} access={deploymentAccess} request={request} onChanged={refreshSelectedDeployment} onError={onError} /> : selectedSection !== "resources" ? <div className="workspace-empty panel" role="status"><p>Loading deployment access…</p></div> : selectedResource ? (
                                <section className="resource-detail panel" aria-labelledby="resource-detail-heading">
                                    <div className="resource-detail-heading">
                                        <div className="resource-detail-icon"><Server size={19} /></div>
                                        <div><p className="eyebrow">{selectedResource.kind.replaceAll("_", " ")}</p><h3 id="resource-detail-heading">{selectedResource.name}</h3></div>
                                        <span className={`power-state${selectedResource.power_state === "running" ? " is-running" : ""}`}>{selectedResource.power_state}</span>
                                    </div>
                                    <dl className="resource-facts">
                                        <div><dt>Resource ID</dt><dd>{selectedResource.id}</dd></div>
                                        <div><dt>Type</dt><dd>{selectedResource.kind.replaceAll("_", " ")}</dd></div>
                                        <div><dt>Power state</dt><dd>{selectedResource.power_state}</dd></div>
                                    </dl>
                                    <ResourcePowerControl resource={selectedResource} onPower={changePower} />
                                    {selectedResource.can_snapshot_control && <VMSnapshotPanel resourceID={selectedResource.id} request={request} onError={onError} />}
                                    {selectedResource.can_console_control && <div className="console-launch-row"><span>Console</span><a className="primary-action" href={`/console/${selectedResource.id}`}>Open console</a><a className="quiet-button" href={`/console/${selectedResource.id}`} target="_blank" rel="noreferrer">Pop out</a></div>}
                                </section>
                            ) : (
                                <section className="resource-overview panel" aria-labelledby="resource-list-heading">
                                    <div className="resource-overview-heading">
                                        <div><p className="eyebrow">Visible to you</p><h3 id="resource-list-heading">Resources</h3></div>
                                        <span className="resource-count">{selectedDeployment.resources.length} total</span>
                                    </div>
                                    <div className="resource-summary-grid">
                                        <div className="resource-summary"><span>Resources</span><strong>{selectedDeployment.resources.length}</strong></div>
                                        <div className="resource-summary"><span>Running</span><strong>{runningCount}</strong></div>
                                        <div className="resource-summary"><span>Stopped</span><strong>{stoppedCount}</strong></div>
                                    </div>
                                    {selectedDeployment.resources.length === 0 ? <div className="resource-list-empty"><Boxes size={20} /><p>No resources in this deployment are visible to you.</p></div> : (
                                        <ul className="resource-table">
                                            {selectedDeployment.resources.map((resource) => (
                                                <li key={resource.id}>
                                                    <button className="resource-table-row" type="button" onClick={() => setSelectedResourceID(resource.id)}>
                                                        <span className="resource-symbol"><Server size={16} /></span>
                                                        <span className="resource-copy"><strong>{resource.name}</strong><small>{resource.kind.replaceAll("_", " ")} · ID {resource.id}</small></span>
                                                        <span className={`power-state${resource.power_state === "running" ? " is-running" : ""}`}>{resource.power_state}</span>
                                                        <ChevronRight size={16} />
                                                    </button>
                                                </li>
                                            ))}
                                        </ul>
                                    )}
                                </section>
                            )}
                        </>
                    ) : <div className="workspace-empty panel" role="status"><p>Deployment details could not be loaded.</p></div>}
                </main>
            </div>
        </section>
    );
}
