package domain

import (
	"encoding/json"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/z46-dev/golog"
	"github.com/z46-dev/organesson/backend/db"
	"github.com/z46-dev/organesson/backend/proxmox"
)

// TestSnapshotReservationsEnforcePermissionAndPerVMCount covers snapshot permission and count bounds.
func TestSnapshotReservationsEnforcePermissionAndPerVMCount(t *testing.T) {
	var store *db.Store
	var err error
	if store, err = db.Open(filepath.Join(t.TempDir(), "snapshots.db"), golog.New(), false); err != nil {
		t.Fatalf("open database: %v", err)
	}
	defer store.Close()
	var service *Service = New(store)
	var admin *db.Account
	if admin, _, err = store.InitialAdministrator(); err != nil {
		t.Fatalf("load administrator: %v", err)
	}
	var deployment *db.Deployment
	if deployment, err = service.CreateDeployment(admin.ID, "snapshot-lab", ""); err != nil {
		t.Fatalf("create deployment: %v", err)
	}
	var vm *db.ManagedResource
	if vm, err = service.CreateVirtualMachine(admin.ID, deployment.ID, *deployment.RootNodeID, "snapshot-vm"); err != nil {
		t.Fatalf("create VM: %v", err)
	}
	var request proxmox.VMCloneRequest = proxmox.VMCloneRequest{BootDiskGiB: 10}
	var encoded []byte
	if encoded, err = json.Marshal(request); err != nil {
		t.Fatal(err)
	}
	vm.ConfigurationJSON = string(encoded)
	vm.ExternalID = "900"
	vm.ExternalNode = "pve1"
	if err = store.ManagedResources.Update(vm); err != nil {
		t.Fatal(err)
	}
	var policy proxmox.ResourcePolicy = proxmox.ResourcePolicy{
		Limits:           proxmox.CapacityLimits{SnapshotStorageGiB: 100},
		DeploymentLimits: proxmox.DeploymentLimits{SnapshotStorageGiB: 15},
		VMLimits:         proxmox.VMLimits{MaxSnapshots: 1},
		ResourcePools:    []string{"organesson"}, Storages: []string{"laas"},
	}
	var policyJSON []byte
	if policyJSON, err = json.Marshal(policy); err != nil {
		t.Fatal(err)
	}
	var policyHash string
	if policyHash, err = proxmox.ResourcePolicyHash(policy); err != nil {
		t.Fatal(err)
	}
	var now time.Time = time.Now()
	if err = store.ProxmoxResourcePolicies.Insert(&db.ProxmoxResourcePolicy{
		ID: 1, ConfigurationJSON: string(policyJSON), ValidationJSON: `{"valid":true}`,
		ValidatedConfigHash: policyHash, ValidatedAt: &now, UpdatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	var first *db.ManagedVMSnapshot
	if first, _, err = service.ReserveVMSnapshot(admin.ID, vm.ID, "before updates"); err != nil {
		t.Fatalf("reserve first snapshot: %v", err)
	}
	if first.ReservedGiB != 10 || first.State != "creating" {
		t.Fatalf("unexpected reservation: %#v", first)
	}
	if _, _, err = service.ReserveVMSnapshot(admin.ID, vm.ID, "over per-VM limit"); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("snapshot exceeding per-VM count should be rejected, got %v", err)
	}
	policy.VMLimits.MaxSnapshots = 0
	if policyJSON, err = json.Marshal(policy); err != nil {
		t.Fatal(err)
	}
	if policyHash, err = proxmox.ResourcePolicyHash(policy); err != nil {
		t.Fatal(err)
	}
	var policyRecord *db.ProxmoxResourcePolicy
	if policyRecord, err = store.ProxmoxResourcePolicies.Select(1); err != nil {
		t.Fatal(err)
	}
	policyRecord.ConfigurationJSON = string(policyJSON)
	policyRecord.ValidatedConfigHash = policyHash
	if err = store.ProxmoxResourcePolicies.Update(policyRecord); err != nil {
		t.Fatal(err)
	}
	if _, _, err = service.ReserveVMSnapshot(admin.ID, vm.ID, "over deployment storage quota"); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("snapshot exceeding deployment storage quota should be rejected, got %v", err)
	}
	policy.Limits.SnapshotStorageGiB = 15
	policy.DeploymentLimits.SnapshotStorageGiB = 0
	if policyJSON, err = json.Marshal(policy); err != nil {
		t.Fatal(err)
	}
	if policyHash, err = proxmox.ResourcePolicyHash(policy); err != nil {
		t.Fatal(err)
	}
	policyRecord.ConfigurationJSON = string(policyJSON)
	policyRecord.ValidatedConfigHash = policyHash
	if err = store.ProxmoxResourcePolicies.Update(policyRecord); err != nil {
		t.Fatal(err)
	}
	if _, _, err = service.ReserveVMSnapshot(admin.ID, vm.ID, "over platform storage quota"); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("snapshot exceeding platform storage quota should be rejected, got %v", err)
	}
	if _, _, err = service.CompleteVMSnapshot(admin.ID, first.ID, true); err != nil {
		t.Fatalf("complete snapshot: %v", err)
	}
	var outsider *db.Account = &db.Account{DisplayName: "Outsider", CreatedAt: time.Now()}
	if err = store.Accounts.Insert(outsider); err != nil {
		t.Fatal(err)
	}
	if _, _, err = service.ReserveVMSnapshot(outsider.ID, vm.ID, "unauthorized"); !errors.Is(err, ErrForbidden) {
		t.Fatalf("ungranted account should not create snapshots, got %v", err)
	}
}
