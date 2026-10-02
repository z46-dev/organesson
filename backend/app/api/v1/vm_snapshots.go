package v1

import (
	"github.com/gofiber/fiber/v3"
	"github.com/z46-dev/organesson/backend/app/api/common"
	"github.com/z46-dev/organesson/backend/db"
)

type createVMSnapshotRequest struct {
	Description string `json:"description"`
}

// listVMSnapshots returns the managed snapshot catalog for an authorized VM.
func listVMSnapshots(services common.Services) (handler fiber.Handler) {
	handler = func(ctx fiber.Ctx) (err error) {
		var actorID int
		if actorID, _ = common.AccountID(ctx); actorID < 1 {
			return ctx.SendStatus(fiber.StatusUnauthorized)
		}
		var resourceID int
		if resourceID, err = common.ParseID(ctx, "resource_id"); err != nil {
			return ctx.SendStatus(fiber.StatusBadRequest)
		}
		var snapshots []*db.ManagedVMSnapshot
		if snapshots, _, err = services.Domain.ListVMSnapshots(actorID, resourceID); err != nil {
			return common.DomainError(ctx, err)
		}
		err = ctx.JSON(fiber.Map{"snapshots": snapshots})
		return
	}
	return
}

// createVMSnapshot reserves policy capacity and asks Proxmox to create a managed snapshot.
func createVMSnapshot(services common.Services) (handler fiber.Handler) {
	handler = func(ctx fiber.Ctx) (err error) {
		var actorID int
		if actorID, _ = common.AccountID(ctx); actorID < 1 {
			return ctx.SendStatus(fiber.StatusUnauthorized)
		}
		var resourceID int
		if resourceID, err = common.ParseID(ctx, "resource_id"); err != nil {
			return ctx.SendStatus(fiber.StatusBadRequest)
		}
		var request createVMSnapshotRequest
		if err = ctx.Bind().Body(&request); err != nil {
			return ctx.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "Invalid snapshot request."})
		}
		var snapshot *db.ManagedVMSnapshot
		var resource *db.ManagedResource
		if snapshot, resource, err = services.Domain.ReserveVMSnapshot(actorID, resourceID, request.Description); err != nil {
			return common.DomainError(ctx, err)
		}
		if services.Proxmox == nil {
			_ = services.Domain.SetVMSnapshotState(snapshot, "failed")
			return ctx.Status(fiber.StatusServiceUnavailable).JSON(fiber.Map{"error": "Proxmox snapshot operations are unavailable."})
		}
		if err = services.Proxmox.CreateVMSnapshot(ctx, resource.ExternalNode, resource.ExternalID, resource.OperationKey, snapshot.Name, snapshot.SnapshotKey); err != nil {
			_ = services.Domain.SetVMSnapshotState(snapshot, "failed")
			return ctx.Status(fiber.StatusBadGateway).JSON(fiber.Map{"error": "Proxmox did not confirm snapshot creation; its capacity reservation is retained for review."})
		}
		if snapshot, _, err = services.Domain.CompleteVMSnapshot(actorID, snapshot.ID, true); err != nil {
			return common.DomainError(ctx, err)
		}
		err = ctx.Status(fiber.StatusCreated).JSON(fiber.Map{"snapshot": snapshot})
		return
	}
	return
}

// restoreVMSnapshot rolls the VM back to one managed snapshot after authorization.
func restoreVMSnapshot(services common.Services) (handler fiber.Handler) {
	handler = func(ctx fiber.Ctx) (err error) {
		var actorID int
		if actorID, _ = common.AccountID(ctx); actorID < 1 {
			return ctx.SendStatus(fiber.StatusUnauthorized)
		}
		var snapshotID int
		if snapshotID, err = common.ParseID(ctx, "snapshot_id"); err != nil {
			return ctx.SendStatus(fiber.StatusBadRequest)
		}
		var snapshot *db.ManagedVMSnapshot
		var resource *db.ManagedResource
		if snapshot, resource, err = services.Domain.GetVMSnapshot(actorID, snapshotID); err != nil {
			return common.DomainError(ctx, err)
		}
		if snapshot.State != "ready" {
			return ctx.Status(fiber.StatusConflict).JSON(fiber.Map{"error": "Only confirmed snapshots can be restored."})
		}
		if services.Proxmox == nil {
			return ctx.SendStatus(fiber.StatusServiceUnavailable)
		}
		if err = services.Proxmox.RestoreVMSnapshot(ctx, resource.ExternalNode, resource.ExternalID, resource.OperationKey, snapshot.Name, snapshot.SnapshotKey); err != nil {
			return ctx.Status(fiber.StatusBadGateway).JSON(fiber.Map{"error": "Proxmox did not confirm snapshot restore."})
		}
		if err = services.Domain.RecordVMSnapshotRestore(actorID, snapshotID); err != nil {
			return common.DomainError(ctx, err)
		}
		err = ctx.SendStatus(fiber.StatusNoContent)
		return
	}
	return
}

// deleteVMSnapshot removes an authorized managed snapshot and releases its reserved capacity.
func deleteVMSnapshot(services common.Services) (handler fiber.Handler) {
	handler = func(ctx fiber.Ctx) (err error) {
		var actorID int
		if actorID, _ = common.AccountID(ctx); actorID < 1 {
			return ctx.SendStatus(fiber.StatusUnauthorized)
		}
		var snapshotID int
		if snapshotID, err = common.ParseID(ctx, "snapshot_id"); err != nil {
			return ctx.SendStatus(fiber.StatusBadRequest)
		}
		var snapshot *db.ManagedVMSnapshot
		var resource *db.ManagedResource
		if snapshot, resource, err = services.Domain.GetVMSnapshot(actorID, snapshotID); err != nil {
			return common.DomainError(ctx, err)
		}
		if services.Proxmox == nil {
			return ctx.SendStatus(fiber.StatusServiceUnavailable)
		}
		if err = services.Proxmox.DeleteVMSnapshot(ctx, resource.ExternalNode, resource.ExternalID, resource.OperationKey, snapshot.Name, snapshot.SnapshotKey); err != nil {
			return ctx.Status(fiber.StatusBadGateway).JSON(fiber.Map{"error": "Proxmox did not confirm snapshot deletion; the capacity reservation was retained."})
		}
		if err = services.Domain.DeleteVMSnapshotRecord(actorID, snapshotID); err != nil {
			return common.DomainError(ctx, err)
		}
		err = ctx.SendStatus(fiber.StatusNoContent)
		return
	}
	return
}
