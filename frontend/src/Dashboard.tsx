import { useEffect, useState } from "react";
import { Boxes, ChevronDown, ChevronRight, CircleUserRound, KeyRound, RefreshCw, Server, Shield, Users } from "lucide-react";
import type { ApiRequest } from "./api";
import type { Deployment, DeploymentDetail, Resource } from "./types";
import { ResourcePowerControl } from "./ResourcePowerControl";

type Props = {
    request: ApiRequest;
    onError: (message: string) => void;
};

// Shows deployments in a resource tree and opens selected items in the workspace.
export function Dashboard({ request, onError }: Props) {
    const [deployments, setDeployments] = useState<Deployment[]>([]);
    const [selectedDeploymentID, setSelectedDeploymentID] = useState<number | null>(null);
    const [selectedDeployment, setSelectedDeployment] = useState<DeploymentDetail | null>(null);
    const [selectedResourceID, setSelectedResourceID] = useState<number | null>(null);
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
            return;
        }

        let active = true;
        setLoadingDetails(true);
        request<DeploymentDetail>(`/deployments/${selectedDeploymentID}`)
            .then((result) => active && setSelectedDeployment(result))
            .catch((requestError: Error) => active && onError(requestError.message))
            .finally(() => active && setLoadingDetails(false));
        return () => {
            active = false;
        };
    }, [request, onError, selectedDeploymentID]);

    async function refreshSelectedDeployment() {
        if (selectedDeploymentID === null) {
            await loadDeployments();
            return;
        }
        setLoadingDetails(true);
        try {
            setSelectedDeployment(await request<DeploymentDetail>(`/deployments/${selectedDeploymentID}`));
        } catch (requestError) {
            onError((requestError as Error).message);
        } finally {
            setLoadingDetails(false);
        }
    }

    async function changePower(resource: Resource) {
        try {
            await request(`/virtual-machines/${resource.id}/power`, "POST", {
                action: resource.power_state === "running" ? "stop" : "start"
            });
            await refreshSelectedDeployment();
        } catch (requestError) {
            onError((requestError as Error).message);
        }
    }

    const selectedResource = selectedDeployment?.resources.find((resource) => resource.id === selectedResourceID);
    const runningCount = selectedDeployment?.resources.filter((resource) => resource.power_state === "running").length ?? 0;
    const stoppedCount = selectedDeployment?.resources.filter((resource) => resource.power_state === "stopped").length ?? 0;

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
                                        }}>
                                            {isExpanded ? <ChevronDown size={15} /> : <ChevronRight size={15} />}
                                            <Boxes size={16} />
                                            <span>{deployment.name}</span>
                                        </button>
                                        {isExpanded && (
                                            <ul className="tree-branches">
                                                <li>
                                                    <div className="tree-category is-active"><Server size={14} /><span>Resources</span><small>{selectedDeployment?.resources.length ?? "—"}</small></div>
                                                    {resources.length > 0 && <ul className="tree-resources">
                                                        {resources.map((resource) => (
                                                            <li key={resource.id}>
                                                                <button className={`tree-resource${resource.id === selectedResourceID ? " is-current" : ""}`} type="button" onClick={() => setSelectedResourceID(resource.id)}>
                                                                    <span className={`tree-resource-state${resource.power_state === "running" ? " is-running" : ""}`} />
                                                                    <span>{resource.name}</span>
                                                                </button>
                                                            </li>
                                                        ))}
                                                    </ul>}
                                                </li>
                                                <li><div className="tree-category is-unavailable"><CircleUserRound size={14} /><span>Users</span><small>Later</small></div></li>
                                                <li><div className="tree-category is-unavailable"><Users size={14} /><span>Groups</span><small>Later</small></div></li>
                                                <li><div className="tree-category is-unavailable"><KeyRound size={14} /><span>Permissions</span><small>Later</small></div></li>
                                            </ul>
                                        )}
                                    </li>
                                );
                            })}
                        </ul>
                    )}
                    <p className="sidebar-footnote"><Shield size={13} /> Only resources visible to your account are listed.</p>
                </aside>

                <main className="workspace-content">
                    {selectedDeploymentID === null ? (
                        <div className="workspace-empty panel">
                            <Boxes size={26} />
                            <h2>{loadingDeployments ? "Loading your workspace" : "No deployments yet"}</h2>
                            <p>{loadingDeployments ? "Your accessible deployments will appear here." : "When a deployment is shared with your account, it will appear in this workspace."}</p>
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
                                <span className="workspace-tab is-current"><Server size={14} /> Resources</span>
                                <span className="workspace-tab is-unavailable"><CircleUserRound size={14} /> Users</span>
                                <span className="workspace-tab is-unavailable"><Users size={14} /> Groups</span>
                                <span className="workspace-tab is-unavailable"><KeyRound size={14} /> Permissions</span>
                            </div>

                            {selectedResource ? (
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
