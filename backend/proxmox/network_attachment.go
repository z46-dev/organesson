package proxmox

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"

	pve "github.com/luthermonson/go-proxmox"
	"github.com/z46-dev/organesson/backend/config"
)

var ErrNetworkAttachmentNotFound = errors.New("managed Proxmox VM network attachment is missing")

type (
	// NetworkAttachmentRequest identifies a managed VM and its approved Proxmox L2 network.
	NetworkAttachmentRequest struct {
		Node                   string `json:"node"`
		VMID                   string `json:"vmid"`
		VMOperationKey         string `json:"vm_operation_key"`
		Bridge                 string `json:"bridge"`
		NetworkOperationKey    string `json:"network_operation_key,omitempty"`
		AttachmentOperationKey string `json:"attachment_operation_key"`
	}

	// NetworkAttachmentPlacement is the stable PVE virtual NIC identity.
	NetworkAttachmentPlacement struct {
		Device string `json:"device"`
		MAC    string `json:"mac"`
	}

	apiNetworkAttachmentDriver struct {
		settings config.ProxmoxConfiguration
	}
)

// Attach creates a marked PVE virtual NIC on a bridge or verified Organesson VNet.
func (service *Service) AttachNetwork(ctx context.Context, request NetworkAttachmentRequest) (placement NetworkAttachmentPlacement, err error) {
	if service == nil || service.networkAttachmentDriver == nil || !service.Configured() {
		err = ErrNotConfigured
		return
	}
	service.lifecycleLock.Lock()
	defer service.lifecycleLock.Unlock()
	placement, err = service.networkAttachmentDriver.Attach(ctx, request)
	return
}

// ReadNetworkAttachment verifies the stable NIC still matches its VM and network ownership.
func (service *Service) ReadNetworkAttachment(ctx context.Context, request NetworkAttachmentRequest, placement NetworkAttachmentPlacement) (err error) {
	if service == nil || service.networkAttachmentDriver == nil || !service.Configured() {
		err = ErrNotConfigured
		return
	}
	service.lifecycleLock.Lock()
	defer service.lifecycleLock.Unlock()
	err = service.networkAttachmentDriver.Read(ctx, request, placement)
	return
}

// DetachNetwork removes only the marked NIC from its managed VM.
func (service *Service) DetachNetwork(ctx context.Context, request NetworkAttachmentRequest, placement NetworkAttachmentPlacement) (err error) {
	if service == nil || service.networkAttachmentDriver == nil || !service.Configured() {
		err = ErrNotConfigured
		return
	}
	service.lifecycleLock.Lock()
	defer service.lifecycleLock.Unlock()
	err = service.networkAttachmentDriver.Detach(ctx, request, placement)
	return
}

// Attach locates a safe netN slot and applies an idempotently marked network device.
func (driver *apiNetworkAttachmentDriver) Attach(ctx context.Context, request NetworkAttachmentRequest) (placement NetworkAttachmentPlacement, err error) {
	if err = validateNetworkAttachmentRequest(request); err != nil {
		return
	}
	var client *pve.Client
	if client, err = newAPIClient(driver.settings); err != nil {
		return
	}
	var vmid int
	if vmid, err = parseVMID(request.VMID); err != nil {
		return
	}
	var node *pve.Node
	var vm *pve.VirtualMachine
	if node, vm, _, err = locateManagedVM(ctx, client, vmid); err != nil {
		return
	}
	if err = verifyManagedVM(vm, request.VMOperationKey); err != nil {
		return
	}
	if err = verifyAttachmentTarget(ctx, client, node, request); err != nil {
		return
	}
	var mac string = networkAttachmentMAC(request.AttachmentOperationKey)
	var interfaces map[string]string
	if vm.VirtualMachineConfig != nil {
		interfaces = vm.VirtualMachineConfig.Nets
	}
	for device, value := range interfaces {
		var options map[string]string = networkOptionMap(value)
		if strings.EqualFold(options["macaddr"], mac) {
			if options["bridge"] != request.Bridge {
				err = errors.New("marked Proxmox network device is attached to a different bridge")
				return
			}
			placement = NetworkAttachmentPlacement{Device: device, MAC: mac}
			return
		}
	}
	var device string
	if device, err = nextNetworkDevice(interfaces); err != nil {
		return
	}
	var task *pve.Task
	if task, err = vm.Config(ctx, pve.VirtualMachineOption{Name: device, Value: "virtio=" + mac + ",bridge=" + request.Bridge}); err != nil {
		return
	}
	if err = waitTask(ctx, client, task); err != nil {
		return
	}
	placement = NetworkAttachmentPlacement{Device: device, MAC: mac}
	return
}

// Read confirms the configured netN device still carries the deterministic Organesson MAC.
func (driver *apiNetworkAttachmentDriver) Read(ctx context.Context, request NetworkAttachmentRequest, placement NetworkAttachmentPlacement) (err error) {
	if err = validateNetworkAttachmentRequest(request); err != nil {
		return
	}
	var client *pve.Client
	if client, err = newAPIClient(driver.settings); err != nil {
		return
	}
	var vmid int
	if vmid, err = parseVMID(request.VMID); err != nil {
		return
	}
	var node *pve.Node
	var vm *pve.VirtualMachine
	if node, vm, _, err = locateManagedVM(ctx, client, vmid); err != nil {
		return
	}
	if err = verifyManagedVM(vm, request.VMOperationKey); err != nil {
		return
	}
	var value string
	if vm.VirtualMachineConfig != nil {
		value = vm.VirtualMachineConfig.Nets[placement.Device]
	}
	var options map[string]string = networkOptionMap(value)
	if value == "" || !strings.EqualFold(options["macaddr"], networkAttachmentMAC(request.AttachmentOperationKey)) {
		err = ErrNetworkAttachmentNotFound
		return
	}
	if options["bridge"] != request.Bridge || placement.MAC != networkAttachmentMAC(request.AttachmentOperationKey) {
		err = errors.New("Proxmox virtual NIC differs from its recorded Organesson configuration")
		return
	}
	err = verifyAttachmentTarget(ctx, client, node, request)
	return
}

// Detach removes a NIC only if its recorded device and ownership MAC still match.
func (driver *apiNetworkAttachmentDriver) Detach(ctx context.Context, request NetworkAttachmentRequest, placement NetworkAttachmentPlacement) (err error) {
	if err = validateNetworkAttachmentRequest(request); err != nil {
		return
	}
	var client *pve.Client
	if client, err = newAPIClient(driver.settings); err != nil {
		return
	}
	var vmid int
	if vmid, err = parseVMID(request.VMID); err != nil {
		return
	}
	var vm *pve.VirtualMachine
	if _, vm, _, err = locateManagedVM(ctx, client, vmid); err != nil {
		return
	}
	if err = verifyManagedVM(vm, request.VMOperationKey); err != nil {
		return
	}
	var value string
	if vm.VirtualMachineConfig != nil {
		value = vm.VirtualMachineConfig.Nets[placement.Device]
	}
	if value == "" {
		return
	}
	var options map[string]string = networkOptionMap(value)
	if !strings.EqualFold(options["macaddr"], networkAttachmentMAC(request.AttachmentOperationKey)) || options["bridge"] != request.Bridge {
		err = errors.New("refusing to detach a Proxmox NIC that no longer matches the Organesson marker")
		return
	}
	var task *pve.Task
	if task, err = vm.Config(ctx, pve.VirtualMachineOption{Name: "delete", Value: placement.Device}); err != nil {
		return
	}
	err = waitTask(ctx, client, task)
	return
}

func validateNetworkAttachmentRequest(request NetworkAttachmentRequest) (err error) {
	if request.Node == "" || request.VMID == "" || request.VMOperationKey == "" || request.AttachmentOperationKey == "" || request.Bridge == "" || strings.ContainsAny(request.Bridge, ",=") {
		err = errors.New("network attachment requires managed VM identity and a valid approved bridge")
		return
	}
	if request.NetworkOperationKey == "" && strings.HasPrefix(request.Bridge, "on") {
		err = errors.New("Organesson SDN VNets require their ownership operation key")
	}
	return
}

func verifyAttachmentTarget(ctx context.Context, client *pve.Client, node *pve.Node, request NetworkAttachmentRequest) (err error) {
	if request.NetworkOperationKey != "" {
		var cluster *pve.Cluster
		if cluster, err = client.Cluster(ctx); err != nil {
			return
		}
		var vnets []*pve.VNet
		if vnets, err = cluster.SDNVNets(ctx); err != nil {
			return
		}
		for _, vnet := range vnets {
			if vnet == nil || vnet.Name != request.Bridge {
				continue
			}
			var expected SDNNetworkPlacement = namesForSDNNetwork(request.NetworkOperationKey)
			if vnet.Alias != "organesson:"+request.NetworkOperationKey || vnet.Name != expected.VNet {
				err = errors.New("target Proxmox SDN VNet is not owned by the referenced Organesson network")
				return
			}
			// Cluster SDN VNets are not necessarily exposed by a node's local
			// network-interface inventory; the ownership-checked SDN record is
			// the authoritative attachment target.
			return
		}
		err = ErrSDNNetworkNotFound
		return
	}
	var networks pve.NodeNetworks
	if networks, err = node.Networks(ctx, "bridge"); err != nil {
		return
	}
	for _, network := range networks {
		if network != nil && network.Iface == request.Bridge {
			return
		}
	}
	err = fmt.Errorf("approved Proxmox bridge %q is not present on node %q", request.Bridge, request.Node)
	return
}

func networkAttachmentMAC(operationKey string) (mac string) {
	var digest [sha256.Size]byte = sha256.Sum256([]byte(operationKey))
	mac = fmt.Sprintf("02:%02x:%02x:%02x:%02x:%02x", digest[0], digest[1], digest[2], digest[3], digest[4])
	return
}

func networkOptionMap(value string) (options map[string]string) {
	options = make(map[string]string)
	for _, option := range strings.Split(value, ",") {
		var pair []string = strings.SplitN(option, "=", 2)
		if len(pair) == 2 {
			options[strings.ToLower(pair[0])] = pair[1]
			if (pair[0] == "virtio" || pair[0] == "e1000" || pair[0] == "rtl8139") && strings.Contains(pair[1], ":") {
				options["macaddr"] = pair[1]
			}
		} else if len(pair) == 1 && strings.Contains(pair[0], ":") {
			options["macaddr"] = pair[0]
		}
	}
	if value != "" && !strings.Contains(value, "=") {
		var parts []string = strings.Split(value, ",")
		if len(parts) > 0 {
			options["macaddr"] = parts[0]
		}
	}
	return
}

func nextNetworkDevice(interfaces map[string]string) (device string, err error) {
	var indexes []int
	for name := range interfaces {
		if !strings.HasPrefix(name, "net") {
			continue
		}
		var index int
		if index, err = strconv.Atoi(strings.TrimPrefix(name, "net")); err != nil || index < 0 || index > 31 {
			continue
		}
		indexes = append(indexes, index)
	}
	sort.Ints(indexes)
	for index := 0; index < 32; index++ {
		if index >= len(indexes) || indexes[index] != index {
			device = fmt.Sprintf("net%d", index)
			return
		}
	}
	err = errors.New("managed VM has no free Proxmox network device slot")
	return
}
