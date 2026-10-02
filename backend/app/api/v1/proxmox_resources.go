package v1

import (
	"encoding/json"
	"strings"
	"time"

	"github.com/gofiber/fiber/v3"
	"github.com/z46-dev/organesson/backend/app/api/common"
	"github.com/z46-dev/organesson/backend/db"
	"github.com/z46-dev/organesson/backend/proxmox"
)

// initProxmoxResources registers platform-admin capacity policy and read-only inventory routes.
func initProxmoxResources(parent fiber.Router, services common.Services) {
	var admin fiber.Router = parent.Group("/admin", common.RequirePlatformAdministrator(services.Authentication))
	admin.Get("/proxmox/resources", getProxmoxResources(services))
	admin.Put("/proxmox/resources", saveProxmoxResources(services))
}

// getProxmoxResources returns persisted policy and current inventory when PVE is configured.
func getProxmoxResources(services common.Services) (handler fiber.Handler) {
	handler = func(ctx fiber.Ctx) (err error) {
		var policy proxmox.ResourcePolicy
		var stored *db.ProxmoxResourcePolicy
		if stored, err = services.Store.ProxmoxResourcePolicies.Select(1); err != nil {
			return ctx.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "Could not load resource policy."})
		}
		if stored != nil && stored.ConfigurationJSON != "" {
			if err = json.Unmarshal([]byte(stored.ConfigurationJSON), &policy); err != nil {
				return ctx.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "Stored resource policy is invalid."})
			}
		}
		response := fiber.Map{"policy": policy, "configured": services.Proxmox != nil && services.Proxmox.Configured()}
		if stored != nil {
			response["validation_json"] = stored.ValidationJSON
			response["validated_at"] = stored.ValidatedAt
		}
		if services.Proxmox != nil && services.Proxmox.Configured() {
			var inventory proxmox.ResourceInventory
			if inventory, err = services.Proxmox.ResourceInventory(ctx); err != nil {
				response["inventory_error"] = "Proxmox inventory could not be read. Check the configured token permissions and connection."
			} else {
				response["inventory"] = inventory
			}
		}
		err = ctx.JSON(response)
		return
	}
	return
}

// saveProxmoxResources validates locally and against a fresh read-only PVE inventory before saving.
func saveProxmoxResources(services common.Services) (handler fiber.Handler) {
	handler = func(ctx fiber.Ctx) (err error) {
		var policy proxmox.ResourcePolicy
		if err = ctx.Bind().Body(&policy); err != nil {
			return ctx.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "Invalid resource policy request."})
		}
		var inventory *proxmox.ResourceInventory
		if services.Proxmox == nil || !services.Proxmox.Configured() {
			return ctx.Status(fiber.StatusServiceUnavailable).JSON(fiber.Map{"error": "Configure the Proxmox read-only API connection before validating and saving this policy."})
		}
		var current proxmox.ResourceInventory
		if current, err = services.Proxmox.ResourceInventory(ctx); err != nil {
			return ctx.Status(fiber.StatusBadGateway).JSON(fiber.Map{"error": "Could not read the current Proxmox resource inventory."})
		}
		inventory = &current
		var validation proxmox.ResourcePolicyValidation = proxmox.ValidateResourcePolicy(policy, inventory)
		if !validation.Valid {
			return ctx.Status(fiber.StatusUnprocessableEntity).JSON(fiber.Map{"error": strings.Join(validation.Issues, " "), "validation": validation})
		}
		var encoded []byte
		if encoded, err = json.Marshal(policy); err != nil {
			return ctx.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "Could not encode resource policy."})
		}
		var validationJSON []byte
		if validationJSON, err = json.Marshal(validation); err != nil {
			return ctx.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "Could not encode validation result."})
		}
		var configHash string
		if configHash, err = proxmox.ResourcePolicyHash(policy); err != nil {
			return ctx.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "Could not fingerprint resource policy."})
		}
		var timestamp time.Time = time.Now().UTC()
		var record *db.ProxmoxResourcePolicy
		if record, err = services.Store.ProxmoxResourcePolicies.Select(1); err != nil {
			return ctx.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "Could not load resource policy."})
		}
		if record == nil {
			record = &db.ProxmoxResourcePolicy{ID: 1}
		}
		record.ConfigurationJSON = string(encoded)
		record.ValidationJSON = string(validationJSON)
		record.ValidatedConfigHash = configHash
		record.ValidatedAt = &timestamp
		record.UpdatedAt = timestamp
		if record.ID == 1 {
			var existing *db.ProxmoxResourcePolicy
			if existing, err = services.Store.ProxmoxResourcePolicies.Select(1); err != nil {
				return ctx.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "Could not load resource policy."})
			}
			if existing == nil {
				err = services.Store.ProxmoxResourcePolicies.Insert(record)
			} else {
				err = services.Store.ProxmoxResourcePolicies.Update(record)
			}
			if err != nil {
				return ctx.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "Could not save resource policy."})
			}
		}
		var actorID int
		actorID, _ = common.AccountID(ctx)
		if err = services.Store.AuditEvents.Insert(&db.AuditEvent{
			ActorAccountID: &actorID,
			Action:         "proxmox.resource_policy.updated",
			Target:         "proxmox.resource_policy",
			Result:         "success",
			DetailsJSON:    `{"validated_against":"current_proxmox_inventory"}`,
			CreatedAt:      timestamp,
		}); err != nil {
			return ctx.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "Policy was saved, but its audit record could not be written."})
		}
		err = ctx.JSON(fiber.Map{"policy": policy, "validation": validation, "validated_at": timestamp, "inventory": current})
		return
	}
	return
}
