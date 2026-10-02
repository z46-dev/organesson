package domain

import (
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/z46-dev/golog"
	"github.com/z46-dev/organesson/backend/db"
	"github.com/z46-dev/organesson/backend/proxmox"
)

// TestVMTemplateCatalogLifecycle covers source validation, selector handling, and readiness gates.
func TestVMTemplateCatalogLifecycle(t *testing.T) {
	var store *db.Store
	var err error
	if store, err = db.Open(filepath.Join(t.TempDir(), "organesson.db"), golog.New(), false); err != nil {
		t.Fatalf("open database: %v", err)
	}
	defer store.Close()

	var administrator *db.Account
	if administrator, _, err = store.InitialAdministrator(); err != nil {
		t.Fatalf("load administrator: %v", err)
	}
	var service *Service = New(store)
	var input VMTemplateInput = VMTemplateInput{
		DisplayName:     "Fedora Workstation",
		Description:     "Fedora workstation clone source",
		SourceID:        "156",
		GuestOS:         "fedora",
		GuestOSVersion:  "44",
		Edition:         "workstation",
		Architecture:    "x86_64",
		ExecutionMethod: "qemu_guest_agent",
	}
	var record *VMTemplateRecord
	if record, err = service.CreateVMTemplate(administrator.ID, input, []string{"og-template-fedora-workstation-latest", "fedora-workstation"}); err != nil {
		t.Fatalf("create source record: %v", err)
	}
	if record.Template.ProvisioningReady || len(record.Aliases) != 2 {
		t.Fatalf("new source must be not-ready and have both aliases: %#v", record)
	}
	if _, err = service.CreateVMTemplate(administrator.ID, input, []string{"another-alias"}); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("expected duplicate Proxmox VMID to be rejected, got %v", err)
	}
	if _, err = service.SetVMTemplateReadiness(administrator.ID, record.Template.ID, true, true); err == nil {
		t.Fatal("readiness should be rejected before a passing preflight")
	}

	var checkTime time.Time = time.Now()
	var result proxmox.PreflightResult = proxmox.PreflightResult{
		SourceID:        "156",
		Node:            "pve1",
		Name:            "fedora-workstation-source",
		PowerState:      "stopped",
		AgentConfigured: true,
		IsQEMU:          true,
		Passed:          true,
		CheckedAt:       checkTime,
	}
	if record, err = service.RecordVMTemplatePreflight(administrator.ID, record.Template.ID, result, nil); err != nil {
		t.Fatalf("record preflight: %v", err)
	}
	if record.Template.ProvisioningReady {
		t.Fatal("a passing Proxmox preflight must not skip the operator readiness checks")
	}
	if _, err = service.SetVMTemplateReadiness(administrator.ID, record.Template.ID, true, true); err == nil {
		t.Fatal("a stopped source must not be marked ready")
	}
	result.PowerState = "running"
	result.GuestOSID = "fedora"
	result.AgentReachable = true
	result.GuestAgentRootVerified = true
	result.Checks = []proxmox.Check{{Name: "guest_agent_root_execution", Passed: true, Required: true}}
	if record, err = service.RecordVMTemplatePreflight(administrator.ID, record.Template.ID, result, nil); err != nil {
		t.Fatalf("record running source preflight: %v", err)
	}
	if record, err = service.SetVMTemplateReadiness(administrator.ID, record.Template.ID, true, true); err != nil {
		t.Fatalf("mark running source ready: %v", err)
	}
	if !record.Template.ProvisioningReady {
		t.Fatal("source should be ready after the preflight and both operator checks")
	}
	result.PowerState = "stopped"
	result.AgentReachable = false
	result.GuestAgentRootVerified = false
	result.Checks = nil
	if record, err = service.RecordVMTemplatePreflight(administrator.ID, record.Template.ID, result, nil); err != nil {
		t.Fatalf("record stopped source preflight: %v", err)
	}
	if record.Template.ProvisioningAccountRemoved || record.Template.GuestAgentRootVerified || record.Template.ProvisioningReady {
		t.Fatal("a new preflight must clear stale readiness confirmations")
	}

	if record, err = service.AddVMTemplateAlias(administrator.ID, record.Template.ID, "fedora-desktop"); err != nil {
		t.Fatalf("add alias: %v", err)
	}
	if record, err = service.RemoveVMTemplateAlias(administrator.ID, record.Template.ID, record.Aliases[0].ID); err != nil {
		t.Fatalf("remove one of multiple aliases: %v", err)
	}
	if record, err = service.RemoveVMTemplateAlias(administrator.ID, record.Template.ID, record.Aliases[0].ID); err != nil {
		t.Fatalf("remove second alias: %v", err)
	}
	if _, err = service.RemoveVMTemplateAlias(administrator.ID, record.Template.ID, record.Aliases[0].ID); err == nil {
		t.Fatal("removing the last alias should fail")
	}

	input.Description = "Updated source description"
	if record, err = service.UpdateVMTemplate(administrator.ID, record.Template.ID, input); err != nil {
		t.Fatalf("update source record: %v", err)
	}
	if record.Template.ProvisioningReady || record.Template.LastPreflightAt != nil {
		t.Fatal("editing source metadata must invalidate readiness and preflight")
	}
}

// TestVMTemplateCatalogRequiresPlatformAdministrator verifies catalog writes are admin-only.
func TestVMTemplateCatalogRequiresPlatformAdministrator(t *testing.T) {
	var store *db.Store
	var err error
	if store, err = db.Open(filepath.Join(t.TempDir(), "organesson.db"), golog.New(), false); err != nil {
		t.Fatalf("open database: %v", err)
	}
	defer store.Close()

	var account *db.Account = &db.Account{DisplayName: "User", CreatedAt: time.Now()}
	if err = store.Accounts.Insert(account); err != nil {
		t.Fatalf("insert non-admin account: %v", err)
	}
	if _, err = New(store).ListVMTemplates(account.ID); err != ErrForbidden {
		t.Fatalf("expected non-admin catalog access to be forbidden, got %v", err)
	}
}
