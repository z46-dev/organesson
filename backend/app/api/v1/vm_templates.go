package v1

import (
	"github.com/gofiber/fiber/v3"
	"github.com/z46-dev/organesson/backend/app/api/common"
	"github.com/z46-dev/organesson/backend/db"
	"github.com/z46-dev/organesson/backend/domain"
	"github.com/z46-dev/organesson/backend/proxmox"
)

type (
	createVMTemplateRequest struct {
		domain.VMTemplateInput
		Aliases []string `json:"aliases"`
	}

	addVMTemplateAliasRequest struct {
		Alias string `json:"alias"`
	}

	setVMTemplateReadinessRequest struct {
		GuestAgentRootVerified     bool `json:"guest_agent_root_verified"`
		ProvisioningAccountRemoved bool `json:"provisioning_account_removed"`
	}
)

// initVMTemplates registers platform-admin source catalog and read-only preflight routes.
func initVMTemplates(parent fiber.Router, services common.Services) {
	var admin fiber.Router = parent.Group("/admin", common.RequirePlatformAdministrator(services.Authentication))
	admin.Get("/vm-templates", listVMTemplates(services))
	admin.Post("/vm-templates", createVMTemplate(services))
	admin.Put("/vm-templates/:template_id", updateVMTemplate(services))
	admin.Post("/vm-templates/:template_id/aliases", addVMTemplateAlias(services))
	admin.Delete("/vm-templates/:template_id/aliases/:alias_id", removeVMTemplateAlias(services))
	admin.Post("/vm-templates/:template_id/preflight", preflightVMTemplate(services))
	admin.Put("/vm-templates/:template_id/readiness", setVMTemplateReadiness(services))
	admin.Get("/proxmox/status", proxmoxConfigurationStatus(services))
}

// listVMTemplates shows source metadata and aliases to platform administrators.
func listVMTemplates(services common.Services) (handler fiber.Handler) {
	handler = func(ctx fiber.Ctx) (err error) {
		var actorID int
		if actorID, _ = common.AccountID(ctx); actorID < 1 {
			return ctx.SendStatus(fiber.StatusUnauthorized)
		}
		var records []*domain.VMTemplateRecord
		if records, err = services.Domain.ListVMTemplates(actorID); err != nil {
			return common.DomainError(ctx, err)
		}
		err = ctx.JSON(fiber.Map{"templates": records})
		return
	}
	return
}

// createVMTemplate records one source VM and its initial aliases.
func createVMTemplate(services common.Services) (handler fiber.Handler) {
	handler = func(ctx fiber.Ctx) (err error) {
		var actorID int
		if actorID, _ = common.AccountID(ctx); actorID < 1 {
			return ctx.SendStatus(fiber.StatusUnauthorized)
		}
		var request createVMTemplateRequest
		if err = ctx.Bind().Body(&request); err != nil {
			return ctx.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "Invalid VM template request."})
		}
		var record *domain.VMTemplateRecord
		if record, err = services.Domain.CreateVMTemplate(actorID, request.VMTemplateInput, request.Aliases); err != nil {
			return common.DomainError(ctx, err)
		}
		err = ctx.Status(fiber.StatusCreated).JSON(fiber.Map{"template": record})
		return
	}
	return
}

// updateVMTemplate changes source metadata and invalidates prior readiness checks.
func updateVMTemplate(services common.Services) (handler fiber.Handler) {
	handler = func(ctx fiber.Ctx) (err error) {
		var actorID int
		if actorID, _ = common.AccountID(ctx); actorID < 1 {
			return ctx.SendStatus(fiber.StatusUnauthorized)
		}
		var templateID int
		if templateID, err = common.ParseID(ctx, "template_id"); err != nil {
			return ctx.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "Invalid VM template identifier."})
		}
		var request domain.VMTemplateInput
		if err = ctx.Bind().Body(&request); err != nil {
			return ctx.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "Invalid VM template request."})
		}
		var record *domain.VMTemplateRecord
		if record, err = services.Domain.UpdateVMTemplate(actorID, templateID, request); err != nil {
			return common.DomainError(ctx, err)
		}
		err = ctx.JSON(fiber.Map{"template": record})
		return
	}
	return
}

// addVMTemplateAlias adds a selector to one catalog record.
func addVMTemplateAlias(services common.Services) (handler fiber.Handler) {
	handler = func(ctx fiber.Ctx) (err error) {
		var actorID int
		if actorID, _ = common.AccountID(ctx); actorID < 1 {
			return ctx.SendStatus(fiber.StatusUnauthorized)
		}
		var templateID int
		if templateID, err = common.ParseID(ctx, "template_id"); err != nil {
			return ctx.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "Invalid VM template identifier."})
		}
		var request addVMTemplateAliasRequest
		if err = ctx.Bind().Body(&request); err != nil {
			return ctx.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "Invalid alias request."})
		}
		var record *domain.VMTemplateRecord
		if record, err = services.Domain.AddVMTemplateAlias(actorID, templateID, request.Alias); err != nil {
			return common.DomainError(ctx, err)
		}
		err = ctx.Status(fiber.StatusCreated).JSON(fiber.Map{"template": record})
		return
	}
	return
}

// removeVMTemplateAlias removes an alias while preserving one stable selector.
func removeVMTemplateAlias(services common.Services) (handler fiber.Handler) {
	handler = func(ctx fiber.Ctx) (err error) {
		var actorID int
		if actorID, _ = common.AccountID(ctx); actorID < 1 {
			return ctx.SendStatus(fiber.StatusUnauthorized)
		}
		var templateID, aliasID int
		if templateID, err = common.ParseID(ctx, "template_id"); err != nil {
			return ctx.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "Invalid VM template identifier."})
		}
		if aliasID, err = common.ParseID(ctx, "alias_id"); err != nil {
			return ctx.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "Invalid alias identifier."})
		}
		var record *domain.VMTemplateRecord
		if record, err = services.Domain.RemoveVMTemplateAlias(actorID, templateID, aliasID); err != nil {
			return common.DomainError(ctx, err)
		}
		err = ctx.JSON(fiber.Map{"template": record})
		return
	}
	return
}

// preflightVMTemplate reads source status and configuration from the configured Proxmox cluster.
func preflightVMTemplate(services common.Services) (handler fiber.Handler) {
	handler = func(ctx fiber.Ctx) (err error) {
		var actorID int
		if actorID, _ = common.AccountID(ctx); actorID < 1 {
			return ctx.SendStatus(fiber.StatusUnauthorized)
		}
		var templateID int
		if templateID, err = common.ParseID(ctx, "template_id"); err != nil {
			return ctx.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "Invalid VM template identifier."})
		}
		var sourceID string
		var expectedOS string
		var selected *db.VMTemplate
		if selected, err = services.Store.VMTemplates.Select(templateID); err != nil {
			return ctx.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "Could not load VM template."})
		}
		if selected == nil {
			return ctx.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "VM template not found."})
		}
		sourceID = selected.SourceID
		expectedOS = selected.GuestOS
		if services.Proxmox == nil || !services.Proxmox.Configured() {
			return ctx.Status(fiber.StatusServiceUnavailable).JSON(fiber.Map{"error": "Proxmox connection is not configured on the backend."})
		}
		var result proxmox.PreflightResult
		var preflightErr error
		if result, preflightErr = services.Proxmox.InspectTemplate(ctx, sourceID, expectedOS); preflightErr != nil {
			result.SourceID = sourceID
			result.Passed = false
			result.Checks = []proxmox.Check{{
				Name:     "proxmox_api_access",
				Passed:   false,
				Required: true,
				Details:  "The Proxmox read-only check could not be completed. Verify the endpoint, token permissions, and VMID.",
			}}
		}
		var record *domain.VMTemplateRecord
		if record, err = services.Domain.RecordVMTemplatePreflight(actorID, templateID, result, preflightErr); err != nil {
			return common.DomainError(ctx, err)
		}
		err = ctx.JSON(fiber.Map{"template": record, "preflight": result})
		return
	}
	return
}

// setVMTemplateReadiness records explicit guest-agent and source-account checks.
func setVMTemplateReadiness(services common.Services) (handler fiber.Handler) {
	handler = func(ctx fiber.Ctx) (err error) {
		var actorID int
		if actorID, _ = common.AccountID(ctx); actorID < 1 {
			return ctx.SendStatus(fiber.StatusUnauthorized)
		}
		var templateID int
		if templateID, err = common.ParseID(ctx, "template_id"); err != nil {
			return ctx.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "Invalid VM template identifier."})
		}
		var request setVMTemplateReadinessRequest
		if err = ctx.Bind().Body(&request); err != nil {
			return ctx.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "Invalid readiness request."})
		}
		var record *domain.VMTemplateRecord
		if record, err = services.Domain.SetVMTemplateReadiness(actorID, templateID, request.GuestAgentRootVerified, request.ProvisioningAccountRemoved); err != nil {
			return common.DomainError(ctx, err)
		}
		err = ctx.JSON(fiber.Map{"template": record})
		return
	}
	return
}

// proxmoxConfigurationStatus reports whether server-side connection settings are present.
func proxmoxConfigurationStatus(services common.Services) (handler fiber.Handler) {
	handler = func(ctx fiber.Ctx) (err error) {
		var configured bool
		var insecureTLS bool
		if services.Proxmox != nil {
			configured = services.Proxmox.Configured()
			insecureTLS = services.Proxmox.InsecureTLS()
		}
		err = ctx.JSON(fiber.Map{"configured": configured, "insecure_tls": insecureTLS})
		return
	}
	return
}
