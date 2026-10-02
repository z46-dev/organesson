import { createElement } from "react";
import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, test } from "bun:test";
import { DeploymentAccessPanel } from "./DeploymentAccessPanel";

const access = {
    accounts: [{ id: 2, qualified_name: "alice@organesson", display_name: "Alice" }],
    groups: [{ id: 4, deployment_id: 1, name: "students", members: [] }],
    ownership_nodes: [{ id: 7, deployment_id: 1, parent_id: null, kind: 0, name: "class-lab" }],
    permission_grants: [],
    can_manage_configuration: false,
    can_manage_groups: false,
    can_manage_permissions: true
};

describe("deployment access workspace", () => {
    test("shows available users and limits managers to permissions they may delegate", () => {
        const usersMarkup = renderToStaticMarkup(createElement(DeploymentAccessPanel, { deploymentID: 1, section: "users", access, request: async () => ({}), onChanged: async () => undefined, onError: () => undefined }));
        const permissionsMarkup = renderToStaticMarkup(createElement(DeploymentAccessPanel, { deploymentID: 1, section: "permissions", access, request: async () => ({}), onChanged: async () => undefined, onError: () => undefined }));

        expect(usersMarkup).toContain("do not have permission to manage deployment users");
        expect(permissionsMarkup).toContain("alice@organesson");
        expect(permissionsMarkup).toContain("Control VM power");
        expect(permissionsMarkup).not.toContain("Manage deployment configuration");
        expect(permissionsMarkup).toContain("Manage permissions</option>");
    });
});
