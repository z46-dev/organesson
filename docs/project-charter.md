# Organesson Project Charter

> Status: Draft template
>
> Owner: `[name or group]`
>
> Last reviewed: `[YYYY-MM-DD]`

## 1. Purpose

Organesson is a provisioning and management system for infrastructure hosted in Proxmox VE (PVE). It gives operators a consistent way to organize resources, delegate access, and automate lifecycle work without taking exclusive control of a PVE cluster.

### Problem statement

`[Describe the current operational problem, who experiences it, and the consequences of leaving it unsolved.]`

### Intended outcome

`[Describe what a successful deployment lets users accomplish.]`

## 2. Scope

### In scope for the first release

- Register one or more PVE environments.
- Discover, identify, and manage Organesson-owned virtual machines, containers, and virtual networks.
- Organize managed resources into an ownership tree.
- Grant people and groups access without changing the single accountable owner of a resource.
- Provision supported resources from approved templates.
- Record resource lifecycle actions and access changes in an audit trail.

### Explicitly out of scope

- Replacing the native PVE UI, permissions system, or cluster administration tools.
- Assuming exclusive use of a PVE cluster.
- Managing every existing resource in a registered PVE environment.
- Bare-metal provisioning.
- `[Add exclusions that protect the initial release from scope expansion.]`

### Later candidates

- PVE console access and guest-agent execution.
- FreeIPA/LDAP identity synchronization.
- OpenTofu/Terraform and Ansible integrations.
- Importing existing PVE resources into Organesson management.
- `[Add candidates.]`

## 3. Core domain model

### Environment

An **environment** is a connection to a PVE cluster or standalone PVE host. Registering an environment does not grant Organesson ownership of the cluster, and does not prevent other PVE administrators from creating or managing their own resources.

Organesson must only act on resources it can positively identify as managed by Organesson. Identification method for the first release:

- [ ] PVE tags, such as `organesson` and an environment-scoped identifier.
- [ ] Reserved naming prefix or VMID range.
- [ ] Both tags and a naming/ID convention.

Required safety rules:

- Resources without the Organesson management marker are read-only or invisible to normal Organesson users.
- Discovery must never alter an unmanaged PVE resource.
- Provisioning must create the selected management marker before reporting success.
- Destructive actions must verify the management marker immediately before execution.
- PVE-side permissions used by Organesson must be limited to the intended nodes, storage, pools, and resources where possible.

### Ownership tree

Every managed resource has exactly one owner. Ownership is represented by a single location in a tree, not by a list of owners.

Suggested shape:

```text
Organization
└── Group
    └── Workspace / Project / Course instance
        └── Managed resource
            ├── VM or container
            └── Virtual network
```

Terms to decide:

| Concept | Proposed meaning | Decision needed |
| --- | --- | --- |
| Organization | Top-level administrative boundary. | Can one deployment host more than one organization? |
| Group | A set of people that may own a workspace or receive access. | Are groups nested? Can identity-provider groups be used? |
| Workspace | A purpose-bound collection of resources, such as a course, team, lab, or project. | Is this called workspace, project, tenancy, or something else? |
| Resource | A managed VM, container, network, or future supported type. | Which types are included in v1? |
| Owner | The one tree node accountable for a resource. | Must the owner always be a group/workspace, never an individual? |

Recommended rule: the owner is an ownership node (normally a workspace or group), rather than a person. A resource changes owner only by an explicit transfer that is audited. This preserves one clear accountable party even when membership changes.

### Access grants

Access and ownership are different concepts:

- A resource has one owner.
- A person or group may receive zero or more access grants.
- A grant contains a role, scope, and optional expiry.
- A grant may be applied to a tree node and inherited by descendants, unless a more restrictive rule is added later.
- Direct grants should be exceptional and visible in the UI.

Initial role candidates:

| Role | Intended capabilities | Decisions needed |
| --- | --- | --- |
| Viewer | View resource metadata, status, and permitted activity. | Can view console output? |
| Operator | Perform day-to-day non-destructive resource actions. | Start/stop/reboot? Snapshot? Console? |
| Maintainer | Modify resource configuration within policy. | Can provision and delete? |
| Owner administrator | Manage child resources, grants, and transfers for an ownership node. | May change its parent or delete it? |
| Organization administrator | Manage environments, policy, and all ownership nodes. | Separation from PVE administrator? |

## 4. Users and user stories

### Platform administrator

- As a platform administrator, I can register a PVE environment with constrained credentials so that Organesson can manage only intended infrastructure.
- As a platform administrator, I can see which PVE resources are managed by Organesson and which are unmanaged.
- As a platform administrator, I can review an audit trail before investigating an unexpected change.

### Group or workspace administrator

- As a workspace administrator, I can create a workspace beneath my group so that resources have a clear accountable owner.
- As a workspace administrator, I can grant a person or group a role on my workspace without transferring ownership.
- As a workspace administrator, I can transfer a resource to another authorized ownership node with an audited confirmation.

### Operator

- As an operator, I can see resources I have been granted access to, even if I do not own them.
- As an operator, I can perform actions permitted by my role without seeing unrelated resources on the same PVE environment.

### PVE administrator outside Organesson

- As an existing PVE administrator, I can continue to create and operate non-Organesson resources without Organesson modifying or claiming them.

## 5. Functional requirements

### Environment management

- [ ] The system shall store the PVE endpoint, authentication reference, cluster identity, and allowed scope for each environment.
- [ ] The system shall verify connectivity and report a useful health status.
- [ ] The system shall support a documented resource-identification strategy for each environment.
- [ ] The system shall distinguish managed, unmanaged, and ambiguous resources.

### Ownership and authorization

- [ ] The system shall require exactly one ownership node for every managed resource.
- [ ] The system shall prevent ownership-tree cycles.
- [ ] The system shall make ownership transfer an explicit, authorized, audited action.
- [ ] The system shall allow grants to identities and groups independently of ownership.
- [ ] The system shall calculate effective access from direct and inherited grants.
- [ ] The system shall show why a user has access to a resource.

### Resource lifecycle

- [ ] The system shall provision resources only into a selected ownership node.
- [ ] The system shall add the selected Organesson marker to every provisioned resource.
- [ ] The system shall verify the marker before destructive or configuration-changing operations.
- [ ] The system shall record actor, time, target, action, result, and relevant request details for lifecycle changes.

## 6. Non-functional requirements

- Security: `[authentication, credential storage, MFA/SSO expectations, audit-retention period]`
- Availability: `[target availability and acceptable maintenance window]`
- Recovery: `[backup, restore, and disaster-recovery expectations]`
- Performance: `[expected environments, resources, users, and operation latency]`
- Usability: `[accessibility, browser support, and operator workflow expectations]`
- Observability: `[logs, metrics, alerting, and audit export requirements]`

## 7. Acceptance criteria for the first vertical slice

- [ ] An administrator can register a test PVE environment.
- [ ] An administrator can create an ownership tree with an organization, group, and workspace.
- [ ] An administrator can provision one managed test resource into that workspace.
- [ ] The resource is identifiable in PVE as Organesson-managed.
- [ ] A granted operator can view and operate that resource according to its role.
- [ ] An unrelated PVE resource is neither changed nor offered for destructive action.
- [ ] An ownership transfer and an access-grant change both appear in the audit trail.

## 8. Decisions and open questions

| Question | Options / notes | Decision | Owner | Due date |
| --- | --- | --- | --- | --- |
| What PVE marker is authoritative? | Tag, name/VMID convention, or both. | `[ ]` | `[ ]` | `[ ]` |
| What is the ownership-node name? | Workspace, project, tenancy, or course instance. | `[ ]` | `[ ]` | `[ ]` |
| Can groups be nested? | Simpler flat groups vs. inherited membership. | `[ ]` | `[ ]` | `[ ]` |
| What roles exist in v1? | Viewer, operator, maintainer, administrator. | `[ ]` | `[ ]` | `[ ]` |
| How are PVE credentials scoped and stored? | PVE API token, service account, secret manager. | `[ ]` | `[ ]` | `[ ]` |
| How are pre-existing resources adopted? | Never, manual import, or approved discovery flow. | `[ ]` | `[ ]` | `[ ]` |
