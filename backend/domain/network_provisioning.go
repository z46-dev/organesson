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
	if err = proxmox.ValidateSDNNetworkRequest(request); err != nil {
		err = fmt.Errorf("%w: %v", ErrInvalidInput, err)
		return
	}
	var placement proxmox.SDNNetworkPlacement = proxmox.SDNNetworkNames(request.OperationKey)
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
		if current.Kind != "virtual_network" || current.ConfigurationJSON != string(encoded) {
			err = fmt.Errorf("%w: network %q already exists with different settings", ErrInvalidInput, request.Name)
			return
		}
		resource = current
		return
	}
	var policy proxmox.ResourcePolicy
	if err = service.validatedProxmoxPolicy(&policy); err != nil {
		return
	}
	if !policy.AllowIsolatedSDNNetworks {
		err = fmt.Errorf("%w: platform policy does not allow isolated Proxmox SDN networks", ErrInvalidInput)
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
	var expected proxmox.SDNNetworkPlacement = proxmox.SDNNetworkNames(resource.OperationKey)
	if placement != expected || resource.ExternalID != expected.VNet || resource.ExternalNode != expected.Zone {
		err = fmt.Errorf("%w: Proxmox SDN placement does not match the reserved resource", ErrInvalidInput)
		return
	}
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
	if err = service.Require(actorID, db.PermissionResourceView, resource.OwnershipID); err != nil {
		err = service.Require(actorID, db.PermissionDeploymentManage, resource.OwnershipID)
		if err != nil {
			resource = nil
			return
		}
	}
	err = json.Unmarshal([]byte(resource.ConfigurationJSON), &configuration)
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
