package common

import (
	"errors"
	"strconv"
	"strings"

	"github.com/gofiber/fiber/v3"
	"github.com/gofiber/fiber/v3/middleware/session"
	"github.com/z46-dev/organesson/backend/auth"
	"github.com/z46-dev/organesson/backend/db"
	"github.com/z46-dev/organesson/backend/domain"
	"github.com/z46-dev/organesson/backend/proxmox"
)

type (
	// Services are application services shared by HTTP handlers.
	Services struct {
		Authentication *auth.Service
		Domain         *domain.Service
		Store          *db.Store
		Proxmox        *proxmox.Service
	}

	// AccountResponse is the public account shape returned to browser clients.
	AccountResponse struct {
		ID                    int    `json:"id"`
		QualifiedName         string `json:"qualified_name"`
		DisplayName           string `json:"display_name"`
		PlatformAdministrator bool   `json:"platform_administrator"`
	}
)

// AccountID reads the authenticated account identifier set by session middleware.
func AccountID(ctx fiber.Ctx) (id int, ok bool) {
	var value any = ctx.Locals("account_id")
	id, ok = value.(int)
	return
}

// RequirePlatformAdministrator restricts an endpoint to active platform admins with browser sessions.
func RequirePlatformAdministrator(authentication *auth.Service) (handler fiber.Handler) {
	handler = func(ctx fiber.Ctx) (err error) {
		var middleware = session.FromContext(ctx)
		if middleware == nil {
			return ctx.SendStatus(fiber.StatusUnauthorized)
		}
		var accountID int
		var valid bool
		if accountID, valid = middleware.Get("account_id").(int); !valid {
			return ctx.SendStatus(fiber.StatusUnauthorized)
		}
		var account *db.Account
		if account, err = authentication.AccountByID(accountID); err != nil {
			return ctx.SendStatus(fiber.StatusInternalServerError)
		}
		if account == nil || account.Disabled {
			return ctx.SendStatus(fiber.StatusUnauthorized)
		}
		if !account.PlatformAdministrator {
			return ctx.SendStatus(fiber.StatusForbidden)
		}
		ctx.Locals("account_id", account.ID)
		err = ctx.Next()
		return
	}
	return
}

// RequireSession rejects unauthenticated requests and refreshes account state from SQLite.
func RequireSession(authentication *auth.Service) (handler fiber.Handler) {
	handler = func(ctx fiber.Ctx) (err error) {
		var middleware = session.FromContext(ctx)
		if middleware == nil {
			return ctx.SendStatus(fiber.StatusUnauthorized)
		}
		var accountID int
		var valid bool
		if accountID, valid = middleware.Get("account_id").(int); !valid {
			return ctx.SendStatus(fiber.StatusUnauthorized)
		}
		var account *db.Account
		if account, err = authentication.AccountByID(accountID); err != nil {
			return ctx.SendStatus(fiber.StatusInternalServerError)
		}
		if account == nil {
			return ctx.SendStatus(fiber.StatusUnauthorized)
		}
		ctx.Locals("account_id", account.ID)
		err = ctx.Next()
		return
	}
	return
}

// RequireActor accepts either a browser session or a bearer token on protected routes.
func RequireActor(authentication *auth.Service) (handler fiber.Handler) {
	handler = func(ctx fiber.Ctx) (err error) {
		var authorization string = ctx.Get(fiber.HeaderAuthorization)
		if strings.HasPrefix(authorization, "Bearer ") {
			var accountID int
			var tokenErr error
			if accountID, tokenErr = authenticateBearer(authentication, strings.TrimPrefix(authorization, "Bearer ")); tokenErr != nil {
				return ctx.SendStatus(fiber.StatusUnauthorized)
			}
			ctx.Locals("account_id", accountID)
			err = ctx.Next()
			return
		}
		var middleware = session.FromContext(ctx)
		if middleware == nil {
			return ctx.SendStatus(fiber.StatusUnauthorized)
		}
		var accountID int
		var valid bool
		if accountID, valid = middleware.Get("account_id").(int); !valid {
			return ctx.SendStatus(fiber.StatusUnauthorized)
		}
		var account *db.Account
		if account, err = authentication.AccountByID(accountID); err != nil {
			return ctx.SendStatus(fiber.StatusInternalServerError)
		}
		if account == nil {
			return ctx.SendStatus(fiber.StatusUnauthorized)
		}
		ctx.Locals("account_id", account.ID)
		err = ctx.Next()
		return
	}
	return
}

// authenticateBearer validates a token without exposing token records to handlers.
func authenticateBearer(authentication *auth.Service, token string) (accountID int, err error) {
	var accountIDRecord *db.Account
	if accountIDRecord, err = authentication.AuthenticateAPIToken(token); err != nil {
		return
	}
	accountID = accountIDRecord.ID
	return
}

// ParseID validates a positive route identifier.
func ParseID(ctx fiber.Ctx, key string) (id int, err error) {
	var parsed int64
	if parsed, err = strconv.ParseInt(ctx.Params(key), 10, 32); err != nil || parsed < 1 {
		err = errors.New("invalid identifier")
		return
	}
	id = int(parsed)
	return
}

// DomainError writes a safe client response for expected domain errors.
func DomainError(ctx fiber.Ctx, err error) (responseErr error) {
	var status int = fiber.StatusInternalServerError
	var message string = "The request could not be completed."
	switch {
	case errors.Is(err, domain.ErrForbidden):
		status = fiber.StatusForbidden
		message = "Permission denied."
	case errors.Is(err, domain.ErrNotFound):
		status = fiber.StatusNotFound
		message = "Resource not found."
	case errors.Is(err, domain.ErrInvalidPermission):
		status = fiber.StatusBadRequest
		message = err.Error()
	case errors.Is(err, domain.ErrInvalidInput):
		status = fiber.StatusBadRequest
		message = "The provided values are invalid."
	}
	responseErr = ctx.Status(status).JSON(fiber.Map{"error": message})
	return
}

// AuthError writes a generic authentication response without account enumeration.
func AuthError(ctx fiber.Ctx, err error) (responseErr error) {
	if errors.Is(err, auth.ErrInvalidCredentials) || errors.Is(err, auth.ErrInvalidToken) {
		responseErr = ctx.Status(fiber.StatusUnauthorized).JSON(fiber.Map{"error": "Invalid credentials or password link."})
		return
	}
	if errors.Is(err, auth.ErrInvalidPassword) {
		responseErr = ctx.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": err.Error()})
		return
	}
	if errors.Is(err, auth.ErrForbidden) {
		responseErr = ctx.Status(fiber.StatusForbidden).JSON(fiber.Map{"error": "Permission denied."})
		return
	}
	if errors.Is(err, auth.ErrInvalidInput) {
		responseErr = ctx.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "The provided account values are invalid."})
		return
	}
	if errors.Is(err, auth.ErrConflict) {
		responseErr = ctx.Status(fiber.StatusConflict).JSON(fiber.Map{"error": "That account name is already in use."})
		return
	}
	responseErr = ctx.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "The request could not be completed."})
	return
}

// AccountResponseFor resolves public profile fields without exposing credential data.
func AccountResponseFor(store *db.Store, accountID int) (response AccountResponse, err error) {
	var account *db.Account
	if account, err = store.Accounts.Select(accountID); err != nil {
		return
	}
	if account == nil {
		err = domain.ErrNotFound
		return
	}
	var identities []*db.AccountIdentity
	if identities, err = store.AccountIdentities.SelectAll(); err != nil {
		return
	}
	response = AccountResponse{ID: account.ID, DisplayName: account.DisplayName, PlatformAdministrator: account.PlatformAdministrator}
	for _, identity := range identities {
		if identity.AccountID == account.ID {
			response.QualifiedName = identity.QualifiedName
			break
		}
	}
	return
}
