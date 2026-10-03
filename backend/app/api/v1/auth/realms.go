package auth

import (
	"errors"

	"github.com/gofiber/fiber/v3"
	"github.com/z46-dev/organesson/backend/app/api/common"
	localauth "github.com/z46-dev/organesson/backend/auth"
	"github.com/z46-dev/organesson/backend/db"
)

type (
	localRealmRequest struct {
		Enabled bool `json:"enabled"`
	}

	userIdentityResponse struct {
		QualifiedName string `json:"qualified_name"`
		Realm         string `json:"realm"`
		Kind          string `json:"kind"`
	}

	userResponse struct {
		ID                    int                    `json:"id"`
		DisplayName           string                 `json:"display_name"`
		PlatformAdministrator bool                   `json:"platform_administrator"`
		Disabled              bool                   `json:"disabled"`
		Identities            []userIdentityResponse `json:"identities"`
	}
)

// listAuthenticationRealms exposes non-secret login provider configuration to platform administrators.
func listAuthenticationRealms(services common.Services) (handler fiber.Handler) {
	handler = func(ctx fiber.Ctx) (err error) {
		var realms []localauth.AuthenticationRealm
		if realms, err = services.Authentication.ListAuthenticationRealms(); err != nil {
			return ctx.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "Could not load authentication realms."})
		}
		err = ctx.JSON(fiber.Map{"realms": realms, "encryption_ready": services.Authentication.AuthenticationEncryptionReady()})
		return
	}
	return
}

// createLDAPRealm saves a new directory realm without returning its bind credential.
func createLDAPRealm(services common.Services) (handler fiber.Handler) {
	handler = func(ctx fiber.Ctx) (err error) {
		var input localauth.LDAPRealmInput
		if err = ctx.Bind().Body(&input); err != nil || input.BindPassword == "" {
			return ctx.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "LDAP realm settings and a service bind password are required."})
		}
		var realm localauth.AuthenticationRealm
		if realm, err = services.Authentication.CreateLDAPRealm(input); err != nil {
			if errors.Is(err, localauth.ErrEncryptionKeyRequired) {
				return ctx.Status(fiber.StatusServiceUnavailable).JSON(fiber.Map{"error": "Configure ORGANESSON_AUTH_ENCRYPTION_KEY before adding LDAP credentials."})
			}
			if errors.Is(err, localauth.ErrConflict) {
				return ctx.Status(fiber.StatusConflict).JSON(fiber.Map{"error": "That realm alias is already in use."})
			}
			return ctx.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": err.Error()})
		}
		err = ctx.Status(fiber.StatusCreated).JSON(fiber.Map{"realm": realm})
		return
	}
	return
}

// updateLDAPRealm changes connection settings while keeping the existing password when omitted.
func updateLDAPRealm(services common.Services) (handler fiber.Handler) {
	handler = func(ctx fiber.Ctx) (err error) {
		var input localauth.LDAPRealmInput
		if err = ctx.Bind().Body(&input); err != nil {
			return ctx.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "Invalid LDAP realm settings."})
		}
		var realm localauth.AuthenticationRealm
		if realm, err = services.Authentication.UpdateLDAPRealm(ctx.Params("alias"), input); err != nil {
			return ctx.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": err.Error()})
		}
		err = ctx.JSON(fiber.Map{"realm": realm})
		return
	}
	return
}

// updateLocalRealm changes only the reserved local realm's login availability.
func updateLocalRealm(services common.Services) (handler fiber.Handler) {
	handler = func(ctx fiber.Ctx) (err error) {
		var request localRealmRequest
		if err = ctx.Bind().Body(&request); err != nil {
			return ctx.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "Invalid local realm settings."})
		}
		var realm localauth.AuthenticationRealm
		if realm, err = services.Authentication.SetLocalRealmEnabled(request.Enabled); err != nil {
			return ctx.Status(fiber.StatusConflict).JSON(fiber.Map{"error": err.Error()})
		}
		err = ctx.JSON(fiber.Map{"realm": realm})
		return
	}
	return
}

// testLDAPRealm validates the configured TLS connection and service-account search access.
func testLDAPRealm(services common.Services) (handler fiber.Handler) {
	handler = func(ctx fiber.Ctx) (err error) {
		if err = services.Authentication.TestLDAPRealm(ctx.Params("alias")); err != nil {
			return ctx.Status(fiber.StatusBadGateway).JSON(fiber.Map{"error": "LDAP connection test failed. Verify the realm URL, TLS trust, bind account, and base DN."})
		}
		err = ctx.JSON(fiber.Map{"passed": true})
		return
	}
	return
}

// listUsers returns every local account and the external realms linked to it.
func listUsers(services common.Services) (handler fiber.Handler) {
	handler = func(ctx fiber.Ctx) (err error) {
		var accounts []*db.Account
		if accounts, err = services.Store.Accounts.SelectAll(); err != nil {
			return ctx.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "Could not load users."})
		}
		var identities []*db.AccountIdentity
		if identities, err = services.Store.AccountIdentities.SelectAll(); err != nil {
			return ctx.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "Could not load user sources."})
		}
		var providers []*db.AuthenticationProvider
		if providers, err = services.Store.AuthenticationProviders.SelectAll(); err != nil {
			return ctx.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "Could not load user sources."})
		}
		var providerByID map[int]*db.AuthenticationProvider = make(map[int]*db.AuthenticationProvider, len(providers))
		for _, provider := range providers {
			providerByID[provider.ID] = provider
		}
		var users []userResponse = make([]userResponse, 0, len(accounts))
		for _, account := range accounts {
			var user userResponse = userResponse{
				ID: account.ID, DisplayName: account.DisplayName, PlatformAdministrator: account.PlatformAdministrator,
				Disabled: account.Disabled, Identities: make([]userIdentityResponse, 0),
			}
			for _, identity := range identities {
				if identity.AccountID != account.ID {
					continue
				}
				var provider *db.AuthenticationProvider = providerByID[identity.AuthenticationProviderID]
				if provider == nil {
					continue
				}
				var kind string
				switch provider.Kind {
				case db.AuthenticationProviderKindLocal:
					kind = "local"
				case db.AuthenticationProviderKindLDAP:
					kind = "ldap"
				default:
					kind = "other"
				}
				user.Identities = append(user.Identities, userIdentityResponse{QualifiedName: identity.QualifiedName, Realm: provider.Alias, Kind: kind})
			}
			users = append(users, user)
		}
		err = ctx.JSON(fiber.Map{"users": users})
		return
	}
	return
}

// updatePlatformAdministrator grants or removes the platform-wide administrator flag safely.
func updatePlatformAdministrator(services common.Services) (handler fiber.Handler) {
	handler = func(ctx fiber.Ctx) (err error) {
		var actorID int
		if actorID, _ = common.AccountID(ctx); actorID < 1 {
			return ctx.SendStatus(fiber.StatusUnauthorized)
		}
		var userID int
		if userID, err = common.ParseID(ctx, "user_id"); err != nil {
			return ctx.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "Invalid user identifier."})
		}
		var request localRealmRequest
		if err = ctx.Bind().Body(&request); err != nil {
			return ctx.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "Invalid administrator setting."})
		}
		var account *db.Account
		if account, err = services.Store.Accounts.Select(userID); err != nil || account == nil {
			return ctx.SendStatus(fiber.StatusNotFound)
		}
		if !request.Enabled && account.PlatformAdministrator {
			var accounts []*db.Account
			if accounts, err = services.Store.Accounts.SelectAll(); err != nil {
				return ctx.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "Could not validate administrator access."})
			}
			var activeAdmins int
			for _, candidate := range accounts {
				if candidate.PlatformAdministrator && !candidate.Disabled && candidate.ActivatedAt != nil {
					activeAdmins++
				}
			}
			if activeAdmins <= 1 {
				return ctx.Status(fiber.StatusConflict).JSON(fiber.Map{"error": "The last active platform administrator cannot be demoted."})
			}
		}
		account.PlatformAdministrator = request.Enabled
		if err = services.Store.Accounts.Update(account); err != nil {
			return ctx.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "Could not update administrator access."})
		}
		err = ctx.JSON(fiber.Map{"id": account.ID, "platform_administrator": account.PlatformAdministrator})
		return
	}
	return
}
