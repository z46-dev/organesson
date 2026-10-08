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
		GuestOS:         "linux",
		GuestOSName:     "Fedora Linux 44",
		GuestOSVersion:  "44",
		Edition:         "workstation",
		Architecture:    "x86_64",
		ExecutionMethod: "qemu_guest_agent",
		SystemOnly:      true,
	}
	var record *VMTemplateRecord
	if record, err = service.CreateVMTemplate(administrator.ID, input, []string{"og-template-fedora-workstation-latest", "fedora-workstation"}); err != nil {
		t.Fatalf("create source record: %v", err)
	}
	if record.Template.ProvisioningReady || len(record.Aliases) != 2 {
		t.Fatalf("new source must be not-ready and have both aliases: %#v", record)
	}
	record.Template.ProvisioningReady = true
	record.Template.GuestAgentRootVerified = true
	record.Template.ProvisioningAccountRemoved = true
	if err = service.store.VMTemplates.Update(record.Template); err != nil {
		t.Fatalf("simulate a pre-workflow ready record: %v", err)
	}
	if _, err = service.ProvisioningSystemVMTemplate("og-template-fedora-workstation-latest"); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("legacy readiness without a completed preparation must not be cloneable, got %v", err)
	}
	record.Template.ProvisioningReady = false
	record.Template.GuestAgentRootVerified = false
	record.Template.ProvisioningAccountRemoved = false
	if err = service.store.VMTemplates.Update(record.Template); err != nil {
		t.Fatalf("restore initial readiness state: %v", err)
	}
	if _, err = service.CreateVMTemplate(administrator.ID, input, []string{"another-alias"}); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("expected duplicate Proxmox VMID to be rejected, got %v", err)
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
		t.Fatal("a passing Proxmox preflight must not skip the preparation workflow")
	}
	result.PowerState = "running"
	result.GuestOSID = "fedora"
	result.AgentReachable = true
	result.GuestAgentRootVerified = true
	result.Checks = []proxmox.Check{
		{Name: "source_exists", Passed: true, Required: true},
		{Name: "qemu_guest_agent_enabled", Passed: true, Required: true},
		{Name: "guest_os_matches", Passed: true, Required: true},
		{Name: "guest_agent_root_execution", Passed: true, Required: true},
		{Name: "preparation_script", Passed: true, Required: true},
		{Name: "requested_accounts_removed", Passed: true, Required: true},
	}
	if record, err = service.RecordVMTemplatePreflight(administrator.ID, record.Template.ID, result, nil); err != nil {
		t.Fatalf("record running source preflight: %v", err)
	}
	if err = service.BeginVMTemplatePreparation(administrator.ID, record.Template.ID); err != nil {
		t.Fatalf("begin source preparation: %v", err)
	}
	result.PowerState = "stopped"
	if record, err = service.CompleteVMTemplatePreparation(administrator.ID, record.Template.ID, result, nil); err != nil {
		t.Fatalf("complete source preparation: %v", err)
	}
	if !record.Template.ProvisioningReady {
		t.Fatal("source should be ready after successful automated preparation")
	}
	if _, err = service.ProvisioningVMTemplate("og-template-fedora-workstation-latest"); !errors.Is(err, ErrForbidden) {
		t.Fatalf("deployment VM requests should not resolve a system-only source, got %v", err)
	}
	if _, err = service.ProvisioningSystemVMTemplate("og-template-fedora-workstation-latest"); err != nil {
		t.Fatalf("platform-managed provisioning should resolve a system-only source: %v", err)
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
	if err = service.BeginVMTemplatePreparation(administrator.ID, record.Template.ID); err != nil {
		t.Fatalf("begin failed source preparation: %v", err)
	}
	var failedResult *VMTemplateRecord
	if failedResult, err = service.CompleteVMTemplatePreparation(administrator.ID, record.Template.ID, result, errors.New("script failed")); err != nil {
		t.Fatalf("record failed source preparation: %v", err)
	}
	if failedResult.Template.ProvisioningReady {
		t.Fatal("failed preparation must never leave the source ready")
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
	var deletedTemplateID int = record.Template.ID
	if err = service.DeleteVMTemplate(administrator.ID, deletedTemplateID); err != nil {
		t.Fatalf("delete source catalog record: %v", err)
	}
	var deletedTemplate *db.VMTemplate
	if deletedTemplate, err = store.VMTemplates.Select(deletedTemplateID); err != nil || deletedTemplate != nil {
		t.Fatalf("deleted source catalog record should not be found: record=%#v err=%v", deletedTemplate, err)
	}
	var aliases []*db.VMTemplateAlias
	if aliases, err = store.VMTemplateAliases.SelectAll(); err != nil {
		t.Fatalf("list aliases after source catalog deletion: %v", err)
	}
	for _, alias := range aliases {
		if alias.VMTemplateID == deletedTemplateID {
			t.Fatalf("alias %q remains after deleting its source record", alias.Alias)
		}
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
	if err = New(store).DeleteVMTemplate(account.ID, 1); err != ErrForbidden {
		t.Fatalf("expected non-admin source deletion to be forbidden, got %v", err)
	}
}
