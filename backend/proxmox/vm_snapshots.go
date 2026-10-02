package proxmox

import (
	"context"
	"errors"

	pve "github.com/luthermonson/go-proxmox"
	"github.com/z46-dev/organesson/backend/config"
)

type apiVMSnapshotDriver struct {
	settings config.ProxmoxConfiguration
}

// Create adds a snapshot to a verified Organesson VM and labels it for safe later management.
func (driver *apiVMSnapshotDriver) Create(ctx context.Context, nodeName string, id string, operationKey string, snapshotName string, snapshotKey string) (err error) {
	var vm *pve.VirtualMachine
	var client *pve.Client
	if client, vm, err = driver.managedVM(ctx, nodeName, id, operationKey); err != nil {
		return
	}
	var task *pve.Task
	if task, err = vm.NewSnapshot(ctx, snapshotName); err != nil {
		return
	}
	if err = waitTask(ctx, client, task); err != nil {
		return
	}
	err = vm.Snapshot(snapshotName).UpdateConfig(ctx, &pve.VirtualMachineSnapshotUpdateOptions{Description: "Organesson managed snapshot " + snapshotKey})
	return
}

// Restore rolls back only a snapshot whose marker matches the Organesson record.
func (driver *apiVMSnapshotDriver) Restore(ctx context.Context, nodeName string, id string, operationKey string, snapshotName string, snapshotKey string) (err error) {
	var vm *pve.VirtualMachine
	var client *pve.Client
	if client, vm, err = driver.managedVM(ctx, nodeName, id, operationKey); err != nil {
		return
	}
	if err = verifyManagedSnapshot(ctx, vm, snapshotName, snapshotKey); err != nil {
		return
	}
	var task *pve.Task
	if task, err = vm.Snapshot(snapshotName).Rollback(ctx); err != nil {
		return
	}
	err = waitTask(ctx, client, task)
	return
}

// Delete removes only a snapshot whose marker matches the Organesson record.
func (driver *apiVMSnapshotDriver) Delete(ctx context.Context, nodeName string, id string, operationKey string, snapshotName string, snapshotKey string) (err error) {
	var vm *pve.VirtualMachine
	var client *pve.Client
	if client, vm, err = driver.managedVM(ctx, nodeName, id, operationKey); err != nil {
		return
	}
	if err = verifyManagedSnapshot(ctx, vm, snapshotName, snapshotKey); err != nil {
		return
	}
	var task *pve.Task
	if task, err = vm.Snapshot(snapshotName).Delete(ctx); err != nil {
		return
	}
	err = waitTask(ctx, client, task)
	return
}

// managedVM resolves a VM and enforces its existing resource ownership marker.
func (driver *apiVMSnapshotDriver) managedVM(ctx context.Context, nodeName string, id string, operationKey string) (client *pve.Client, vm *pve.VirtualMachine, err error) {
	var vmid int
	if vmid, err = parseVMID(id); err != nil {
		return
	}
	if client, err = newAPIClient(driver.settings); err != nil {
		return
	}
	var node *pve.Node
	if node, err = client.Node(ctx, nodeName); err != nil {
		return
	}
	if vm, err = node.VirtualMachine(ctx, vmid); err != nil {
		return
	}
	err = verifyManagedVM(vm, operationKey)
	return
}

// verifyManagedSnapshot prevents managing snapshots created outside Organesson.
func verifyManagedSnapshot(ctx context.Context, vm *pve.VirtualMachine, name string, key string) (err error) {
	if name == "" || key == "" || name != "og-"+key {
		err = errors.New("snapshot identity is incomplete")
		return
	}
	var snapshot *pve.VirtualMachineSnapshot = vm.Snapshot(name)
	_, err = snapshot.Config(ctx)
	return
}
