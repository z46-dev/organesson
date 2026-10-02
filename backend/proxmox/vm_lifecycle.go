package proxmox

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	pve "github.com/luthermonson/go-proxmox"
	"github.com/z46-dev/organesson/backend/config"
)

type (
	// VMDriver owns Proxmox operations for Organesson-managed QEMU VMs.
	VMDriver interface {
		Clone(context.Context, VMCloneRequest) (placement VMPlacement, err error)
		Read(context.Context, string, string, string) (placement VMPlacement, err error)
		Power(context.Context, string, string, string, string) (placement VMPlacement, err error)
		Delete(context.Context, string, string, string) (err error)
	}

	// VMCloneRequest describes the bounded configuration applied to a VM clone.
	VMCloneRequest struct {
		SourceVMID    string `json:"source_vm_id"`
		TemplateAlias string `json:"template_alias"`
		Name          string `json:"name"`
		Pool          string `json:"pool"`
		Storage       string `json:"storage"`
		Cores         int    `json:"cores"`
		MemoryMiB     int    `json:"memory_mib"`
		BootDiskGiB   int    `json:"boot_disk_gib"`
		OperationKey  string `json:"operation_key"`
	}

	// VMPlacement is the authoritative Proxmox location and current power state.
	VMPlacement struct {
		VMID       string `json:"vmid"`
		Node       string `json:"node"`
		Name       string `json:"name"`
		PowerState string `json:"power_state"`
	}

	apiVMDriver struct {
		settings config.ProxmoxConfiguration
	}
)

// CloneVM provisions a guest from an already-checked QEMU source and waits for every PVE task.
func (service *Service) CloneVM(ctx context.Context, request VMCloneRequest) (placement VMPlacement, err error) {
	if service == nil || service.vmDriver == nil || !service.Configured() {
		err = ErrNotConfigured
		return
	}
	service.lifecycleLock.Lock()
	defer service.lifecycleLock.Unlock()
	placement, err = service.vmDriver.Clone(ctx, request)
	return
}

// ReadVM obtains a managed VM's live placement and status from Proxmox.
func (service *Service) ReadVM(ctx context.Context, node string, vmid string, operationKey string) (placement VMPlacement, err error) {
	if service == nil || service.vmDriver == nil || !service.Configured() {
		err = ErrNotConfigured
		return
	}
	placement, err = service.vmDriver.Read(ctx, node, vmid, operationKey)
	return
}

// PowerVM changes a Proxmox VM state and waits for the corresponding PVE task.
func (service *Service) PowerVM(ctx context.Context, node string, vmid string, operationKey string, action string) (placement VMPlacement, err error) {
	if service == nil || service.vmDriver == nil || !service.Configured() {
		err = ErrNotConfigured
		return
	}
	service.lifecycleLock.Lock()
	defer service.lifecycleLock.Unlock()
	placement, err = service.vmDriver.Power(ctx, node, vmid, operationKey, action)
	return
}

// DeleteVM removes a Proxmox VM after its ownership has been verified by Organesson.
func (service *Service) DeleteVM(ctx context.Context, node string, vmid string, operationKey string) (err error) {
	if service == nil || service.vmDriver == nil || !service.Configured() {
		err = ErrNotConfigured
		return
	}
	err = service.vmDriver.Delete(ctx, node, vmid, operationKey)
	return
}

// Clone locates a source, clones it into the selected pool/storage, and applies a safe NIC-free baseline.
func (driver *apiVMDriver) Clone(ctx context.Context, request VMCloneRequest) (placement VMPlacement, err error) {
	var sourceID int
	if sourceID, err = strconv.Atoi(request.SourceVMID); err != nil || sourceID < 1 {
		err = errors.New("source identifier must be a positive Proxmox VMID")
		return
	}
	if request.Cores < 1 || request.MemoryMiB < 1 || request.BootDiskGiB < 1 || request.Name == "" || request.Pool == "" || request.Storage == "" || request.OperationKey == "" {
		err = errors.New("VM clone request is missing a required setting")
		return
	}
	var client *pve.Client
	if client, err = newAPIClient(driver.settings); err != nil {
		return
	}
	var cluster *pve.Cluster
	if cluster, err = client.Cluster(ctx); err != nil {
		return
	}
	var resources pve.ClusterResources
	if resources, err = cluster.Resources(ctx, "vm"); err != nil {
		return
	}
	if placement, err = findExistingClone(ctx, client, resources, request); err != nil || placement.VMID != "" {
		if err != nil {
			return
		}
		var vmid int
		if vmid, err = strconv.Atoi(placement.VMID); err != nil {
			return
		}
		placement, err = driver.applyBaseline(ctx, client, placement.Node, vmid, request)
		return
	}
	var sourceNode string
	for _, resource := range resources {
		if resource != nil && resource.Type == "qemu" && resource.VMID == uint64(sourceID) {
			sourceNode = resource.Node
			break
		}
	}
	if sourceNode == "" {
		err = fmt.Errorf("QEMU source VM %d was not found", sourceID)
		return
	}
	var node *pve.Node
	if node, err = client.Node(ctx, sourceNode); err != nil {
		return
	}
	var source *pve.VirtualMachine
	if source, err = node.VirtualMachine(ctx, sourceID); err != nil {
		return
	}
	if source.Template {
		err = errors.New("Proxmox source must be an ordinary VM, not a native PVE template")
		return
	}
	if !source.IsStopped() {
		err = errors.New("Proxmox source must be stopped before cloning")
		return
	}
	var vmid int
	var cloneTask *pve.Task
	if vmid, cloneTask, err = source.Clone(ctx, &pve.VirtualMachineCloneOptions{
		Full:        true,
		Name:        request.Name,
		Pool:        request.Pool,
		Storage:     request.Storage,
		Description: "Organesson managed resource " + request.OperationKey,
	}); err != nil {
		return
	}
	if err = waitTask(ctx, client, cloneTask); err != nil {
		return
	}
	if placement, err = driver.applyBaseline(ctx, client, sourceNode, vmid, request); err != nil {
		return
	}
	return
}

// findExistingClone recovers an earlier successful clone whose API response was interrupted.
func findExistingClone(ctx context.Context, client *pve.Client, resources pve.ClusterResources, request VMCloneRequest) (placement VMPlacement, err error) {
	var marker string = "Organesson managed resource " + request.OperationKey
	for _, resource := range resources {
		if resource == nil || resource.Type != "qemu" || resource.Name != request.Name || resource.Node == "" {
			continue
		}
		var node *pve.Node
		if node, err = client.Node(ctx, resource.Node); err != nil {
			return
		}
		var vm *pve.VirtualMachine
		if vm, err = node.VirtualMachine(ctx, int(resource.VMID)); err != nil {
			return
		}
		if vm.VirtualMachineConfig != nil && vm.VirtualMachineConfig.Description == marker {
			placement = placementFor(vm, int(resource.VMID), resource.Node)
			return
		}
	}
	return
}

// applyBaseline removes inherited NICs, applies compute sizing, and grows the source boot disk if needed.
func (driver *apiVMDriver) applyBaseline(ctx context.Context, client *pve.Client, nodeName string, vmid int, request VMCloneRequest) (placement VMPlacement, err error) {
	var node *pve.Node
	if node, err = client.Node(ctx, nodeName); err != nil {
		return
	}
	var vm *pve.VirtualMachine
	if vm, err = node.VirtualMachine(ctx, vmid); err != nil {
		return
	}
	var bootDisk string
	if bootDisk, _, err = primaryBootDisk(vm); err != nil {
		return
	}
	var options []pve.VirtualMachineOption = []pve.VirtualMachineOption{
		{Name: "cores", Value: request.Cores},
		{Name: "memory", Value: request.MemoryMiB},
		{Name: "description", Value: "Organesson managed resource " + request.OperationKey},
		{Name: "boot", Value: "order=" + bootDisk},
	}
	var inheritedDevices []string
	if vm.VirtualMachineConfig != nil {
		for key := range vm.VirtualMachineConfig.Nets {
			inheritedDevices = append(inheritedDevices, key)
		}
		for _, devices := range []map[string]string{
			vm.VirtualMachineConfig.IDEs,
			vm.VirtualMachineConfig.SATAs,
			vm.VirtualMachineConfig.SCSIs,
		} {
			for key, value := range devices {
				if strings.Contains(value, "media=cdrom") {
					inheritedDevices = append(inheritedDevices, key)
				}
			}
		}
	}
	if len(inheritedDevices) > 0 {
		sort.Strings(inheritedDevices)
		options = append(options, pve.VirtualMachineOption{Name: "delete", Value: strings.Join(inheritedDevices, ",")})
	}
	var configTask *pve.Task
	if configTask, err = vm.Config(ctx, options...); err != nil {
		return
	}
	if err = waitTask(ctx, client, configTask); err != nil {
		return
	}
	if vm.VirtualMachineConfig == nil {
		err = errors.New("cloned VM configuration could not be read")
		return
	}
	if err = resizeBootDisk(ctx, client, vm, request.BootDiskGiB); err != nil {
		return
	}
	placement = VMPlacement{VMID: strconv.Itoa(vmid), Node: nodeName, Name: request.Name, PowerState: vm.Status}
	return
}

// Read fetches live PVE status and configuration for one known managed VM.
func (driver *apiVMDriver) Read(ctx context.Context, nodeName string, id string, operationKey string) (placement VMPlacement, err error) {
	var vmid int
	if vmid, err = parseVMID(id); err != nil {
		return
	}
	var client *pve.Client
	if client, err = newAPIClient(driver.settings); err != nil {
		return
	}
	var node *pve.Node
	if node, err = client.Node(ctx, nodeName); err != nil {
		return
	}
	var vm *pve.VirtualMachine
	if vm, err = node.VirtualMachine(ctx, vmid); err != nil {
		return
	}
	if err = verifyManagedVM(vm, operationKey); err != nil {
		return
	}
	placement = placementFor(vm, vmid, nodeName)
	return
}

// Power performs only explicit start, stop, or restart operations on the selected managed VM.
func (driver *apiVMDriver) Power(ctx context.Context, nodeName string, id string, operationKey string, action string) (placement VMPlacement, err error) {
	var vm *pve.VirtualMachine
	var vmid int
	if vmid, err = parseVMID(id); err != nil {
		return
	}
	var client *pve.Client
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
	if err = verifyManagedVM(vm, operationKey); err != nil {
		return
	}
	var task *pve.Task
	switch action {
	case "start":
		if vm.IsStopped() {
			task, err = vm.Start(ctx)
		} else {
			placement = placementFor(vm, vmid, nodeName)
			return
		}
	case "stop":
		if !vm.IsStopped() {
			task, err = vm.Shutdown(ctx)
		} else {
			placement = placementFor(vm, vmid, nodeName)
			return
		}
	case "restart":
		task, err = vm.Reboot(ctx)
	default:
		err = fmt.Errorf("unsupported Proxmox VM power action %q", action)
		return
	}
	if err != nil {
		return
	}
	if err = waitTask(ctx, client, task); err != nil {
		return
	}
	placement, err = driver.Read(ctx, nodeName, id, operationKey)
	return
}

// Delete waits for Proxmox to finish removing one VM.
func (driver *apiVMDriver) Delete(ctx context.Context, nodeName string, id string, operationKey string) (err error) {
	var vmid int
	if vmid, err = parseVMID(id); err != nil {
		return
	}
	var client *pve.Client
	if client, err = newAPIClient(driver.settings); err != nil {
		return
	}
	var node *pve.Node
	if node, err = client.Node(ctx, nodeName); err != nil {
		return
	}
	var vm *pve.VirtualMachine
	if vm, err = node.VirtualMachine(ctx, vmid); err != nil {
		if pve.IsNotFound(err) {
			err = nil
		}
		return
	}
	if err = verifyManagedVM(vm, operationKey); err != nil {
		return
	}
	if !vm.IsStopped() {
		var shutdownTask *pve.Task
		if shutdownTask, err = vm.Shutdown(ctx); err != nil {
			return
		}
		if err = waitTask(ctx, client, shutdownTask); err != nil {
			return
		}
		if vm, err = node.VirtualMachine(ctx, vmid); err != nil {
			return
		}
		if !vm.IsStopped() {
			err = errors.New("Proxmox did not stop the managed VM before deletion")
			return
		}
	}
	var task *pve.Task
	if task, err = vm.Delete(ctx, &pve.VirtualMachineDeleteOptions{Purge: true}); err != nil {
		return
	}
	err = waitTask(ctx, client, task)
	return
}

func resizeBootDisk(ctx context.Context, client *pve.Client, vm *pve.VirtualMachine, targetGiB int) (err error) {
	var selected string
	var currentGiB int
	if selected, currentGiB, err = primaryBootDisk(vm); err != nil {
		return
	}
	if targetGiB < currentGiB {
		err = fmt.Errorf("requested boot disk size %d GiB is smaller than the source disk size %d GiB", targetGiB, currentGiB)
		return
	}
	if targetGiB == currentGiB {
		return
	}
	var task *pve.Task
	if task, err = vm.ResizeDisk(ctx, selected, fmt.Sprintf("+%dG", targetGiB-currentGiB)); err != nil {
		return
	}
	err = waitTask(ctx, client, task)
	return
}

// primaryBootDisk selects and sizes the first supported primary disk from a source VM.
func primaryBootDisk(vm *pve.VirtualMachine) (selected string, currentGiB int, err error) {
	if vm == nil || vm.VirtualMachineConfig == nil {
		err = errors.New("source VM configuration could not be read")
		return
	}
	var drives map[string]string = vm.VirtualMachineConfig.MergeDisks()
	for _, candidate := range []string{"scsi0", "virtio0", "sata0", "ide0"} {
		value, exists := drives[candidate]
		if !exists {
			continue
		}
		selected = candidate
		for _, setting := range strings.Split(value, ",") {
			if !strings.HasPrefix(setting, "size=") {
				continue
			}
			var parsed int64
			if parsed, err = strconv.ParseInt(strings.TrimSuffix(strings.TrimPrefix(setting, "size="), "G"), 10, 32); err != nil {
				return
			}
			currentGiB = int(parsed)
			break
		}
		break
	}
	if selected == "" || currentGiB < 1 {
		err = errors.New("source VM has no supported primary disk to resize")
	}
	return
}

func waitTask(ctx context.Context, client *pve.Client, task *pve.Task) (err error) {
	if task == nil {
		err = errors.New("Proxmox did not return a task identifier")
		return
	}
	if task.UPID == "" || task.Node == "" {
		err = fmt.Errorf("Proxmox returned an invalid task identifier %q", task.UPID)
		return
	}
	var node string = task.Node
	var upid pve.UPID = task.UPID
	var deadline time.Time = time.Now().Add(10 * time.Minute)
	for {
		var status struct {
			Status     string `json:"status"`
			ExitStatus string `json:"exitstatus"`
		}
		if err = client.Get(ctx, fmt.Sprintf("/nodes/%s/tasks/%s/status", node, upid), &status); err != nil {
			return
		}
		switch status.Status {
		case "running":
			if time.Now().After(deadline) {
				err = pve.ErrTimeout
				return
			}
			var timer *time.Timer = time.NewTimer(time.Second)
			select {
			case <-ctx.Done():
				timer.Stop()
				err = ctx.Err()
				return
			case <-timer.C:
			}
		case "stopped":
			if status.ExitStatus != "OK" {
				err = fmt.Errorf("Proxmox task %s failed: %s", upid, status.ExitStatus)
			}
			return
		default:
			err = fmt.Errorf("Proxmox task %s returned unexpected status %q", upid, status.Status)
			return
		}
	}
}

func parseVMID(value string) (vmid int, err error) {
	if vmid, err = strconv.Atoi(value); err != nil || vmid < 1 {
		err = errors.New("managed Proxmox VM identifier is invalid")
	}
	return
}

func placementFor(vm *pve.VirtualMachine, vmid int, node string) (placement VMPlacement) {
	placement = VMPlacement{VMID: strconv.Itoa(vmid), Node: node, Name: vm.Name, PowerState: vm.Status}
	return
}

// verifyManagedVM prevents stale Organesson IDs from controlling unrelated Proxmox VMs.
func verifyManagedVM(vm *pve.VirtualMachine, operationKey string) (err error) {
	if operationKey == "" || vm == nil || vm.VirtualMachineConfig == nil || vm.VirtualMachineConfig.Description != "Organesson managed resource "+operationKey {
		err = errors.New("Proxmox VM ownership marker does not match the Organesson resource")
	}
	return
}
