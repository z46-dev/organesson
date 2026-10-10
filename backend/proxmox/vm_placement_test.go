package proxmox

import (
	"testing"

	pve "github.com/luthermonson/go-proxmox"
)

// TestSelectVMCloneTargetBalancesProjectedNodeLoad chooses the less-loaded node for a shared-storage clone.
func TestSelectVMCloneTargetBalancesProjectedNodeLoad(t *testing.T) {
	var resources pve.ClusterResources = pve.ClusterResources{
		{Type: "node", Node: "pve1", Status: "online", MaxCPU: 16, MaxMem: 16 * 1024 * 1024 * 1024, Mem: 8 * 1024 * 1024 * 1024, CPU: 0.2},
		{Type: "node", Node: "pve2", Status: "online", MaxCPU: 16, MaxMem: 16 * 1024 * 1024 * 1024, Mem: 2 * 1024 * 1024 * 1024, CPU: 0.05},
		{Type: "qemu", Node: "pve1", MaxCPU: 4, MaxMem: 8 * 1024 * 1024 * 1024},
	}
	var storages pve.ClusterStorages = pve.ClusterStorages{
		{Storage: "laas", Shared: 1, Content: "images,backup"},
	}
	var source *pve.VirtualMachineConfig = &pve.VirtualMachineConfig{
		SCSIs: map[string]string{"scsi0": "laas:vm-100-disk-0,size=32G"},
	}
	var request VMCloneRequest = VMCloneRequest{Storage: "laas", Cores: 4, MemoryMiB: 4096}
	var target string
	var err error
	if target, err = selectVMCloneTarget(resources, storages, "pve1", source, request); err != nil {
		t.Fatalf("select clone target: %v", err)
	}
	if target != "pve2" {
		t.Fatalf("expected less-loaded pve2, got %q", target)
	}
}

// TestSelectVMCloneTargetKeepsNonSharedTemplatesOnTheirSourceNode avoids unsupported cross-node clones.
func TestSelectVMCloneTargetKeepsNonSharedTemplatesOnTheirSourceNode(t *testing.T) {
	var resources pve.ClusterResources = pve.ClusterResources{
		{Type: "node", Node: "pve1", Status: "online", MaxCPU: 16, MaxMem: 16 * 1024 * 1024 * 1024},
		{Type: "node", Node: "pve2", Status: "online", MaxCPU: 16, MaxMem: 16 * 1024 * 1024 * 1024},
	}
	var storages pve.ClusterStorages = pve.ClusterStorages{
		{Storage: "local-lvm", Shared: 0, Content: "images,rootdir"},
		{Storage: "laas", Shared: 1, Content: "images,backup"},
	}
	var source *pve.VirtualMachineConfig = &pve.VirtualMachineConfig{
		SCSIs: map[string]string{"scsi0": "local-lvm:vm-100-disk-0,size=32G"},
	}
	var request VMCloneRequest = VMCloneRequest{Storage: "laas", Cores: 4, MemoryMiB: 4096}
	var target string
	var err error
	if target, err = selectVMCloneTarget(resources, storages, "pve1", source, request); err != nil {
		t.Fatalf("select clone target: %v", err)
	}
	if target != "pve1" {
		t.Fatalf("expected source node pve1 for a local template, got %q", target)
	}
}
