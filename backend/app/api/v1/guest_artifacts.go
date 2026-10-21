package v1

import (
	"fmt"
	"strings"
	"time"

	"github.com/gofiber/fiber/v3"
	"github.com/z46-dev/organesson/backend/app/api/common"
	"github.com/z46-dev/organesson/backend/domain"
	"github.com/z46-dev/organesson/backend/proxmox"
)

// executeGuestArtifactHandler authorizes and synchronously executes one package on a managed Linux, Windows, or BSD VM.
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
		if err = services.Domain.BeginGuestArtifactExecution(actorID, resourceID, request.SHA256, request.Entrypoint); err != nil {
			return common.DomainError(ctx, err)
		}
		if err = ensureGuestVMRunning(ctx, services, resourceID); err != nil {
			var completionErr error = services.Domain.CompleteGuestArtifactExecution(actorID, resourceID, request.SHA256, request.Entrypoint, err)
			if completionErr != nil {
				return common.DomainError(ctx, completionErr)
			}
			return ctx.Status(fiber.StatusBadGateway).JSON(fiber.Map{"error": "Proxmox could not start the managed guest for setup."})
		}
		var result proxmox.GuestArtifactResult
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
				"error":     fmt.Sprintf("QEMU Guest Agent artifact execution failed: %v. Temporary guest files were cleaned up when reachable.", err),
				"execution": result,
			})
		}
		err = ctx.JSON(fiber.Map{"execution": result})
		return
	}
	return
}

// waitForGuestRebootHandler requests a VM restart and waits for QEMU Guest Agent readiness.
func waitForGuestRebootHandler(services common.Services) (handler fiber.Handler) {
	handler = func(ctx fiber.Ctx) (err error) {
		var actorID int
		if actorID, _ = common.AccountID(ctx); actorID < 1 {
			return ctx.SendStatus(fiber.StatusUnauthorized)
		}
		var resourceID int
		if resourceID, err = common.ParseID(ctx, "resource_id"); err != nil {
			return ctx.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "Invalid virtual-machine identifier."})
		}
		var input struct {
			TimeoutSeconds int `json:"timeout_seconds"`
		}
		if err = ctx.Bind().Body(&input); err != nil || input.TimeoutSeconds < 60 || input.TimeoutSeconds > 3600 {
			return ctx.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "Reboot timeout must be between 60 and 3600 seconds."})
		}
		var request proxmox.GuestArtifactRequest
		if request, err = services.Domain.ResolveGuestVM(actorID, resourceID); err != nil {
			return common.DomainError(ctx, err)
		}
		if services.Proxmox == nil || !services.Proxmox.Configured() {
			return ctx.Status(fiber.StatusServiceUnavailable).JSON(fiber.Map{"error": "Proxmox is unavailable; guest reboot was not checked."})
		}
		if err = ensureGuestVMRunning(ctx, services, resourceID); err != nil {
			return ctx.Status(fiber.StatusBadGateway).JSON(fiber.Map{"error": "Proxmox could not start the guest while waiting for its reboot."})
		}
		if err = services.Proxmox.RestartGuestAfterSetup(ctx, request, time.Duration(input.TimeoutSeconds)*time.Second); err != nil {
			return ctx.Status(fiber.StatusGatewayTimeout).JSON(fiber.Map{"error": err.Error()})
		}
		err = ctx.JSON(fiber.Map{"ready": true})
		return
	}
	return
}

// waitForWindowsFirstBootHandler waits for unattended Windows setup without keeping a QGA process alive across reboot.
func waitForWindowsFirstBootHandler(services common.Services) (handler fiber.Handler) {
	handler = func(ctx fiber.Ctx) (err error) {
		var actorID int
		if actorID, _ = common.AccountID(ctx); actorID < 1 {
			return ctx.SendStatus(fiber.StatusUnauthorized)
		}
		var resourceID int
		if resourceID, err = common.ParseID(ctx, "resource_id"); err != nil {
			return ctx.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "Invalid virtual-machine identifier."})
		}
		var request proxmox.GuestArtifactRequest
		if request, err = services.Domain.ResolveGuestVM(actorID, resourceID); err != nil {
			return common.DomainError(ctx, err)
		}
		if request.GuestOSFamily != "windows" {
			return ctx.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "First-boot readiness is supported for Windows guests only."})
		}
		if services.Proxmox == nil || !services.Proxmox.Configured() {
			return ctx.Status(fiber.StatusServiceUnavailable).JSON(fiber.Map{"error": "Proxmox is unavailable; Windows first boot was not checked."})
		}
		if err = ensureGuestVMRunning(ctx, services, resourceID); err != nil {
			return ctx.Status(fiber.StatusBadGateway).JSON(fiber.Map{"error": "Proxmox could not start the managed guest while waiting for Windows first boot."})
		}
		if err = services.Proxmox.WaitForWindowsFirstBoot(ctx, request); err != nil {
			return ctx.Status(fiber.StatusGatewayTimeout).JSON(fiber.Map{"error": err.Error()})
		}
		err = ctx.JSON(fiber.Map{"ready": true})
		return
	}
	return
}
