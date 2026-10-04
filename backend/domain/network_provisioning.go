package domain

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/z46-dev/organesson/backend/db"
	"github.com/z46-dev/organesson/backend/proxmox"
)

type (
	// StaticNetworkAddress describes a guest address explicitly configured through Organesson.
	StaticNetworkAddress struct {
		Address             string `json:"address"`
		NetworkAttachmentID int    `json:"network_attachment_id"`
		NetworkAttachment   string `json:"network_attachment"`
		VirtualMachineID    int    `json:"virtual_machine_id,omitempty"`
		VirtualMachine      string `json:"virtual_machine,omitempty"`
	}

	// ManagedNetworkConfiguration persists the desired network and PVE placement together.
	ManagedNetworkConfiguration struct {
		Request   proxmox.SDNNetworkRequest   `json:"request"`
		Placement proxmox.SDNNetworkPlacement `json:"placement"`
	}
)

// ReserveSDNNetwork validates policy and ownership before any Proxmox SDN changes are made.
func (service *Service) ReserveSDNNetwork(actorID int, deploymentID int, parentNodeID int, request proxmox.SDNNetworkRequest) (resource *db.ManagedResource, err error) {
	service.provisioningLock.Lock()
	defer service.provisioningLock.Unlock()
	request.Name = strings.TrimSpace(request.Name)
	var parent *db.OwnershipNode
	if parent, err = service.store.OwnershipNodes.Select(parentNodeID); err != nil {
		return
	}
	if parent == nil || parent.DeploymentID != deploymentID || parent.Kind == db.OwnershipNodeKindResource {
		err = ErrNotFound
		return
	}
	if err = service.Require(actorID, db.PermissionDeploymentManage, parentNodeID); err != nil {
		return
	}
	var keyDigest [sha256.Size]byte = sha256.Sum256([]byte(fmt.Sprintf("%d:%d:%s", deploymentID, parentNodeID, request.Name)))
	request.OperationKey = "og-network-" + hex.EncodeToString(keyDigest[:16])
	var policy proxmox.ResourcePolicy
	if err = service.validatedProxmoxPolicy(&policy); err != nil {
		return
	}
	request.VNetSourceZone = policy.VNetSourceZone
	if err = proxmox.ValidateSDNNetworkRequest(request); err != nil {
		err = fmt.Errorf("%w: %v", ErrInvalidInput, err)
		return
	}
	var placement proxmox.SDNNetworkPlacement = proxmox.SDNNetworkNames(request.OperationKey, request.VNetSourceZone)
	var configuration ManagedNetworkConfiguration = ManagedNetworkConfiguration{Request: request, Placement: placement}
	var encoded []byte
	if encoded, err = json.Marshal(configuration); err != nil {
		return
	}
	var resources []*db.ManagedResource
	if resources, err = service.store.ManagedResources.SelectAll(); err != nil {
		return
	}
	for _, current := range resources {
		if current.OperationKey != request.OperationKey {
			continue
		}
		var existingConfiguration ManagedNetworkConfiguration
		if err = json.Unmarshal([]byte(current.ConfigurationJSON), &existingConfiguration); err != nil {
			return
		}
		if current.Kind != "virtual_network" || existingConfiguration.Request != request {
			err = fmt.Errorf("%w: network %q already exists with different settings", ErrInvalidInput, request.Name)
			return
		}
		resource = current
		return
	}
	var deploymentNetworks int
	var globalNetworks int
	for _, current := range resources {
		if current.Kind != "virtual_network" {
			continue
		}
		globalNetworks++
		if current.DeploymentID == deploymentID {
			deploymentNetworks++
		}
	}
	if policy.DeploymentLimits.MaxSDNNetworks > 0 && deploymentNetworks >= policy.DeploymentLimits.MaxSDNNetworks || policy.Limits.MaxSDNNetworks > 0 && globalNetworks >= policy.Limits.MaxSDNNetworks {
		err = fmt.Errorf("%w: isolated Proxmox SDN network quota has been reached", ErrInvalidInput)
		return
	}
	var node *db.OwnershipNode = &db.OwnershipNode{
		DeploymentID: deploymentID,
		ParentID:     &parentNodeID,
		Kind:         db.OwnershipNodeKindResource,
		Name:         request.Name,
		CreatedAt:    service.now(),
	}
	if err = service.store.OwnershipNodes.Insert(node); err != nil {
		return
	}
	resource = &db.ManagedResource{
		DeploymentID:      deploymentID,
		OwnershipID:       node.ID,
		Kind:              "virtual_network",
		Name:              request.Name,
		PowerState:        "provisioning",
		ExternalID:        placement.VNet,
		ExternalNode:      placement.Zone,
		OperationKey:      request.OperationKey,
		ConfigurationJSON: string(encoded),
		CreatedAt:         service.now(),
	}
	if err = service.store.ManagedResources.Insert(resource); err != nil {
		_ = service.store.OwnershipNodes.Delete(node.ID)
		return
	}
	err = service.writeAudit(actorID, "resource.network_provisioning_started", fmt.Sprintf("resource:%d", resource.ID), "succeeded", map[string]string{"name": request.Name, "vnet": placement.VNet})
	return
}

// ReadySDNNetwork stores a successful PVE operation after verifying deterministic placement.
func (service *Service) ReadySDNNetwork(actorID int, resourceID int, placement proxmox.SDNNetworkPlacement) (resource *db.ManagedResource, err error) {
	if resource, err = service.store.ManagedResources.Select(resourceID); err != nil {
		return
	}
	if resource == nil || resource.Kind != "virtual_network" || resource.OperationKey == "" {
		err = ErrNotFound
		return
	}
	var configuration ManagedNetworkConfiguration
	if err = json.Unmarshal([]byte(resource.ConfigurationJSON), &configuration); err != nil {
		return
	}
	var expected proxmox.SDNNetworkPlacement = proxmox.SDNNetworkNames(resource.OperationKey, configuration.Request.VNetSourceZone)
	if placement.Zone != expected.Zone || placement.VNet != expected.VNet || resource.ExternalID != expected.VNet || resource.ExternalNode != expected.Zone ||
		configuration.Request.VNetSourceZone == "" && placement.Tag != 0 ||
		configuration.Request.VNetSourceZone != "" && (placement.Tag == 0 || placement.Tag > 16777215) {
		err = fmt.Errorf("%w: Proxmox SDN placement does not match the reserved resource", ErrInvalidInput)
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
	err = service.writeAudit(actorID, "resource.network_provisioned", fmt.Sprintf("resource:%d", resource.ID), "succeeded", map[string]string{"vnet": placement.VNet, "zone": placement.Zone})
	return
}

// GetSDNNetwork returns a visible network configuration and its managed-resource record.
func (service *Service) GetSDNNetwork(actorID int, resourceID int) (resource *db.ManagedResource, configuration ManagedNetworkConfiguration, err error) {
	if resource, err = service.store.ManagedResources.Select(resourceID); err != nil {
		return
	}
	if resource == nil || resource.Kind != "virtual_network" {
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

// ListNetworkStaticAddresses returns Organesson-declared static addresses visible to the caller.
func (service *Service) ListNetworkStaticAddresses(actorID int, network *db.ManagedResource) (addresses []StaticNetworkAddress, err error) {
	if network == nil || network.Kind != "virtual_network" {
		err = ErrNotFound
		return
	}
	var resources []*db.ManagedResource
	if resources, err = service.store.ManagedResources.SelectAll(); err != nil {
		return
	}
	for _, resource := range resources {
		if resource.DeploymentID != network.DeploymentID || resource.Kind != "network_attachment" {
			continue
		}
		var configuration ManagedNetworkAttachmentConfiguration
		if err = json.Unmarshal([]byte(resource.ConfigurationJSON), &configuration); err != nil {
			return
		}
		if configuration.LogicalNetworkID != network.ID || configuration.GuestNetwork == nil || configuration.GuestNetwork.Method != "static" || configuration.GuestNetwork.Address == "" {
			continue
		}
		var address StaticNetworkAddress = StaticNetworkAddress{
			Address: configuration.GuestNetwork.Address, NetworkAttachmentID: resource.ID,
			NetworkAttachment: resource.Name,
		}
		var vm *db.ManagedResource
		if vm, err = service.store.ManagedResources.Select(configuration.VirtualMachineID); err != nil {
			return
		}
		if vm != nil {
			if err = service.requireResourceView(actorID, vm); err == nil {
				address.VirtualMachineID = vm.ID
				address.VirtualMachine = vm.Name
			} else if errors.Is(err, ErrForbidden) {
				err = nil
			} else {
				return
			}
		}
		addresses = append(addresses, address)
	}
	return
}

// LinkObservedNetworkAddresses adds VM identities only when the viewer may access the matching guest.
func (service *Service) LinkObservedNetworkAddresses(actorID int, network *db.ManagedResource, observed []proxmox.SDNRouterObservedAddress) (linked []proxmox.SDNRouterObservedAddress, err error) {
	linked = append([]proxmox.SDNRouterObservedAddress(nil), observed...)
	if network == nil || network.Kind != "virtual_network" || len(linked) == 0 {
		return
	}
	var resources []*db.ManagedResource
	if resources, err = service.store.ManagedResources.SelectAll(); err != nil {
		return
	}
	var matches map[string]*db.ManagedResource = make(map[string]*db.ManagedResource)
	for _, attachment := range resources {
		if attachment.DeploymentID != network.DeploymentID || attachment.Kind != "network_attachment" {
			continue
		}
		var configuration ManagedNetworkAttachmentConfiguration
		if err = json.Unmarshal([]byte(attachment.ConfigurationJSON), &configuration); err != nil {
			return
		}
		if configuration.LogicalNetworkID != network.ID || configuration.Placement.MAC == "" {
			continue
		}
		var vm *db.ManagedResource
		if vm, err = service.store.ManagedResources.Select(configuration.VirtualMachineID); err != nil {
			return
		}
		if vm != nil && vm.Kind == "virtual_machine" {
			matches[strings.ToLower(configuration.Placement.MAC)] = vm
		}
	}
	for index := range linked {
		var vm *db.ManagedResource = matches[strings.ToLower(linked[index].MAC)]
		if vm == nil {
			continue
		}
		if err = service.requireResourceView(actorID, vm); err != nil {
			if errors.Is(err, ErrForbidden) {
				err = nil
				continue
			}
			return
		}
		linked[index].VirtualMachineID = vm.ID
		linked[index].VirtualMachineName = vm.Name
	}
	return
}

// DeleteSDNNetworkRecord removes Organesson ownership after its PVE VNet has been deleted.
func (service *Service) DeleteSDNNetworkRecord(actorID int, resourceID int) (err error) {
	var resource *db.ManagedResource
	if resource, err = service.store.ManagedResources.Select(resourceID); err != nil {
		return
	}
	if resource == nil || resource.Kind != "virtual_network" {
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
	err = service.writeAudit(actorID, "resource.network_deleted", fmt.Sprintf("resource:%d", resourceID), "succeeded", map[string]string{"name": resource.Name})
	return
}

// validatedProxmoxPolicy loads the saved policy only when its validation still matches its content.
func (service *Service) validatedProxmoxPolicy(policy *proxmox.ResourcePolicy) (err error) {
	var record *db.ProxmoxResourcePolicy
	if record, err = service.store.ProxmoxResourcePolicies.Select(1); err != nil {
		return
	}
	if record == nil || record.ValidatedAt == nil {
		err = fmt.Errorf("%w: Proxmox resource policy must be validated before provisioning", ErrInvalidInput)
		return
	}
	if err = json.Unmarshal([]byte(record.ConfigurationJSON), policy); err != nil {
		return
	}
	var hash string
	if hash, err = proxmox.ResourcePolicyHash(*policy); err != nil {
		return
	}
	if hash != record.ValidatedConfigHash {
		err = fmt.Errorf("%w: Proxmox resource policy changed after validation", ErrInvalidInput)
		return
	}
	var validation proxmox.ResourcePolicyValidation
	if err = json.Unmarshal([]byte(record.ValidationJSON), &validation); err != nil || !validation.Valid {
		err = fmt.Errorf("%w: Proxmox resource policy is not valid", ErrInvalidInput)
	}
	return
}
