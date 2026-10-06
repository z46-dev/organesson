package domain

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/netip"
	"strconv"
	"strings"

	"github.com/z46-dev/organesson/backend/db"
	"github.com/z46-dev/organesson/backend/proxmox"
)

type (
	// NetworkAttachmentRequest reserves one VM-owned attachment and addresses when its network requires filtering.
	NetworkAttachmentRequest struct {
		Name                      string `json:"name"`
		VirtualMachineID          int    `json:"virtual_machine_id"`
		EnvironmentNetwork        string `json:"environment_network,omitempty"`
		LogicalNetworkID          int    `json:"logical_network_id,omitempty"`
		AddressPoolRequestID      int    `json:"address_pool_request_id,omitempty"`
		RequestedAddressCount     int    `json:"requested_address_count,omitempty"`
		IPv6AddressPoolRequestID  int    `json:"ipv6_address_pool_request_id,omitempty"`
		RequestedIPv6AddressCount int    `json:"requested_ipv6_address_count,omitempty"`
	}

	// ManagedNetworkAttachmentConfiguration persists desired inputs, IP claims, and the PVE device.
	ManagedNetworkAttachmentConfiguration struct {
		Request                   proxmox.NetworkAttachmentRequest   `json:"request"`
		VirtualMachineID          int                                `json:"virtual_machine_id"`
		LogicalNetworkID          int                                `json:"logical_network_id,omitempty"`
		NetworkDHCPEnabled        bool                               `json:"network_dhcp_enabled,omitempty"`
		NetworkIPv6DHCPEnabled    bool                               `json:"network_ipv6_dhcp_enabled,omitempty"`
		EnvironmentNetwork        string                             `json:"environment_network,omitempty"`
		AddressPoolRequestID      int                                `json:"address_pool_request_id,omitempty"`
		RequestedAddressCount     int                                `json:"requested_address_count,omitempty"`
		IPv6AddressPoolRequestID  int                                `json:"ipv6_address_pool_request_id,omitempty"`
		RequestedIPv6AddressCount int                                `json:"requested_ipv6_address_count,omitempty"`
		Addresses                 []string                           `json:"addresses"`
		AddressFamily             string                             `json:"address_family,omitempty"`
		AddressPrefix             string                             `json:"address_prefix,omitempty"`
		AddressGateway            string                             `json:"address_gateway,omitempty"`
		AddressDNS                []string                           `json:"address_dns,omitempty"`
		IPv6Addresses             []string                           `json:"ipv6_addresses,omitempty"`
		IPv6AddressPrefix         string                             `json:"ipv6_address_prefix,omitempty"`
		IPv6AddressGateway        string                             `json:"ipv6_address_gateway,omitempty"`
		IPv6AddressDNS            []string                           `json:"ipv6_address_dns,omitempty"`
		Placement                 proxmox.NetworkAttachmentPlacement `json:"placement"`
		GuestNetwork              *proxmox.GuestNetworkRequest       `json:"guest_network,omitempty"`
	}
)

// ReserveNetworkAttachment validates VM/network ownership and reserves any requested pool addresses.
func (service *Service) ReserveNetworkAttachment(actorID int, request NetworkAttachmentRequest) (resource *db.ManagedResource, configuration ManagedNetworkAttachmentConfiguration, err error) {
	service.provisioningLock.Lock()
	defer service.provisioningLock.Unlock()
	request.Name = strings.TrimSpace(request.Name)
	request.EnvironmentNetwork = strings.TrimSpace(request.EnvironmentNetwork)
	if request.Name == "" || len(request.Name) > 128 || request.VirtualMachineID < 1 || (request.EnvironmentNetwork == "") == (request.LogicalNetworkID == 0) || request.RequestedAddressCount < 0 || request.RequestedIPv6AddressCount < 0 || request.AddressPoolRequestID > 0 && request.AddressPoolRequestID == request.IPv6AddressPoolRequestID {
		err = fmt.Errorf("%w: network attachment is invalid", ErrInvalidInput)
		return
	}
	var vm *db.ManagedResource
	if vm, err = service.store.ManagedResources.Select(request.VirtualMachineID); err != nil {
		return
	}
	if vm == nil || vm.Kind != "virtual_machine" || vm.ExternalID == "" || vm.ExternalNode == "" || vm.OperationKey == "" {
		err = fmt.Errorf("%w: network attachments require a Proxmox-backed virtual machine", ErrInvalidInput)
		return
	}
	if err = service.Require(actorID, db.PermissionDeploymentManage, vm.OwnershipID); err != nil {
		return
	}
	var keyDigest [sha256.Size]byte = sha256.Sum256([]byte(fmt.Sprintf("%d:%s", vm.ID, request.Name)))
	var operationKey string = "og-nic-" + hex.EncodeToString(keyDigest[:16])
	var policy proxmox.ResourcePolicy
	if err = service.validatedProxmoxPolicy(&policy); err != nil {
		return
	}
	var pveNetwork string
	var networkOperationKey string
	var enforceAddressFilter bool = request.EnvironmentNetwork != ""
	var allowUnallocatedRouterLAN bool
	var allowManagedNetworkSubnet bool
	var allowDHCPv4Server bool
	var allowDHCPv6Server bool
	var allowedClientSubnets []string
	var managedIPv4Subnet string
	var managedIPv6Subnet string
	if request.LogicalNetworkID > 0 {
		var network *db.ManagedResource
		if network, err = service.store.ManagedResources.Select(request.LogicalNetworkID); err != nil {
			return
		}
		if network == nil || network.Kind != "virtual_network" || network.DeploymentID != vm.DeploymentID || network.PowerState != "ready" {
			err = fmt.Errorf("%w: logical network is not ready in the VM deployment", ErrInvalidInput)
			return
		}
		if err = service.Require(actorID, db.PermissionResourceView, network.OwnershipID); err != nil {
			err = service.Require(actorID, db.PermissionDeploymentManage, network.OwnershipID)
			if err != nil {
				return
			}
		}
		pveNetwork = network.ExternalID
		networkOperationKey = network.OperationKey
		var networkConfiguration ManagedNetworkConfiguration
		if err = json.Unmarshal([]byte(network.ConfigurationJSON), &networkConfiguration); err != nil {
			return
		}
		if exposure := networkConfiguration.Request.ExternalVLAN; exposure != nil && !containsString(exposure.Nodes, vm.ExternalNode) {
			err = fmt.Errorf("%w: external VLAN %d is not available on VM node %q", ErrInvalidInput, exposure.VLANID, vm.ExternalNode)
			return
		}
		configuration.NetworkDHCPEnabled = networkConfiguration.Request.DHCPEnabled
		configuration.NetworkIPv6DHCPEnabled = networkConfiguration.Request.IPv6DHCPEnabled
		managedIPv4Subnet = networkConfiguration.Request.Subnet
		managedIPv6Subnet = networkConfiguration.Request.IPv6Subnet
		enforceAddressFilter = networkConfiguration.Request.Mode == "managed"
		var vmID int
		if vmID, err = strconv.Atoi(vm.ExternalID); err != nil {
			return
		}
		allowUnallocatedRouterLAN = enforceAddressFilter && networkConfiguration.Request.RouterVMID == vmID
		allowManagedNetworkSubnet = enforceAddressFilter && !allowUnallocatedRouterLAN
		allowDHCPv4Server = allowUnallocatedRouterLAN && networkConfiguration.Request.DHCPEnabled
		allowDHCPv6Server = allowUnallocatedRouterLAN && networkConfiguration.Request.IPv6DHCPEnabled
		if allowUnallocatedRouterLAN {
			configuration.Addresses = []string{networkConfiguration.Request.Gateway}
			if networkConfiguration.Request.IPv6Gateway != "" {
				configuration.IPv6Addresses = []string{networkConfiguration.Request.IPv6Gateway}
			}
			if networkConfiguration.Request.Subnet != "" {
				allowedClientSubnets = append(allowedClientSubnets, networkConfiguration.Request.Subnet)
			}
			if networkConfiguration.Request.IPv6Subnet != "" {
				allowedClientSubnets = append(allowedClientSubnets, networkConfiguration.Request.IPv6Subnet)
			}
		}
	} else {
		for _, network := range policy.Networks {
			if network.Name == request.EnvironmentNetwork {
				pveNetwork = network.PVEName
				if network.Kind == "vnet" {
					// Platform-owned VNets are authorized by the validated policy inventory.
					pveNetwork = network.PVEName
				}
				break
			}
		}
		if pveNetwork == "" {
			err = fmt.Errorf("%w: environment network %q is not in the validated platform policy", ErrInvalidInput, request.EnvironmentNetwork)
			return
		}
	}
	if err = validateNetworkAttachmentAddressPools(request, enforceAddressFilter, allowUnallocatedRouterLAN, allowManagedNetworkSubnet); err != nil {
		return
	}
	var allocations []*AddressPoolRequest
	for _, requestID := range []int{request.AddressPoolRequestID, request.IPv6AddressPoolRequestID} {
		if requestID == 0 {
			continue
		}
		var poolResource *db.ManagedResource
		if poolResource, err = service.store.ManagedResources.Select(requestID); err != nil {
			return
		}
		if poolResource == nil || poolResource.Kind != "address_pool_request" || poolResource.DeploymentID != vm.DeploymentID {
			err = fmt.Errorf("%w: address pool request is not part of the VM deployment", ErrInvalidInput)
			return
		}
		var stored AddressPoolRequest
		if err = json.Unmarshal([]byte(poolResource.ConfigurationJSON), &stored); err != nil {
			return
		}
		if stored.EnvironmentNetwork != request.EnvironmentNetwork || stored.LogicalNetworkID != request.LogicalNetworkID {
			err = fmt.Errorf("%w: address pool must use this attachment's network", ErrInvalidInput)
			return
		}
		if requestID == request.AddressPoolRequestID && stored.AddressFamily != "ipv4" || requestID == request.IPv6AddressPoolRequestID && stored.AddressFamily != "ipv6" {
			err = fmt.Errorf("%w: address pool family does not match its interface field", ErrInvalidInput)
			return
		}
		allocations = append(allocations, &stored)
		if stored.AddressFamily == "ipv4" {
			configuration.AddressFamily, configuration.AddressPrefix, configuration.AddressGateway, configuration.AddressDNS = stored.AddressFamily, stored.Prefix, stored.Gateway, stored.DNS
		} else {
			configuration.IPv6AddressPrefix, configuration.IPv6AddressGateway, configuration.IPv6AddressDNS = stored.Prefix, stored.Gateway, stored.DNS
		}
	}
	var resources []*db.ManagedResource
	if resources, err = service.store.ManagedResources.SelectAll(); err != nil {
		return
	}
	for _, current := range resources {
		if current.OperationKey != operationKey {
			continue
		}
		if current.Kind != "network_attachment" {
			err = fmt.Errorf("%w: attachment name is already used by another resource", ErrInvalidInput)
			return
		}
		if err = json.Unmarshal([]byte(current.ConfigurationJSON), &configuration); err != nil {
			return
		}
		if configuration.VirtualMachineID != request.VirtualMachineID || configuration.EnvironmentNetwork != request.EnvironmentNetwork || configuration.LogicalNetworkID != request.LogicalNetworkID || configuration.AddressPoolRequestID != request.AddressPoolRequestID || configuration.RequestedAddressCount != request.RequestedAddressCount || configuration.IPv6AddressPoolRequestID != request.IPv6AddressPoolRequestID || configuration.RequestedIPv6AddressCount != request.RequestedIPv6AddressCount {
			err = fmt.Errorf("%w: network attachment already exists with different settings", ErrInvalidInput)
			return
		}
		resource = current
		return
	}
	for _, allocation := range allocations {
		var requestID int = request.AddressPoolRequestID
		var count int = request.RequestedAddressCount
		if allocation.AddressFamily == "ipv6" {
			requestID, count = request.IPv6AddressPoolRequestID, request.RequestedIPv6AddressCount
		}
		var claimed map[string]bool = make(map[string]bool)
		for _, current := range resources {
			if current.Kind != "network_attachment" || current.ConfigurationJSON == "" {
				continue
			}
			var existing ManagedNetworkAttachmentConfiguration
			if err = json.Unmarshal([]byte(current.ConfigurationJSON), &existing); err != nil {
				return
			}
			if existing.AddressPoolRequestID == requestID || existing.IPv6AddressPoolRequestID == requestID {
				for _, address := range existing.Addresses {
					claimed[address] = true
				}
				for _, address := range existing.IPv6Addresses {
					claimed[address] = true
				}
			}
		}
		for _, address := range allocation.Addresses {
			if !claimed[address] {
				if allocation.AddressFamily == "ipv6" && len(configuration.IPv6Addresses) < count {
					configuration.IPv6Addresses = append(configuration.IPv6Addresses, address)
				} else if allocation.AddressFamily == "ipv4" && len(configuration.Addresses) < count {
					configuration.Addresses = append(configuration.Addresses, address)
				}
			}
		}
		var countReserved int = len(configuration.Addresses)
		if allocation.AddressFamily == "ipv6" {
			countReserved = len(configuration.IPv6Addresses)
		}
		if countReserved != count {
			err = fmt.Errorf("%w: address pool request has too few unclaimed addresses", ErrInvalidInput)
			return
		}
	}
	configuration.VirtualMachineID = vm.ID
	configuration.EnvironmentNetwork = request.EnvironmentNetwork
	configuration.LogicalNetworkID = request.LogicalNetworkID
	configuration.AddressPoolRequestID = request.AddressPoolRequestID
	configuration.RequestedAddressCount = request.RequestedAddressCount
	configuration.IPv6AddressPoolRequestID = request.IPv6AddressPoolRequestID
	configuration.RequestedIPv6AddressCount = request.RequestedIPv6AddressCount
	var allowedAddresses []string = append(append([]string{}, configuration.Addresses...), configuration.IPv6Addresses...)
	if allowManagedNetworkSubnet {
		if request.AddressPoolRequestID == 0 && managedIPv4Subnet != "" {
			allowedAddresses = append(allowedAddresses, managedIPv4Subnet)
		}
		if request.IPv6AddressPoolRequestID == 0 && managedIPv6Subnet != "" {
			allowedAddresses = append(allowedAddresses, managedIPv6Subnet)
		}
	}
	configuration.Request = proxmox.NetworkAttachmentRequest{
		Node: vm.ExternalNode, VMID: vm.ExternalID, VMOperationKey: vm.OperationKey,
		Bridge: pveNetwork, NetworkOperationKey: networkOperationKey, AttachmentOperationKey: operationKey,
		EnforceAddressFilter: enforceAddressFilter, AllowDHCPv4Server: allowDHCPv4Server, AllowDHCPv6Server: allowDHCPv6Server,
		AllowedAddresses: allowedAddresses, AllowedClientSubnets: allowedClientSubnets,
	}
	var node *db.OwnershipNode = &db.OwnershipNode{
		DeploymentID: vm.DeploymentID, ParentID: &vm.OwnershipID, Kind: db.OwnershipNodeKindResource,
		Name: request.Name, CreatedAt: service.now(),
	}
	if err = service.store.OwnershipNodes.Insert(node); err != nil {
		return
	}
	var encoded []byte
	if encoded, err = json.Marshal(configuration); err != nil {
		_ = service.store.OwnershipNodes.Delete(node.ID)
		return
	}
	resource = &db.ManagedResource{
		DeploymentID: vm.DeploymentID, OwnershipID: node.ID, Kind: "network_attachment", Name: request.Name,
		PowerState: "provisioning", OperationKey: operationKey, ConfigurationJSON: string(encoded), CreatedAt: service.now(),
	}
	if err = service.store.ManagedResources.Insert(resource); err != nil {
		_ = service.store.OwnershipNodes.Delete(node.ID)
		return
	}
	err = service.writeAudit(actorID, "resource.network_attachment_started", fmt.Sprintf("resource:%d", resource.ID), "succeeded", map[string]string{"name": request.Name, "vm": vm.Name})
	return
}

// validateNetworkAttachmentAddressPools requires requests on environment networks and permits managed subnet scopes.
func validateNetworkAttachmentAddressPools(request NetworkAttachmentRequest, enforceAddressFilter bool, allowUnallocatedRouterLAN bool, allowManagedNetworkSubnet bool) (err error) {
	if (request.AddressPoolRequestID == 0) != (request.RequestedAddressCount == 0) || (request.IPv6AddressPoolRequestID == 0) != (request.RequestedIPv6AddressCount == 0) || request.RequestedAddressCount < 0 || request.RequestedIPv6AddressCount < 0 {
		err = fmt.Errorf("%w: each address-family pool request and positive address count must be configured together", ErrInvalidInput)
		return
	}
	if enforceAddressFilter && !allowUnallocatedRouterLAN && !allowManagedNetworkSubnet && request.AddressPoolRequestID == 0 && request.IPv6AddressPoolRequestID == 0 {
		err = fmt.Errorf("%w: environment and managed-network attachments require an address-pool request for IP filtering", ErrInvalidInput)
	}
	return
}

// GuestNetworkInput is the desired guest-side IPv4 and IPv6 setup for a managed attachment.
type GuestNetworkInput struct {
	Method           string   `json:"ipv4_method"`
	Address          string   `json:"ipv4_address,omitempty"`
	Gateway          string   `json:"ipv4_gateway,omitempty"`
	DNS              []string `json:"ipv4_dns,omitempty"`
	NeverDefault     bool     `json:"ipv4_never_default,omitempty"`
	IPv6Method       string   `json:"ipv6_method,omitempty"`
	IPv6Address      string   `json:"ipv6_address,omitempty"`
	IPv6Gateway      string   `json:"ipv6_gateway,omitempty"`
	IPv6DNS          []string `json:"ipv6_dns,omitempty"`
	IPv6NeverDefault bool     `json:"ipv6_never_default,omitempty"`
}

// SaveGuestNetworkConfiguration stores guest settings only after the caller applies them through QGA.
func (service *Service) SaveGuestNetworkConfiguration(actorID int, resourceID int, input GuestNetworkInput) (resource *db.ManagedResource, request proxmox.GuestNetworkRequest, err error) {
	service.provisioningLock.Lock()
	defer service.provisioningLock.Unlock()
	if input.IPv6Method == "" {
		input.IPv6Method = "disabled"
	}
	if resource, err = service.store.ManagedResources.Select(resourceID); err != nil {
		return
	}
	if resource == nil || resource.Kind != "network_attachment" {
		err = ErrNotFound
		return
	}
	if err = service.Require(actorID, db.PermissionDeploymentManage, resource.OwnershipID); err != nil {
		return
	}
	var configuration ManagedNetworkAttachmentConfiguration
	if err = json.Unmarshal([]byte(resource.ConfigurationJSON), &configuration); err != nil {
		return
	}
	if resource.PowerState != "ready" || configuration.Placement.Device == "" {
		err = fmt.Errorf("%w: network attachment must be ready before guest setup", ErrInvalidInput)
		return
	}
	if err = validateGuestNetworkInput(configuration, input); err != nil {
		return
	}
	request = proxmox.GuestNetworkRequest{
		Node: configuration.Request.Node, VMID: configuration.Request.VMID,
		VMOperationKey: configuration.Request.VMOperationKey, AttachmentKey: configuration.Request.AttachmentOperationKey,
		Bridge: configuration.Request.Bridge, NetworkOperationKey: configuration.Request.NetworkOperationKey,
		Placement: configuration.Placement, Method: input.Method, Address: input.Address, Gateway: input.Gateway, DNS: input.DNS, NeverDefault: input.NeverDefault,
		IPv6Method: input.IPv6Method, IPv6Address: input.IPv6Address, IPv6Prefix: configuration.IPv6AddressPrefix,
		IPv6Gateway: input.IPv6Gateway, IPv6DNS: input.IPv6DNS, IPv6NeverDefault: input.IPv6NeverDefault,
		EnforceAddressFilter: configuration.Request.EnforceAddressFilter, AllowedAddresses: append([]string{}, configuration.Request.AllowedAddresses...),
	}
	configuration.GuestNetwork = &request
	var encoded []byte
	if encoded, err = json.Marshal(configuration); err != nil {
		return
	}
	resource.ConfigurationJSON = string(encoded)
	err = service.store.ManagedResources.Update(resource)
	return
}

// ValidateGuestNetworkInput rejects guest addresses outside the attachment's reserved allocation.
func (service *Service) ValidateGuestNetworkInput(actorID int, resourceID int, input GuestNetworkInput) (err error) {
	var resource *db.ManagedResource
	if resource, err = service.store.ManagedResources.Select(resourceID); err != nil {
		return
	}
	if resource == nil || resource.Kind != "network_attachment" {
		err = ErrNotFound
		return
	}
	if err = service.Require(actorID, db.PermissionDeploymentManage, resource.OwnershipID); err != nil {
		return
	}
	var configuration ManagedNetworkAttachmentConfiguration
	if err = json.Unmarshal([]byte(resource.ConfigurationJSON), &configuration); err != nil {
		return
	}
	err = validateGuestNetworkInput(configuration, input)
	return
}

// validateGuestNetworkInput binds static guest addresses to reservations or the managed network's subnet scope.
func validateGuestNetworkInput(configuration ManagedNetworkAttachmentConfiguration, input GuestNetworkInput) (err error) {
	if !configuration.Request.EnforceAddressFilter {
		return
	}
	if configuration.LogicalNetworkID > 0 && input.Method == "dhcp" && !configuration.NetworkDHCPEnabled {
		err = fmt.Errorf("%w: IPv4 DHCP is disabled on this managed network", ErrInvalidInput)
		return
	}
	if input.Method == "dhcp" && !containsAllowedFamily(configuration.Request.AllowedAddresses, true) {
		err = fmt.Errorf("%w: IPv4 DHCP requires IPv4 addresses in this interface's allocation", ErrInvalidInput)
		return
	}
	if (input.IPv6Method == "dhcp" || input.IPv6Method == "slaac") && !containsAllowedFamily(configuration.Request.AllowedAddresses, false) {
		err = fmt.Errorf("%w: dynamic IPv6 requires IPv6 addresses in this interface's allocation", ErrInvalidInput)
		return
	}
	if configuration.LogicalNetworkID > 0 && input.IPv6Method == "dhcp" && !configuration.NetworkIPv6DHCPEnabled {
		err = fmt.Errorf("%w: DHCPv6 is disabled on this managed network", ErrInvalidInput)
		return
	}
	if input.IPv6Method == "slaac" {
		if err = proxmox.ValidateSLAACAllocation(configuration.IPv6AddressPrefix, configuration.Placement.MAC, configuration.Request.AllowedAddresses); err != nil {
			err = fmt.Errorf("%w: %v", ErrInvalidInput, err)
			return
		}
	}
	if input.Method == "static" {
		var prefix netip.Prefix
		if prefix, err = netip.ParsePrefix(input.Address); err != nil {
			return
		}
		if !containsAllowedAddressScope(configuration.Request.AllowedAddresses, prefix.Addr()) {
			err = fmt.Errorf("%w: static IPv4 address is outside this interface's allowed address scope", ErrInvalidInput)
			return
		}
	}
	if input.IPv6Method == "static" {
		var prefix netip.Prefix
		if prefix, err = netip.ParsePrefix(input.IPv6Address); err != nil {
			return
		}
		if !containsAllowedAddressScope(configuration.Request.AllowedAddresses, prefix.Addr()) {
			err = fmt.Errorf("%w: static IPv6 address is outside this interface's allowed address scope", ErrInvalidInput)
			return
		}
	}
	return
}

// containsAllowedAddressScope checks whether an address matches a host allocation or an allowed subnet.
func containsAllowedAddressScope(addresses []string, candidate netip.Addr) (found bool) {
	for _, value := range addresses {
		var prefix netip.Prefix
		var address netip.Addr
		var parseErr error
		if address, parseErr = netip.ParseAddr(value); parseErr == nil {
			prefix = netip.PrefixFrom(address, address.BitLen())
		} else {
			prefix, _ = netip.ParsePrefix(value)
		}
		if prefix.IsValid() && prefix.Contains(candidate) {
			found = true
			return
		}
	}
	return
}

// containsAllowedFamily reports whether the interface has an allowed address or prefix of the requested family.
func containsAllowedFamily(addresses []string, ipv4 bool) (found bool) {
	for _, value := range addresses {
		var prefix netip.Prefix
		var address netip.Addr
		var parseErr error
		if address, parseErr = netip.ParseAddr(value); parseErr == nil {
			prefix = netip.PrefixFrom(address, address.BitLen())
		} else {
			prefix, _ = netip.ParsePrefix(value)
		}
		if prefix.IsValid() && prefix.Addr().Is4() == ipv4 {
			found = true
			return
		}
	}
	return
}

// ClearGuestNetworkConfiguration clears persisted desired state after QGA removes its managed profile.
func (service *Service) ClearGuestNetworkConfiguration(actorID int, resourceID int) (err error) {
	service.provisioningLock.Lock()
	defer service.provisioningLock.Unlock()
	var resource *db.ManagedResource
	if resource, err = service.store.ManagedResources.Select(resourceID); err != nil {
		return
	}
	if resource == nil || resource.Kind != "network_attachment" {
		err = ErrNotFound
		return
	}
	if err = service.Require(actorID, db.PermissionDeploymentManage, resource.OwnershipID); err != nil {
		return
	}
	var configuration ManagedNetworkAttachmentConfiguration
	if err = json.Unmarshal([]byte(resource.ConfigurationJSON), &configuration); err != nil {
		return
	}
	configuration.GuestNetwork = nil
	var encoded []byte
	if encoded, err = json.Marshal(configuration); err != nil {
		return
	}
	resource.ConfigurationJSON = string(encoded)
	err = service.store.ManagedResources.Update(resource)
	return
}

// ReadyNetworkAttachment records the PVE NIC slot after Proxmox confirms attachment.
func (service *Service) ReadyNetworkAttachment(actorID int, resourceID int, placement proxmox.NetworkAttachmentPlacement) (resource *db.ManagedResource, err error) {
	if resource, err = service.store.ManagedResources.Select(resourceID); err != nil {
		return
	}
	if resource == nil || resource.Kind != "network_attachment" {
		err = ErrNotFound
		return
	}
	var configuration ManagedNetworkAttachmentConfiguration
	if err = json.Unmarshal([]byte(resource.ConfigurationJSON), &configuration); err != nil {
		return
	}
	if !strings.HasPrefix(placement.Device, "net") || placement.MAC == "" {
		err = fmt.Errorf("%w: Proxmox returned an invalid NIC placement", ErrInvalidInput)
		return
	}
	configuration.Placement = placement
	var encoded []byte
	if encoded, err = json.Marshal(configuration); err != nil {
		return
	}
	resource.ConfigurationJSON = string(encoded)
	resource.PowerState = "ready"
	if err = service.store.ManagedResources.Update(resource); err != nil {
		return
	}
	err = service.writeAudit(actorID, "resource.network_attachment_ready", fmt.Sprintf("resource:%d", resourceID), "succeeded", map[string]string{"device": placement.Device})
	return
}

// GetNetworkAttachment returns a visible attachment and its current PVE placement.
func (service *Service) GetNetworkAttachment(actorID int, resourceID int) (resource *db.ManagedResource, configuration ManagedNetworkAttachmentConfiguration, err error) {
	if resource, err = service.store.ManagedResources.Select(resourceID); err != nil {
		return
	}
	if resource == nil || resource.Kind != "network_attachment" {
		err = ErrNotFound
		return
	}
	if err = service.requireResourceView(actorID, resource); err != nil {
		resource = nil
		return
	}
	err = json.Unmarshal([]byte(resource.ConfigurationJSON), &configuration)
	return
}

// DeleteNetworkAttachmentRecord frees any claimed addresses after PVE detached the device.
func (service *Service) DeleteNetworkAttachmentRecord(actorID int, resourceID int) (err error) {
	var resource *db.ManagedResource
	if resource, err = service.store.ManagedResources.Select(resourceID); err != nil {
		return
	}
	if resource == nil || resource.Kind != "network_attachment" {
		err = ErrNotFound
		return
	}
	if err = service.Require(actorID, db.PermissionDeploymentManage, resource.OwnershipID); err != nil {
		return
	}
	if err = service.store.ManagedResources.Delete(resource.ID); err != nil {
		return
	}
	if err = service.store.OwnershipNodes.Delete(resource.OwnershipID); err != nil {
		return
	}
	err = service.writeAudit(actorID, "resource.network_attachment_deleted", fmt.Sprintf("resource:%d", resourceID), "succeeded", map[string]string{"name": resource.Name})
	return
}
