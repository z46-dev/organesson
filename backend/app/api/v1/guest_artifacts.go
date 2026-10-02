package v1

import (
	"strings"

	"github.com/gofiber/fiber/v3"
	"github.com/z46-dev/organesson/backend/app/api/common"
	"github.com/z46-dev/organesson/backend/domain"
	"github.com/z46-dev/organesson/backend/proxmox"
)

// executeGuestArtifactHandler authorizes and synchronously executes one package on a managed Linux VM.
func executeGuestArtifactHandler(services common.Services) (handler fiber.Handler) {
	handler = func(ctx fiber.Ctx) (err error) {
		var actorID int
		if actorID, _ = common.AccountID(ctx); actorID < 1 {
			return ctx.SendStatus(fiber.StatusUnauthorized)
		}
		if !strings.EqualFold(strings.TrimSpace(ctx.Get(fiber.HeaderContentType)), "application/vnd.organesson.artifact+gzip") {
			return ctx.Status(fiber.StatusUnsupportedMediaType).JSON(fiber.Map{"error": "Artifact package must be a gzip-compressed tar archive."})
		}
		var resourceID int
		if resourceID, err = common.ParseID(ctx, "resource_id"); err != nil {
			return ctx.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "Invalid virtual-machine identifier."})
		}
		var request proxmox.GuestArtifactRequest
		if request, err = services.Domain.ResolveGuestArtifact(actorID, resourceID, domain.GuestArtifactInput{
			Entrypoint: ctx.Get("X-Organesson-Artifact-Entrypoint"), SHA256: ctx.Get("X-Organesson-Artifact-SHA256"),
		}); err != nil {
			return common.DomainError(ctx, err)
		}
		request.Archive = ctx.Body()
		if err = proxmox.ValidateGuestArtifactArchive(request); err != nil {
			return ctx.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "Artifact package failed validation."})
		}
		if services.Proxmox == nil || !services.Proxmox.Configured() {
			return ctx.Status(fiber.StatusServiceUnavailable).JSON(fiber.Map{"error": "Proxmox is unavailable; artifact was not executed."})
		}
		var result proxmox.GuestArtifactResult
		var alreadySucceeded bool
		if result, alreadySucceeded, err = services.Domain.BeginGuestArtifactExecution(actorID, resourceID, request.SHA256, request.Entrypoint); err != nil {
			return common.DomainError(ctx, err)
		}
		if alreadySucceeded {
			err = ctx.JSON(fiber.Map{"execution": result})
			return
		}
		if err = ensureGuestVMRunning(ctx, services, resourceID); err != nil {
			var completionErr error = services.Domain.CompleteGuestArtifactExecution(actorID, resourceID, request.SHA256, request.Entrypoint, err)
			if completionErr != nil {
				return common.DomainError(ctx, completionErr)
			}
			return ctx.Status(fiber.StatusBadGateway).JSON(fiber.Map{"error": "Proxmox could not start the managed guest for setup."})
		}
		result, err = services.Proxmox.ExecuteGuestArtifact(ctx, request)
		var completionErr error = services.Domain.CompleteGuestArtifactExecution(actorID, resourceID, request.SHA256, request.Entrypoint, err)
		if completionErr != nil {
			return common.DomainError(ctx, completionErr)
		}
		if err != nil {
			if result.Status == "" {
				result = proxmox.GuestArtifactResult{SHA256: request.SHA256, Status: "failed", ExitCode: -1}
			}
			return ctx.Status(fiber.StatusBadGateway).JSON(fiber.Map{
				"error":     "QEMU Guest Agent did not confirm successful artifact execution; temporary guest files were cleaned up when reachable.",
				"execution": result,
			})
		}
		err = ctx.JSON(fiber.Map{"execution": result})
		return
	}
	return
}
