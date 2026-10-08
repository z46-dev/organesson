package domain

import (
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/z46-dev/organesson/backend/db"
	"github.com/z46-dev/organesson/backend/proxmox"
)

type (
	// VMTemplateRecord combines a source record with its independent selector aliases.
	VMTemplateRecord struct {
		Template *db.VMTemplate        `json:"template"`
		Aliases  []*db.VMTemplateAlias `json:"aliases"`
	}

	// VMTemplateInput contains the editable descriptive fields for one Proxmox source VM.
	VMTemplateInput struct {
		DisplayName     string `json:"display_name"`
		Description     string `json:"description"`
		SourceID        string `json:"source_id"`
		GuestOS         string `json:"guest_os"`
		GuestOSName     string `json:"guest_os_name"`
		GuestOSVersion  string `json:"guest_os_version"`
		Edition         string `json:"edition"`
		Architecture    string `json:"architecture"`
		ExecutionMethod string `json:"execution_method"`
		SystemOnly      bool   `json:"system_only"`
	}
)

var templateAliasPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{0,127}$`)

// ListVMTemplates returns every platform source record with its aliases.
func (service *Service) ListVMTemplates(actorID int) (records []*VMTemplateRecord, err error) {
	if err = service.requirePlatformAdministrator(actorID); err != nil {
		return
	}
	var templates []*db.VMTemplate
	if templates, err = service.store.VMTemplates.SelectAll(); err != nil {
		return
	}
	var aliases []*db.VMTemplateAlias
	if aliases, err = service.store.VMTemplateAliases.SelectAll(); err != nil {
		return
	}
	records = make([]*VMTemplateRecord, 0, len(templates))
	for _, template := range templates {
		var record *VMTemplateRecord = &VMTemplateRecord{Template: template, Aliases: make([]*db.VMTemplateAlias, 0)}
		for _, alias := range aliases {
			if alias.VMTemplateID == template.ID {
				record.Aliases = append(record.Aliases, alias)
			}
		}
		sort.Slice(record.Aliases, func(left int, right int) bool {
			return record.Aliases[left].Alias < record.Aliases[right].Alias
		})
		records = append(records, record)
	}
	sort.Slice(records, func(left int, right int) bool {
		return records[left].Template.DisplayName < records[right].Template.DisplayName
	})
	return
}

// CreateVMTemplate registers an ordinary Proxmox QEMU source with its first aliases.
func (service *Service) CreateVMTemplate(actorID int, input VMTemplateInput, aliases []string) (record *VMTemplateRecord, err error) {
	if err = service.requirePlatformAdministrator(actorID); err != nil {
		return
	}
	if err = validateVMTemplateInput(input); err != nil {
		return
	}
	if aliases, err = normalizeTemplateAliases(aliases); err != nil {
		return
	}
	if len(aliases) == 0 {
		err = fmt.Errorf("%w: at least one template alias is required", ErrInvalidInput)
		return
	}
	if err = service.ensureVMTemplateSelectorsAvailable(strings.TrimSpace(input.SourceID), aliases, 0); err != nil {
		return
	}
	var now time.Time = service.now()
	var template *db.VMTemplate = &db.VMTemplate{
		DisplayName:       strings.TrimSpace(input.DisplayName),
		Description:       strings.TrimSpace(input.Description),
		SourcePlatform:    "proxmox",
		SourceID:          strings.TrimSpace(input.SourceID),
		GuestOS:           strings.ToLower(strings.TrimSpace(input.GuestOS)),
		GuestOSName:       strings.TrimSpace(input.GuestOSName),
		GuestOSVersion:    strings.TrimSpace(input.GuestOSVersion),
		Edition:           strings.TrimSpace(input.Edition),
		Architecture:      strings.TrimSpace(input.Architecture),
		ExecutionMethod:   strings.TrimSpace(input.ExecutionMethod),
		SystemOnly:        input.SystemOnly,
		ProvisioningReady: false,
		LastPreflightJSON: "{}",
		CreatedAt:         now,
		UpdatedAt:         now,
	}
	if err = service.store.VMTemplates.Insert(template); err != nil {
		return
	}
	record = &VMTemplateRecord{Template: template, Aliases: make([]*db.VMTemplateAlias, 0, len(aliases))}
	for _, alias := range aliases {
		var templateAlias *db.VMTemplateAlias = &db.VMTemplateAlias{VMTemplateID: template.ID, Alias: alias, CreatedAt: now}
		if err = service.store.VMTemplateAliases.Insert(templateAlias); err != nil {
			_ = service.store.VMTemplates.Delete(template.ID)
			return nil, err
		}
		record.Aliases = append(record.Aliases, templateAlias)
	}
	if err = service.writeAudit(actorID, "vm_template.create", fmt.Sprintf("vm-template:%d", template.ID), "succeeded", map[string]string{"source_id": template.SourceID}); err != nil {
		return
	}
	return
}

// UpdateVMTemplate changes descriptive source metadata and invalidates readiness checks.
func (service *Service) UpdateVMTemplate(actorID int, templateID int, input VMTemplateInput) (record *VMTemplateRecord, err error) {
	if err = service.requirePlatformAdministrator(actorID); err != nil {
		return
	}
	if err = validateVMTemplateInput(input); err != nil {
		return
	}
	var template *db.VMTemplate
	if template, err = service.store.VMTemplates.Select(templateID); err != nil {
		return
	}
	if template == nil {
		err = ErrNotFound
		return
	}
	if err = service.ensureVMTemplateSelectorsAvailable(strings.TrimSpace(input.SourceID), nil, templateID); err != nil {
		return
	}
	template.DisplayName = strings.TrimSpace(input.DisplayName)
	template.Description = strings.TrimSpace(input.Description)
	template.SourceID = strings.TrimSpace(input.SourceID)
	template.GuestOS = strings.ToLower(strings.TrimSpace(input.GuestOS))
	template.GuestOSName = strings.TrimSpace(input.GuestOSName)
	template.GuestOSVersion = strings.TrimSpace(input.GuestOSVersion)
	template.Edition = strings.TrimSpace(input.Edition)
	template.Architecture = strings.TrimSpace(input.Architecture)
	template.ExecutionMethod = strings.TrimSpace(input.ExecutionMethod)
	template.SystemOnly = input.SystemOnly
	template.PreparationValidated = false
	template.ProvisioningReady = false
	template.GuestAgentRootVerified = false
	template.ProvisioningAccountRemoved = false
	template.LastPreflightAt = nil
	template.LastPreflightJSON = "{}"
	template.UpdatedAt = service.now()
	if err = service.store.VMTemplates.Update(template); err != nil {
		return
	}
	if record, err = service.vmTemplateRecord(template); err != nil {
		return
	}
	err = service.writeAudit(actorID, "vm_template.update", fmt.Sprintf("vm-template:%d", template.ID), "succeeded", map[string]string{"source_id": template.SourceID})
	return
}

// DeleteVMTemplate removes a source catalog entry and its aliases without touching Proxmox.
func (service *Service) DeleteVMTemplate(actorID int, templateID int) (err error) {
	if err = service.requirePlatformAdministrator(actorID); err != nil {
		return
	}
	var template *db.VMTemplate
	if template, err = service.store.VMTemplates.Select(templateID); err != nil {
		return
	}
	if template == nil {
		err = ErrNotFound
		return
	}
	var aliases []*db.VMTemplateAlias
	if aliases, err = service.store.VMTemplateAliases.SelectAll(); err != nil {
		return
	}
	for _, alias := range aliases {
		if alias.VMTemplateID == templateID {
			if err = service.store.VMTemplateAliases.Delete(alias.ID); err != nil {
				return
			}
		}
	}
	if err = service.store.VMTemplates.Delete(templateID); err != nil {
		return
	}
	err = service.writeAudit(actorID, "vm_template.delete", fmt.Sprintf("vm-template:%d", templateID), "succeeded", map[string]string{"source_id": template.SourceID})
	return
}

// AddVMTemplateAlias adds a globally unique selector to a source record.
func (service *Service) AddVMTemplateAlias(actorID int, templateID int, value string) (record *VMTemplateRecord, err error) {
	if err = service.requirePlatformAdministrator(actorID); err != nil {
		return
	}
	var template *db.VMTemplate
	if template, err = service.store.VMTemplates.Select(templateID); err != nil {
		return
	}
	if template == nil {
		err = ErrNotFound
		return
	}
	var aliases []string
	if aliases, err = normalizeTemplateAliases([]string{value}); err != nil {
		return
	}
	if err = service.ensureVMTemplateSelectorsAvailable("", aliases, templateID); err != nil {
		return
	}
	var createdAlias *db.VMTemplateAlias = &db.VMTemplateAlias{VMTemplateID: templateID, Alias: aliases[0], CreatedAt: service.now()}
	if err = service.store.VMTemplateAliases.Insert(createdAlias); err != nil {
		return
	}
	if record, err = service.vmTemplateRecord(template); err != nil {
		return
	}
	err = service.writeAudit(actorID, "vm_template.alias_add", fmt.Sprintf("vm-template:%d", template.ID), "succeeded", map[string]string{"alias": createdAlias.Alias})
	return
}

// RemoveVMTemplateAlias removes a selector without allowing an unaddressable source record.
func (service *Service) RemoveVMTemplateAlias(actorID int, templateID int, aliasID int) (record *VMTemplateRecord, err error) {
	if err = service.requirePlatformAdministrator(actorID); err != nil {
		return
	}
	var template *db.VMTemplate
	if template, err = service.store.VMTemplates.Select(templateID); err != nil {
		return
	}
	if template == nil {
		err = ErrNotFound
		return
	}
	var aliases []*db.VMTemplateAlias
	if aliases, err = service.store.VMTemplateAliases.SelectAll(); err != nil {
		return
	}
	var target *db.VMTemplateAlias
	var count int
	for _, alias := range aliases {
		if alias.VMTemplateID == templateID {
			count++
			if alias.ID == aliasID {
				target = alias
			}
		}
	}
	if target == nil {
		err = ErrNotFound
		return
	}
	if count < 2 {
		err = fmt.Errorf("%w: a template must retain at least one alias", ErrInvalidInput)
		return
	}
	if err = service.store.VMTemplateAliases.Delete(aliasID); err != nil {
		return
	}
	if record, err = service.vmTemplateRecord(template); err != nil {
		return
	}
	err = service.writeAudit(actorID, "vm_template.alias_remove", fmt.Sprintf("vm-template:%d", template.ID), "succeeded", map[string]string{"alias": target.Alias})
	return
}

// RecordVMTemplatePreflight saves read-only Proxmox checks and revokes stale readiness.
func (service *Service) RecordVMTemplatePreflight(actorID int, templateID int, result proxmox.PreflightResult, preflightErr error) (record *VMTemplateRecord, err error) {
	if err = service.requirePlatformAdministrator(actorID); err != nil {
		return
	}
	var template *db.VMTemplate
	if template, err = service.store.VMTemplates.Select(templateID); err != nil {
		return
	}
	if template == nil {
		err = ErrNotFound
		return
	}
	if preflightErr != nil {
		result.Passed = false
	}
	if result.CheckedAt.IsZero() {
		result.CheckedAt = service.now().UTC()
	}
	var encoded []byte
	if encoded, err = json.Marshal(result); err != nil {
		return
	}
	template.LastPreflightJSON = string(encoded)
	var now time.Time = service.now()
	template.LastPreflightAt = &now
	template.PreparationValidated = false
	template.ProvisioningReady = false
	template.GuestAgentRootVerified = result.GuestAgentRootVerified
	template.ProvisioningAccountRemoved = false
	if strings.TrimSpace(result.GuestOSName) != "" {
		template.GuestOSName = strings.TrimSpace(result.GuestOSName)
	}
	if strings.TrimSpace(result.GuestOSVersion) != "" {
		template.GuestOSVersion = strings.TrimSpace(result.GuestOSVersion)
	}
	if result.GuestArchitecture != "" {
		template.Architecture = result.GuestArchitecture
	}
	template.UpdatedAt = now
	if err = service.store.VMTemplates.Update(template); err != nil {
		return
	}
	if record, err = service.vmTemplateRecord(template); err != nil {
		return
	}
	var auditResult string = "failed"
	if result.Passed && preflightErr == nil {
		auditResult = "succeeded"
	}
	err = service.writeAudit(actorID, "vm_template.preflight", fmt.Sprintf("vm-template:%d", template.ID), auditResult, result)
	return
}

// BeginVMTemplatePreparation revokes prior readiness before a maintenance attempt changes the source VM.
func (service *Service) BeginVMTemplatePreparation(actorID int, templateID int) (err error) {
	if err = service.requirePlatformAdministrator(actorID); err != nil {
		return
	}
	var template *db.VMTemplate
	if template, err = service.store.VMTemplates.Select(templateID); err != nil {
		return
	}
	if template == nil {
		err = ErrNotFound
		return
	}
	template.PreparationValidated = false
	template.ProvisioningReady = false
	template.GuestAgentRootVerified = false
	template.ProvisioningAccountRemoved = false
	template.UpdatedAt = service.now()
	if err = service.store.VMTemplates.Update(template); err != nil {
		return
	}
	err = service.writeAudit(actorID, "vm_template.prepare", fmt.Sprintf("vm-template:%d", template.ID), "started", map[string]string{"source_id": template.SourceID})
	return
}

// CompleteVMTemplatePreparation records the operation result and grants readiness only after a complete successful run.
func (service *Service) CompleteVMTemplatePreparation(actorID int, templateID int, result proxmox.PreflightResult, preparationErr error) (record *VMTemplateRecord, err error) {
	if err = service.requirePlatformAdministrator(actorID); err != nil {
		return
	}
	var template *db.VMTemplate
	if template, err = service.store.VMTemplates.Select(templateID); err != nil {
		return
	}
	if template == nil {
		err = ErrNotFound
		return
	}
	if preparationErr != nil {
		result.Passed = false
	}
	if result.CheckedAt.IsZero() {
		result.CheckedAt = service.now().UTC()
	}
	var encoded []byte
	if encoded, err = json.Marshal(result); err != nil {
		return
	}
	var successfulChecks map[string]bool = make(map[string]bool, len(result.Checks))
	for _, check := range result.Checks {
		if check.Required {
			successfulChecks[check.Name] = check.Passed
		}
	}
	var succeeded bool = preparationErr == nil && result.Passed && result.IsQEMU && !result.IsProxmoxTemplate &&
		result.AgentConfigured && result.AgentReachable && result.GuestAgentRootVerified && result.PowerState == "stopped" &&
		strings.TrimSpace(result.GuestOSID) != "" && successfulChecks["source_exists"] && successfulChecks["qemu_guest_agent_enabled"] &&
		successfulChecks["guest_os_matches"] && successfulChecks["guest_agent_root_execution"] &&
		successfulChecks["preparation_script"] && successfulChecks["requested_accounts_removed"]
	var now time.Time = service.now()
	template.LastPreflightAt = &now
	template.LastPreflightJSON = string(encoded)
	template.GuestAgentRootVerified = succeeded && result.GuestAgentRootVerified
	template.ProvisioningAccountRemoved = succeeded
	template.PreparationValidated = succeeded
	template.ProvisioningReady = succeeded
	if succeeded {
		if strings.TrimSpace(result.GuestOSName) != "" {
			template.GuestOSName = strings.TrimSpace(result.GuestOSName)
		}
		if strings.TrimSpace(result.GuestOSVersion) != "" {
			template.GuestOSVersion = strings.TrimSpace(result.GuestOSVersion)
		}
		if result.GuestArchitecture != "" {
			template.Architecture = result.GuestArchitecture
		}
	}
	template.UpdatedAt = now
	if err = service.store.VMTemplates.Update(template); err != nil {
		return
	}
	if record, err = service.vmTemplateRecord(template); err != nil {
		return
	}
	var auditResult string = "failed"
	if succeeded {
		auditResult = "succeeded"
	}
	err = service.writeAudit(actorID, "vm_template.prepare", fmt.Sprintf("vm-template:%d", template.ID), auditResult, result)
	return
}

func (service *Service) vmTemplateRecord(template *db.VMTemplate) (record *VMTemplateRecord, err error) {
	var aliases []*db.VMTemplateAlias
	if aliases, err = service.store.VMTemplateAliases.SelectAll(); err != nil {
		return
	}
	record = &VMTemplateRecord{Template: template, Aliases: make([]*db.VMTemplateAlias, 0)}
	for _, alias := range aliases {
		if alias.VMTemplateID == template.ID {
			record.Aliases = append(record.Aliases, alias)
		}
	}
	sort.Slice(record.Aliases, func(left int, right int) bool {
		return record.Aliases[left].Alias < record.Aliases[right].Alias
	})
	return
}

func (service *Service) requirePlatformAdministrator(actorID int) (err error) {
	var actor *db.Account
	if actor, err = service.store.Accounts.Select(actorID); err != nil {
		return
	}
	if actor == nil || actor.Disabled || !actor.PlatformAdministrator {
		err = ErrForbidden
	}
	return
}

func (service *Service) ensureVMTemplateSelectorsAvailable(sourceID string, aliases []string, exceptTemplateID int) (err error) {
	var templates []*db.VMTemplate
	if templates, err = service.store.VMTemplates.SelectAll(); err != nil {
		return
	}
	for _, template := range templates {
		if template.ID != exceptTemplateID && sourceID != "" && template.SourceID == sourceID {
			err = fmt.Errorf("%w: Proxmox VMID %s is already registered", ErrInvalidInput, sourceID)
			return
		}
	}
	var existingAliases []*db.VMTemplateAlias
	if existingAliases, err = service.store.VMTemplateAliases.SelectAll(); err != nil {
		return
	}
	var existingByName map[string]bool = make(map[string]bool, len(existingAliases))
	for _, alias := range existingAliases {
		existingByName[alias.Alias] = true
	}
	for _, alias := range aliases {
		if existingByName[alias] {
			err = fmt.Errorf("%w: template alias %q is already in use", ErrInvalidInput, alias)
			return
		}
	}
	return
}

func validateVMTemplateInput(input VMTemplateInput) (err error) {
	var vmID int
	if vmID, err = strconv.Atoi(strings.TrimSpace(input.SourceID)); err != nil || vmID < 1 {
		err = fmt.Errorf("%w: source ID must be a positive Proxmox VMID", ErrInvalidInput)
		return
	}
	if strings.TrimSpace(input.DisplayName) == "" || len(input.DisplayName) > 128 || len(input.Description) > 2048 {
		err = fmt.Errorf("%w: template name or description is invalid", ErrInvalidInput)
		return
	}
	var supportedOS map[string]bool = map[string]bool{
		"almalinux": true, "alpine": true, "amzn": true, "arch": true, "centos": true, "debian": true, "endeavouros": true,
		"fedora": true, "freebsd": true, "bsd": true, "linux": true, "linuxmint": true, "manjaro": true, "ol": true, "opensuse": true,
		"opensuse-leap": true, "opensuse-tumbleweed": true, "oracle": true, "pop": true, "rhel": true, "rocky": true,
		"sled": true, "sles": true, "ubuntu": true, "windows": true,
	}
	if !supportedOS[strings.ToLower(strings.TrimSpace(input.GuestOS))] {
		err = fmt.Errorf("%w: guest OS must be a supported Linux distribution or the linux, windows, or bsd family", ErrInvalidInput)
		return
	}
	if len(input.GuestOSName) > 128 || strings.TrimSpace(input.GuestOSVersion) == "" || len(input.GuestOSVersion) > 64 || len(input.Edition) > 64 {
		err = fmt.Errorf("%w: detected guest version or name is invalid", ErrInvalidInput)
		return
	}
	if input.Architecture != "x86_64" && input.Architecture != "aarch64" {
		err = fmt.Errorf("%w: architecture must be x86_64 or aarch64", ErrInvalidInput)
		return
	}
	if input.ExecutionMethod != "qemu_guest_agent" {
		err = fmt.Errorf("%w: execution method must be qemu_guest_agent for a ready source", ErrInvalidInput)
	}
	return
}

func normalizeTemplateAliases(values []string) (aliases []string, err error) {
	var seen map[string]bool = make(map[string]bool, len(values))
	for _, value := range values {
		var alias string = strings.ToLower(strings.TrimSpace(value))
		if !templateAliasPattern.MatchString(alias) || seen[alias] {
			err = fmt.Errorf("%w: aliases must be unique lowercase names using letters, numbers, dots, underscores, and hyphens", ErrInvalidInput)
			return
		}
		seen[alias] = true
		aliases = append(aliases, alias)
	}
	return
}
