package domain

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/z46-dev/organesson/backend/db"
	"github.com/z46-dev/organesson/backend/proxmox"
)

const (
	guestArtifactRunsConfigurationKey string        = "guest_artifact_runs"
	guestArtifactStaleRunAfter        time.Duration = 15 * time.Minute
)

type guestArtifactRun struct {
	Entrypoint string     `json:"entrypoint"`
	SHA256     string     `json:"sha256"`
	Status     string     `json:"status"`
	ExitCode   int        `json:"exit_code"`
	StartedAt  time.Time  `json:"started_at"`
	FinishedAt *time.Time `json:"finished_at,omitempty"`
}

// GuestArtifactInput identifies the package entrypoint and expected archive digest.
type GuestArtifactInput struct {
	Entrypoint string
	SHA256     string
}

// ResolveGuestArtifact authorizes one-time root execution on an owned, Linux Proxmox VM.
func (service *Service) ResolveGuestArtifact(actorID int, resourceID int, input GuestArtifactInput) (request proxmox.GuestArtifactRequest, err error) {
	var resource *db.ManagedResource
	if resource, err = service.store.ManagedResources.Select(resourceID); err != nil {
		return
	}
	if resource == nil || resource.Kind != "virtual_machine" || resource.ExternalID == "" || resource.ExternalNode == "" || resource.OperationKey == "" {
		err = ErrNotFound
		return
	}
	if err = service.Require(actorID, db.PermissionDeploymentManage, resource.OwnershipID); err != nil {
		return
	}
	var configuration proxmox.VMCloneRequest
	if err = json.Unmarshal([]byte(resource.ConfigurationJSON), &configuration); err != nil {
		return
	}
	var template *db.VMTemplate
	if template, err = service.ProvisioningVMTemplate(configuration.TemplateAlias); err != nil {
		return
	}
	var guestOS string = strings.ToLower(template.GuestOS)
	if !isSupportedArtifactLinux(guestOS) {
		err = fmt.Errorf("%w: guest artifacts currently support prepared Linux sources only", ErrInvalidInput)
		return
	}
	request = proxmox.GuestArtifactRequest{
		Node: resource.ExternalNode, VMID: resource.ExternalID, VMOperationKey: resource.OperationKey,
		Entrypoint: input.Entrypoint, SHA256: input.SHA256,
	}
	return
}

// BeginGuestArtifactExecution reserves a digest/entrypoint pair or returns its saved success result.
func (service *Service) BeginGuestArtifactExecution(actorID int, resourceID int, digest string, entrypoint string) (result proxmox.GuestArtifactResult, alreadySucceeded bool, err error) {
	service.provisioningLock.Lock()
	defer service.provisioningLock.Unlock()
	var resource *db.ManagedResource
	if resource, err = service.store.ManagedResources.Select(resourceID); err != nil {
		return
	}
	if resource == nil || resource.Kind != "virtual_machine" {
		err = ErrNotFound
		return
	}
	if err = service.Require(actorID, db.PermissionDeploymentManage, resource.OwnershipID); err != nil {
		return
	}
	var configuration map[string]json.RawMessage
	if err = json.Unmarshal([]byte(resource.ConfigurationJSON), &configuration); err != nil {
		return
	}
	var runs map[string]guestArtifactRun = make(map[string]guestArtifactRun)
	if stored, exists := configuration[guestArtifactRunsConfigurationKey]; exists {
		if err = json.Unmarshal(stored, &runs); err != nil {
			return
		}
	}
	var operationKey string = digest + ":" + entrypoint
	var now time.Time = service.now()
	if previous, exists := runs[operationKey]; exists {
		switch previous.Status {
		case "succeeded":
			result = proxmox.GuestArtifactResult{SHA256: digest, Status: "succeeded", ExitCode: previous.ExitCode, Reused: true}
			alreadySucceeded = true
			return
		case "running":
			if now.Sub(previous.StartedAt) < guestArtifactStaleRunAfter {
				err = ErrProvisioningInProgress
				return
			}
		}
	}
	runs[operationKey] = guestArtifactRun{Entrypoint: entrypoint, SHA256: digest, Status: "running", StartedAt: now}
	var encodedRuns []byte
	if encodedRuns, err = json.Marshal(runs); err != nil {
		return
	}
	configuration[guestArtifactRunsConfigurationKey] = encodedRuns
	var encodedConfiguration []byte
	if encodedConfiguration, err = json.Marshal(configuration); err != nil {
		return
	}
	resource.ConfigurationJSON = string(encodedConfiguration)
	err = service.store.ManagedResources.Update(resource)
	return
}

// CompleteGuestArtifactExecution persists the exit result and audit event without saving artifact bytes.
func (service *Service) CompleteGuestArtifactExecution(actorID int, resourceID int, digest string, entrypoint string, executionErr error) (err error) {
	service.provisioningLock.Lock()
	defer service.provisioningLock.Unlock()
	var resource *db.ManagedResource
	if resource, err = service.store.ManagedResources.Select(resourceID); err != nil {
		return
	}
	if resource == nil || resource.Kind != "virtual_machine" {
		err = ErrNotFound
		return
	}
	if err = service.Require(actorID, db.PermissionDeploymentManage, resource.OwnershipID); err != nil {
		return
	}
	var configuration map[string]json.RawMessage
	if err = json.Unmarshal([]byte(resource.ConfigurationJSON), &configuration); err != nil {
		return
	}
	var runs map[string]guestArtifactRun = make(map[string]guestArtifactRun)
	var encodedRuns []byte
	var runsExist bool
	if encodedRuns, runsExist = configuration[guestArtifactRunsConfigurationKey]; !runsExist {
		err = fmt.Errorf("%w: guest artifact execution was not reserved", ErrInvalidInput)
		return
	}
	if err = json.Unmarshal(encodedRuns, &runs); err != nil {
		return
	}
	var operationKey string = digest + ":" + entrypoint
	var run guestArtifactRun
	var runExists bool
	if run, runExists = runs[operationKey]; !runExists || run.Status != "running" {
		err = fmt.Errorf("%w: guest artifact execution was not reserved", ErrInvalidInput)
		return
	}
	var result string = "succeeded"
	run.Status = result
	if executionErr != nil {
		run.Status = "failed"
		run.ExitCode = -1
		var guestExitError *proxmox.GuestArtifactExitError
		if errors.As(executionErr, &guestExitError) {
			run.ExitCode = guestExitError.ExitCode
		}
		result = "failed"
	}
	var finishedAt time.Time = service.now()
	run.FinishedAt = &finishedAt
	runs[operationKey] = run
	if encodedRuns, err = json.Marshal(runs); err != nil {
		return
	}
	configuration[guestArtifactRunsConfigurationKey] = encodedRuns
	var encodedConfiguration []byte
	if encodedConfiguration, err = json.Marshal(configuration); err != nil {
		return
	}
	resource.ConfigurationJSON = string(encodedConfiguration)
	if err = service.store.ManagedResources.Update(resource); err != nil {
		return
	}
	err = service.writeAudit(actorID, "resource.guest_artifact_executed", fmt.Sprintf("resource:%d", resourceID), result, map[string]string{"entrypoint": entrypoint, "sha256": digest})
	return
}

func isSupportedArtifactLinux(guestOS string) (supported bool) {
	for _, family := range []string{"fedora", "ubuntu", "debian", "rhel", "rocky", "alma", "centos"} {
		if strings.Contains(guestOS, family) {
			supported = true
			return
		}
	}
	return
}
