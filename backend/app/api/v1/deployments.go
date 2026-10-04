package v1

import (
	"encoding/json"
	"errors"
	"strings"

	"github.com/gofiber/fiber/v3"
	"github.com/z46-dev/organesson/backend/app/api/common"
	"github.com/z46-dev/organesson/backend/db"
	"github.com/z46-dev/organesson/backend/domain"
	"github.com/z46-dev/organesson/backend/proxmox"
)

type (
	createDeploymentRequest struct {
		Name        string `json:"name"`
		Description string `json:"description"`
	}

	createVMRequest struct {
		ParentNodeID     int    `json:"parent_node_id"`
		Name             string `json:"name"`
		ProvisioningMode string `json:"provisioning_mode"`
		Template         string `json:"template"`
		Pool             string `json:"pool"`
		Storage          string `json:"storage"`
		Cores            int    `json:"cpu_cores"`
		MemoryMiB        int    `json:"memory_mib"`
		BootDiskGiB      int    `json:"boot_disk_gib"`
	}

	createAddressPoolRequest struct {
		Name               string `json:"name"`
		EnvironmentNetwork string `json:"environment_network"`
		AddressFamily      string `json:"address_family"`
		AddressCount       int    `json:"address_count"`
	}

	createNetworkRequest struct {
		ParentNodeID int    `json:"parent_node_id"`
		Name         string `json:"name"`
		Mode         string `json:"mode"`
		Subnet       string `json:"ipv4_subnet"`
		Gateway      string `json:"ipv4_gateway"`
		DHCPEnabled  bool   `json:"dhcp_enabled"`
		EgressPolicy string `json:"egress_policy"`
	}

	createNetworkAttachmentRequest struct {
		Name                  string `json:"name"`
		EnvironmentNetwork    string `json:"environment_network"`
		LogicalNetworkID      int    `json:"logical_network_id"`
		AddressPoolRequestID  int    `json:"address_pool_request_id"`
		RequestedAddressCount int    `json:"requested_address_count"`
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
	router.Get("/:deployment_id/access", getDeploymentAccess(services))
	router.Put("/:deployment_id", updateDeployment(services))
	router.Delete("/:deployment_id", deleteDeployment(services))
	router.Post("/:deployment_id/virtual-machines", createVirtualMachine(services))
	router.Post("/:deployment_id/address-pool-requests", createAddressPoolRequestHandler(services))
	router.Post("/:deployment_id/networks", createNetworkHandler(services))
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
	parent.Get("/virtual-machines/:resource_id/snapshots", common.RequireActor(services.Authentication), listVMSnapshots(services))
	parent.Post("/virtual-machines/:resource_id/snapshots", common.RequireActor(services.Authentication), createVMSnapshot(services))
	parent.Get("/virtual-machines/:resource_id/console", common.RequireActor(services.Authentication), vmConsole(services))
	parent.Post("/vm-snapshots/:snapshot_id/restore", common.RequireActor(services.Authentication), restoreVMSnapshot(services))
	parent.Delete("/vm-snapshots/:snapshot_id", common.RequireActor(services.Authentication), deleteVMSnapshot(services))
	parent.Post("/virtual-machines/:resource_id/guest-setup", common.RequireActor(services.Authentication), executeGuestArtifactHandler(services))
	parent.Get("/virtual-machines/:resource_id", common.RequireActor(services.Authentication), getVirtualMachine(services))
	parent.Get("/address-pool-requests/:resource_id", common.RequireActor(services.Authentication), getAddressPoolRequestHandler(services))
	parent.Delete("/address-pool-requests/:resource_id", common.RequireActor(services.Authentication), deleteAddressPoolRequestHandler(services))
	parent.Get("/networks/:resource_id", common.RequireActor(services.Authentication), getNetworkHandler(services))
	parent.Delete("/networks/:resource_id", common.RequireActor(services.Authentication), deleteNetworkHandler(services))
	parent.Delete("/virtual-machines/:resource_id", common.RequireActor(services.Authentication), deleteVirtualMachine(services))
	parent.Post("/virtual-machines/:resource_id/network-attachments", common.RequireActor(services.Authentication), createNetworkAttachmentHandler(services))
	parent.Post("/network-attachments/:resource_id/guest-network-configuration", common.RequireActor(services.Authentication), configureGuestNetworkHandler(services))
	parent.Get("/network-attachments/:resource_id/guest-network-configuration", common.RequireActor(services.Authentication), getGuestNetworkConfigurationHandler(services))
	parent.Delete("/network-attachments/:resource_id/guest-network-configuration", common.RequireActor(services.Authentication), deleteGuestNetworkConfigurationHandler(services))
	parent.Get("/network-attachments/:resource_id", common.RequireActor(services.Authentication), getNetworkAttachmentHandler(services))
	parent.Delete("/network-attachments/:resource_id", common.RequireActor(services.Authentication), deleteNetworkAttachmentHandler(services))
}

// getDeploymentAccess returns the caller's authorized group and permission workspace data.
func getDeploymentAccess(services common.Services) (handler fiber.Handler) {
	handler = func(ctx fiber.Ctx) (err error) {
		var actorID int
		if actorID, _ = common.AccountID(ctx); actorID < 1 {
			return ctx.SendStatus(fiber.StatusUnauthorized)
		}
		var deploymentID int
		if deploymentID, err = common.ParseID(ctx, "deployment_id"); err != nil {
			return ctx.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "Invalid deployment identifier."})
		}
		var access *domain.DeploymentAccessSummary
		if access, err = services.Domain.GetDeploymentAccess(actorID, deploymentID); err != nil {
			return common.DomainError(ctx, err)
		}
		err = ctx.JSON(access)
		return
	}
	return
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
		var resource *db.ManagedResource
		if resource, err = services.Store.ManagedResources.Select(resourceID); err != nil {
			return common.DomainError(ctx, err)
		}
		if resource == nil || resource.Kind != "virtual_machine" {
			return ctx.SendStatus(fiber.StatusNotFound)
		}
		if err = services.Domain.Require(actorID, db.PermissionDeploymentManage, resource.OwnershipID); err != nil {
			return common.DomainError(ctx, err)
		}
		if resource.ExternalID != "" {
			if services.Proxmox == nil {
				return ctx.Status(fiber.StatusServiceUnavailable).JSON(fiber.Map{"error": "Proxmox is unavailable; the managed VM was not removed from Organesson."})
			}
			if err = services.Proxmox.DeleteVM(ctx, resource.ExternalNode, resource.ExternalID, resource.OperationKey); err != nil {
				return ctx.Status(fiber.StatusBadGateway).JSON(fiber.Map{"error": "Proxmox did not confirm VM deletion; the Organesson record was retained."})
			}
		}
		if err = services.Domain.DeleteVirtualMachine(actorID, resourceID); err != nil {
			return common.DomainError(ctx, err)
		}
		err = ctx.SendStatus(fiber.StatusNoContent)
		return
	}
	return
}

// createAddressPoolRequestHandler allocates the configured addresses for one deployment.
func createAddressPoolRequestHandler(services common.Services) (handler fiber.Handler) {
	handler = func(ctx fiber.Ctx) (err error) {
		var actorID int
		if actorID, _ = common.AccountID(ctx); actorID < 1 {
			return ctx.SendStatus(fiber.StatusUnauthorized)
		}
		var deploymentID int
		if deploymentID, err = common.ParseID(ctx, "deployment_id"); err != nil {
			return ctx.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "Invalid deployment identifier."})
		}
		var request createAddressPoolRequest
		if err = ctx.Bind().Body(&request); err != nil {
			return ctx.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "Invalid address-pool request."})
		}
		var resource *db.ManagedResource
		var allocation domain.AddressPoolRequest
		if resource, allocation, err = services.Domain.ReserveAddressPoolRequest(actorID, domain.AddressPoolRequest{
			DeploymentID: deploymentID, Name: request.Name, EnvironmentNetwork: request.EnvironmentNetwork,
			AddressFamily: request.AddressFamily, AddressCount: request.AddressCount,
		}); err != nil {
			return common.DomainError(ctx, err)
		}
		err = ctx.JSON(fiber.Map{"resource": resource, "allocation": allocation})
		return
	}
	return
}

// getAddressPoolRequestHandler reads an allocation visible to the caller.
func getAddressPoolRequestHandler(services common.Services) (handler fiber.Handler) {
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
		var allocation domain.AddressPoolRequest
		if resource, allocation, err = services.Domain.GetAddressPoolRequest(actorID, resourceID); err != nil {
			return common.DomainError(ctx, err)
		}
		err = ctx.JSON(fiber.Map{"resource": resource, "allocation": allocation})
		return
	}
	return
}

// deleteAddressPoolRequestHandler releases a deployment allocation.
func deleteAddressPoolRequestHandler(services common.Services) (handler fiber.Handler) {
	handler = func(ctx fiber.Ctx) (err error) {
		var actorID int
		if actorID, _ = common.AccountID(ctx); actorID < 1 {
			return ctx.SendStatus(fiber.StatusUnauthorized)
		}
		var resourceID int
		if resourceID, err = common.ParseID(ctx, "resource_id"); err != nil {
			return ctx.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "Invalid resource identifier."})
		}
		if err = services.Domain.DeleteAddressPoolRequest(actorID, resourceID); err != nil {
			return common.DomainError(ctx, err)
		}
		err = ctx.SendStatus(fiber.StatusNoContent)
		return
	}
	return
}

// createNetworkHandler reserves an ownership resource and creates its isolated PVE SDN network.
func createNetworkHandler(services common.Services) (handler fiber.Handler) {
	handler = func(ctx fiber.Ctx) (err error) {
		var actorID int
		if actorID, _ = common.AccountID(ctx); actorID < 1 {
			return ctx.SendStatus(fiber.StatusUnauthorized)
		}
		if services.Proxmox == nil || !services.Proxmox.Configured() {
			return ctx.Status(fiber.StatusServiceUnavailable).JSON(fiber.Map{"error": "Proxmox is not configured; no network was created."})
		}
		var deploymentID int
		if deploymentID, err = common.ParseID(ctx, "deployment_id"); err != nil {
			return ctx.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "Invalid deployment identifier."})
		}
		var request createNetworkRequest
		if err = ctx.Bind().Body(&request); err != nil {
			return ctx.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "Invalid network request."})
		}
		var networkRequest proxmox.SDNNetworkRequest = proxmox.SDNNetworkRequest{
			Name: request.Name, Mode: request.Mode, Subnet: request.Subnet, Gateway: request.Gateway,
			DHCPEnabled: request.DHCPEnabled, EgressPolicy: request.EgressPolicy,
		}
		var resource *db.ManagedResource
		if resource, err = services.Domain.ReserveSDNNetwork(actorID, deploymentID, request.ParentNodeID, networkRequest); err != nil {
			return common.DomainError(ctx, err)
		}
		var configuration domain.ManagedNetworkConfiguration
		if err = json.Unmarshal([]byte(resource.ConfigurationJSON), &configuration); err != nil {
			return ctx.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "Stored network configuration is invalid."})
		}
		var placement proxmox.SDNNetworkPlacement
		if placement, err = services.Proxmox.CreateSDNNetwork(ctx, configuration.Request); err != nil {
			return ctx.Status(fiber.StatusBadGateway).JSON(fiber.Map{"error": "Proxmox did not confirm isolated network creation; the ownership reservation remains for safe retry."})
		}
		if resource, err = services.Domain.ReadySDNNetwork(actorID, resource.ID, placement); err != nil {
			return common.DomainError(ctx, err)
		}
		configuration.Placement = placement
		err = ctx.Status(fiber.StatusCreated).JSON(fiber.Map{"resource": resource, "configuration": configuration})
		return
	}
	return
}

// getNetworkHandler returns the recorded network configuration and best-effort live PVE status.
func getNetworkHandler(services common.Services) (handler fiber.Handler) {
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
		var configuration domain.ManagedNetworkConfiguration
		if resource, configuration, err = services.Domain.GetSDNNetwork(actorID, resourceID); err != nil {
			return common.DomainError(ctx, err)
		}
		var liveState string = "unavailable"
		var ipamState string = "unavailable"
		var ipamEntries []proxmox.SDNIPAMEntry
		if services.Proxmox != nil && services.Proxmox.Configured() {
			if err = services.Proxmox.ReadSDNNetwork(ctx, configuration.Request, configuration.Placement); err == nil {
				liveState = "verified"
				ipamEntries, ipamState, err = services.Proxmox.ReadSDNNetworkIPAM(ctx, configuration.Request, configuration.Placement)
				if err != nil {
					ipamState = "unavailable"
				}
			} else if errors.Is(err, proxmox.ErrSDNNetworkNotFound) {
				liveState = "missing"
			} else {
				liveState = "unknown"
			}
		}
		err = ctx.JSON(fiber.Map{"resource": resource, "configuration": configuration, "live_state": liveState, "ipam_state": ipamState, "ipam_entries": ipamEntries})
		return
	}
	return
}

// deleteNetworkHandler deletes the PVE VNet before releasing Organesson ownership.
func deleteNetworkHandler(services common.Services) (handler fiber.Handler) {
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
		var configuration domain.ManagedNetworkConfiguration
		if resource, configuration, err = services.Domain.GetSDNNetwork(actorID, resourceID); err != nil {
			return common.DomainError(ctx, err)
		}
		if err = services.Domain.Require(actorID, db.PermissionDeploymentManage, resource.OwnershipID); err != nil {
			return common.DomainError(ctx, err)
		}
		if services.Proxmox == nil || !services.Proxmox.Configured() {
			return ctx.Status(fiber.StatusServiceUnavailable).JSON(fiber.Map{"error": "Proxmox is unavailable; the network record was retained."})
		}
		if err = services.Proxmox.DeleteSDNNetwork(ctx, resource.ExternalID, configuration.Request.OperationKey, configuration.Request.VNetSourceZone); err != nil {
			return ctx.Status(fiber.StatusBadGateway).JSON(fiber.Map{"error": "Proxmox did not confirm network deletion; the Organesson record was retained."})
		}
		if err = services.Domain.DeleteSDNNetworkRecord(actorID, resourceID); err != nil {
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
		for _, resource := range summary.Resources {
			if resource.Kind != "virtual_machine" || resource.ExternalID == "" {
				continue
			}
			if services.Proxmox == nil {
				return ctx.Status(fiber.StatusServiceUnavailable).JSON(fiber.Map{"error": "Proxmox is unavailable; live VM state cannot be refreshed."})
			}
			var placement proxmox.VMPlacement
			if placement, err = services.Proxmox.ReadVM(ctx, resource.ExternalNode, resource.ExternalID, resource.OperationKey); err != nil {
				return ctx.Status(fiber.StatusBadGateway).JSON(fiber.Map{"error": "Could not refresh deployment VM state from Proxmox."})
			}
			resource.PowerState = placement.PowerState
			resource.ExternalNode = placement.Node
			if _, err = services.Domain.RecordLiveVMPlacement(accountID, resource.ID, placement); err != nil {
				return common.DomainError(ctx, err)
			}
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
		if request.ProvisioningMode == "proxmox" {
			if services.Proxmox == nil || !services.Proxmox.Configured() {
				return ctx.Status(fiber.StatusServiceUnavailable).JSON(fiber.Map{"error": "Proxmox provisioning was requested but the backend connection is not configured."})
			}
			var policyRecord *db.ProxmoxResourcePolicy
			if policyRecord, err = services.Store.ProxmoxResourcePolicies.Select(1); err != nil {
				return ctx.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "Could not load the platform resource policy."})
			}
			if policyRecord == nil || policyRecord.ValidatedAt == nil {
				return ctx.Status(fiber.StatusConflict).JSON(fiber.Map{"error": "Validate the Proxmox resource policy before provisioning VMs."})
			}
			var policy proxmox.ResourcePolicy
			if err = json.Unmarshal([]byte(policyRecord.ConfigurationJSON), &policy); err != nil {
				return ctx.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "The stored Proxmox resource policy is invalid."})
			}
			var configHash string
			if configHash, err = proxmox.ResourcePolicyHash(policy); err != nil || configHash != policyRecord.ValidatedConfigHash {
				return ctx.Status(fiber.StatusConflict).JSON(fiber.Map{"error": "The Proxmox resource policy changed since it was validated."})
			}
			var inventory proxmox.ResourceInventory
			if inventory, err = services.Proxmox.ResourceInventory(ctx); err != nil {
				return ctx.Status(fiber.StatusBadGateway).JSON(fiber.Map{"error": "Could not verify current Proxmox inventory."})
			}
			var validation proxmox.ResourcePolicyValidation = proxmox.ValidateResourcePolicy(policy, &inventory)
			if !validation.Valid {
				return ctx.Status(fiber.StatusConflict).JSON(fiber.Map{"error": "The Proxmox resource policy no longer matches current inventory.", "validation": validation})
			}
			if request.Pool == "" && len(policy.ResourcePools) == 1 {
				request.Pool = policy.ResourcePools[0]
			}
			if request.Storage == "" && len(policy.Storages) == 1 {
				request.Storage = policy.Storages[0]
			}
			var spec proxmox.VMCloneRequest = proxmox.VMCloneRequest{
				TemplateAlias: request.Template,
				Name:          request.Name,
				Pool:          request.Pool,
				Storage:       request.Storage,
				Cores:         request.Cores,
				MemoryMiB:     request.MemoryMiB,
				BootDiskGiB:   request.BootDiskGiB,
			}
			var template *db.VMTemplate
			if resource, template, err = services.Domain.ReserveProxmoxVirtualMachine(accountID, deploymentID, request.ParentNodeID, spec); err != nil {
				return common.DomainError(ctx, err)
			}
			if resource.ExternalID != "" {
				var placement proxmox.VMPlacement
				if placement, err = services.Proxmox.ReadVM(ctx, resource.ExternalNode, resource.ExternalID, resource.OperationKey); err != nil {
					return ctx.Status(fiber.StatusBadGateway).JSON(fiber.Map{"error": "The already-managed Proxmox VM could not be read; refusing to create a duplicate."})
				}
				if resource, err = services.Domain.RecordProxmoxVMPlacement(accountID, resource.ID, placement); err != nil {
					return common.DomainError(ctx, err)
				}
			}
			if resource.ExternalID == "" {
				var placement proxmox.VMPlacement
				if placement, err = services.Proxmox.CloneVM(ctx, proxmox.VMCloneRequest{
					SourceVMID:    template.SourceID,
					TemplateAlias: request.Template,
					Name:          spec.Name,
					Pool:          spec.Pool,
					Storage:       spec.Storage,
					Cores:         spec.Cores,
					MemoryMiB:     spec.MemoryMiB,
					BootDiskGiB:   spec.BootDiskGiB,
					OperationKey:  resource.OperationKey,
				}); err != nil {
					return ctx.Status(fiber.StatusBadGateway).JSON(fiber.Map{"error": "Proxmox VM provisioning failed. The reservation was retained so a retry can safely recover or continue it."})
				}
				if resource, err = services.Domain.RecordProxmoxVMPlacement(accountID, resource.ID, placement); err != nil {
					return common.DomainError(ctx, err)
				}
			}
		} else if request.ProvisioningMode == "" || request.ProvisioningMode == "simulated" {
			if resource, err = services.Domain.CreateVirtualMachine(accountID, deploymentID, request.ParentNodeID, request.Name); err != nil {
				return common.DomainError(ctx, err)
			}
		} else {
			return ctx.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "VM provisioning_mode must be simulated or proxmox."})
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
		var managed *db.ManagedResource
		if managed, err = services.Store.ManagedResources.Select(resourceID); err != nil {
			return common.DomainError(ctx, err)
		}
		if managed == nil || managed.Kind != "virtual_machine" {
			return ctx.SendStatus(fiber.StatusNotFound)
		}
		var resource any
		if managed.ExternalID != "" {
			if _, err = services.Domain.AuthorizeVirtualMachinePower(accountID, resourceID, request.Action); err != nil {
				return common.DomainError(ctx, err)
			}
			if services.Proxmox == nil {
				return ctx.Status(fiber.StatusServiceUnavailable).JSON(fiber.Map{"error": "Proxmox is unavailable; no power action was applied."})
			}
			var placement proxmox.VMPlacement
			if placement, err = services.Proxmox.PowerVM(ctx, managed.ExternalNode, managed.ExternalID, managed.OperationKey, request.Action); err != nil {
				return ctx.Status(fiber.StatusBadGateway).JSON(fiber.Map{"error": "Proxmox did not confirm the requested power action."})
			}
			if resource, err = services.Domain.RecordLiveVMPlacement(accountID, resourceID, placement); err != nil {
				return common.DomainError(ctx, err)
			}
		} else if managed.OperationKey != "" {
			return ctx.Status(fiber.StatusConflict).JSON(fiber.Map{"error": "Proxmox VM provisioning has not completed; no power action was applied."})
		} else if resource, err = services.Domain.SetVirtualMachinePower(accountID, resourceID, request.Action); err != nil {
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
		var liveCPU proxmox.VMPlacement
		if resource.ExternalID != "" {
			if services.Proxmox == nil {
				return ctx.Status(fiber.StatusServiceUnavailable).JSON(fiber.Map{"error": "Proxmox is unavailable; live VM state cannot be refreshed."})
			}
			if liveCPU, err = services.Proxmox.ReadVM(ctx, resource.ExternalNode, resource.ExternalID, resource.OperationKey); err != nil {
				return ctx.Status(fiber.StatusBadGateway).JSON(fiber.Map{"error": "Could not refresh this VM from Proxmox."})
			}
			if resource, err = services.Domain.RecordLiveVMPlacement(actorID, resourceID, liveCPU); err != nil {
				return common.DomainError(ctx, err)
			}
		}
		var canConsoleControl bool
		if canConsoleControl, err = services.Domain.Can(actorID, db.PermissionVMConsole, resource.OwnershipID); err != nil {
			return common.DomainError(ctx, err)
		}
		var specification proxmox.VMCloneRequest
		if resource.ConfigurationJSON != "" {
			if err = json.Unmarshal([]byte(resource.ConfigurationJSON), &specification); err != nil {
				return ctx.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "Stored VM configuration is invalid."})
			}
		}
		var architecture string
		if specification.TemplateAlias != "" {
			var aliases []*db.VMTemplateAlias
			if aliases, err = services.Store.VMTemplateAliases.SelectAll(); err != nil {
				return common.DomainError(ctx, err)
			}
			var templates []*db.VMTemplate
			if templates, err = services.Store.VMTemplates.SelectAll(); err != nil {
				return common.DomainError(ctx, err)
			}
			for _, alias := range aliases {
				if alias.Alias == specification.TemplateAlias {
					for _, template := range templates {
						if template.ID == alias.VMTemplateID {
							architecture = template.Architecture
						}
					}
				}
			}
		}
		var sockets int = liveCPU.Sockets
		var cores int = liveCPU.Cores
		if sockets < 1 {
			sockets = 1
		}
		if cores < 1 {
			cores = specification.Cores
		}
		err = ctx.JSON(fiber.Map{"resource": struct {
			*db.ManagedResource
			CanConsoleControl bool `json:"can_console_control"`
		}{resource, canConsoleControl}, "specification": fiber.Map{
			"template_alias": specification.TemplateAlias,
			"sockets":        sockets,
			"cores":          cores,
			"architecture":   architecture,
			"cpu_model":      liveCPU.CPUModel,
			"memory_mib":     specification.MemoryMiB,
			"boot_disk_gib":  specification.BootDiskGiB,
			"pool":           specification.Pool,
			"storage":        specification.Storage,
		}})
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
