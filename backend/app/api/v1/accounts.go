package v1

import (
	"github.com/gofiber/fiber/v3"
	"github.com/z46-dev/organesson/backend/app/api/common"
	"github.com/z46-dev/organesson/backend/auth"
)

type (
	createLocalAccountRequest struct {
		Username    string `json:"username"`
		DisplayName string `json:"display_name"`
	}
)

// initAccounts registers platform-administrator account provisioning routes.
func initAccounts(parent fiber.Router, services common.Services) {
	parent.Post("/admin/accounts/local", common.RequireSession(services.Authentication), createLocalAccount(services))
	parent.Post("/admin/accounts/local/:account_id/password-link", common.RequireSession(services.Authentication), resetLocalAccountLink(services))
}

// resetLocalAccountLink issues a one-time activation or password-reset link.
func resetLocalAccountLink(services common.Services) (handler fiber.Handler) {
	handler = func(ctx fiber.Ctx) (err error) {
		var actorID int
		if actorID, _ = common.AccountID(ctx); actorID == 0 {
			return ctx.SendStatus(fiber.StatusUnauthorized)
		}
		var accountID int
		if accountID, err = common.ParseID(ctx, "account_id"); err != nil {
			return ctx.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "Invalid account identifier."})
		}
		var token string
		if token, err = services.Authentication.ResetLocalAccountLink(actorID, accountID); err != nil {
			return common.AuthError(ctx, err)
		}
		ctx.Set(fiber.HeaderCacheControl, "no-store")
		ctx.Set(fiber.HeaderPragma, "no-cache")
		err = ctx.JSON(fiber.Map{"setup_token": token})
		return
	}
	return
}

// createLocalAccount provisions a non-administrator account and returns its setup token once.
func createLocalAccount(services common.Services) (handler fiber.Handler) {
	handler = func(ctx fiber.Ctx) (err error) {
		var actorID int
		if actorID, _ = common.AccountID(ctx); actorID == 0 {
			return ctx.SendStatus(fiber.StatusUnauthorized)
		}
		var request createLocalAccountRequest
		if err = ctx.Bind().Body(&request); err != nil {
			return ctx.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "Invalid local account request."})
		}
		var setup *auth.LocalAccountSetup
		if setup, err = services.Authentication.CreateLocalAccount(actorID, request.Username, request.DisplayName); err != nil {
			return common.AuthError(ctx, err)
		}
		ctx.Set(fiber.HeaderCacheControl, "no-store")
		ctx.Set(fiber.HeaderPragma, "no-cache")
		err = ctx.Status(fiber.StatusCreated).JSON(fiber.Map{
			"account": fiber.Map{
				"id":             setup.Account.ID,
				"qualified_name": setup.QualifiedName,
				"display_name":   setup.Account.DisplayName,
			},
			"setup_token": setup.SetupToken,
		})
		return
	}
	return
}
