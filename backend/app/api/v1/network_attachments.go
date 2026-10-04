package v1

import (
	"errors"

	"github.com/gofiber/fiber/v3"
	"github.com/z46-dev/organesson/backend/app/api/common"
	"github.com/z46-dev/organesson/backend/db"
	"github.com/z46-dev/organesson/backend/domain"
	"github.com/z46-dev/organesson/backend/proxmox"
)

// createNetworkAttachmentHandler reserves an owned NIC, then attaches it through the PVE API.
func createNetworkAttachmentHandler(services common.Services) (handler fiber.Handler) {
	handler = func(ctx fiber.Ctx) (err error) {
		var actorID int
		if actorID, _ = common.AccountID(ctx); actorID < 1 {
			return ctx.SendStatus(fiber.StatusUnauthorized)
		}
		if services.Proxmox == nil || !services.Proxmox.Configured() {
			return ctx.Status(fiber.StatusServiceUnavailable).JSON(fiber.Map{"error": "Proxmox is not configured; no NIC was attached."})
		}
		var vmResourceID int
		if vmResourceID, err = common.ParseID(ctx, "resource_id"); err != nil {
			return ctx.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "Invalid virtual-machine identifier."})
		}
		var request createNetworkAttachmentRequest
		if err = ctx.Bind().Body(&request); err != nil {
			return ctx.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "Invalid network attachment request."})
		}
		var resource *db.ManagedResource
		var configuration domain.ManagedNetworkAttachmentConfiguration
		if resource, configuration, err = services.Domain.ReserveNetworkAttachment(actorID, domain.NetworkAttachmentRequest{
			Name: request.Name, VirtualMachineID: vmResourceID, EnvironmentNetwork: request.EnvironmentNetwork,
			LogicalNetworkID: request.LogicalNetworkID, AddressPoolRequestID: request.AddressPoolRequestID,
			RequestedAddressCount: request.RequestedAddressCount,
		}); err != nil {
			return common.DomainError(ctx, err)
		}
		var placement proxmox.NetworkAttachmentPlacement
		if placement, err = services.Proxmox.AttachNetwork(ctx, configuration.Request); err != nil {
			return ctx.Status(fiber.StatusBadGateway).JSON(fiber.Map{"error": "Proxmox did not confirm NIC attachment; the ownership reservation remains for safe retry."})
		}
		if resource, err = services.Domain.ReadyNetworkAttachment(actorID, resource.ID, placement); err != nil {
			return common.DomainError(ctx, err)
		}
		configuration.Placement = placement
		err = ctx.Status(fiber.StatusCreated).JSON(fiber.Map{"resource": resource, "configuration": configuration})
		return
	}
	return
}

// configureGuestNetworkHandler applies a temporary QGA-delivered network script and saves its desired state.
func configureGuestNetworkHandler(services common.Services) (handler fiber.Handler) {
	handler = func(ctx fiber.Ctx) (err error) {
		var actorID int
		if actorID, _ = common.AccountID(ctx); actorID < 1 {
			return ctx.SendStatus(fiber.StatusUnauthorized)
		}
		var resourceID int
		if resourceID, err = common.ParseID(ctx, "resource_id"); err != nil {
			return ctx.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "Invalid attachment identifier."})
		}
		var input domain.GuestNetworkInput
		if err = ctx.Bind().Body(&input); err != nil {
			return ctx.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "Invalid guest network configuration."})
		}
		var resource *db.ManagedResource
		var attachment domain.ManagedNetworkAttachmentConfiguration
		if resource, attachment, err = services.Domain.GetNetworkAttachment(actorID, resourceID); err != nil {
			return common.DomainError(ctx, err)
		}
		if err = services.Domain.Require(actorID, db.PermissionDeploymentManage, resource.OwnershipID); err != nil {
			return common.DomainError(ctx, err)
		}
		var request proxmox.GuestNetworkRequest = proxmox.GuestNetworkRequest{
			Node: attachment.Request.Node, VMID: attachment.Request.VMID, VMOperationKey: attachment.Request.VMOperationKey,
			AttachmentKey: attachment.Request.AttachmentOperationKey, Bridge: attachment.Request.Bridge,
			NetworkOperationKey: attachment.Request.NetworkOperationKey, Placement: attachment.Placement,
			Method: input.Method, Address: input.Address, Gateway: input.Gateway, DNS: input.DNS, NeverDefault: input.NeverDefault,
		}
		if services.Proxmox == nil || !services.Proxmox.Configured() {
			return ctx.Status(fiber.StatusServiceUnavailable).JSON(fiber.Map{"error": "Proxmox is unavailable; guest network configuration was not applied."})
		}
		if err = ensureGuestVMRunning(ctx, services, attachment.VirtualMachineID); err != nil {
			return ctx.Status(fiber.StatusBadGateway).JSON(fiber.Map{"error": "Proxmox did not confirm the guest power-on needed for QEMU Guest Agent setup."})
		}
		if err = services.Proxmox.ConfigureGuestNetwork(ctx, request); err != nil {
			return ctx.Status(fiber.StatusBadGateway).JSON(fiber.Map{"error": "QEMU Guest Agent did not confirm guest network configuration."})
		}
		if resource, request, err = services.Domain.SaveGuestNetworkConfiguration(actorID, resourceID, input); err != nil {
			return common.DomainError(ctx, err)
		}
		err = ctx.JSON(fiber.Map{"resource": resource, "guest_network": request})
		return
	}
	return
}

// getGuestNetworkConfigurationHandler checks saved guest setup against NetworkManager through QGA.
func getGuestNetworkConfigurationHandler(services common.Services) (handler fiber.Handler) {
	handler = func(ctx fiber.Ctx) (err error) {
		var actorID int
		if actorID, _ = common.AccountID(ctx); actorID < 1 {
			return ctx.SendStatus(fiber.StatusUnauthorized)
		}
		var resourceID int
		if resourceID, err = common.ParseID(ctx, "resource_id"); err != nil {
			return ctx.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "Invalid attachment identifier."})
		}
		var resource *db.ManagedResource
		var attachment domain.ManagedNetworkAttachmentConfiguration
		if resource, attachment, err = services.Domain.GetNetworkAttachment(actorID, resourceID); err != nil {
			return common.DomainError(ctx, err)
		}
		if attachment.GuestNetwork == nil {
			return ctx.SendStatus(fiber.StatusNotFound)
		}
		if services.Proxmox == nil || !services.Proxmox.Configured() {
			return ctx.Status(fiber.StatusServiceUnavailable).JSON(fiber.Map{"error": "Proxmox is unavailable; guest network state cannot be verified."})
		}
		var verificationDeferred bool
		if verificationDeferred, err = verifyGuestNetworkConfiguration(ctx, services, *attachment.GuestNetwork); err != nil {
			return ctx.Status(fiber.StatusBadGateway).JSON(fiber.Map{"error": "Guest network configuration drifted or QEMU Guest Agent is unavailable."})
		}
		err = ctx.JSON(fiber.Map{"resource": resource, "guest_network": attachment.GuestNetwork, "verification_deferred": verificationDeferred})
		return
	}
	return
}

// verifyGuestNetworkConfiguration defers only the QGA check when the verified VM is stopped.
func verifyGuestNetworkConfiguration(ctx fiber.Ctx, services common.Services, request proxmox.GuestNetworkRequest) (deferred bool, err error) {
	var placement proxmox.VMPlacement
	if placement, err = services.Proxmox.ReadVM(ctx, request.Node, request.VMID, request.VMOperationKey); err != nil {
		return
	}
	if placement.PowerState == "stopped" {
		deferred = true
		return
	}
	if placement.PowerState != "running" {
		err = errors.New("managed guest power state is not available for QEMU Guest Agent verification")
		return
	}
	err = services.Proxmox.ReadGuestNetwork(ctx, request)
	return
}

// deleteGuestNetworkConfigurationHandler removes only the managed NetworkManager profile before clearing state.
func deleteGuestNetworkConfigurationHandler(services common.Services) (handler fiber.Handler) {
	handler = func(ctx fiber.Ctx) (err error) {
		var actorID int
		if actorID, _ = common.AccountID(ctx); actorID < 1 {
			return ctx.SendStatus(fiber.StatusUnauthorized)
		}
		var resourceID int
		if resourceID, err = common.ParseID(ctx, "resource_id"); err != nil {
			return ctx.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "Invalid attachment identifier."})
		}
		var resource *db.ManagedResource
		var attachment domain.ManagedNetworkAttachmentConfiguration
		if resource, attachment, err = services.Domain.GetNetworkAttachment(actorID, resourceID); err != nil {
			return common.DomainError(ctx, err)
		}
		if err = services.Domain.Require(actorID, db.PermissionDeploymentManage, resource.OwnershipID); err != nil {
			return common.DomainError(ctx, err)
		}
		if attachment.GuestNetwork == nil {
			return ctx.SendStatus(fiber.StatusNoContent)
		}
		if services.Proxmox == nil || !services.Proxmox.Configured() {
			return ctx.Status(fiber.StatusServiceUnavailable).JSON(fiber.Map{"error": "Proxmox is unavailable; guest setup state was retained."})
		}
		if err = ensureGuestVMRunning(ctx, services, attachment.VirtualMachineID); err != nil {
			return ctx.Status(fiber.StatusBadGateway).JSON(fiber.Map{"error": "Proxmox could not start the guest for managed profile cleanup."})
		}
		if err = services.Proxmox.RemoveGuestNetwork(ctx, *attachment.GuestNetwork); err != nil {
			return ctx.Status(fiber.StatusBadGateway).JSON(fiber.Map{"error": "QEMU Guest Agent did not confirm managed guest profile removal."})
		}
		if err = services.Domain.ClearGuestNetworkConfiguration(actorID, resourceID); err != nil {
			return common.DomainError(ctx, err)
		}
		err = ctx.SendStatus(fiber.StatusNoContent)
		return
	}
	return
}

// ensureGuestVMRunning starts a stopped managed guest before QEMU Guest Agent operations.
func ensureGuestVMRunning(ctx fiber.Ctx, services common.Services, resourceID int) (err error) {
	var vm *db.ManagedResource
	if vm, err = services.Store.ManagedResources.Select(resourceID); err != nil {
		return
	}
	if vm == nil || vm.Kind != "virtual_machine" || vm.ExternalID == "" || vm.ExternalNode == "" || vm.OperationKey == "" {
		err = domain.ErrNotFound
		return
	}
	var livePlacement proxmox.VMPlacement
	if livePlacement, err = services.Proxmox.ReadVM(ctx, vm.ExternalNode, vm.ExternalID, vm.OperationKey); err != nil {
		return
	}
	if livePlacement.PowerState != "running" && livePlacement.PowerState != "stopped" {
		err = errors.New("managed guest is not in a startable power state")
		return
	}
	vm.PowerState = livePlacement.PowerState
	if err = services.Store.ManagedResources.Update(vm); err != nil {
		return
	}
	if livePlacement.PowerState == "running" {
		return
	}
	var placement proxmox.VMPlacement
	if placement, err = services.Proxmox.PowerVM(ctx, vm.ExternalNode, vm.ExternalID, vm.OperationKey, "start"); err != nil {
		return
	}
	vm.PowerState = placement.PowerState
	err = services.Store.ManagedResources.Update(vm)
	return
}

// getNetworkAttachmentHandler returns saved NIC details with best-effort live Proxmox status.
func getNetworkAttachmentHandler(services common.Services) (handler fiber.Handler) {
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
		var configuration domain.ManagedNetworkAttachmentConfiguration
		if resource, configuration, err = services.Domain.GetNetworkAttachment(actorID, resourceID); err != nil {
			return common.DomainError(ctx, err)
		}
		var liveState string = "unavailable"
		if services.Proxmox != nil && services.Proxmox.Configured() {
			if err = services.Proxmox.ReadNetworkAttachment(ctx, configuration.Request, configuration.Placement); err == nil {
				liveState = "verified"
			} else if errors.Is(err, proxmox.ErrNetworkAttachmentNotFound) {
				liveState = "missing"
			} else {
				liveState = "unknown"
			}
		}
		err = ctx.JSON(fiber.Map{"resource": resource, "configuration": configuration, "live_state": liveState})
		return
	}
	return
}

// deleteNetworkAttachmentHandler removes the PVE NIC before releasing the claim and ownership node.
func deleteNetworkAttachmentHandler(services common.Services) (handler fiber.Handler) {
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
		var configuration domain.ManagedNetworkAttachmentConfiguration
		if resource, configuration, err = services.Domain.GetNetworkAttachment(actorID, resourceID); err != nil {
			return common.DomainError(ctx, err)
		}
		if err = services.Domain.Require(actorID, db.PermissionDeploymentManage, resource.OwnershipID); err != nil {
			return common.DomainError(ctx, err)
		}
		if services.Proxmox == nil || !services.Proxmox.Configured() {
			return ctx.Status(fiber.StatusServiceUnavailable).JSON(fiber.Map{"error": "Proxmox is unavailable; the NIC record and address claim were retained."})
		}
		if err = services.Proxmox.DetachNetwork(ctx, configuration.Request, configuration.Placement); err != nil {
			return ctx.Status(fiber.StatusBadGateway).JSON(fiber.Map{"error": "Proxmox did not confirm NIC removal; the record and address claim were retained."})
		}
		if err = services.Domain.DeleteNetworkAttachmentRecord(actorID, resourceID); err != nil {
			return common.DomainError(ctx, err)
		}
		err = ctx.SendStatus(fiber.StatusNoContent)
		return
	}
	return
}
