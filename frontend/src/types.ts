export type Account = {
    id: number;
    qualified_name: string;
    display_name: string;
    platform_administrator: boolean;
};

export type Deployment = {
    id: number;
    name: string;
    description: string;
    root_node_id: number | null;
};

export type Resource = {
    id: number;
    ownership_id: number;
    name: string;
    kind: string;
    power_state: string;
    external_id?: string;
    external_node?: string;
    can_manage_resource: boolean;
    can_power_control: boolean;
    can_snapshot_control: boolean;
    can_console_control: boolean;
};

export type DeploymentDetail = {
    deployment: Deployment;
    resources: Resource[];
    ownership_nodes: OwnershipNode[];
    can_manage_configuration: boolean;
    can_manage_groups: boolean;
    can_manage_permissions: boolean;
};

export type DeploymentAccessAccount = {
    id: number;
    qualified_name: string;
    display_name: string;
};

export type DeploymentAccessGroup = {
    id: number;
    deployment_id: number;
    name: string;
    members: DeploymentAccessAccount[];
};

export type OwnershipNode = {
    id: number;
    deployment_id: number;
    parent_id: number | null;
    kind: number;
    name: string;
};

export type PermissionGrant = {
    id: number;
    subject_kind: number;
    subject_id: number;
    permission: string;
    inherit_descendants: boolean;
    target_node_id: number;
    subject_name: string;
    node_name: string;
};

export type DeploymentAccess = {
    accounts: DeploymentAccessAccount[];
    groups: DeploymentAccessGroup[];
    ownership_nodes: OwnershipNode[];
    permission_grants: PermissionGrant[];
    can_manage_configuration: boolean;
    can_manage_groups: boolean;
    can_manage_permissions: boolean;
};

export type AuthStatus = {
    setup_required: boolean;
    authenticated: boolean;
    realms?: string[];
    account?: Account;
};
