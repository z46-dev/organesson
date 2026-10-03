import { createElement } from "react";
import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, test } from "bun:test";
import { DeploymentAccessPanel } from "./DeploymentAccessPanel";

const access = {
    accounts: [{ id: 2, qualified_name: "alice@organesson", display_name: "Alice" }],
    groups: [{ id: 4, deployment_id: 1, name: "students", members: [{ id: 2, qualified_name: "alice@organesson", display_name: "Alice" }] }],
    ownership_nodes: [{ id: 7, deployment_id: 1, parent_id: null, kind: 0, name: "class-lab" }],
    permission_grants: [{ id: 9, subject_kind: 0, subject_id: 2, permission: "resource.view", inherit_descendants: true, target_node_id: 7, subject_name: "Alice", node_name: "class-lab" }, { id: 10, subject_kind: 1, subject_id: 4, permission: "vm.power_control", inherit_descendants: true, target_node_id: 7, subject_name: "students", node_name: "class-lab" }],
    can_manage_configuration: false,
    can_manage_groups: false,
    can_manage_permissions: true
};

describe("deployment access workspace", () => {
    test("shows user memberships, direct grants, inherited grants, and links to groups", () => {
        const props = { section: "users", access, selectedUserID: null, selectedGroupID: null, onSelectUser: () => undefined, onSelectGroup: () => undefined };
        const usersMarkup = renderToStaticMarkup(createElement(DeploymentAccessPanel, props));
        const userDetailsMarkup = renderToStaticMarkup(createElement(DeploymentAccessPanel, { ...props, selectedUserID: 2 }));

        expect(usersMarkup).toContain("aria-label=\"No selection\"");
        expect(usersMarkup).toContain("lucide-boxes");
        expect(usersMarkup).not.toContain("alice@organesson");
        expect(userDetailsMarkup).toContain("Direct permissions");
        expect(userDetailsMarkup).toContain("View resources");
        expect(userDetailsMarkup).toContain("From groups");
        expect(userDetailsMarkup).toContain("Control VM power");
        expect(userDetailsMarkup).toContain("via students");
        expect(userDetailsMarkup).not.toContain("<form");
        expect(userDetailsMarkup).not.toContain("Revoke");
        expect(userDetailsMarkup).not.toContain("Grant");
    });

    test("shows selected group details without offering edits", () => {
        const groupMarkup = renderToStaticMarkup(createElement(DeploymentAccessPanel, {
            section: "groups",
            access,
            selectedUserID: null,
            selectedGroupID: 4,
            onSelectUser: () => undefined,
            onSelectGroup: () => undefined
        }));

        expect(groupMarkup).toContain("Members");
        expect(groupMarkup).toContain("Alice");
        expect(groupMarkup).toContain("Permissions");
        expect(groupMarkup).not.toContain("<form");
        expect(groupMarkup).not.toContain("Delete");
        expect(groupMarkup).not.toContain("Grant");
    });
});
