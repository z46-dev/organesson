package v1

import (
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
		Name string `json:"name"`
	}

	addGroupMemberRequest struct {
		AccountID int `json:"account_id"`
	}

	createGrantRequest struct {
		SubjectKind int    `json:"subject_kind"`
		SubjectID   int    `json:"subject_id"`
		Permission  string `json:"permission"`
	}
)

// initDeployments registers authenticated deployment and initial VM operations.
func initDeployments(parent fiber.Router, services common.Services) {
	var router fiber.Router = parent.Group("/deployments", common.RequireSession(services.Authentication))
	router.Get("/", listDeployments(services))
	router.Post("/", createDeployment(services))
	router.Get("/:deployment_id", getDeployment(services))
	router.Post("/:deployment_id/virtual-machines", createVirtualMachine(services))
	router.Post("/:deployment_id/logical-groups", createLogicalGroup(services))
	router.Post("/:deployment_id/user-groups", createUserGroup(services))
	parent.Post("/user-groups/:group_id/members", common.RequireSession(services.Authentication), addGroupMember(services))
	parent.Delete("/user-groups/:group_id/members/:account_id", common.RequireSession(services.Authentication), removeGroupMember(services))
	parent.Post("/ownership-nodes/:node_id/grants", common.RequireSession(services.Authentication), createPermissionGrant(services))
	parent.Delete("/permission-grants/:grant_id", common.RequireSession(services.Authentication), deletePermissionGrant(services))
	parent.Post("/virtual-machines/:resource_id/power", common.RequireSession(services.Authentication), setVirtualMachinePower(services))
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
		if err = ctx.Bind().Body(&request); err != nil || request.ParentNodeID < 1 {
			return ctx.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "A parent node and group name are required."})
		}
		var accountID int
		if accountID, _ = common.AccountID(ctx); accountID == 0 {
			return ctx.SendStatus(fiber.StatusUnauthorized)
		}
		var deploymentID int
		if deploymentID, err = common.ParseID(ctx, "deployment_id"); err != nil {
			return ctx.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "Invalid deployment identifier."})
		}
		var node *db.OwnershipNode
		if node, err = services.Domain.CreateLogicalGroup(accountID, deploymentID, request.ParentNodeID, request.Name); err != nil {
			return common.DomainError(ctx, err)
		}
		err = ctx.Status(fiber.StatusCreated).JSON(fiber.Map{"logical_group": node})
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
		err = ctx.Status(fiber.StatusCreated).JSON(fiber.Map{"user_group": group})
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

// createPermissionGrant creates a fixed permission mapping on an ownership node.
func createPermissionGrant(services common.Services) (handler fiber.Handler) {
	handler = func(ctx fiber.Ctx) (err error) {
		var request createGrantRequest
		if err = ctx.Bind().Body(&request); err != nil || request.SubjectID < 1 {
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
		var grant *db.PermissionGrant
		if grant, err = services.Domain.CreatePermissionGrant(accountID, db.GrantSubjectKind(request.SubjectKind), request.SubjectID, request.Permission, nodeID); err != nil {
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
