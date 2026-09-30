package v1

import (
	"strings"

	"github.com/gofiber/fiber/v3"
	"github.com/z46-dev/organesson/backend/app/api/common"
	"github.com/z46-dev/organesson/backend/db"
	"github.com/z46-dev/organesson/backend/domain"
)

type (
	createDeploymentRequest struct {
		Name        string `json:"name"`
		Description string `json:"description"`
	}

	createVMRequest struct {
		ParentNodeID int    `json:"parent_node_id"`
		Name         string `json:"name"`
	}

	setPowerRequest struct {
		Action string `json:"action"`
	}

	createLogicalGroupRequest struct {
		ParentNodeID int    `json:"parent_node_id"`
		Name         string `json:"name"`
	}

	createUserGroupRequest struct {
		Name    string   `json:"name"`
		Members []string `json:"members"`
	}

	addGroupMemberRequest struct {
		AccountID int `json:"account_id"`
	}

	createGrantRequest struct {
		SubjectKind        int    `json:"subject_kind"`
		SubjectID          int    `json:"subject_id"`
		SubjectName        string `json:"subject_name"`
		Permission         string `json:"permission"`
		InheritDescendants bool   `json:"inherit_descendants"`
	}
)

// initDeployments registers authenticated deployment and initial VM operations.
func initDeployments(parent fiber.Router, services common.Services) {
	var router fiber.Router = parent.Group("/deployments", common.RequireActor(services.Authentication))
	router.Get("/", listDeployments(services))
	router.Post("/", createDeployment(services))
	router.Get("/:deployment_id", getDeployment(services))
	router.Put("/:deployment_id", updateDeployment(services))
	router.Delete("/:deployment_id", deleteDeployment(services))
	router.Post("/:deployment_id/virtual-machines", createVirtualMachine(services))
	router.Post("/:deployment_id/logical-groups", createLogicalGroup(services))
	router.Post("/:deployment_id/user-groups", createUserGroup(services))
	parent.Post("/user-groups/:group_id/members", common.RequireActor(services.Authentication), addGroupMember(services))
	parent.Get("/user-groups/:group_id", common.RequireActor(services.Authentication), getUserGroup(services))
	parent.Put("/user-groups/:group_id/members", common.RequireActor(services.Authentication), setGroupMembers(services))
	parent.Delete("/user-groups/:group_id", common.RequireActor(services.Authentication), deleteUserGroup(services))
	parent.Delete("/user-groups/:group_id/members/:account_id", common.RequireActor(services.Authentication), removeGroupMember(services))
	parent.Post("/ownership-nodes/:node_id/grants", common.RequireActor(services.Authentication), createPermissionGrant(services))
	parent.Get("/ownership-nodes/:node_id", common.RequireActor(services.Authentication), getOwnershipNode(services))
	parent.Delete("/ownership-nodes/:node_id", common.RequireActor(services.Authentication), deleteLogicalGroup(services))
	parent.Get("/permission-grants/:grant_id", common.RequireActor(services.Authentication), getPermissionGrant(services))
	parent.Delete("/permission-grants/:grant_id", common.RequireActor(services.Authentication), deletePermissionGrant(services))
	parent.Post("/virtual-machines/:resource_id/power", common.RequireActor(services.Authentication), setVirtualMachinePower(services))
	parent.Get("/virtual-machines/:resource_id", common.RequireActor(services.Authentication), getVirtualMachine(services))
	parent.Delete("/virtual-machines/:resource_id", common.RequireActor(services.Authentication), deleteVirtualMachine(services))
}

// updateDeployment changes deployment metadata for platform administrators.
func updateDeployment(services common.Services) (handler fiber.Handler) {
	handler = func(ctx fiber.Ctx) (err error) {
		var actorID int
		if actorID, _ = common.AccountID(ctx); actorID < 1 {
			return ctx.SendStatus(fiber.StatusUnauthorized)
		}
		var deploymentID int
		if deploymentID, err = common.ParseID(ctx, "deployment_id"); err != nil {
			return ctx.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "Invalid deployment identifier."})
		}
		var request createDeploymentRequest
		if err = ctx.Bind().Body(&request); err != nil {
			return ctx.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "Invalid deployment request."})
		}
		var deployment *db.Deployment
		if deployment, err = services.Domain.UpdateDeployment(actorID, deploymentID, request.Name, request.Description); err != nil {
			return common.DomainError(ctx, err)
		}
		err = ctx.JSON(fiber.Map{"deployment": deployment})
		return
	}
	return
}

// deleteDeployment removes one deployment after the domain service checks platform ownership.
func deleteDeployment(services common.Services) (handler fiber.Handler) {
	handler = func(ctx fiber.Ctx) (err error) {
		var actorID int
		if actorID, _ = common.AccountID(ctx); actorID < 1 {
			return ctx.SendStatus(fiber.StatusUnauthorized)
		}
		var deploymentID int
		if deploymentID, err = common.ParseID(ctx, "deployment_id"); err != nil {
			return ctx.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "Invalid deployment identifier."})
		}
		if err = services.Domain.DeleteDeployment(actorID, deploymentID); err != nil {
			return common.DomainError(ctx, err)
		}
		err = ctx.SendStatus(fiber.StatusNoContent)
		return
	}
	return
}

// deleteLogicalGroup deletes an empty ownership-tree node.
func deleteLogicalGroup(services common.Services) (handler fiber.Handler) {
	handler = func(ctx fiber.Ctx) (err error) {
		var actorID int
		if actorID, _ = common.AccountID(ctx); actorID < 1 {
			return ctx.SendStatus(fiber.StatusUnauthorized)
		}
		var nodeID int
		if nodeID, err = common.ParseID(ctx, "node_id"); err != nil {
			return ctx.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "Invalid ownership node identifier."})
		}
		if err = services.Domain.DeleteLogicalGroup(actorID, nodeID); err != nil {
			return common.DomainError(ctx, err)
		}
		err = ctx.SendStatus(fiber.StatusNoContent)
		return
	}
	return
}

// deleteVirtualMachine deletes one simulated VM and its ownership node.
func deleteVirtualMachine(services common.Services) (handler fiber.Handler) {
	handler = func(ctx fiber.Ctx) (err error) {
		var actorID int
		if actorID, _ = common.AccountID(ctx); actorID < 1 {
			return ctx.SendStatus(fiber.StatusUnauthorized)
		}
		var resourceID int
		if resourceID, err = common.ParseID(ctx, "resource_id"); err != nil {
			return ctx.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "Invalid resource identifier."})
		}
		if err = services.Domain.DeleteVirtualMachine(actorID, resourceID); err != nil {
			return common.DomainError(ctx, err)
		}
		err = ctx.SendStatus(fiber.StatusNoContent)
		return
	}
	return
}

// deleteUserGroup removes one user group and associated grants.
func deleteUserGroup(services common.Services) (handler fiber.Handler) {
	handler = func(ctx fiber.Ctx) (err error) {
		var actorID int
		if actorID, _ = common.AccountID(ctx); actorID < 1 {
			return ctx.SendStatus(fiber.StatusUnauthorized)
		}
		var groupID int
		if groupID, err = common.ParseID(ctx, "group_id"); err != nil {
			return ctx.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "Invalid user group identifier."})
		}
		if err = services.Domain.DeleteUserGroup(actorID, groupID); err != nil {
			return common.DomainError(ctx, err)
		}
		err = ctx.SendStatus(fiber.StatusNoContent)
		return
	}
	return
}

// removeGroupMember revokes one account's membership in a user group.
func removeGroupMember(services common.Services) (handler fiber.Handler) {
	handler = func(ctx fiber.Ctx) (err error) {
		var actorID int
		if actorID, _ = common.AccountID(ctx); actorID == 0 {
			return ctx.SendStatus(fiber.StatusUnauthorized)
		}
		var groupID, accountID int
		if groupID, err = common.ParseID(ctx, "group_id"); err != nil {
			return ctx.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "Invalid group identifier."})
		}
		if accountID, err = common.ParseID(ctx, "account_id"); err != nil {
			return ctx.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "Invalid account identifier."})
		}
		if err = services.Domain.RemoveGroupMember(actorID, groupID, accountID); err != nil {
			return common.DomainError(ctx, err)
		}
		err = ctx.SendStatus(fiber.StatusNoContent)
		return
	}
	return
}

// deletePermissionGrant revokes a permission mapping.
func deletePermissionGrant(services common.Services) (handler fiber.Handler) {
	handler = func(ctx fiber.Ctx) (err error) {
		var actorID int
		if actorID, _ = common.AccountID(ctx); actorID == 0 {
			return ctx.SendStatus(fiber.StatusUnauthorized)
		}
		var grantID int
		if grantID, err = common.ParseID(ctx, "grant_id"); err != nil {
			return ctx.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "Invalid permission grant identifier."})
		}
		if err = services.Domain.DeletePermissionGrant(actorID, grantID); err != nil {
			return common.DomainError(ctx, err)
		}
		err = ctx.SendStatus(fiber.StatusNoContent)
		return
	}
	return
}

// createLogicalGroup creates a child node in a deployment ownership tree.
func createLogicalGroup(services common.Services) (handler fiber.Handler) {
	handler = func(ctx fiber.Ctx) (err error) {
		var request createLogicalGroupRequest
		if err = ctx.Bind().Body(&request); err != nil {
			return ctx.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "Invalid logical group request."})
		}
		var accountID int
		if accountID, _ = common.AccountID(ctx); accountID == 0 {
			return ctx.SendStatus(fiber.StatusUnauthorized)
		}
		var deploymentID int
		if deploymentID, err = common.ParseID(ctx, "deployment_id"); err != nil {
			return ctx.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "Invalid deployment identifier."})
		}
		if request.ParentNodeID == 0 {
			var deployment *db.Deployment
			if deployment, err = services.Store.Deployments.Select(deploymentID); err != nil {
				return common.DomainError(ctx, err)
			}
			if deployment == nil || deployment.RootNodeID == nil {
				return ctx.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "Deployment not found."})
			}
			request.ParentNodeID = *deployment.RootNodeID
		}
		if request.ParentNodeID < 1 {
			return ctx.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "A valid ownership parent is required."})
		}
		var node *db.OwnershipNode
		if node, err = services.Domain.CreateLogicalGroup(accountID, deploymentID, request.ParentNodeID, request.Name); err != nil {
			return common.DomainError(ctx, err)
		}
		err = ctx.Status(fiber.StatusCreated).JSON(fiber.Map{"ownership_node": node})
		return
	}
	return
}

// createUserGroup creates a deployment-local access group.
func createUserGroup(services common.Services) (handler fiber.Handler) {
	handler = func(ctx fiber.Ctx) (err error) {
		var request createUserGroupRequest
		if err = ctx.Bind().Body(&request); err != nil {
			return ctx.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "A user group name is required."})
		}
		var accountID int
		if accountID, _ = common.AccountID(ctx); accountID == 0 {
			return ctx.SendStatus(fiber.StatusUnauthorized)
		}
		var deploymentID int
		if deploymentID, err = common.ParseID(ctx, "deployment_id"); err != nil {
			return ctx.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "Invalid deployment identifier."})
		}
		var group *db.UserGroup
		if group, err = services.Domain.CreateUserGroup(accountID, deploymentID, request.Name); err != nil {
			return common.DomainError(ctx, err)
		}
		for _, qualifiedName := range request.Members {
			var memberID int
			if memberID, err = resolveLocalAccountID(services.Store, qualifiedName); err != nil {
				_ = services.Domain.DeleteUserGroup(accountID, group.ID)
				return common.DomainError(ctx, err)
			}
			if err = services.Domain.AddGroupMember(accountID, group.ID, memberID); err != nil {
				_ = services.Domain.DeleteUserGroup(accountID, group.ID)
				return common.DomainError(ctx, err)
			}
		}
		err = ctx.Status(fiber.StatusCreated).JSON(fiber.Map{"user_group": group, "members": request.Members})
		return
	}
	return
}

// addGroupMember assigns one local account to a deployment-local user group.
func addGroupMember(services common.Services) (handler fiber.Handler) {
	handler = func(ctx fiber.Ctx) (err error) {
		var request addGroupMemberRequest
		if err = ctx.Bind().Body(&request); err != nil || request.AccountID < 1 {
			return ctx.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "A valid account identifier is required."})
		}
		var accountID int
		if accountID, _ = common.AccountID(ctx); accountID == 0 {
			return ctx.SendStatus(fiber.StatusUnauthorized)
		}
		var groupID int
		if groupID, err = common.ParseID(ctx, "group_id"); err != nil {
			return ctx.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "Invalid group identifier."})
		}
		if err = services.Domain.AddGroupMember(accountID, groupID, request.AccountID); err != nil {
			return common.DomainError(ctx, err)
		}
		err = ctx.SendStatus(fiber.StatusNoContent)
		return
	}
	return
}

// setGroupMembers replaces group membership from qualified Organesson identities.
func setGroupMembers(services common.Services) (handler fiber.Handler) {
	handler = func(ctx fiber.Ctx) (err error) {
		var request createUserGroupRequest
		if err = ctx.Bind().Body(&request); err != nil {
			return ctx.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "Invalid group membership request."})
		}
		var actorID int
		if actorID, _ = common.AccountID(ctx); actorID < 1 {
			return ctx.SendStatus(fiber.StatusUnauthorized)
		}
		var groupID int
		if groupID, err = common.ParseID(ctx, "group_id"); err != nil {
			return ctx.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "Invalid user group identifier."})
		}
		var accountIDs []int
		for _, qualifiedName := range request.Members {
			var accountID int
			if accountID, err = resolveLocalAccountID(services.Store, qualifiedName); err != nil {
				return common.DomainError(ctx, err)
			}
			accountIDs = append(accountIDs, accountID)
		}
		if err = services.Domain.SetGroupMembers(actorID, groupID, accountIDs); err != nil {
			return common.DomainError(ctx, err)
		}
		err = ctx.SendStatus(fiber.StatusNoContent)
		return
	}
	return
}

// createPermissionGrant creates a fixed permission mapping on an ownership node.
func createPermissionGrant(services common.Services) (handler fiber.Handler) {
	handler = func(ctx fiber.Ctx) (err error) {
		var request createGrantRequest
		if err = ctx.Bind().Body(&request); err != nil || request.SubjectID < 0 || request.SubjectID == 0 && request.SubjectName == "" {
			return ctx.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "A valid permission subject is required."})
		}
		var accountID int
		if accountID, _ = common.AccountID(ctx); accountID == 0 {
			return ctx.SendStatus(fiber.StatusUnauthorized)
		}
		var nodeID int
		if nodeID, err = common.ParseID(ctx, "node_id"); err != nil {
			return ctx.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "Invalid ownership node identifier."})
		}
		var subjectID int = request.SubjectID
		if db.GrantSubjectKind(request.SubjectKind) == db.GrantSubjectKindAccount && request.SubjectName != "" {
			if subjectID, err = resolveLocalAccountID(services.Store, request.SubjectName); err != nil {
				return common.DomainError(ctx, err)
			}
		}
		var grant *db.PermissionGrant
		if grant, err = services.Domain.CreatePermissionGrantScoped(accountID, db.GrantSubjectKind(request.SubjectKind), subjectID, request.Permission, nodeID, request.InheritDescendants); err != nil {
			return common.DomainError(ctx, err)
		}
		err = ctx.Status(fiber.StatusCreated).JSON(fiber.Map{"permission_grant": grant})
		return
	}
	return
}

// listDeployments returns deployments visible to the current account.
func listDeployments(services common.Services) (handler fiber.Handler) {
	handler = func(ctx fiber.Ctx) (err error) {
		var accountID int
		if accountID, _ = common.AccountID(ctx); accountID == 0 {
			return ctx.SendStatus(fiber.StatusUnauthorized)
		}
		var deployments []*db.Deployment
		if deployments, err = services.Domain.ListDeployments(accountID); err != nil {
			return common.DomainError(ctx, err)
		}
		err = ctx.JSON(fiber.Map{"deployments": deployments})
		return
	}
	return
}

// createDeployment creates a deployment using the platform administrator permission.
func createDeployment(services common.Services) (handler fiber.Handler) {
	handler = func(ctx fiber.Ctx) (err error) {
		var request createDeploymentRequest
		if err = ctx.Bind().Body(&request); err != nil {
			return ctx.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "Invalid deployment request."})
		}
		var accountID int
		if accountID, _ = common.AccountID(ctx); accountID == 0 {
			return ctx.SendStatus(fiber.StatusUnauthorized)
		}
		var deployment *db.Deployment
		if deployment, err = services.Domain.CreateDeployment(accountID, request.Name, request.Description); err != nil {
			return common.DomainError(ctx, err)
		}
		err = ctx.Status(fiber.StatusCreated).JSON(fiber.Map{"deployment": deployment})
		return
	}
	return
}

// getDeployment returns a filtered view of a deployment.
func getDeployment(services common.Services) (handler fiber.Handler) {
	handler = func(ctx fiber.Ctx) (err error) {
		var accountID int
		if accountID, _ = common.AccountID(ctx); accountID == 0 {
			return ctx.SendStatus(fiber.StatusUnauthorized)
		}
		var deploymentID int
		if deploymentID, err = common.ParseID(ctx, "deployment_id"); err != nil {
			return ctx.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "Invalid deployment identifier."})
		}
		var summary *domain.DeploymentSummary
		if summary, err = services.Domain.GetDeployment(accountID, deploymentID); err != nil {
			return common.DomainError(ctx, err)
		}
		err = ctx.JSON(summary)
		return
	}
	return
}

// createVirtualMachine records a VM beneath an authorized ownership node.
func createVirtualMachine(services common.Services) (handler fiber.Handler) {
	handler = func(ctx fiber.Ctx) (err error) {
		var request createVMRequest
		if err = ctx.Bind().Body(&request); err != nil || request.ParentNodeID < 1 {
			return ctx.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "A valid owner node and VM name are required."})
		}
		var accountID int
		if accountID, _ = common.AccountID(ctx); accountID == 0 {
			return ctx.SendStatus(fiber.StatusUnauthorized)
		}
		var deploymentID int
		if deploymentID, err = common.ParseID(ctx, "deployment_id"); err != nil {
			return ctx.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "Invalid deployment identifier."})
		}
		var resource *db.ManagedResource
		if resource, err = services.Domain.CreateVirtualMachine(accountID, deploymentID, request.ParentNodeID, request.Name); err != nil {
			return common.DomainError(ctx, err)
		}
		err = ctx.Status(fiber.StatusCreated).JSON(fiber.Map{"resource": resource})
		return
	}
	return
}

// setVirtualMachinePower applies an authorized power-state change to a managed VM record.
func setVirtualMachinePower(services common.Services) (handler fiber.Handler) {
	handler = func(ctx fiber.Ctx) (err error) {
		var request setPowerRequest
		if err = ctx.Bind().Body(&request); err != nil || request.Action == "" {
			return ctx.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "A VM power action is required."})
		}
		var accountID int
		if accountID, _ = common.AccountID(ctx); accountID == 0 {
			return ctx.SendStatus(fiber.StatusUnauthorized)
		}
		var resourceID int
		if resourceID, err = common.ParseID(ctx, "resource_id"); err != nil {
			return ctx.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "Invalid resource identifier."})
		}
		var resource any
		if resource, err = services.Domain.SetVirtualMachinePower(accountID, resourceID, request.Action); err != nil {
			return common.DomainError(ctx, err)
		}
		err = ctx.JSON(fiber.Map{"resource": resource})
		return
	}
	return
}

// getOwnershipNode returns an ownership node when the caller can view or configure it.
func getOwnershipNode(services common.Services) (handler fiber.Handler) {
	handler = func(ctx fiber.Ctx) (err error) {
		var actorID int
		if actorID, _ = common.AccountID(ctx); actorID < 1 {
			return ctx.SendStatus(fiber.StatusUnauthorized)
		}
		var nodeID int
		if nodeID, err = common.ParseID(ctx, "node_id"); err != nil {
			return ctx.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "Invalid ownership node identifier."})
		}
		var node *db.OwnershipNode
		if node, err = services.Store.OwnershipNodes.Select(nodeID); err != nil {
			return common.DomainError(ctx, err)
		}
		if node == nil {
			return ctx.SendStatus(fiber.StatusNotFound)
		}
		if err = services.Domain.Require(actorID, db.PermissionDeploymentManage, nodeID); err != nil {
			return common.DomainError(ctx, err)
		}
		err = ctx.JSON(fiber.Map{"ownership_node": node})
		return
	}
	return
}

// getVirtualMachine returns one simulated VM if it is visible to the actor.
func getVirtualMachine(services common.Services) (handler fiber.Handler) {
	handler = func(ctx fiber.Ctx) (err error) {
		var actorID int
		if actorID, _ = common.AccountID(ctx); actorID < 1 {
			return ctx.SendStatus(fiber.StatusUnauthorized)
		}
		var resourceID int
		if resourceID, err = common.ParseID(ctx, "resource_id"); err != nil {
			return ctx.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "Invalid resource identifier."})
		}
		var resource *db.ManagedResource
		if resource, err = services.Domain.GetVirtualMachine(actorID, resourceID); err != nil {
			return common.DomainError(ctx, err)
		}
		err = ctx.JSON(fiber.Map{"resource": resource})
		return
	}
	return
}

// getUserGroup returns a group's local membership alongside its metadata.
func getUserGroup(services common.Services) (handler fiber.Handler) {
	handler = func(ctx fiber.Ctx) (err error) {
		var actorID int
		if actorID, _ = common.AccountID(ctx); actorID < 1 {
			return ctx.SendStatus(fiber.StatusUnauthorized)
		}
		var groupID int
		if groupID, err = common.ParseID(ctx, "group_id"); err != nil {
			return ctx.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "Invalid user group identifier."})
		}
		var group *db.UserGroup
		if group, err = services.Store.UserGroups.Select(groupID); err != nil {
			return common.DomainError(ctx, err)
		}
		if group == nil {
			return ctx.SendStatus(fiber.StatusNotFound)
		}
		var deployment *db.Deployment
		if deployment, err = services.Store.Deployments.Select(group.DeploymentID); err != nil || deployment == nil || deployment.RootNodeID == nil {
			return ctx.SendStatus(fiber.StatusNotFound)
		}
		if err = services.Domain.Require(actorID, db.PermissionDeploymentManageGroups, *deployment.RootNodeID); err != nil {
			return common.DomainError(ctx, err)
		}
		var memberships []*db.GroupMembership
		if memberships, err = services.Store.GroupMemberships.SelectAll(); err != nil {
			return common.DomainError(ctx, err)
		}
		var identities []*db.AccountIdentity
		if identities, err = services.Store.AccountIdentities.SelectAll(); err != nil {
			return common.DomainError(ctx, err)
		}
		var names []string
		for _, membership := range memberships {
			if membership.GroupID != groupID {
				continue
			}
			for _, identity := range identities {
				if identity.AccountID == membership.AccountID {
					names = append(names, identity.QualifiedName)
					break
				}
			}
		}
		err = ctx.JSON(fiber.Map{"user_group": group, "members": names})
		return
	}
	return
}

// getPermissionGrant returns a grant for provider refresh after authorization.
func getPermissionGrant(services common.Services) (handler fiber.Handler) {
	handler = func(ctx fiber.Ctx) (err error) {
		var actorID int
		if actorID, _ = common.AccountID(ctx); actorID < 1 {
			return ctx.SendStatus(fiber.StatusUnauthorized)
		}
		var grantID int
		if grantID, err = common.ParseID(ctx, "grant_id"); err != nil {
			return ctx.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "Invalid permission grant identifier."})
		}
		var grant *db.PermissionGrant
		if grant, err = services.Store.PermissionGrants.Select(grantID); err != nil {
			return common.DomainError(ctx, err)
		}
		if grant == nil {
			return ctx.SendStatus(fiber.StatusNotFound)
		}
		if err = services.Domain.Require(actorID, db.PermissionDeploymentManagePermissions, grant.TargetNodeID); err != nil {
			return common.DomainError(ctx, err)
		}
		var subjectName string
		if grant.SubjectKind == db.GrantSubjectKindAccount {
			var identities []*db.AccountIdentity
			if identities, err = services.Store.AccountIdentities.SelectAll(); err != nil {
				return common.DomainError(ctx, err)
			}
			for _, identity := range identities {
				if identity.AccountID == grant.SubjectID {
					subjectName = identity.QualifiedName
					break
				}
			}
		}
		err = ctx.JSON(fiber.Map{"permission_grant": grant, "subject_name": subjectName})
		return
	}
	return
}

// resolveLocalAccountID maps one qualified local or sourced identity to its Organesson account.
func resolveLocalAccountID(store *db.Store, qualifiedName string) (accountID int, err error) {
	var identities []*db.AccountIdentity
	if identities, err = store.AccountIdentities.SelectAll(); err != nil {
		return
	}
	for _, identity := range identities {
		if strings.EqualFold(identity.QualifiedName, strings.TrimSpace(qualifiedName)) {
			accountID = identity.AccountID
			return
		}
	}
	err = domain.ErrNotFound
	return
}
