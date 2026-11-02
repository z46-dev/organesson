package domain

import (
	"fmt"

	"github.com/z46-dev/organesson/backend/db"
)

// IncompleteProvisioningResourcesForGroup lists unfinished resources directly under an authorized group.
func (service *Service) IncompleteProvisioningResourcesForGroup(actorID int, nodeID int) (reservations []*db.ManagedResource, err error) {
	var node *db.OwnershipNode
	if node, err = service.store.OwnershipNodes.Select(nodeID); err != nil {
		return
	}
	if node == nil || node.Kind != db.OwnershipNodeKindGroup && node.Kind != db.OwnershipNodeKindInternal {
		err = ErrNotFound
		return
	}
	if err = service.Require(actorID, db.PermissionDeploymentManage, nodeID); err != nil {
		return
	}
	var nodes []*db.OwnershipNode
	if nodes, err = service.store.OwnershipNodes.SelectAll(); err != nil {
		return
	}
	var childIDs map[int]struct{} = make(map[int]struct{})
	for _, child := range nodes {
		if child.ParentID != nil && *child.ParentID == nodeID {
			childIDs[child.ID] = struct{}{}
		}
	}
	var resources []*db.ManagedResource
	if resources, err = service.store.ManagedResources.SelectAll(); err != nil {
		return
	}
	for _, resource := range resources {
		if _, child := childIDs[resource.OwnershipID]; child && isIncompleteProvisioningResource(resource) {
			reservations = append(reservations, resource)
		}
	}
	return
}

// RemoveIncompleteProvisioningResourcesForGroup removes reservations only after Proxmox confirms cleanup.
func (service *Service) RemoveIncompleteProvisioningResourcesForGroup(actorID int, nodeID int, resourceIDs []int) (err error) {
	var reservations []*db.ManagedResource
	if reservations, err = service.IncompleteProvisioningResourcesForGroup(actorID, nodeID); err != nil {
		return
	}
	err = service.removeIncompleteProvisioningResources(actorID, reservations, resourceIDs)
	return
}

// IncompleteProvisioningResourcesForDeployment lists unfinished resources in an authorized deployment.
func (service *Service) IncompleteProvisioningResourcesForDeployment(actorID int, deploymentID int) (reservations []*db.ManagedResource, err error) {
	if _, err = service.authorizeDeploymentDeletion(actorID, deploymentID); err != nil {
		return
	}
	var resources []*db.ManagedResource
	if resources, err = service.store.ManagedResources.SelectAll(); err != nil {
		return
	}
	for _, resource := range resources {
		if resource.DeploymentID == deploymentID && isIncompleteProvisioningResource(resource) {
			reservations = append(reservations, resource)
		}
	}
	return
}

// RemoveIncompleteProvisioningResourcesForDeployment forgets checked reservations without deleting other resources.
func (service *Service) RemoveIncompleteProvisioningResourcesForDeployment(actorID int, deploymentID int, resourceIDs []int) (err error) {
	var reservations []*db.ManagedResource
	if reservations, err = service.IncompleteProvisioningResourcesForDeployment(actorID, deploymentID); err != nil {
		return
	}
	err = service.removeIncompleteProvisioningResources(actorID, reservations, resourceIDs)
	return
}

// removeIncompleteProvisioningResources removes records after external cleanup succeeds.
func (service *Service) removeIncompleteProvisioningResources(actorID int, reservations []*db.ManagedResource, resourceIDs []int) (err error) {
	var selectedIDs map[int]struct{} = make(map[int]struct{}, len(resourceIDs))
	for _, resourceID := range resourceIDs {
		selectedIDs[resourceID] = struct{}{}
	}
	for _, resource := range reservations {
		if _, selected := selectedIDs[resource.ID]; !selected || !isIncompleteProvisioningResource(resource) {
			continue
		}
		if err = service.store.ManagedResources.Delete(resource.ID); err != nil {
			return
		}
		if err = service.store.OwnershipNodes.Delete(resource.OwnershipID); err != nil {
			return
		}
		if err = service.writeAudit(actorID, "resource.delete", fmt.Sprintf("resource:%d", resource.ID), "succeeded", map[string]string{"kind": resource.Kind, "reason": "incomplete_provisioning_cleanup"}); err != nil {
			return
		}
	}
	return
}

func isIncompleteProvisioningResource(resource *db.ManagedResource) (incomplete bool) {
	incomplete = resource != nil && resource.PowerState == "provisioning" && resource.OperationKey != "" &&
		(resource.Kind == "virtual_machine" && resource.ExternalID == "" || resource.Kind == "virtual_network" && resource.ConfigurationJSON != "")
	return
}
