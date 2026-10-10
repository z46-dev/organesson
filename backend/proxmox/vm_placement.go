package proxmox

import (
	"errors"
	"math"
	"sort"
	"strings"

	pve "github.com/luthermonson/go-proxmox"
)

type vmCloneNodeLoad struct {
	name           string
	cpuCapacity    float64
	memoryCapacity float64
	allocatedCPU   float64
	allocatedRAM   float64
	actualCPU      float64
	actualRAM      float64
}

// selectVMCloneTarget chooses the online node with the lowest projected CPU and memory pressure.
func selectVMCloneTarget(resources pve.ClusterResources, storages pve.ClusterStorages, sourceNode string, sourceConfig *pve.VirtualMachineConfig, request VMCloneRequest) (target string, err error) {
	var nodes map[string]*vmCloneNodeLoad = make(map[string]*vmCloneNodeLoad)
	for _, resource := range resources {
		if resource == nil {
			continue
		}
		if resource.Type == "node" && resource.Status == "online" && resource.Node != "" && resource.MaxCPU > 0 && resource.MaxMem > 0 {
			nodes[resource.Node] = &vmCloneNodeLoad{
				name: resource.Node, cpuCapacity: float64(resource.MaxCPU), memoryCapacity: float64(resource.MaxMem),
				actualCPU: resource.CPU, actualRAM: float64(resource.Mem),
			}
		}
	}
	for _, resource := range resources {
		if resource == nil || resource.Type != "qemu" {
			continue
		}
		var node *vmCloneNodeLoad
		if node = nodes[resource.Node]; node != nil {
			node.allocatedCPU += float64(resource.MaxCPU)
			node.allocatedRAM += float64(resource.MaxMem)
		}
	}
	if len(nodes) < 2 {
		target = sourceNode
		return
	}
	if !sourceVMIsOnSharedStorage(sourceConfig, storages) {
		target = sourceNode
		return
	}

	var candidates []vmCloneNodeLoad
	for _, node := range nodes {
		if !storageCanHostImage(storages, request.Storage, node.name) {
			continue
		}
		candidates = append(candidates, *node)
	}
	if len(candidates) == 0 {
		err = errors.New("no online Proxmox node has access to the requested VM storage")
		return
	}
	sort.Slice(candidates, func(left int, right int) bool {
		return candidates[left].name < candidates[right].name
	})

	var requestedCPU float64 = float64(request.Cores)
	var requestedRAM float64 = float64(request.MemoryMiB) * 1024 * 1024
	var selectedScore float64 = math.Inf(1)
	for _, candidate := range candidates {
		var cpuScore float64 = math.Max(candidate.actualCPU, (candidate.allocatedCPU+requestedCPU)/candidate.cpuCapacity)
		var memoryScore float64 = math.Max(candidate.actualRAM/candidate.memoryCapacity, (candidate.allocatedRAM+requestedRAM)/candidate.memoryCapacity)
		var score float64 = math.Max(cpuScore, memoryScore)
		if score < selectedScore {
			selectedScore = score
			target = candidate.name
		}
	}
	if target == "" {
		err = errors.New("no online Proxmox node can host this VM clone")
	}
	return
}

// sourceVMIsOnSharedStorage confirms every source disk can be read from a different node.
func sourceVMIsOnSharedStorage(source *pve.VirtualMachineConfig, storages pve.ClusterStorages) (shared bool) {
	if source == nil {
		return
	}
	var storageByName map[string]*pve.ClusterStorage = make(map[string]*pve.ClusterStorage, len(storages))
	for _, storage := range storages {
		if storage != nil {
			storageByName[storage.Storage] = storage
		}
	}

	var volumes []string
	for _, devices := range []map[string]string{source.IDEs, source.SATAs, source.SCSIs, source.VirtIOs} {
		for _, device := range devices {
			if strings.Contains(device, "media=cdrom") || strings.TrimSpace(device) == "none" {
				continue
			}
			volumes = append(volumes, device)
		}
	}
	if source.EFIDisk0 != "" {
		volumes = append(volumes, source.EFIDisk0)
	}
	if source.TPMState0 != "" {
		volumes = append(volumes, source.TPMState0)
	}
	if len(volumes) == 0 {
		return
	}
	for _, volume := range volumes {
		var identifier string = strings.SplitN(strings.SplitN(volume, ",", 2)[0], ":", 2)[0]
		if identifier == "" {
			return
		}
		var storage *pve.ClusterStorage
		if storage = storageByName[identifier]; storage == nil || storage.Shared != 1 {
			return
		}
	}
	shared = true
	return
}

// storageCanHostImage checks a destination storage's content and node restrictions.
func storageCanHostImage(storages pve.ClusterStorages, name string, node string) (available bool) {
	for _, storage := range storages {
		if storage == nil || storage.Storage != name || !strings.Contains(","+storage.Content+",", ",images,") {
			continue
		}
		if storage.Nodes == "" {
			available = true
			return
		}
		for _, allowedNode := range strings.FieldsFunc(storage.Nodes, func(character rune) bool {
			return character == ',' || character == ';'
		}) {
			if strings.TrimSpace(allowedNode) == node {
				available = true
				return
			}
		}
	}
	return
}
