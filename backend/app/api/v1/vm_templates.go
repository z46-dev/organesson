package v1

import (
	"strconv"
	"strings"

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

	prepareVMTemplateRequest struct {
		Scripts   map[string]string `json:"scripts"`
		Usernames []string          `json:"usernames"`
	}
)

// initVMTemplates registers platform-admin source catalog and preflight routes.
func initVMTemplates(parent fiber.Router, services common.Services) {
	var admin fiber.Router = parent.Group("/admin", common.RequirePlatformAdministrator(services.Authentication))
	admin.Get("/vm-templates", listVMTemplates(services))
	admin.Post("/vm-templates", createVMTemplate(services))
	admin.Put("/vm-templates/:template_id", updateVMTemplate(services))
	admin.Delete("/vm-templates/:template_id", deleteVMTemplate(services))
	admin.Post("/vm-templates/:template_id/aliases", addVMTemplateAlias(services))
	admin.Delete("/vm-templates/:template_id/aliases/:alias_id", removeVMTemplateAlias(services))
	admin.Post("/vm-templates/:template_id/preflight", preflightVMTemplate(services))
	admin.Post("/vm-templates/:template_id/prepare", prepareVMTemplate(services))
	admin.Get("/proxmox/status", proxmoxConfigurationStatus(services))
}

// prepareVMTemplate runs the selected guest preparation workflow, optionally removes named accounts, and seals the PVE source.
func prepareVMTemplate(services common.Services) (handler fiber.Handler) {
	handler = func(ctx fiber.Ctx) (err error) {
		var actorID int
		if actorID, _ = common.AccountID(ctx); actorID < 1 {
			return ctx.SendStatus(fiber.StatusUnauthorized)
		}
		var templateID int
		if templateID, err = common.ParseID(ctx, "template_id"); err != nil {
			return ctx.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "Invalid VM template identifier."})
		}
		var request prepareVMTemplateRequest
		if err = ctx.Bind().Body(&request); err != nil {
			return ctx.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "Invalid template preparation request."})
		}
		if len(request.Usernames) > 32 {
			return ctx.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "No more than 32 account names may be removed in one run."})
		}
		var selected *db.VMTemplate
		if selected, err = services.Store.VMTemplates.Select(templateID); err != nil {
			return ctx.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "Could not load VM template."})
		}
		if selected == nil {
			return ctx.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "VM template not found."})
		}
		if !proxmox.IsTemplatePreparationSupported(selected.GuestOS) {
			return ctx.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "Automated preparation supports Linux and Windows plus FreeBSD guests."})
		}
		if err = proxmox.ValidateTemplatePreparationScripts(selected.GuestOS, request.Scripts); err != nil {
			return ctx.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": err.Error()})
		}
		if services.Proxmox == nil || !services.Proxmox.Configured() {
			return ctx.Status(fiber.StatusServiceUnavailable).JSON(fiber.Map{"error": "Proxmox connection is not configured on the backend."})
		}
		if err = services.Domain.BeginVMTemplatePreparation(actorID, templateID); err != nil {
			return common.DomainError(ctx, err)
		}
		var result proxmox.PreflightResult
		var preparationErr error
		result, preparationErr = services.Proxmox.PrepareTemplate(ctx, proxmox.TemplatePreparationRequest{
			SourceID: selected.SourceID, ExpectedOS: selected.GuestOS, Scripts: request.Scripts, Usernames: request.Usernames,
		})
		var record *domain.VMTemplateRecord
		if record, err = services.Domain.CompleteVMTemplatePreparation(actorID, templateID, result, preparationErr); err != nil {
			return common.DomainError(ctx, err)
		}
		if preparationErr != nil {
			return ctx.Status(fiber.StatusBadGateway).JSON(fiber.Map{"error": preparationErr.Error(), "template": record, "preparation": result})
		}
		return ctx.JSON(fiber.Map{"template": record, "preparation": result})
	}
	return
}

// deleteVMTemplate removes an Organesson catalog entry without deleting its Proxmox VM.
func deleteVMTemplate(services common.Services) (handler fiber.Handler) {
	handler = func(ctx fiber.Ctx) (err error) {
		var actorID int
		if actorID, _ = common.AccountID(ctx); actorID < 1 {
			return ctx.SendStatus(fiber.StatusUnauthorized)
		}
		var templateID int
		if templateID, err = common.ParseID(ctx, "template_id"); err != nil {
			return ctx.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "Invalid VM template identifier."})
		}
		if err = services.Domain.DeleteVMTemplate(actorID, templateID); err != nil {
			return common.DomainError(ctx, err)
		}
		return ctx.SendStatus(fiber.StatusNoContent)
	}
	return
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
		var sourceVMID int
		if sourceVMID, err = strconv.Atoi(strings.TrimSpace(request.SourceID)); err != nil || sourceVMID < 1 {
			return ctx.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "Proxmox VMID must be a positive integer."})
		}
		request.SourceID = strconv.Itoa(sourceVMID)
		if request.GuestOS != "linux" && request.GuestOS != "windows" && request.GuestOS != "bsd" {
			return ctx.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "Select a broad guest type: Linux, Windows, or BSD."})
		}
		if services.Proxmox == nil || !services.Proxmox.Configured() {
			return ctx.Status(fiber.StatusServiceUnavailable).JSON(fiber.Map{"error": "Proxmox connection is not configured on the backend."})
		}
		var detection proxmox.PreflightResult
		if detection, err = services.Proxmox.DetectTemplate(ctx, request.SourceID, request.GuestOS); err != nil {
			return ctx.Status(fiber.StatusBadGateway).JSON(fiber.Map{"error": err.Error(), "detection": detection})
		}
		if !detection.Passed || detection.PowerState != "stopped" || strings.TrimSpace(detection.GuestOSName) == "" || strings.TrimSpace(detection.GuestOSVersion) == "" || detection.GuestArchitecture == "" {
			return ctx.Status(fiber.StatusBadGateway).JSON(fiber.Map{"error": "Source detection did not pass all checks or restore the VM to stopped state.", "detection": detection})
		}
		request.GuestOSName = detection.GuestOSName
		request.GuestOSVersion = detection.GuestOSVersion
		request.Architecture = detection.GuestArchitecture
		request.Edition = ""
		var record *domain.VMTemplateRecord
		if record, err = services.Domain.CreateVMTemplate(actorID, request.VMTemplateInput, request.Aliases); err != nil {
			return common.DomainError(ctx, err)
		}
		err = ctx.Status(fiber.StatusCreated).JSON(fiber.Map{"template": record, "detection": detection})
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

// preflightVMTemplate inspects the source and performs a harmless guest identity query when running.
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
