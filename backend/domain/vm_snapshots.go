package domain

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/z46-dev/organesson/backend/db"
	"github.com/z46-dev/organesson/backend/proxmox"
)

// ReserveVMSnapshot checks permission and reserves the VM's declared disk size for one snapshot.
func (service *Service) ReserveVMSnapshot(actorID int, resourceID int, description string) (snapshot *db.ManagedVMSnapshot, resource *db.ManagedResource, err error) {
	service.provisioningLock.Lock()
	defer service.provisioningLock.Unlock()
	if resource, err = service.store.ManagedResources.Select(resourceID); err != nil {
		return
	}
	if resource == nil || resource.Kind != "virtual_machine" || resource.ExternalID == "" {
		err = ErrNotFound
		return
	}
	if err = service.Require(actorID, db.PermissionVMSnapshot, resource.OwnershipID); err != nil {
		return
	}
	description = strings.TrimSpace(description)
	if description == "" || len(description) > 160 {
		err = fmt.Errorf("%w: snapshot description must be between 1 and 160 characters", ErrInvalidInput)
		return
	}
	var request proxmox.VMCloneRequest
	if err = json.Unmarshal([]byte(resource.ConfigurationJSON), &request); err != nil || request.BootDiskGiB < 1 {
		err = fmt.Errorf("%w: managed VM disk size is unavailable for snapshot reservation", ErrInvalidInput)
		return
	}
	var policyRecord *db.ProxmoxResourcePolicy
	if policyRecord, err = service.store.ProxmoxResourcePolicies.Select(1); err != nil {
		return
	}
	if policyRecord == nil || policyRecord.ValidatedAt == nil {
		err = fmt.Errorf("%w: Proxmox resource policy must be validated before snapshots can be created", ErrInvalidInput)
		return
	}
	var policy proxmox.ResourcePolicy
	if err = json.Unmarshal([]byte(policyRecord.ConfigurationJSON), &policy); err != nil {
		return
	}
	var policyHash string
	if policyHash, err = proxmox.ResourcePolicyHash(policy); err != nil {
		return
	}
	if policyHash != policyRecord.ValidatedConfigHash {
		err = fmt.Errorf("%w: Proxmox resource policy changed after validation", ErrInvalidInput)
		return
	}
	var validation proxmox.ResourcePolicyValidation
	if err = json.Unmarshal([]byte(policyRecord.ValidationJSON), &validation); err != nil || !validation.Valid {
		err = fmt.Errorf("%w: Proxmox resource policy is not valid", ErrInvalidInput)
		return
	}
	var used int64
	var snapshots []*db.ManagedVMSnapshot
	if snapshots, err = service.store.ManagedVMSnapshots.SelectAll(); err != nil {
		return
	}
	for _, current := range snapshots {
		if current.State == "creating" || current.State == "ready" || current.State == "deleting" {
			used += int64(current.ReservedGiB)
		}
	}
	if policy.Limits.SnapshotStorageGiB > 0 && used+int64(request.BootDiskGiB) > policy.Limits.SnapshotStorageGiB {
		err = fmt.Errorf("%w: snapshot reservation exceeds the configured platform capacity limit", ErrInvalidInput)
		return
	}
	var keyBytes [12]byte
	if _, err = rand.Read(keyBytes[:]); err != nil {
		return
	}
	var key string = hex.EncodeToString(keyBytes[:])
	snapshot = &db.ManagedVMSnapshot{
		ResourceID:  resource.ID,
		SnapshotKey: key,
		Name:        "og-" + key,
		Description: description,
		ReservedGiB: request.BootDiskGiB,
		State:       "creating",
		CreatedAt:   service.now(),
	}
	err = service.store.ManagedVMSnapshots.Insert(snapshot)
	return
}

// CompleteVMSnapshot records whether Proxmox confirmed creation of a reserved snapshot.
func (service *Service) CompleteVMSnapshot(actorID int, snapshotID int, ready bool) (snapshot *db.ManagedVMSnapshot, resource *db.ManagedResource, err error) {
	if snapshot, err = service.store.ManagedVMSnapshots.Select(snapshotID); err != nil {
		return
	}
	if snapshot == nil {
		err = ErrNotFound
		return
	}
	if resource, err = service.store.ManagedResources.Select(snapshot.ResourceID); err != nil {
		return
	}
	if resource == nil {
		err = ErrNotFound
		return
	}
	if err = service.Require(actorID, db.PermissionVMSnapshot, resource.OwnershipID); err != nil {
		return
	}
	if ready {
		snapshot.State = "ready"
	} else {
		snapshot.State = "failed"
	}
	if err = service.store.ManagedVMSnapshots.Update(snapshot); err != nil {
		return
	}
	var result string = "failed"
	if ready {
		result = "succeeded"
	}
	err = service.writeAudit(actorID, "vm.snapshot.create", fmt.Sprintf("snapshot:%d", snapshot.ID), result, map[string]string{"vm": resource.Name, "reserved_gib": fmt.Sprintf("%d", snapshot.ReservedGiB)})
	return
}

// ListVMSnapshots returns snapshots registered by Organesson for one authorized VM.
func (service *Service) ListVMSnapshots(actorID int, resourceID int) (snapshots []*db.ManagedVMSnapshot, resource *db.ManagedResource, err error) {
	if resource, err = service.store.ManagedResources.Select(resourceID); err != nil {
		return
	}
	if resource == nil || resource.Kind != "virtual_machine" {
		err = ErrNotFound
		return
	}
	if err = service.Require(actorID, db.PermissionVMSnapshot, resource.OwnershipID); err != nil {
		return
	}
	var all []*db.ManagedVMSnapshot
	if all, err = service.store.ManagedVMSnapshots.SelectAll(); err != nil {
		return
	}
	for _, snapshot := range all {
		if snapshot.ResourceID == resource.ID {
			snapshots = append(snapshots, snapshot)
		}
	}
	return
}

// GetVMSnapshot returns a managed snapshot and its VM after checking snapshot permission.
func (service *Service) GetVMSnapshot(actorID int, snapshotID int) (snapshot *db.ManagedVMSnapshot, resource *db.ManagedResource, err error) {
	if snapshot, err = service.store.ManagedVMSnapshots.Select(snapshotID); err != nil {
		return
	}
	if snapshot == nil {
		err = ErrNotFound
		return
	}
	if resource, err = service.store.ManagedResources.Select(snapshot.ResourceID); err != nil {
		return
	}
	if resource == nil {
		err = ErrNotFound
		return
	}
	err = service.Require(actorID, db.PermissionVMSnapshot, resource.OwnershipID)
	return
}

// DeleteVMSnapshotRecord removes a local snapshot reservation after Proxmox confirms deletion.
func (service *Service) DeleteVMSnapshotRecord(actorID int, snapshotID int) (err error) {
	var snapshot *db.ManagedVMSnapshot
	var resource *db.ManagedResource
	if snapshot, resource, err = service.GetVMSnapshot(actorID, snapshotID); err != nil {
		return
	}
	if err = service.store.ManagedVMSnapshots.Delete(snapshot.ID); err != nil {
		return
	}
	err = service.writeAudit(actorID, "vm.snapshot.delete", fmt.Sprintf("snapshot:%d", snapshotID), "succeeded", map[string]string{"vm": resource.Name})
	return
}

// RecordVMSnapshotRestore audits a Proxmox-confirmed restore of an authorized snapshot.
func (service *Service) RecordVMSnapshotRestore(actorID int, snapshotID int) (err error) {
	var snapshot *db.ManagedVMSnapshot
	var resource *db.ManagedResource
	if snapshot, resource, err = service.GetVMSnapshot(actorID, snapshotID); err != nil {
		return
	}
	err = service.writeAudit(actorID, "vm.snapshot.restore", fmt.Sprintf("snapshot:%d", snapshotID), "succeeded", map[string]string{"vm": resource.Name, "description": snapshot.Description})
	return
}

// SetVMSnapshotState updates a persisted snapshot operation state.
func (service *Service) SetVMSnapshotState(snapshot *db.ManagedVMSnapshot, state string) (err error) {
	if snapshot == nil || (state != "creating" && state != "ready" && state != "failed" && state != "deleting") {
		err = ErrInvalidInput
		return
	}
	snapshot.State = state
	err = service.store.ManagedVMSnapshots.Update(snapshot)
	return
}
