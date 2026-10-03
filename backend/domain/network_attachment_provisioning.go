package domain

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/z46-dev/organesson/backend/db"
	"github.com/z46-dev/organesson/backend/proxmox"
)

type (
	// NetworkAttachmentRequest reserves one VM-owned attachment and optional environment addresses.
	NetworkAttachmentRequest struct {
		Name                  string `json:"name"`
		VirtualMachineID      int    `json:"virtual_machine_id"`
		EnvironmentNetwork    string `json:"environment_network,omitempty"`
		LogicalNetworkID      int    `json:"logical_network_id,omitempty"`
		AddressPoolRequestID  int    `json:"address_pool_request_id,omitempty"`
		RequestedAddressCount int    `json:"requested_address_count,omitempty"`
	}

	// ManagedNetworkAttachmentConfiguration persists desired inputs, IP claims, and the PVE device.
	ManagedNetworkAttachmentConfiguration struct {
		Request               proxmox.NetworkAttachmentRequest   `json:"request"`
		VirtualMachineID      int                                `json:"virtual_machine_id"`
		LogicalNetworkID      int                                `json:"logical_network_id,omitempty"`
		EnvironmentNetwork    string                             `json:"environment_network,omitempty"`
		AddressPoolRequestID  int                                `json:"address_pool_request_id,omitempty"`
		RequestedAddressCount int                                `json:"requested_address_count,omitempty"`
		Addresses             []string                           `json:"addresses"`
		AddressPrefix         string                             `json:"address_prefix,omitempty"`
		AddressGateway        string                             `json:"address_gateway,omitempty"`
		AddressDNS            []string                           `json:"address_dns,omitempty"`
		Placement             proxmox.NetworkAttachmentPlacement `json:"placement"`
		GuestNetwork          *proxmox.GuestNetworkRequest       `json:"guest_network,omitempty"`
	}
)

// ReserveNetworkAttachment validates VM/network ownership and reserves any requested pool addresses.
func (service *Service) ReserveNetworkAttachment(actorID int, request NetworkAttachmentRequest) (resource *db.ManagedResource, configuration ManagedNetworkAttachmentConfiguration, err error) {
	service.provisioningLock.Lock()
	defer service.provisioningLock.Unlock()
	request.Name = strings.TrimSpace(request.Name)
	request.EnvironmentNetwork = strings.TrimSpace(request.EnvironmentNetwork)
	if request.Name == "" || len(request.Name) > 128 || request.VirtualMachineID < 1 || (request.EnvironmentNetwork == "") == (request.LogicalNetworkID == 0) || request.RequestedAddressCount < 0 {
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
	if (request.AddressPoolRequestID == 0) != (request.RequestedAddressCount == 0) {
		err = fmt.Errorf("%w: address pool request and positive requested address count must be configured together", ErrInvalidInput)
		return
	}
	var allocation *AddressPoolRequest
	if request.AddressPoolRequestID > 0 {
		var poolResource *db.ManagedResource
		if poolResource, err = service.store.ManagedResources.Select(request.AddressPoolRequestID); err != nil {
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
		if request.EnvironmentNetwork == "" || stored.EnvironmentNetwork != request.EnvironmentNetwork || stored.AddressFamily != "ipv4" {
			err = fmt.Errorf("%w: address pool must use this attachment's IPv4 environment network", ErrInvalidInput)
			return
		}
		allocation = &stored
		configuration.AddressPrefix = stored.Prefix
		configuration.AddressGateway = stored.Gateway
		configuration.AddressDNS = stored.DNS
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
		if configuration.VirtualMachineID != request.VirtualMachineID || configuration.EnvironmentNetwork != request.EnvironmentNetwork || configuration.LogicalNetworkID != request.LogicalNetworkID || configuration.AddressPoolRequestID != request.AddressPoolRequestID || configuration.RequestedAddressCount != request.RequestedAddressCount {
			err = fmt.Errorf("%w: network attachment already exists with different settings", ErrInvalidInput)
			return
		}
		resource = current
		return
	}
	if allocation != nil {
		var claimed map[string]bool = make(map[string]bool)
		for _, current := range resources {
			if current.Kind != "network_attachment" || current.ConfigurationJSON == "" {
				continue
			}
			var existing ManagedNetworkAttachmentConfiguration
			if err = json.Unmarshal([]byte(current.ConfigurationJSON), &existing); err != nil {
				return
			}
			if existing.AddressPoolRequestID == request.AddressPoolRequestID {
				for _, address := range existing.Addresses {
					claimed[address] = true
				}
			}
		}
		for _, address := range allocation.Addresses {
			if !claimed[address] && len(configuration.Addresses) < request.RequestedAddressCount {
				configuration.Addresses = append(configuration.Addresses, address)
			}
		}
		if len(configuration.Addresses) != request.RequestedAddressCount {
			err = fmt.Errorf("%w: address pool request has too few unclaimed addresses", ErrInvalidInput)
			return
		}
	}
	configuration.VirtualMachineID = vm.ID
	configuration.EnvironmentNetwork = request.EnvironmentNetwork
	configuration.LogicalNetworkID = request.LogicalNetworkID
	configuration.AddressPoolRequestID = request.AddressPoolRequestID
	configuration.RequestedAddressCount = request.RequestedAddressCount
	configuration.Request = proxmox.NetworkAttachmentRequest{
		Node: vm.ExternalNode, VMID: vm.ExternalID, VMOperationKey: vm.OperationKey,
		Bridge: pveNetwork, NetworkOperationKey: networkOperationKey, AttachmentOperationKey: operationKey,
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

// GuestNetworkInput is the desired guest-side IPv4 setup for a managed attachment.
type GuestNetworkInput struct {
	Method  string   `json:"ipv4_method"`
	Address string   `json:"ipv4_address,omitempty"`
	Gateway string   `json:"ipv4_gateway,omitempty"`
	DNS     []string `json:"ipv4_dns,omitempty"`
}

// SaveGuestNetworkConfiguration stores guest settings only after the caller applies them through QGA.
func (service *Service) SaveGuestNetworkConfiguration(actorID int, resourceID int, input GuestNetworkInput) (resource *db.ManagedResource, request proxmox.GuestNetworkRequest, err error) {
	service.provisioningLock.Lock()
	defer service.provisioningLock.Unlock()
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
	request = proxmox.GuestNetworkRequest{
		Node: configuration.Request.Node, VMID: configuration.Request.VMID,
		VMOperationKey: configuration.Request.VMOperationKey, AttachmentKey: configuration.Request.AttachmentOperationKey,
		Bridge: configuration.Request.Bridge, NetworkOperationKey: configuration.Request.NetworkOperationKey,
		Placement: configuration.Placement, Method: input.Method, Address: input.Address, Gateway: input.Gateway, DNS: input.DNS,
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
