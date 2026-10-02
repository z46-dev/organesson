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
    name: string;
    kind: string;
    power_state: string;
    can_power_control: boolean;
};

export type DeploymentDetail = {
    deployment: Deployment;
    resources: Resource[];
};

export type AuthStatus = {
    setup_required: boolean;
    authenticated: boolean;
    account?: Account;
};
