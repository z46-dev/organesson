import { useState } from "react";
import type { ApiRequest } from "./api";
import type { DeploymentAccess, DeploymentAccessAccount, DeploymentAccessGroup } from "./types";

type Props = {
    deploymentID: number;
    section: "users" | "groups" | "permissions";
    access: DeploymentAccess;
    request: ApiRequest;
    onChanged: () => Promise<void>;
    onError: (message: string) => void;
};

const permissionCatalog: [string, string][] = [
    ["deployment.view", "View deployment"],
    ["deployment.manage_configuration", "Manage deployment configuration"],
    ["deployment.manage_groups", "Manage groups"],
    ["deployment.manage_permissions", "Manage permissions"],
    ["deployment.manage_users", "Manage users"],
    ["resource.view", "View resources"],
    ["resource.create", "Create resources"],
    ["vm.power_control", "Control VM power"],
    ["vm.console_control", "Control VM console"],
    ["vm.snapshot_control", "Manage VM snapshots"]
];

// Renders deployment-scoped user, group, and permission management.
export function DeploymentAccessPanel({ deploymentID, section, access, request, onChanged, onError }: Props) {
    const [groupName, setGroupName] = useState("");
    const [subject, setSubject] = useState("");
    const [permission, setPermission] = useState("resource.view");
    const [targetNodeID, setTargetNodeID] = useState("");
    const [saving, setSaving] = useState(false);

    async function createGroup(event: React.FormEvent<HTMLFormElement>) {
        event.preventDefault();
        setSaving(true);
        try {
            await request(`/deployments/${deploymentID}/user-groups`, "POST", { name: groupName, members: [] });
            setGroupName("");
            await onChanged();
        } catch (requestError) {
            onError((requestError as Error).message);
        } finally {
            setSaving(false);
        }
    }

    async function saveMembers(group: DeploymentAccessGroup, event: React.FormEvent<HTMLFormElement>) {
        event.preventDefault();
        const form = new FormData(event.currentTarget);
        const members = form.getAll("members").map((value) => String(value));
        setSaving(true);
        try {
            await request(`/user-groups/${group.id}/members`, "PUT", { members });
            await onChanged();
        } catch (requestError) {
            onError((requestError as Error).message);
        } finally {
            setSaving(false);
        }
    }

    async function createGrant(event: React.FormEvent<HTMLFormElement>) {
        event.preventDefault();
        const [subjectKind, subjectID] = subject.split(":");
        if (!subjectKind || !subjectID || !targetNodeID) {
            onError("Choose a subject and ownership group.");
            return;
        }
        setSaving(true);
        try {
            await request(`/ownership-nodes/${targetNodeID}/grants`, "POST", {
                subject_kind: Number(subjectKind),
                subject_id: Number(subjectID),
                permission,
                inherit_descendants: true
            });
            await onChanged();
        } catch (requestError) {
            onError((requestError as Error).message);
        } finally {
            setSaving(false);
        }
    }

    async function revokeGrant(grantID: number) {
        setSaving(true);
        try {
            await request(`/permission-grants/${grantID}`, "DELETE");
            await onChanged();
        } catch (requestError) {
            onError((requestError as Error).message);
        } finally {
            setSaving(false);
        }
    }

    async function deleteGroup(group: DeploymentAccessGroup) {
        if (!window.confirm(`Delete ${group.name} and its permission grants?`)) {
            return;
        }
        setSaving(true);
        try {
            await request(`/user-groups/${group.id}`, "DELETE");
            await onChanged();
        } catch (requestError) {
            onError((requestError as Error).message);
        } finally {
            setSaving(false);
        }
    }

    const groupsByID = new Map<number, DeploymentAccessGroup>(access.groups.map((group) => [group.id, group]));
    const accountsByID = new Map<number, DeploymentAccessAccount>(access.accounts.map((account) => [account.id, account]));
    const availablePermissions = permissionCatalog.filter(([name]) => access.can_manage_configuration || name !== "deployment.manage_configuration");

    if (section === "users") {
        return (
            <section className="access-panel panel" aria-labelledby="access-panel-title">
                <div className="resource-overview-heading"><div><p className="eyebrow">Deployment access</p><h3 id="access-panel-title">Users</h3></div><span className="resource-count">{access.accounts.length} available</span></div>
                {access.can_manage_groups ? <ul className="access-list">{access.accounts.map((account) => <li key={account.id}><strong>{account.display_name}</strong><span>{account.qualified_name}</span></li>)}</ul> : <p className="access-empty">You do not have permission to manage deployment users.</p>}
            </section>
        );
    }

    if (section === "groups") {
        return (
            <section className="access-panel panel" aria-labelledby="access-panel-title">
                <div className="resource-overview-heading"><div><p className="eyebrow">Deployment access</p><h3 id="access-panel-title">Groups</h3></div><span className="resource-count">{access.groups.length} groups</span></div>
                {access.can_manage_groups && <form className="access-inline-form" onSubmit={createGroup}><label>New group<input aria-label="New group name" value={groupName} onChange={(event) => setGroupName(event.target.value)} required maxLength={128} /></label><button className="primary-action" type="submit" disabled={saving || groupName.trim() === ""}>Create group</button></form>}
                {access.groups.length === 0 ? <p className="access-empty">No groups have been created.</p> : <ul className="access-groups">{access.groups.map((group) => <li key={group.id}><form onSubmit={(event) => saveMembers(group, event)}><div className="access-group-heading"><strong>{group.name}</strong><span>{group.members.length} members</span></div><label className="access-members-label">Members<select name="members" multiple defaultValue={group.members.map((member) => member.qualified_name)} disabled={!access.can_manage_groups}>{access.accounts.map((account) => <option key={account.qualified_name} value={account.qualified_name}>{account.display_name} · {account.qualified_name}</option>)}</select></label>{access.can_manage_groups && <div className="access-group-actions"><button className="secondary-action" type="submit" disabled={saving}>Save members</button><button className="text-action" type="button" disabled={saving} onClick={() => deleteGroup(group)}>Delete group</button></div>}</form></li>)}</ul>}
            </section>
        );
    }

    return (
        <section className="access-panel panel" aria-labelledby="access-panel-title">
            <div className="resource-overview-heading"><div><p className="eyebrow">Deployment access</p><h3 id="access-panel-title">Permissions</h3></div><span className="resource-count">{access.permission_grants.length} grants</span></div>
            {access.can_manage_permissions && <form className="access-grant-form" onSubmit={createGrant}>
                <label>Subject<select value={subject} onChange={(event) => setSubject(event.target.value)} required><option value="">Select user or group</option><optgroup label="Users">{access.accounts.map((account) => <option key={account.id} value={`0:${account.id}`}>{account.display_name} · {account.qualified_name}</option>)}</optgroup><optgroup label="Groups">{access.groups.map((group) => <option key={group.id} value={`1:${group.id}`}>{group.name}</option>)}</optgroup></select></label>
                <label>Permission<select value={permission} onChange={(event) => setPermission(event.target.value)}>{availablePermissions.map(([name, label]) => <option key={name} value={name}>{label}</option>)}</select></label>
                <label>Ownership node<select value={targetNodeID} onChange={(event) => setTargetNodeID(event.target.value)} required><option value="">Select group</option>{access.ownership_nodes.map((node) => <option key={node.id} value={node.id}>{node.name}</option>)}</select></label>
                <button className="primary-action" type="submit" disabled={saving || !subject || !targetNodeID}>Grant</button>
            </form>}
            {access.permission_grants.length === 0 ? <p className="access-empty">No explicit permission grants.</p> : <ul className="access-list">{access.permission_grants.map((grant) => <li key={grant.id}><div><strong>{grant.subject_name || groupsByID.get(grant.subject_id)?.name || accountsByID.get(grant.subject_id)?.qualified_name || "Unknown subject"}</strong><span>{grant.permission} · {grant.node_name}{grant.inherit_descendants ? " and descendants" : ""}</span></div>{access.can_manage_permissions && <button className="text-action" type="button" disabled={saving} onClick={() => revokeGrant(grant.id)}>Revoke</button>}</li>)}</ul>}
        </section>
    );
}
