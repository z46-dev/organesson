import { Boxes, CircleUserRound, Users } from "lucide-react";
import type { DeploymentAccess, DeploymentAccessAccount, PermissionGrant } from "./types";

type Props = {
    section: "users" | "groups";
    access: DeploymentAccess;
    selectedUserID: number | null;
    selectedGroupID: number | null;
    onSelectUser: (accountID: number) => void;
    onSelectGroup: (groupID: number) => void;
};

function permissionName(permission: string) {
    const names: Record<string, string> = {
        "deployment.view": "View deployment",
        "deployment.manage": "Manage deployment",
        "resource.view": "View resources",
        "resource.manage": "Manage resources",
        "vm.power_control": "Control VM power",
        "vm.snapshot_control": "Manage VM snapshots",
        "vm.console": "Open VM console"
    };
    return names[permission] ?? permission.split(".").map((part) => part.replaceAll("_", " ")).join(" · ");
}

function GrantList({ grants, onSelectGroup }: { grants: PermissionGrant[]; onSelectGroup?: (groupID: number) => void }) {
    if (grants.length === 0) {
        return <p className="access-empty">No permissions.</p>;
    }

    return <ul className="access-list">
        {grants.map((grant) => <li key={grant.id}>
            <div className="access-grant-description">
                <strong>{permissionName(grant.permission)}</strong>
                <span>{grant.node_name}{grant.inherit_descendants ? " and children" : ""}</span>
                {grant.subject_kind === 1 && onSelectGroup && <button className="inline-link" type="button" onClick={() => onSelectGroup(grant.subject_id)}>via {grant.subject_name}</button>}
            </div>
        </li>)}
    </ul>;
}

function AccountLink({ account, onSelectUser }: { account: DeploymentAccessAccount; onSelectUser: (accountID: number) => void }) {
    return <button className="access-entity-link" type="button" onClick={() => onSelectUser(account.id)}>
        <CircleUserRound size={14} />
        <strong>{account.display_name}</strong>
        <span>{account.qualified_name}</span>
    </button>;
}

// Shows deployment ownership and permission data without changing OpenTofu-managed configuration.
export function DeploymentAccessPanel({ section, access, selectedUserID, selectedGroupID, onSelectUser, onSelectGroup }: Props) {
    if (section === "users") {
        const account = access.accounts.find((item) => item.id === selectedUserID);
        if (!account) {
            return <div className="workspace-empty" aria-label="No selection"><Boxes size={26} /></div>;
        }

        const memberships = access.groups.filter((group) => group.members.some((member) => member.id === account.id));
        const directGrants = access.permission_grants.filter((grant) => grant.subject_kind === 0 && grant.subject_id === account.id);
        const inheritedGrants = access.permission_grants.filter((grant) => grant.subject_kind === 1 && memberships.some((group) => group.id === grant.subject_id));

        return <section className="access-panel panel user-detail-panel" aria-label={`User ${account.display_name}`}>
            <header className="resource-detail-heading">
                <div className="resource-detail-icon"><CircleUserRound size={18} /></div>
                <div><p className="eyebrow">User</p><h3>{account.display_name}</h3></div>
                <span className="account-qualified-name">{account.qualified_name}</span>
            </header>
            <section className="access-detail-section">
                <h4>Groups</h4>
                {memberships.length ? <ul className="access-membership-list">{memberships.map((group) => <li key={group.id}><button className="access-entity-link" type="button" onClick={() => onSelectGroup(group.id)}><Users size={14} /><strong>{group.name}</strong></button></li>)}</ul> : <p className="access-empty">No group memberships.</p>}
            </section>
            <section className="access-detail-section">
                <h4>Direct permissions</h4>
                {access.can_manage_permissions ? <GrantList grants={directGrants} /> : <p className="access-empty">Permission details are not available.</p>}
            </section>
            <section className="access-detail-section">
                <h4>From groups</h4>
                {access.can_manage_permissions ? <GrantList grants={inheritedGrants} onSelectGroup={onSelectGroup} /> : <p className="access-empty">Permission details are not available.</p>}
            </section>
        </section>;
    }

    const group = access.groups.find((item) => item.id === selectedGroupID);
    if (!group) {
        return <div className="workspace-empty" aria-label="No selection"><Boxes size={26} /></div>;
    }

    const grants = access.permission_grants.filter((grant) => grant.subject_kind === 1 && grant.subject_id === group.id);
    return <section className="access-panel panel group-detail-panel" aria-label={`Group ${group.name}`}>
        <header className="resource-detail-heading">
            <div className="resource-detail-icon"><Users size={18} /></div>
            <div><p className="eyebrow">Group</p><h3>{group.name}</h3></div>
        </header>
        <section className="access-detail-section">
            <h4>Members</h4>
            {group.members.length ? <ul className="access-membership-list">{group.members.map((member) => <li key={member.id}><AccountLink account={member} onSelectUser={onSelectUser} /></li>)}</ul> : <p className="access-empty">No members.</p>}
        </section>
        <section className="access-detail-section">
            <h4>Permissions</h4>
            {access.can_manage_permissions ? <GrantList grants={grants} /> : <p className="access-empty">Permission details are not available.</p>}
        </section>
    </section>;
}
