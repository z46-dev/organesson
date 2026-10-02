package domain

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/netip"
	"strings"

	"github.com/z46-dev/organesson/backend/db"
	"github.com/z46-dev/organesson/backend/proxmox"
)

type (
	// AddressPoolRequest reserves environment-network addresses for one deployment.
	AddressPoolRequest struct {
		DeploymentID       int      `json:"deployment_id"`
		Name               string   `json:"name"`
		EnvironmentNetwork string   `json:"environment_network"`
		AddressFamily      string   `json:"address_family"`
		AddressCount       int      `json:"address_count"`
		PoolName           string   `json:"pool_name"`
		Prefix             string   `json:"prefix"`
		Gateway            string   `json:"gateway,omitempty"`
		DNS                []string `json:"dns,omitempty"`
		Addresses          []string `json:"addresses"`
	}
)

// ReserveAddressPoolRequest validates policy and durably allocates a deployment's environment addresses.
func (service *Service) ReserveAddressPoolRequest(actorID int, request AddressPoolRequest) (resource *db.ManagedResource, allocation AddressPoolRequest, err error) {
	service.provisioningLock.Lock()
	defer service.provisioningLock.Unlock()
	request.Name = strings.TrimSpace(request.Name)
	request.EnvironmentNetwork = strings.TrimSpace(request.EnvironmentNetwork)
	request.AddressFamily = strings.TrimSpace(request.AddressFamily)
	if request.DeploymentID < 1 || request.Name == "" || len(request.Name) > 128 || request.EnvironmentNetwork == "" || (request.AddressFamily != "ipv4" && request.AddressFamily != "ipv6") || request.AddressCount < 1 || request.AddressCount > 4096 {
		err = fmt.Errorf("%w: address-pool request is invalid", ErrInvalidInput)
		return
	}
	var deployment *db.Deployment
	if deployment, err = service.store.Deployments.Select(request.DeploymentID); err != nil {
		return
	}
	if deployment == nil || deployment.RootNodeID == nil {
		err = ErrNotFound
		return
	}
	if err = service.Require(actorID, db.PermissionDeploymentManage, *deployment.RootNodeID); err != nil {
		return
	}
	var resources []*db.ManagedResource
	if resources, err = service.store.ManagedResources.SelectAll(); err != nil {
		return
	}
	var keyDigest [sha256.Size]byte = sha256.Sum256([]byte(fmt.Sprintf("%d:%s", request.DeploymentID, request.Name)))
	var operationKey string = "og-address-" + hex.EncodeToString(keyDigest[:16])
	for _, current := range resources {
		if current.OperationKey != operationKey {
			continue
		}
		if current.Kind != "address_pool_request" || current.ConfigurationJSON == "" {
			err = fmt.Errorf("%w: address-pool request name is already used by another resource", ErrInvalidInput)
			return
		}
		if err = json.Unmarshal([]byte(current.ConfigurationJSON), &allocation); err != nil {
			return
		}
		if allocation.DeploymentID != request.DeploymentID || allocation.Name != request.Name || allocation.EnvironmentNetwork != request.EnvironmentNetwork || allocation.AddressFamily != request.AddressFamily || allocation.AddressCount != request.AddressCount {
			err = fmt.Errorf("%w: address-pool request already exists with different settings", ErrInvalidInput)
			return
		}
		resource = current
		return
	}
	var policy proxmox.ResourcePolicy
	if err = service.validatedProxmoxPolicy(&policy); err != nil {
		return
	}
	var selectedPool *proxmox.AddressPool
	for _, network := range policy.Networks {
		if network.Name != request.EnvironmentNetwork {
			continue
		}
		for index := range network.AddressPools {
			var pool proxmox.AddressPool = network.AddressPools[index]
			var start netip.Addr
			if start, err = netip.ParseAddr(pool.Start); err != nil {
				return
			}
			if (request.AddressFamily == "ipv4") != start.Is4() {
				continue
			}
			if selectedPool != nil {
				err = fmt.Errorf("%w: environment network %q has multiple %s pools; allocation is ambiguous", ErrInvalidInput, request.EnvironmentNetwork, request.AddressFamily)
				return
			}
			selectedPool = &pool
		}
	}
	if selectedPool == nil {
		err = fmt.Errorf("%w: no %s address pool is configured for environment network %q", ErrInvalidInput, request.AddressFamily, request.EnvironmentNetwork)
		return
	}
	var reserved []string
	for _, current := range resources {
		if current.Kind != "address_pool_request" || current.ConfigurationJSON == "" {
			continue
		}
		var existing AddressPoolRequest
		if err = json.Unmarshal([]byte(current.ConfigurationJSON), &existing); err != nil {
			return
		}
		if existing.EnvironmentNetwork == request.EnvironmentNetwork && existing.PoolName == selectedPool.Name && existing.AddressFamily == request.AddressFamily {
			reserved = append(reserved, existing.Addresses...)
		}
	}
	if allocation.Addresses, err = proxmox.AllocateAddresses(*selectedPool, request.AddressCount, reserved); err != nil {
		err = fmt.Errorf("%w: %v", ErrInvalidInput, err)
		return
	}
	allocation.DeploymentID = request.DeploymentID
	allocation.Name = request.Name
	allocation.EnvironmentNetwork = request.EnvironmentNetwork
	allocation.AddressFamily = request.AddressFamily
	allocation.AddressCount = request.AddressCount
	allocation.PoolName = selectedPool.Name
	allocation.Prefix = selectedPool.Prefix
	allocation.Gateway = selectedPool.Gateway
	allocation.DNS = selectedPool.DNS
	var encoded []byte
	if encoded, err = json.Marshal(allocation); err != nil {
		return
	}
	var node *db.OwnershipNode = &db.OwnershipNode{
		DeploymentID: request.DeploymentID,
		ParentID:     deployment.RootNodeID,
		Kind:         db.OwnershipNodeKindResource,
		Name:         request.Name,
		CreatedAt:    service.now(),
	}
	if err = service.store.OwnershipNodes.Insert(node); err != nil {
		return
	}
	resource = &db.ManagedResource{
		DeploymentID:      request.DeploymentID,
		OwnershipID:       node.ID,
		Kind:              "address_pool_request",
		Name:              request.Name,
		PowerState:        "allocated",
		OperationKey:      operationKey,
		ConfigurationJSON: string(encoded),
		CreatedAt:         service.now(),
	}
	if err = service.store.ManagedResources.Insert(resource); err != nil {
		_ = service.store.OwnershipNodes.Delete(node.ID)
		return
	}
	err = service.writeAudit(actorID, "resource.addresses_allocated", fmt.Sprintf("resource:%d", resource.ID), "succeeded", map[string]string{"network": request.EnvironmentNetwork, "count": fmt.Sprint(request.AddressCount)})
	return
}

// GetAddressPoolRequest returns the allocation after checking resource visibility.
func (service *Service) GetAddressPoolRequest(actorID int, resourceID int) (resource *db.ManagedResource, allocation AddressPoolRequest, err error) {
	if resource, err = service.store.ManagedResources.Select(resourceID); err != nil {
		return
	}
	if resource == nil || resource.Kind != "address_pool_request" {
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
	err = json.Unmarshal([]byte(resource.ConfigurationJSON), &allocation)
	return
}

// DeleteAddressPoolRequest releases an allocation after deployment-management authorization.
func (service *Service) DeleteAddressPoolRequest(actorID int, resourceID int) (err error) {
	var resource *db.ManagedResource
	if resource, err = service.store.ManagedResources.Select(resourceID); err != nil {
		return
	}
	if resource == nil || resource.Kind != "address_pool_request" {
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
	err = service.writeAudit(actorID, "resource.addresses_released", fmt.Sprintf("resource:%d", resourceID), "succeeded", map[string]string{"name": resource.Name})
	return
}
