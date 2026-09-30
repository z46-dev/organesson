package domain

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/z46-dev/gosqlite"
	"github.com/z46-dev/organesson/backend/db"
)

var (
	ErrForbidden         = errors.New("permission denied")
	ErrNotFound          = errors.New("resource not found")
	ErrInvalidPermission = errors.New("permission is not defined by Organesson")
	ErrInvalidInput      = errors.New("invalid input")
)

type (
	// Service implements ownership, fixed permission grants, and deployment operations.
	Service struct {
		store *db.Store
		now   func() time.Time
	}

	// DeploymentSummary contains only resources the caller is allowed to view.
	DeploymentSummary struct {
		Deployment *db.Deployment        `json:"deployment"`
		Resources  []*db.ManagedResource `json:"resources"`
	}
)

var validPermissions = map[string]struct{}{
	db.PermissionDeploymentView:              {},
	db.PermissionDeploymentManage:            {},
	db.PermissionDeploymentManageGroups:      {},
	db.PermissionDeploymentManagePermissions: {},
	db.PermissionDeploymentManageUsers:       {},
	db.PermissionResourceView:                {},
	db.PermissionResourceCreate:              {},
	db.PermissionResourcePower:               {},
	db.PermissionVMConsole:                   {},
	db.PermissionVMSnapshot:                  {},
}

// New creates the domain service over an initialized database.
func New(store *db.Store) (service *Service) {
	service = &Service{store: store, now: time.Now}
	return
}

// CreateDeployment creates a deployment and its unique root ownership node.
func (service *Service) CreateDeployment(actorID int, name string, description string) (deployment *db.Deployment, err error) {
	var actor *db.Account
	if actor, err = service.store.Accounts.Select(actorID); err != nil {
		return
	}
	if actor == nil || actor.Disabled || !actor.PlatformAdministrator {
		err = ErrForbidden
		return
	}
	name = strings.TrimSpace(name)
	if name == "" || len(name) > 128 || len(description) > 2048 {
		err = fmt.Errorf("%w: deployment name or description is invalid", ErrInvalidInput)
		return
	}

	deployment = &db.Deployment{
		Name:        name,
		Description: description,
		CreatedByID: actorID,
		CreatedAt:   service.now(),
	}
	if err = service.store.Deployments.Insert(deployment); err != nil {
		return
	}

	var root *db.OwnershipNode = &db.OwnershipNode{
		DeploymentID: deployment.ID,
		Kind:         db.OwnershipNodeKindDeployment,
		Name:         name,
		CreatedAt:    service.now(),
	}
	if err = service.store.OwnershipNodes.Insert(root); err != nil {
		return
	}
	deployment.RootNodeID = &root.ID
	if err = service.store.Deployments.Update(deployment); err != nil {
		return
	}
	if err = service.writeAudit(actorID, "deployment.create", fmt.Sprintf("deployment:%d", deployment.ID), "succeeded", map[string]string{"name": name}); err != nil {
		return
	}
	return
}

// DeleteDeployment removes a deployment tree after platform-administrator authorization.
func (service *Service) DeleteDeployment(actorID int, deploymentID int) (err error) {
	var actor *db.Account
	if actor, err = service.store.Accounts.Select(actorID); err != nil {
		return
	}
	if actor == nil || actor.Disabled || !actor.PlatformAdministrator {
		err = ErrForbidden
		return
	}
	var deployment *db.Deployment
	if deployment, err = service.store.Deployments.Select(deploymentID); err != nil {
		return
	}
	if deployment == nil {
		err = ErrNotFound
		return
	}
	if err = service.store.Deployments.Delete(deploymentID); err != nil {
		return
	}
	err = service.writeAudit(actorID, "deployment.delete", fmt.Sprintf("deployment:%d", deploymentID), "succeeded", map[string]string{"name": deployment.Name})
	return
}

// UpdateDeployment modifies editable metadata of a platform-managed deployment.
func (service *Service) UpdateDeployment(actorID int, deploymentID int, name string, description string) (deployment *db.Deployment, err error) {
	var actor *db.Account
	if actor, err = service.store.Accounts.Select(actorID); err != nil {
		return
	}
	if actor == nil || actor.Disabled || !actor.PlatformAdministrator {
		err = ErrForbidden
		return
	}
	if deployment, err = service.store.Deployments.Select(deploymentID); err != nil {
		return
	}
	if deployment == nil {
		err = ErrNotFound
		return
	}
	name = strings.TrimSpace(name)
	if name == "" || len(name) > 128 || len(description) > 2048 {
		err = fmt.Errorf("%w: deployment metadata is invalid", ErrInvalidInput)
		return
	}
	deployment.Name = name
	deployment.Description = description
	if err = service.store.Deployments.Update(deployment); err != nil {
		return
	}
	err = service.writeAudit(actorID, "deployment.update", fmt.Sprintf("deployment:%d", deploymentID), "succeeded", map[string]string{"name": name})
	return
}

// CreateLogicalGroup adds a child ownership node to a deployment's resource tree.
func (service *Service) CreateLogicalGroup(actorID int, deploymentID int, parentNodeID int, name string) (node *db.OwnershipNode, err error) {
	var parent *db.OwnershipNode
	if parent, err = service.store.OwnershipNodes.Select(parentNodeID); err != nil {
		return
	}
	if parent == nil || parent.DeploymentID != deploymentID {
		err = ErrNotFound
		return
	}
	if err = service.Require(actorID, db.PermissionDeploymentManage, parentNodeID); err != nil {
		return
	}
	name = strings.TrimSpace(name)
	if name == "" || len(name) > 128 || parent.Kind == db.OwnershipNodeKindResource {
		err = fmt.Errorf("%w: logical group name or parent is invalid", ErrInvalidInput)
		return
	}

	node = &db.OwnershipNode{
		DeploymentID: deploymentID,
		ParentID:     &parentNodeID,
		Kind:         db.OwnershipNodeKindGroup,
		Name:         name,
		CreatedAt:    service.now(),
	}
	if err = service.store.OwnershipNodes.Insert(node); err != nil {
		return
	}
	err = service.writeAudit(actorID, "ownership.group.create", fmt.Sprintf("ownership:%d", node.ID), "succeeded", map[string]string{"name": name})
	return
}

// CreateVirtualMachine adds a locally managed VM record beneath one ownership node.
func (service *Service) CreateVirtualMachine(actorID int, deploymentID int, parentNodeID int, name string) (resource *db.ManagedResource, err error) {
	var parent *db.OwnershipNode
	if parent, err = service.store.OwnershipNodes.Select(parentNodeID); err != nil {
		return
	}
	if parent == nil || parent.DeploymentID != deploymentID || parent.Kind == db.OwnershipNodeKindResource {
		err = ErrNotFound
		return
	}
	if err = service.Require(actorID, db.PermissionDeploymentManage, parentNodeID); err != nil {
		return
	}
	name = strings.TrimSpace(name)
	if name == "" || len(name) > 128 {
		err = fmt.Errorf("%w: VM name is invalid", ErrInvalidInput)
		return
	}

	var node *db.OwnershipNode = &db.OwnershipNode{
		DeploymentID: deploymentID,
		ParentID:     &parentNodeID,
		Kind:         db.OwnershipNodeKindResource,
		Name:         name,
		CreatedAt:    service.now(),
	}
	if err = service.store.OwnershipNodes.Insert(node); err != nil {
		return
	}
	resource = &db.ManagedResource{
		DeploymentID: deploymentID,
		OwnershipID:  node.ID,
		Kind:         "virtual_machine",
		Name:         name,
		PowerState:   "stopped",
		CreatedAt:    service.now(),
	}
	if err = service.store.ManagedResources.Insert(resource); err != nil {
		return
	}
	err = service.writeAudit(actorID, "resource.create", fmt.Sprintf("resource:%d", resource.ID), "succeeded", map[string]string{"kind": resource.Kind, "name": name})
	return
}

// GetVirtualMachine returns one simulated managed VM after checking view authorization.
func (service *Service) GetVirtualMachine(actorID int, resourceID int) (resource *db.ManagedResource, err error) {
	if resource, err = service.store.ManagedResources.Select(resourceID); err != nil {
		return
	}
	if resource == nil || resource.Kind != "virtual_machine" {
		err = ErrNotFound
		return
	}
	var viewErr error = service.Require(actorID, db.PermissionResourceView, resource.OwnershipID)
	if viewErr != nil {
		viewErr = service.Require(actorID, db.PermissionDeploymentManage, resource.OwnershipID)
	}
	if viewErr != nil {
		err = viewErr
		resource = nil
	}
	return
}

// DeleteVirtualMachine removes one simulated VM after configuration authorization.
func (service *Service) DeleteVirtualMachine(actorID int, resourceID int) (err error) {
	var resource *db.ManagedResource
	if resource, err = service.store.ManagedResources.Select(resourceID); err != nil {
		return
	}
	if resource == nil || resource.Kind != "virtual_machine" {
		err = ErrNotFound
		return
	}
	if err = service.Require(actorID, db.PermissionDeploymentManage, resource.OwnershipID); err != nil {
		return
	}
	if err = service.store.ManagedResources.Delete(resource.ID); err != nil {
		return
	}
	if err = service.store.OwnershipNodes.Delete(resource.OwnershipID); err != nil {
		return
	}
	err = service.writeAudit(actorID, "resource.delete", fmt.Sprintf("resource:%d", resourceID), "succeeded", map[string]string{"kind": resource.Kind})
	return
}

// DeleteLogicalGroup removes an empty child ownership node after configuration authorization.
func (service *Service) DeleteLogicalGroup(actorID int, nodeID int) (err error) {
	var node *db.OwnershipNode
	if node, err = service.store.OwnershipNodes.Select(nodeID); err != nil {
		return
	}
	if node == nil || node.Kind != db.OwnershipNodeKindGroup {
		err = ErrNotFound
		return
	}
	if err = service.Require(actorID, db.PermissionDeploymentManage, nodeID); err != nil {
		return
	}
	var nodes []*db.OwnershipNode
	if nodes, err = service.store.OwnershipNodes.SelectAll(); err != nil {
		return
	}
	for _, child := range nodes {
		if child.ParentID != nil && *child.ParentID == nodeID {
			err = fmt.Errorf("%w: ownership group is not empty", ErrInvalidInput)
			return
		}
	}
	err = service.store.OwnershipNodes.Delete(nodeID)
	if err == nil {
		err = service.writeAudit(actorID, "ownership.group.delete", fmt.Sprintf("ownership:%d", nodeID), "succeeded", map[string]string{"name": node.Name})
	}
	return
}

// CreatePermissionGrant assigns one valid permission to an account or user group.
func (service *Service) CreatePermissionGrant(actorID int, kind db.GrantSubjectKind, subjectID int, permission string, targetNodeID int) (grant *db.PermissionGrant, err error) {
	grant, err = service.CreatePermissionGrantScoped(actorID, kind, subjectID, permission, targetNodeID, true)
	return
}

// CreatePermissionGrantScoped assigns one fixed permission with explicit descendant inheritance.
func (service *Service) CreatePermissionGrantScoped(actorID int, kind db.GrantSubjectKind, subjectID int, permission string, targetNodeID int, inheritDescendants bool) (grant *db.PermissionGrant, err error) {
	if _, valid := validPermissions[permission]; !valid {
		err = ErrInvalidPermission
		return
	}
	var target *db.OwnershipNode
	if target, err = service.store.OwnershipNodes.Select(targetNodeID); err != nil {
		return
	}
	if target == nil {
		err = ErrNotFound
		return
	}
	if err = service.Require(actorID, db.PermissionDeploymentManagePermissions, targetNodeID); err != nil {
		return
	}
	if err = service.validateGrantSubject(kind, subjectID, target.DeploymentID); err != nil {
		return
	}
	var grants []*db.PermissionGrant
	if grants, err = service.store.PermissionGrants.SelectAll(); err != nil {
		return
	}
	for _, existing := range grants {
		if existing.SubjectKind == kind && existing.SubjectID == subjectID && existing.Permission == permission && existing.TargetNodeID == targetNodeID && existing.InheritDescendants == inheritDescendants {
			grant = existing
			return
		}
	}

	grant = &db.PermissionGrant{
		SubjectKind:        kind,
		SubjectID:          subjectID,
		Permission:         permission,
		TargetNodeID:       targetNodeID,
		InheritDescendants: inheritDescendants,
		CreatedByID:        actorID,
		CreatedAt:          service.now(),
	}
	err = service.store.PermissionGrants.Insert(grant)
	if err == nil {
		err = service.writeAudit(actorID, "permission.grant", fmt.Sprintf("ownership:%d", targetNodeID), "succeeded", map[string]any{"permission": permission, "subject_kind": kind, "subject_id": subjectID})
	}
	return
}

// DeletePermissionGrant revokes a permission mapping after checking management access.
func (service *Service) DeletePermissionGrant(actorID int, grantID int) (err error) {
	var grant *db.PermissionGrant
	if grant, err = service.store.PermissionGrants.Select(grantID); err != nil {
		return
	}
	if grant == nil {
		err = ErrNotFound
		return
	}
	if err = service.Require(actorID, db.PermissionDeploymentManagePermissions, grant.TargetNodeID); err != nil {
		return
	}
	if err = service.store.PermissionGrants.Delete(grantID); err != nil {
		return
	}
	err = service.writeAudit(actorID, "permission.revoke", fmt.Sprintf("ownership:%d", grant.TargetNodeID), "succeeded", map[string]any{"permission": grant.Permission, "subject_kind": grant.SubjectKind, "subject_id": grant.SubjectID})
	return
}

// Require checks an actor's effective permission on a node and each ancestor.
func (service *Service) Require(actorID int, permission string, targetNodeID int) (err error) {
	var allowed bool
	if allowed, err = service.Can(actorID, permission, targetNodeID); err != nil {
		return
	}
	if !allowed {
		err = ErrForbidden
	}
	return
}

// Can evaluates account and group grants along the target's ownership ancestry.
func (service *Service) Can(actorID int, permission string, targetNodeID int) (allowed bool, err error) {
	if _, valid := validPermissions[permission]; !valid {
		err = ErrInvalidPermission
		return
	}
	var actor *db.Account
	if actor, err = service.store.Accounts.Select(actorID); err != nil {
		return
	}
	if actor == nil || actor.Disabled {
		return
	}
	if actor.PlatformAdministrator {
		allowed = true
		return
	}

	var nodes []*db.OwnershipNode
	if nodes, err = service.store.OwnershipNodes.SelectAll(); err != nil {
		return
	}
	var nodeByID map[int]*db.OwnershipNode = make(map[int]*db.OwnershipNode, len(nodes))
	for _, node := range nodes {
		nodeByID[node.ID] = node
	}
	var target *db.OwnershipNode = nodeByID[targetNodeID]
	if target == nil {
		err = ErrNotFound
		return
	}
	var grants []*db.PermissionGrant
	if grants, err = service.store.PermissionGrants.SelectAll(); err != nil {
		return
	}
	var memberships []*db.GroupMembership
	if memberships, err = service.store.GroupMemberships.SelectAll(); err != nil {
		return
	}
	var groups []*db.UserGroup
	if groups, err = service.store.UserGroups.SelectAll(); err != nil {
		return
	}
	var memberGroupIDs map[int]struct{} = make(map[int]struct{})
	for _, membership := range memberships {
		if membership.AccountID == actorID {
			for _, group := range groups {
				if group.ID == membership.GroupID && group.DeploymentID == target.DeploymentID {
					memberGroupIDs[group.ID] = struct{}{}
				}
			}
		}
	}

	var visited map[int]struct{} = make(map[int]struct{})
	var current *db.OwnershipNode = target
	for current != nil {
		if _, exists := visited[current.ID]; exists {
			err = errors.New("ownership tree contains a cycle")
			return
		}
		visited[current.ID] = struct{}{}
		for _, grant := range grants {
			if grant.TargetNodeID != current.ID || grant.Permission != permission {
				continue
			}
			if current.ID != target.ID && !grant.InheritDescendants {
				continue
			}
			if grant.SubjectKind == db.GrantSubjectKindAccount && grant.SubjectID == actorID {
				allowed = true
				return
			}
			if grant.SubjectKind == db.GrantSubjectKindGroup {
				if _, exists := memberGroupIDs[grant.SubjectID]; exists {
					allowed = true
					return
				}
			}
		}
		if current.ParentID == nil {
			current = nil
		} else {
			current = nodeByID[*current.ParentID]
			if current == nil {
				err = errors.New("ownership tree has a missing parent")
				return
			}
		}
	}
	return
}

// ListDeployments returns deployments where the caller can view at least one target.
func (service *Service) ListDeployments(actorID int) (deployments []*db.Deployment, err error) {
	var all []*db.Deployment
	if all, err = service.store.Deployments.SelectAll(); err != nil {
		return
	}
	for _, deployment := range all {
		if deployment.RootNodeID == nil {
			continue
		}
		var allowed bool
		if allowed, err = service.Can(actorID, db.PermissionDeploymentView, *deployment.RootNodeID); err != nil {
			return
		}
		if allowed {
			deployments = append(deployments, deployment)
			continue
		}
		var resources []*db.ManagedResource
		if resources, err = service.store.ManagedResources.SelectAll(); err != nil {
			return
		}
		for _, resource := range resources {
			if resource.DeploymentID != deployment.ID {
				continue
			}
			if allowed, err = service.Can(actorID, db.PermissionResourceView, resource.OwnershipID); err != nil {
				return
			}
			if allowed {
				deployments = append(deployments, deployment)
				break
			}
		}
	}
	return
}

// GetDeployment returns a deployment with only the resources visible to the caller.
func (service *Service) GetDeployment(actorID int, deploymentID int) (summary *DeploymentSummary, err error) {
	var deployment *db.Deployment
	if deployment, err = service.store.Deployments.Select(deploymentID); err != nil {
		return
	}
	if deployment == nil || deployment.RootNodeID == nil {
		err = ErrNotFound
		return
	}
	var deploymentAllowed bool
	if deploymentAllowed, err = service.Can(actorID, db.PermissionDeploymentView, *deployment.RootNodeID); err != nil {
		return
	}
	if !deploymentAllowed {
		var hasVisibleResource bool
		if hasVisibleResource, err = service.deploymentHasVisibleResource(actorID, deploymentID); err != nil {
			return
		}
		if !hasVisibleResource {
			err = ErrForbidden
			return
		}
	}

	summary = &DeploymentSummary{Deployment: deployment}
	var resources []*db.ManagedResource
	if resources, err = service.store.ManagedResources.SelectAll(); err != nil {
		return
	}
	for _, resource := range resources {
		if resource.DeploymentID != deploymentID {
			continue
		}
		var visible bool = deploymentAllowed
		if !visible {
			if visible, err = service.Can(actorID, db.PermissionResourceView, resource.OwnershipID); err != nil {
				return
			}
		}
		if visible {
			summary.Resources = append(summary.Resources, resource)
		}
	}
	return
}

// SetVirtualMachinePower applies a permitted lifecycle transition to a managed VM record.
func (service *Service) SetVirtualMachinePower(actorID int, resourceID int, action string) (resource *db.ManagedResource, err error) {
	if resource, err = service.store.ManagedResources.Select(resourceID); err != nil {
		return
	}
	if resource == nil || resource.Kind != "virtual_machine" {
		err = ErrNotFound
		return
	}
	if err = service.Require(actorID, db.PermissionResourcePower, resource.OwnershipID); err != nil {
		return
	}
	var nextState string
	switch action {
	case "start", "resume":
		nextState = "running"
	case "stop":
		nextState = "stopped"
	case "suspend":
		nextState = "suspended"
	case "restart":
		if resource.PowerState != "running" {
			err = fmt.Errorf("%w: only a running VM can be restarted", ErrInvalidInput)
			return
		}
		nextState = "running"
	default:
		err = fmt.Errorf("%w: unsupported VM power action", ErrInvalidInput)
		return
	}
	resource.PowerState = nextState
	if err = service.store.ManagedResources.Update(resource); err != nil {
		return
	}
	err = service.writeAudit(actorID, "vm.power."+action, fmt.Sprintf("resource:%d", resourceID), "succeeded", map[string]string{"state": nextState})
	return
}

// CreateUserGroup creates a deployment-local user group after authorization.
func (service *Service) CreateUserGroup(actorID int, deploymentID int, name string) (group *db.UserGroup, err error) {
	var deployment *db.Deployment
	if deployment, err = service.store.Deployments.Select(deploymentID); err != nil {
		return
	}
	if deployment == nil || deployment.RootNodeID == nil {
		err = ErrNotFound
		return
	}
	if err = service.Require(actorID, db.PermissionDeploymentManageGroups, *deployment.RootNodeID); err != nil {
		return
	}
	name = strings.TrimSpace(name)
	if name == "" || len(name) > 128 {
		err = fmt.Errorf("%w: user group name is invalid", ErrInvalidInput)
		return
	}
	group = &db.UserGroup{DeploymentID: deploymentID, Name: name, CreatedAt: service.now()}
	if err = service.store.UserGroups.Insert(group); err != nil {
		return
	}
	err = service.writeAudit(actorID, "user_group.create", fmt.Sprintf("user_group:%d", group.ID), "succeeded", map[string]string{"name": name})
	return
}

// AddGroupMember adds an account to a deployment-local group.
func (service *Service) AddGroupMember(actorID int, groupID int, accountID int) (err error) {
	var group *db.UserGroup
	if group, err = service.store.UserGroups.Select(groupID); err != nil {
		return
	}
	if group == nil {
		err = ErrNotFound
		return
	}
	var deployment *db.Deployment
	if deployment, err = service.store.Deployments.Select(group.DeploymentID); err != nil {
		return
	}
	if deployment == nil || deployment.RootNodeID == nil {
		err = ErrNotFound
		return
	}
	if err = service.Require(actorID, db.PermissionDeploymentManageGroups, *deployment.RootNodeID); err != nil {
		return
	}
	var account *db.Account
	if account, err = service.store.Accounts.Select(accountID); err != nil {
		return
	}
	if account == nil || account.Disabled {
		err = ErrNotFound
		return
	}
	var memberships []*db.GroupMembership
	if memberships, err = service.store.GroupMemberships.SelectAll(); err != nil {
		return
	}
	for _, membership := range memberships {
		if membership.GroupID == groupID && membership.AccountID == accountID {
			return
		}
	}
	if err = service.store.GroupMemberships.Insert(&db.GroupMembership{GroupID: groupID, AccountID: accountID, CreatedAt: service.now()}); err != nil {
		return
	}
	err = service.writeAudit(actorID, "group.member.add", fmt.Sprintf("user_group:%d", groupID), "succeeded", map[string]int{"account_id": accountID})
	return
}

// SetGroupMembers replaces one deployment-local group's complete member set.
func (service *Service) SetGroupMembers(actorID int, groupID int, accountIDs []int) (err error) {
	var group *db.UserGroup
	if group, err = service.store.UserGroups.Select(groupID); err != nil {
		return
	}
	if group == nil {
		err = ErrNotFound
		return
	}
	var deployment *db.Deployment
	if deployment, err = service.store.Deployments.Select(group.DeploymentID); err != nil {
		return
	}
	if deployment == nil || deployment.RootNodeID == nil {
		err = ErrNotFound
		return
	}
	if err = service.Require(actorID, db.PermissionDeploymentManageGroups, *deployment.RootNodeID); err != nil {
		return
	}
	var desired map[int]struct{} = make(map[int]struct{}, len(accountIDs))
	for _, accountID := range accountIDs {
		if accountID < 1 {
			err = ErrInvalidInput
			return
		}
		if _, duplicate := desired[accountID]; duplicate {
			continue
		}
		var account *db.Account
		if account, err = service.store.Accounts.Select(accountID); err != nil {
			return
		}
		if account == nil || account.Disabled || account.ActivatedAt == nil {
			err = ErrNotFound
			return
		}
		desired[accountID] = struct{}{}
	}
	var memberships []*db.GroupMembership
	if memberships, err = service.store.GroupMemberships.SelectAll(); err != nil {
		return
	}
	var current map[int]struct{} = make(map[int]struct{})
	for _, membership := range memberships {
		if membership.GroupID == groupID {
			current[membership.AccountID] = struct{}{}
		}
	}
	for accountID := range current {
		if _, keep := desired[accountID]; !keep {
			if err = service.RemoveGroupMember(actorID, groupID, accountID); err != nil {
				return
			}
		}
	}
	for accountID := range desired {
		if _, exists := current[accountID]; !exists {
			if err = service.AddGroupMember(actorID, groupID, accountID); err != nil {
				return
			}
		}
	}
	return
}

// RemoveGroupMember revokes one account's membership in a deployment-local group.
func (service *Service) RemoveGroupMember(actorID int, groupID int, accountID int) (err error) {
	var group *db.UserGroup
	if group, err = service.store.UserGroups.Select(groupID); err != nil {
		return
	}
	if group == nil {
		err = ErrNotFound
		return
	}
	var deployment *db.Deployment
	if deployment, err = service.store.Deployments.Select(group.DeploymentID); err != nil {
		return
	}
	if deployment == nil || deployment.RootNodeID == nil {
		err = ErrNotFound
		return
	}
	if err = service.Require(actorID, db.PermissionDeploymentManageGroups, *deployment.RootNodeID); err != nil {
		return
	}
	var rowsAffected int64
	if rowsAffected, err = service.store.GroupMemberships.DeleteWithFilter(
		gosqlite.NewFilter().
			KeyCmp(service.store.GroupMemberships.FieldByGoName("GroupID"), gosqlite.OpEqual, groupID).
			And().
			KeyCmp(service.store.GroupMemberships.FieldByGoName("AccountID"), gosqlite.OpEqual, accountID),
	); err != nil {
		return
	}
	if rowsAffected == 0 {
		err = ErrNotFound
		return
	}
	err = service.writeAudit(actorID, "group.member.remove", fmt.Sprintf("user_group:%d", groupID), "succeeded", map[string]int{"account_id": accountID})
	return
}

// DeleteUserGroup removes a deployment-local group and its permission grants.
func (service *Service) DeleteUserGroup(actorID int, groupID int) (err error) {
	var group *db.UserGroup
	if group, err = service.store.UserGroups.Select(groupID); err != nil {
		return
	}
	if group == nil {
		err = ErrNotFound
		return
	}
	var deployment *db.Deployment
	if deployment, err = service.store.Deployments.Select(group.DeploymentID); err != nil {
		return
	}
	if deployment == nil || deployment.RootNodeID == nil {
		err = ErrNotFound
		return
	}
	if err = service.Require(actorID, db.PermissionDeploymentManageGroups, *deployment.RootNodeID); err != nil {
		return
	}
	var grants []*db.PermissionGrant
	if grants, err = service.store.PermissionGrants.SelectAll(); err != nil {
		return
	}
	for _, grant := range grants {
		if grant.SubjectKind == db.GrantSubjectKindGroup && grant.SubjectID == groupID {
			if err = service.store.PermissionGrants.Delete(grant.ID); err != nil {
				return
			}
		}
	}
	if err = service.store.UserGroups.Delete(groupID); err != nil {
		return
	}
	err = service.writeAudit(actorID, "user_group.delete", fmt.Sprintf("user_group:%d", groupID), "succeeded", map[string]string{"name": group.Name})
	return
}

// validateGrantSubject ensures the permission subject exists in the target scope.
func (service *Service) validateGrantSubject(kind db.GrantSubjectKind, subjectID int, deploymentID int) (err error) {
	switch kind {
	case db.GrantSubjectKindAccount:
		var account *db.Account
		if account, err = service.store.Accounts.Select(subjectID); err != nil {
			return
		}
		if account == nil || account.Disabled {
			err = ErrNotFound
		}
	case db.GrantSubjectKindGroup:
		var group *db.UserGroup
		if group, err = service.store.UserGroups.Select(subjectID); err != nil {
			return
		}
		if group == nil || group.DeploymentID != deploymentID {
			err = ErrNotFound
		}
	default:
		err = fmt.Errorf("%w: invalid permission subject kind", ErrInvalidInput)
	}
	return
}

// deploymentHasVisibleResource checks whether a caller can inspect any resource in a deployment.
func (service *Service) deploymentHasVisibleResource(actorID int, deploymentID int) (visible bool, err error) {
	var resources []*db.ManagedResource
	if resources, err = service.store.ManagedResources.SelectAll(); err != nil {
		return
	}
	for _, resource := range resources {
		if resource.DeploymentID != deploymentID {
			continue
		}
		if visible, err = service.Can(actorID, db.PermissionResourceView, resource.OwnershipID); err != nil || visible {
			return
		}
	}
	return
}

// writeAudit appends a compact JSON audit record for a successful domain operation.
func (service *Service) writeAudit(actorID int, action string, target string, result string, details any) (err error) {
	var detailsJSON []byte
	if detailsJSON, err = json.Marshal(details); err != nil {
		return
	}
	var actorIDPointer *int = &actorID
	err = service.store.AuditEvents.Insert(&db.AuditEvent{
		ActorAccountID: actorIDPointer,
		Action:         action,
		Target:         target,
		Result:         result,
		DetailsJSON:    string(detailsJSON),
		CreatedAt:      service.now(),
	})
	return
}
