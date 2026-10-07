package v1

import (
	"encoding/json"

	"github.com/gofiber/fiber/v3"
	"github.com/z46-dev/organesson/backend/app/api/common"
	"github.com/z46-dev/organesson/backend/db"
	"github.com/z46-dev/organesson/backend/proxmox"
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
		var vmSnapshotCount int
		for _, snapshot := range snapshots {
			if snapshot.State == "creating" || snapshot.State == "ready" || snapshot.State == "deleting" {
				vmSnapshotCount++
			}
		}
		var snapshotQuota = fiber.Map{"vm_snapshots": vmSnapshotCount, "vm_max_snapshots": 0, "validated": false}
		var policyRecord *db.ProxmoxResourcePolicy
		if policyRecord, err = services.Store.ProxmoxResourcePolicies.Select(1); err != nil {
			return common.DomainError(ctx, err)
		}
		if policyRecord != nil {
			var policy proxmox.ResourcePolicy
			if err = json.Unmarshal([]byte(policyRecord.ConfigurationJSON), &policy); err != nil {
				return ctx.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "Stored Proxmox resource policy is invalid."})
			}
			snapshotQuota = fiber.Map{"vm_snapshots": vmSnapshotCount, "vm_max_snapshots": policy.VMLimits.MaxSnapshots, "validated": policyRecord.ValidatedAt != nil}
		}
		err = ctx.JSON(fiber.Map{"snapshots": snapshots, "snapshot_quota": snapshotQuota})
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
