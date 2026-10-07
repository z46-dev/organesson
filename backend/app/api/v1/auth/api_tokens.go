package auth

import (
	"time"

	"github.com/gofiber/fiber/v3"
	"github.com/z46-dev/organesson/backend/app/api/common"
	localauth "github.com/z46-dev/organesson/backend/auth"
)

type (
	apiTokenRequest struct {
		AccountID    int    `json:"account_id"`
		Name         string `json:"name"`
		LifetimeDays int    `json:"lifetime_days"`
	}
)

// listAPITokens returns safe token metadata to platform administrators.
func listAPITokens(services common.Services) (handler fiber.Handler) {
	handler = func(ctx fiber.Ctx) (err error) {
		var tokens []localauth.APITokenSummary
		if tokens, err = services.Authentication.ListAPITokens(); err != nil {
			return ctx.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "Could not load API tokens."})
		}
		ctx.Set(fiber.HeaderCacheControl, "no-store")
		err = ctx.JSON(fiber.Map{"tokens": tokens})
		return
	}
	return
}

// createAPIToken issues a token for a selected account and returns its secret once.
func createAPIToken(services common.Services) (handler fiber.Handler) {
	handler = func(ctx fiber.Ctx) (err error) {
		var actorID int
		if actorID, _ = common.AccountID(ctx); actorID < 1 {
			return ctx.SendStatus(fiber.StatusUnauthorized)
		}
		var request apiTokenRequest
		if err = ctx.Bind().Body(&request); err != nil {
			return ctx.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "Invalid API token request."})
		}
		if request.LifetimeDays == 0 {
			request.LifetimeDays = 90
		}
		if request.LifetimeDays < 1 || request.LifetimeDays > 365 {
			return ctx.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "Token lifetime must be between 1 and 365 days."})
		}
		var credential *localauth.APITokenCredential
		if credential, err = services.Authentication.CreateAPIToken(actorID, request.AccountID, request.Name, time.Duration(request.LifetimeDays)*24*time.Hour); err != nil {
			return common.AuthError(ctx, err)
		}
		err = writeCreatedAPIToken(ctx, credential)
		return
	}
	return
}

// renewAPIToken rotates a selected token and returns its replacement secret once.
func renewAPIToken(services common.Services) (handler fiber.Handler) {
	handler = func(ctx fiber.Ctx) (err error) {
		var actorID int
		if actorID, _ = common.AccountID(ctx); actorID < 1 {
			return ctx.SendStatus(fiber.StatusUnauthorized)
		}
		var tokenID int
		if tokenID, err = common.ParseID(ctx, "token_id"); err != nil {
			return ctx.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "Invalid token identifier."})
		}
		var request apiTokenRequest
		if err = ctx.Bind().Body(&request); err != nil {
			return ctx.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "Invalid API token renewal request."})
		}
		if request.LifetimeDays == 0 {
			request.LifetimeDays = 90
		}
		if request.LifetimeDays < 1 || request.LifetimeDays > 365 {
			return ctx.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "Token lifetime must be between 1 and 365 days."})
		}
		var credential *localauth.APITokenCredential
		if credential, err = services.Authentication.RenewAPIToken(actorID, tokenID, time.Duration(request.LifetimeDays)*24*time.Hour); err != nil {
			return common.AuthError(ctx, err)
		}
		err = writeCreatedAPIToken(ctx, credential)
		return
	}
	return
}

// pruneExpiredAPITokens permanently removes tokens past their expiration time.
func pruneExpiredAPITokens(services common.Services) (handler fiber.Handler) {
	handler = func(ctx fiber.Ctx) (err error) {
		var actorID int
		if actorID, _ = common.AccountID(ctx); actorID < 1 {
			return ctx.SendStatus(fiber.StatusUnauthorized)
		}
		var pruned int
		if pruned, err = services.Authentication.PruneExpiredAPITokens(actorID); err != nil {
			return common.AuthError(ctx, err)
		}
		err = ctx.JSON(fiber.Map{"pruned_count": pruned})
		return
	}
	return
}

// expireAPIToken revokes a token for any owner under platform administrator authority.
func expireAPIToken(services common.Services) (handler fiber.Handler) {
	handler = func(ctx fiber.Ctx) (err error) {
		var actorID int
		if actorID, _ = common.AccountID(ctx); actorID < 1 {
			return ctx.SendStatus(fiber.StatusUnauthorized)
		}
		var tokenID int
		if tokenID, err = common.ParseID(ctx, "token_id"); err != nil {
			return ctx.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "Invalid token identifier."})
		}
		if err = services.Authentication.ExpireAPIToken(actorID, tokenID); err != nil {
			return common.AuthError(ctx, err)
		}
		err = ctx.SendStatus(fiber.StatusNoContent)
		return
	}
	return
}

// writeCreatedAPIToken sends one-time secret material without allowing response caching.
func writeCreatedAPIToken(ctx fiber.Ctx, credential *localauth.APITokenCredential) (err error) {
	ctx.Set(fiber.HeaderCacheControl, "no-store")
	ctx.Set(fiber.HeaderPragma, "no-cache")
	err = ctx.Status(fiber.StatusCreated).JSON(fiber.Map{
		"id":         credential.Token.ID,
		"account_id": credential.Token.AccountID,
		"name":       credential.Token.Name,
		"expires_at": credential.Token.ExpiresAt,
		"token":      credential.Secret,
	})
	return
}
