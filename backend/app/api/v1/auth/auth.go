package auth

import (
	"errors"
	"strings"

	"github.com/gofiber/fiber/v3"
	"github.com/gofiber/fiber/v3/middleware/csrf"
	"github.com/gofiber/fiber/v3/middleware/session"
	"github.com/z46-dev/organesson/backend/app/api/common"
	"github.com/z46-dev/organesson/backend/db"
)

type (
	loginRequest struct {
		QualifiedName string `json:"qualified_name"`
		Username      string `json:"username"`
		Realm         string `json:"realm"`
		Password      string `json:"password"`
	}

	setupRequest struct {
		Token    string `json:"token"`
		Password string `json:"password"`
	}

	createLocalAccountRequest struct {
		Username    string `json:"username"`
		DisplayName string `json:"display_name"`
	}
)

// Init registers authentication endpoints for bootstrap, login, logout, and status.
func Init(parent fiber.Router, services common.Services) {
	var authRouter fiber.Router = parent.Group("/auth")
	authRouter.Get("/csrf", csrfToken)
	authRouter.Get("/status", status(services))
	var admin fiber.Router = authRouter.Group("/admin", common.RequirePlatformAdministrator(services.Authentication))
	admin.Get("/realms", listAuthenticationRealms(services))
	admin.Post("/realms/ldap", createLDAPRealm(services))
	admin.Put("/realms/local", updateLocalRealm(services))
	admin.Put("/realms/ldap/:alias", updateLDAPRealm(services))
	admin.Post("/realms/ldap/:alias/test", testLDAPRealm(services))
	admin.Get("/users", listUsers(services))
	admin.Put("/users/:user_id/platform-administrator", updatePlatformAdministrator(services))
	admin.Get("/api-tokens", listAPITokens(services))
	admin.Post("/api-tokens", createAPIToken(services))
	admin.Post("/api-tokens/prune-expired", pruneExpiredAPITokens(services))
	admin.Post("/api-tokens/:token_id/renew", renewAPIToken(services))
	admin.Delete("/api-tokens/:token_id", expireAPIToken(services))
	authRouter.Post("/bootstrap/redeem", redeemBootstrap(services))
	authRouter.Post("/password/redeem", redeemPasswordLink(services))
	authRouter.Post("/login", login(services))
	authRouter.Delete("/api-tokens/:token_id", common.RequireSession(services.Authentication), revokeAPIToken(services))
	authRouter.Post("/logout", common.RequireSession(services.Authentication), logout)
	authRouter.Get("/me", common.RequireSession(services.Authentication), me(services))
}

// revokeAPIToken revokes a bearer token owned by the signed-in account.
func revokeAPIToken(services common.Services) (handler fiber.Handler) {
	handler = func(ctx fiber.Ctx) (err error) {
		var accountID int
		if accountID, _ = common.AccountID(ctx); accountID < 1 {
			return ctx.SendStatus(fiber.StatusUnauthorized)
		}
		var tokenID int
		if tokenID, err = common.ParseID(ctx, "token_id"); err != nil {
			return ctx.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "Invalid token identifier."})
		}
		if err = services.Authentication.RevokeAPIToken(accountID, tokenID); err != nil {
			return common.AuthError(ctx, err)
		}
		err = ctx.SendStatus(fiber.StatusNoContent)
		return
	}
	return
}

// redeemPasswordLink activates or resets a local account without changing the caller's session.
func redeemPasswordLink(services common.Services) (handler fiber.Handler) {
	handler = func(ctx fiber.Ctx) (err error) {
		var request setupRequest
		if err = ctx.Bind().Body(&request); err != nil || request.Token == "" {
			return ctx.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "A valid password link and password are required."})
		}
		var account *db.Account
		if account, err = services.Authentication.RedeemPasswordLink(request.Token, request.Password); err != nil {
			return common.AuthError(ctx, err)
		}
		var response common.AccountResponse
		if response, err = common.AccountResponseFor(services.Store, account.ID); err != nil {
			return common.AuthError(ctx, err)
		}
		err = ctx.JSON(fiber.Map{"account": response})
		return
	}
	return
}

// csrfToken returns the synchronizer token required for state-changing browser requests.
func csrfToken(ctx fiber.Ctx) (err error) {
	err = ctx.JSON(fiber.Map{"csrf_token": csrf.TokenFromContext(ctx)})
	return
}

// status reports whether setup is needed and whether the current session is authenticated.
func status(services common.Services) (handler fiber.Handler) {
	handler = func(ctx fiber.Ctx) (err error) {
		var setupRequired bool
		if setupRequired, err = services.Authentication.SetupRequired(); err != nil {
			return common.AuthError(ctx, err)
		}
		var realms []string
		if realms, err = services.Authentication.AuthenticationRealms(); err != nil {
			return common.AuthError(ctx, err)
		}
		var response fiber.Map = fiber.Map{"setup_required": setupRequired, "authenticated": false, "realms": realms}
		if middleware := session.FromContext(ctx); middleware != nil {
			if accountID, valid := middleware.Get("account_id").(int); valid {
				var account *db.Account
				if account, err = services.Authentication.AccountByID(accountID); err != nil {
					return common.AuthError(ctx, err)
				}
				if account != nil {
					response["authenticated"] = true
					var publicAccount common.AccountResponse
					if publicAccount, err = common.AccountResponseFor(services.Store, account.ID); err != nil {
						return common.AuthError(ctx, err)
					}
					response["account"] = publicAccount
				}
			}
		}
		err = ctx.JSON(response)
		return
	}
	return
}

// redeemBootstrap consumes the one-time setup link and starts the new administrator session.
func redeemBootstrap(services common.Services) (handler fiber.Handler) {
	handler = func(ctx fiber.Ctx) (err error) {
		var request setupRequest
		if err = ctx.Bind().Body(&request); err != nil || request.Token == "" {
			return ctx.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "A valid setup token and password are required."})
		}
		var account *db.Account
		if account, err = services.Authentication.RedeemPasswordLink(request.Token, request.Password); err != nil {
			return common.AuthError(ctx, err)
		}
		if err = establishSession(ctx, account.ID); err != nil {
			return common.AuthError(ctx, err)
		}
		var response common.AccountResponse
		if response, err = common.AccountResponseFor(services.Store, account.ID); err != nil {
			return common.AuthError(ctx, err)
		}
		err = ctx.Status(fiber.StatusCreated).JSON(fiber.Map{"account": response})
		return
	}
	return
}

// login validates local credentials and issues a fresh server-side session.
func login(services common.Services) (handler fiber.Handler) {
	handler = func(ctx fiber.Ctx) (err error) {
		var request loginRequest
		if err = ctx.Bind().Body(&request); err != nil || request.Password == "" {
			return ctx.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "Username and password are required."})
		}
		if request.Username != "" || request.Realm != "" {
			if request.Username == "" || request.Realm == "" || strings.Contains(request.Username, "@") {
				return ctx.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "Enter a username and select a realm."})
			}
			var realms []string
			if realms, err = services.Authentication.AuthenticationRealms(); err != nil {
				return common.AuthError(ctx, err)
			}
			var available bool
			for _, realm := range realms {
				if strings.EqualFold(realm, request.Realm) {
					available = true
					request.QualifiedName = request.Username + "@" + realm
					break
				}
			}
			if !available {
				return ctx.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "Select an available realm."})
			}
		}
		if request.QualifiedName == "" {
			return ctx.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "Username and password are required."})
		}
		var account *db.Account
		if account, err = services.Authentication.Authenticate(request.QualifiedName, request.Password); err != nil {
			return common.AuthError(ctx, err)
		}
		if err = establishSession(ctx, account.ID); err != nil {
			return common.AuthError(ctx, err)
		}
		var response common.AccountResponse
		if response, err = common.AccountResponseFor(services.Store, account.ID); err != nil {
			return common.AuthError(ctx, err)
		}
		err = ctx.JSON(fiber.Map{"account": response})
		return
	}
	return
}

// logout destroys the active server-side session.
func logout(ctx fiber.Ctx) (err error) {
	var middleware = session.FromContext(ctx)
	if middleware == nil {
		return ctx.SendStatus(fiber.StatusUnauthorized)
	}
	err = middleware.Session.Destroy()
	if err != nil {
		return ctx.SendStatus(fiber.StatusInternalServerError)
	}
	err = ctx.SendStatus(fiber.StatusNoContent)
	return
}

// me returns the account attached to the authenticated server-side session.
func me(services common.Services) (handler fiber.Handler) {
	handler = func(ctx fiber.Ctx) (err error) {
		var accountID int
		var valid bool
		if accountID, valid = common.AccountID(ctx); !valid {
			return ctx.SendStatus(fiber.StatusUnauthorized)
		}
		var response common.AccountResponse
		if response, err = common.AccountResponseFor(services.Store, accountID); err != nil {
			return common.AuthError(ctx, err)
		}
		err = ctx.JSON(fiber.Map{"account": response})
		return
	}
	return
}

// establishSession rotates the session identifier before recording the authenticated account.
func establishSession(ctx fiber.Ctx, accountID int) (err error) {
	var middleware = session.FromContext(ctx)
	if middleware == nil {
		err = errors.New("session middleware is unavailable")
		return
	}
	if err = middleware.Session.Regenerate(); err != nil {
		return
	}
	middleware.Set("account_id", accountID)
	return
}
