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

// ProvisioningVMTemplate resolves a stable alias and rejects sources not explicitly marked ready.
func (service *Service) ProvisioningVMTemplate(selector string) (template *db.VMTemplate, err error) {
	selector = strings.TrimSpace(selector)
	if selector == "" {
		err = fmt.Errorf("%w: a VM template alias is required", ErrInvalidInput)
		return
	}
	var aliases []*db.VMTemplateAlias
	if aliases, err = service.store.VMTemplateAliases.SelectAll(); err != nil {
		return
	}
	var templateID int
	for _, alias := range aliases {
		if alias.Alias == selector {
			templateID = alias.VMTemplateID
			break
		}
	}
	if templateID == 0 {
		err = ErrNotFound
		return
	}
	if template, err = service.store.VMTemplates.Select(templateID); err != nil {
		return
	}
	if template == nil {
		err = ErrNotFound
		return
	}
	if !template.ProvisioningReady || !template.GuestAgentRootVerified || !template.ProvisioningAccountRemoved {
		err = fmt.Errorf("%w: VM template %q has not passed readiness checks", ErrInvalidInput, selector)
		template = nil
	}
	return
}

// ReserveProxmoxVirtualMachine reserves quota and ownership before any external clone is requested.
func (service *Service) ReserveProxmoxVirtualMachine(actorID int, deploymentID int, parentNodeID int, request proxmox.VMCloneRequest) (resource *db.ManagedResource, template *db.VMTemplate, err error) {
	service.provisioningLock.Lock()
	defer service.provisioningLock.Unlock()

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
	request.Name = strings.TrimSpace(request.Name)
	request.Pool = strings.TrimSpace(request.Pool)
	request.Storage = strings.TrimSpace(request.Storage)
	request.TemplateAlias = strings.TrimSpace(request.TemplateAlias)
	if request.Name == "" || len(request.Name) > 128 || request.Cores < 1 || request.MemoryMiB < 1 || request.BootDiskGiB < 1 {
		err = fmt.Errorf("%w: VM name and positive CPU, memory, and boot-disk values are required", ErrInvalidInput)
		return
	}
	if len(request.Name) > 60 {
		err = fmt.Errorf("%w: Proxmox VM names cannot exceed 60 characters", ErrInvalidInput)
		return
	}
	if template, err = service.ProvisioningVMTemplate(request.TemplateAlias); err != nil {
		return
	}
	if request.Pool == "" || request.Storage == "" {
		err = fmt.Errorf("%w: an authorized Proxmox pool and storage are required", ErrInvalidInput)
		return
	}
	var keyMaterial string = fmt.Sprintf("%d:%d:%s", deploymentID, parentNodeID, request.Name)
	var operationDigest [sha256.Size]byte = sha256.Sum256([]byte(keyMaterial))
	request.OperationKey = "og-" + hex.EncodeToString(operationDigest[:16])
	request.SourceVMID = template.SourceID
	var encoded []byte
	if encoded, err = json.Marshal(request); err != nil {
		return
	}
	var resources []*db.ManagedResource
	if resources, err = service.store.ManagedResources.SelectAll(); err != nil {
		return
	}
	for _, current := range resources {
		if current.OperationKey == request.OperationKey {
			if current.ConfigurationJSON != string(encoded) {
				err = fmt.Errorf("%w: VM %q already exists with a different configuration", ErrInvalidInput, request.Name)
				return
			}
			resource = current
			return
		}
	}
	var policyRecord *db.ProxmoxResourcePolicy
	if policyRecord, err = service.store.ProxmoxResourcePolicies.Select(1); err != nil {
		return
	}
	if policyRecord == nil || policyRecord.ValidatedAt == nil {
		err = fmt.Errorf("%w: Proxmox resource policy must be validated before VM provisioning", ErrInvalidInput)
		return
	}
	var policy proxmox.ResourcePolicy
	if err = json.Unmarshal([]byte(policyRecord.ConfigurationJSON), &policy); err != nil {
		return
	}
	var configHash string
	if configHash, err = proxmox.ResourcePolicyHash(policy); err != nil {
		return
	}
	if configHash != policyRecord.ValidatedConfigHash {
		err = fmt.Errorf("%w: Proxmox resource policy changed after validation", ErrInvalidInput)
		return
	}
	var validation proxmox.ResourcePolicyValidation
	if err = json.Unmarshal([]byte(policyRecord.ValidationJSON), &validation); err != nil || !validation.Valid {
		err = fmt.Errorf("%w: Proxmox resource policy is not valid", ErrInvalidInput)
		return
	}
	if !containsString(policy.ResourcePools, request.Pool) || !containsString(policy.Storages, request.Storage) {
		err = fmt.Errorf("%w: requested Proxmox pool or storage is not authorized by platform policy", ErrInvalidInput)
		return
	}
	var usedCores int
	var usedMemoryMiB int64
	var usedStorageGiB int64
	for _, current := range resources {
		if current.Kind != "virtual_machine" || current.ConfigurationJSON == "" {
			continue
		}
		var allocated proxmox.VMCloneRequest
		if err = json.Unmarshal([]byte(current.ConfigurationJSON), &allocated); err != nil {
			return
		}
		usedCores += allocated.Cores
		usedMemoryMiB += int64(allocated.MemoryMiB)
		usedStorageGiB += int64(allocated.BootDiskGiB)
	}
	if (policy.Limits.VirtualCPUs > 0 && usedCores+request.Cores > policy.Limits.VirtualCPUs) ||
		(policy.Limits.MemoryMiB > 0 && usedMemoryMiB+int64(request.MemoryMiB) > policy.Limits.MemoryMiB) ||
		(policy.Limits.StorageGiB > 0 && usedStorageGiB+int64(request.BootDiskGiB) > policy.Limits.StorageGiB) {
		err = fmt.Errorf("%w: VM request exceeds the configured platform capacity limit", ErrInvalidInput)
		return
	}
	if err = service.createProxmoxVMRecord(actorID, deploymentID, parentNodeID, request.Name, request.OperationKey, string(encoded), &resource); err != nil {
		return
	}
	return
}

func (service *Service) createProxmoxVMRecord(actorID int, deploymentID int, parentNodeID int, name string, operationKey string, configurationJSON string, resource **db.ManagedResource) (err error) {
	var node *db.OwnershipNode = &db.OwnershipNode{
		DeploymentID: deploymentID,
		ParentID:     &parentNodeID,
		Kind:         db.OwnershipNodeKindResource,
		Name:         name,
		CreatedAt:    service.now(),
	}
	if err = service.store.OwnershipNodes.Insert(node); err != nil {
		return
	}
	*resource = &db.ManagedResource{
		DeploymentID:      deploymentID,
		OwnershipID:       node.ID,
		Kind:              "virtual_machine",
		Name:              name,
		PowerState:        "provisioning",
		OperationKey:      operationKey,
		ConfigurationJSON: configurationJSON,
		CreatedAt:         service.now(),
	}
	if err = service.store.ManagedResources.Insert(*resource); err != nil {
		_ = service.store.OwnershipNodes.Delete(node.ID)
		return
	}
	err = service.writeAudit(actorID, "resource.provisioning_started", fmt.Sprintf("resource:%d", (*resource).ID), "succeeded", map[string]string{"name": name})
	return
}

// RecordProxmoxVMPlacement stores the VMID only after the PVE clone and configuration tasks succeed.
func (service *Service) RecordProxmoxVMPlacement(actorID int, resourceID int, placement proxmox.VMPlacement) (resource *db.ManagedResource, err error) {
	if resource, err = service.store.ManagedResources.Select(resourceID); err != nil {
		return
	}
	if resource == nil || resource.Kind != "virtual_machine" || resource.OperationKey == "" || placement.VMID == "" || placement.Node == "" {
		err = ErrNotFound
		return
	}
	resource.ExternalID = placement.VMID
	resource.ExternalNode = placement.Node
	resource.PowerState = placement.PowerState
	if resource.PowerState == "" {
		resource.PowerState = "stopped"
	}
	if err = service.store.ManagedResources.Update(resource); err != nil {
		return
	}
	err = service.writeAudit(actorID, "resource.provisioned", fmt.Sprintf("resource:%d", resource.ID), "succeeded", map[string]string{"vmid": placement.VMID, "node": placement.Node})
	return
}

// RecordLivePowerState mirrors a Proxmox power state after an authorized API operation.
func (service *Service) RecordLivePowerState(actorID int, resourceID int, state string) (resource *db.ManagedResource, err error) {
	if resource, err = service.store.ManagedResources.Select(resourceID); err != nil {
		return
	}
	if resource == nil || resource.Kind != "virtual_machine" || resource.ExternalID == "" {
		err = ErrNotFound
		return
	}
	if resource.PowerState == state {
		return
	}
	resource.PowerState = state
	if err = service.store.ManagedResources.Update(resource); err != nil {
		return
	}
	err = service.writeAudit(actorID, "vm.power.state_synchronized", fmt.Sprintf("resource:%d", resourceID), "succeeded", map[string]string{"state": state})
	return
}

// AuthorizeVirtualMachinePower checks a user's power permission without changing local or PVE state.
func (service *Service) AuthorizeVirtualMachinePower(actorID int, resourceID int, action string) (resource *db.ManagedResource, err error) {
	if resource, err = service.store.ManagedResources.Select(resourceID); err != nil {
		return
	}
	if resource == nil || resource.Kind != "virtual_machine" {
		err = ErrNotFound
		return
	}
	if err = service.Require(actorID, db.PermissionResourcePower, resource.OwnershipID); err != nil {
		return
	}
	if action != "start" && action != "stop" && action != "restart" {
		err = fmt.Errorf("%w: unsupported VM power action", ErrInvalidInput)
	}
	return
}

func containsString(values []string, target string) (found bool) {
	for _, value := range values {
		if value == target {
			found = true
			return
		}
	}
	return
}
