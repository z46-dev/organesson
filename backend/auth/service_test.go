package auth

import (
	"bytes"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/z46-dev/golog"
	"github.com/z46-dev/organesson/backend/db"
)

// TestBootstrapLinkIsSingleUseAndCreatesLocalAuthentication exercises first-run activation.
func TestBootstrapLinkIsSingleUseAndCreatesLocalAuthentication(t *testing.T) {
	var store *db.Store
	var err error
	if store, err = db.Open(filepath.Join(t.TempDir(), "organesson.db"), golog.New(), false); err != nil {
		t.Fatalf("open database: %v", err)
	}
	defer store.Close()

	var service *Service
	if service, err = New(store, ""); err != nil {
		t.Fatalf("create auth service: %v", err)
	}
	var token string
	var created bool
	if token, created, err = service.EnsureInitialActivationLink(); err != nil || !created || token == "" {
		t.Fatalf("create activation link: created=%t token-empty=%t err=%v", created, token == "", err)
	}
	var pendingToken string
	if pendingToken, created, err = service.EnsureInitialActivationLink(); err != nil || created || pendingToken != "" {
		t.Fatalf("pending link must not be printed again: created=%t token-empty=%t err=%v", created, pendingToken == "", err)
	}

	var account *db.Account
	if account, err = service.RedeemPasswordLink(token, "a-strong-local-password"); err != nil {
		t.Fatalf("redeem activation link: %v", err)
	}
	if account.ActivatedAt == nil {
		t.Fatal("administrator was not activated")
	}
	if _, err = service.RedeemPasswordLink(token, "another-strong-password"); !errors.Is(err, ErrInvalidToken) {
		t.Fatalf("replay should be rejected, got %v", err)
	}
	if _, err = service.Authenticate("administrator@organesson", "a-strong-local-password"); err != nil {
		t.Fatalf("authenticate activated administrator: %v", err)
	}
	if _, err = service.Authenticate("administrator@organesson", "wrong-password-value"); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("wrong password should be rejected, got %v", err)
	}
}

// TestLDAPRealmCredentialsAreEncryptedAndLocalRealmCannotLockOutTheOnlyAdmin.
func TestLDAPRealmCredentialsAreEncryptedAndLocalRealmCannotLockOutTheOnlyAdmin(t *testing.T) {
	var store *db.Store
	var err error
	if store, err = db.Open(filepath.Join(t.TempDir(), "organesson.db"), golog.New(), false); err != nil {
		t.Fatalf("open database: %v", err)
	}
	defer store.Close()

	var service *Service
	if service, err = New(store, "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA="); err != nil {
		t.Fatalf("create authentication service: %v", err)
	}
	var realm AuthenticationRealm
	if realm, err = service.CreateLDAPRealm(LDAPRealmInput{
		Alias: "cyber", Enabled: true, BindPassword: "sensitive-bind-password",
		LDAPRealmConfiguration: LDAPRealmConfiguration{
			URL: "ldaps://ipa.cyber.lab:636", BaseDN: "cn=users,cn=accounts,dc=cyber,dc=lab",
			UserFilter: "(uid={username})", UsernameAttribute: "uid", DisplayNameAttribute: "cn",
			BindDN:                      "uid=organesson,cn=users,cn=accounts,dc=cyber,dc=lab",
			SkipCertificateVerification: true,
		},
	}); err != nil {
		t.Fatalf("create LDAP realm: %v", err)
	}
	if realm.Alias != "cyber" || realm.Kind != "ldap" || !realm.Enabled || !realm.HasBindPassword {
		t.Fatalf("unexpected LDAP realm metadata: %#v", realm)
	}
	if !realm.Configuration.SkipCertificateVerification {
		t.Fatal("LDAP certificate verification preference was not retained")
	}
	var providers []*db.AuthenticationProvider
	if providers, err = store.AuthenticationProviders.SelectAll(); err != nil {
		t.Fatalf("load authentication providers: %v", err)
	}
	var storedSecret *db.AuthenticationProvider
	for _, provider := range providers {
		if provider.Alias == "cyber" {
			storedSecret = provider
		}
	}
	if storedSecret == nil || bytes.Contains([]byte(storedSecret.EncryptedSecretsJSON), []byte("sensitive-bind-password")) {
		t.Fatalf("LDAP bind password was stored in plaintext: %#v", storedSecret)
	}
	var recoveredPassword string
	if recoveredPassword, err = service.decryptBindPassword(storedSecret); err != nil || recoveredPassword != "sensitive-bind-password" {
		t.Fatalf("could not recover encrypted LDAP bind password: password=%q err=%v", recoveredPassword, err)
	}
	var realms []string
	if realms, err = service.AuthenticationRealms(); err != nil {
		t.Fatalf("list authentication realms: %v", err)
	}
	if len(realms) != 2 || realms[0] != "cyber" || realms[1] != "organesson" {
		t.Fatalf("unexpected login realms: %v", realms)
	}
	if _, err = service.SetLocalRealmEnabled(false); err == nil {
		t.Fatal("local realm was disabled while it was the only administrator login")
	}
}

// TestPasswordLinkExpirationAndReset checks reset invalidates a pending activation link.
func TestPasswordLinkExpirationAndReset(t *testing.T) {
	var store *db.Store
	var err error
	if store, err = db.Open(filepath.Join(t.TempDir(), "organesson.db"), golog.New(), false); err != nil {
		t.Fatalf("open database: %v", err)
	}
	defer store.Close()

	var service *Service
	if service, err = New(store, ""); err != nil {
		t.Fatalf("create auth service: %v", err)
	}
	var fixedTime time.Time = time.Date(2026, time.September, 30, 12, 0, 0, 0, time.UTC)
	service.now = func() time.Time { return fixedTime }
	var token string
	var created bool
	if token, created, err = service.EnsureInitialActivationLink(); err != nil || !created {
		t.Fatalf("create activation link: created=%t err=%v", created, err)
	}
	fixedTime = fixedTime.Add(tokenLifetime + time.Second)
	if found, findErr := service.findValidPasswordToken(token); findErr != nil || found != nil {
		t.Fatalf("expired link should not resolve: found=%#v err=%v", found, findErr)
	}
	var replacement string
	if replacement, err = service.ResetInitialAdministratorLink(); err != nil {
		t.Fatalf("reset activation link: %v", err)
	}
	if replacement == "" || replacement == token {
		t.Fatal("reset should issue a fresh token")
	}
	if found, findErr := service.findValidPasswordToken(token); findErr != nil || found != nil {
		t.Fatalf("replaced link should not resolve: found=%#v err=%v", found, findErr)
	}
}

// TestLocalAccountProvisioningAndReset protects the administrator-only local account flow.
func TestLocalAccountProvisioningAndReset(t *testing.T) {
	var store *db.Store
	var err error
	if store, err = db.Open(filepath.Join(t.TempDir(), "organesson.db"), golog.New(), false); err != nil {
		t.Fatalf("open database: %v", err)
	}
	defer store.Close()

	var service *Service
	if service, err = New(store, ""); err != nil {
		t.Fatalf("create auth service: %v", err)
	}
	var adminToken string
	var created bool
	if adminToken, created, err = service.EnsureInitialActivationLink(); err != nil || !created {
		t.Fatalf("create administrator activation: created=%t err=%v", created, err)
	}
	var administrator *db.Account
	if administrator, err = service.RedeemPasswordLink(adminToken, "administrator-password-2026"); err != nil {
		t.Fatalf("activate administrator: %v", err)
	}
	if _, err = service.CreateLocalAccount(administrator.ID, "student 1", "Student One"); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("invalid local username should be rejected, got %v", err)
	}
	var setup *LocalAccountSetup
	if setup, err = service.CreateLocalAccount(administrator.ID, "Student.One", "Student One"); err != nil {
		t.Fatalf("create local account: %v", err)
	}
	if setup.QualifiedName != "student.one@organesson" || setup.SetupToken == "" || setup.Account.PlatformAdministrator {
		t.Fatalf("unexpected provisioned account: %#v", setup)
	}
	if _, err = service.CreateLocalAccount(administrator.ID, "student.one", "Duplicate"); !errors.Is(err, ErrConflict) {
		t.Fatalf("duplicate local account should conflict, got %v", err)
	}
	if _, err = service.RedeemPasswordLink(setup.SetupToken, "student-password-2026"); err != nil {
		t.Fatalf("activate local account: %v", err)
	}
	if _, err = service.Authenticate(setup.QualifiedName, "student-password-2026"); err != nil {
		t.Fatalf("authenticate local account: %v", err)
	}
	var resetToken string
	if resetToken, err = service.ResetLocalAccountLink(administrator.ID, setup.Account.ID); err != nil {
		t.Fatalf("create local password reset link: %v", err)
	}
	if _, err = service.RedeemPasswordLink(resetToken, "student-password-reset-2026"); err != nil {
		t.Fatalf("redeem local password reset link: %v", err)
	}
	if _, err = service.Authenticate(setup.QualifiedName, "student-password-reset-2026"); err != nil {
		t.Fatalf("authenticate after local password reset: %v", err)
	}
	if _, err = service.ResetLocalAccountLink(setup.Account.ID, setup.Account.ID); !errors.Is(err, ErrForbidden) {
		t.Fatalf("non-admin password-link reset should be forbidden, got %v", err)
	}
}

// TestAPITokenLifecycleEnsuresBearerSecretsAreExpiringAndRevocable exercises provider auth.
func TestAPITokenLifecycleEnsuresBearerSecretsAreExpiringAndRevocable(t *testing.T) {
	var store *db.Store
	var err error
	if store, err = db.Open(filepath.Join(t.TempDir(), "organesson.db"), golog.New(), false); err != nil {
		t.Fatalf("open database: %v", err)
	}
	defer store.Close()

	var service *Service
	if service, err = New(store, ""); err != nil {
		t.Fatalf("create auth service: %v", err)
	}
	var token string
	var created bool
	if token, created, err = service.EnsureInitialActivationLink(); err != nil || !created {
		t.Fatalf("create activation token: created=%t err=%v", created, err)
	}
	var administrator *db.Account
	if administrator, err = service.RedeemPasswordLink(token, "api-token-test-password"); err != nil {
		t.Fatalf("activate administrator: %v", err)
	}
	var regularSetup *LocalAccountSetup
	if regularSetup, err = service.CreateLocalAccount(administrator.ID, "token-user", "Token User"); err != nil {
		t.Fatalf("create regular token-test account: %v", err)
	}
	var regularAccount *db.Account
	if regularAccount, err = service.RedeemPasswordLink(regularSetup.SetupToken, "regular-token-test-password"); err != nil {
		t.Fatalf("activate regular token-test account: %v", err)
	}
	if _, err = service.CreateAPIToken(regularAccount.ID, regularAccount.ID, "not allowed", 24*time.Hour); !errors.Is(err, ErrForbidden) {
		t.Fatalf("non-administrator should not create API tokens: %v", err)
	}
	var credential *APITokenCredential
	if credential, err = service.CreateAPIToken(administrator.ID, administrator.ID, "tofu acceptance", 24*time.Hour); err != nil {
		t.Fatalf("create API token: %v", err)
	}
	if credential.Secret == "" || len(credential.Token.TokenHash) != 32 {
		t.Fatalf("API token secret or digest missing: %#v", credential)
	}
	if summaries, listErr := service.ListAPITokens(); listErr != nil || len(summaries) != 1 || summaries[0].AccountID != administrator.ID || summaries[0].OwnerQualifiedName != "administrator@organesson" {
		t.Fatalf("API token metadata did not map to its owner: %#v, err=%v", summaries, listErr)
	}
	var tokenRows []*db.APIToken
	if tokenRows, err = store.APITokens.SelectAll(); err != nil {
		t.Fatalf("load API tokens: %v", err)
	}
	if len(tokenRows) != 1 || string(tokenRows[0].TokenHash) == credential.Secret {
		t.Fatalf("expected hashed token storage, found %#v", tokenRows)
	}
	var authenticated *db.Account
	if authenticated, err = service.AuthenticateAPIToken(credential.Secret); err != nil || authenticated.ID != administrator.ID {
		t.Fatalf("authenticate API token: account=%#v err=%v", authenticated, err)
	}
	if err = service.RevokeAPIToken(administrator.ID, credential.Token.ID); err != nil {
		t.Fatalf("revoke API token: %v", err)
	}
	if _, err = service.AuthenticateAPIToken(credential.Secret); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("revoked API token should fail, got %v", err)
	}
	var expiredAt time.Time = service.now().Add(-time.Hour)
	credential.Token.ExpiresAt = &expiredAt
	if err = service.store.APITokens.Update(credential.Token); err != nil {
		t.Fatalf("set token expiry for prune check: %v", err)
	}
	var pruned int
	if pruned, err = service.PruneExpiredAPITokens(administrator.ID); err != nil || pruned != 1 {
		t.Fatalf("prune expired API token: count=%d err=%v", pruned, err)
	}
	var prunedToken *db.APIToken
	if prunedToken, err = service.store.APITokens.Select(credential.Token.ID); err != nil || prunedToken != nil {
		t.Fatalf("expired API token remains after prune: token=%#v err=%v", prunedToken, err)
	}
}

// TestDevelopmentFixtureCreationIsRepeatable checks explicit fake-user seeding behavior.
func TestDevelopmentFixtureCreationIsRepeatable(t *testing.T) {
	var store *db.Store
	var err error
	if store, err = db.Open(filepath.Join(t.TempDir(), "organesson.db"), golog.New(), false); err != nil {
		t.Fatalf("open database: %v", err)
	}
	defer store.Close()
	var service *Service
	if service, err = New(store, ""); err != nil {
		t.Fatalf("create auth service: %v", err)
	}
	var admin *db.Account
	if admin, _, err = store.InitialAdministrator(); err != nil {
		t.Fatalf("load administrator: %v", err)
	}
	var setups []*LocalAccountSetup
	if setups, err = service.CreateDevelopmentTestUsers(admin.ID); err != nil || len(setups) != 4 {
		t.Fatalf("create test identities: count=%d err=%v", len(setups), err)
	}
	if setups, err = service.CreateDevelopmentTestUsers(admin.ID); err != nil || len(setups) != 0 {
		t.Fatalf("repeat should not duplicate test identities: count=%d err=%v", len(setups), err)
	}
	var identities []*db.AccountIdentity
	if identities, err = store.AccountIdentities.SelectAll(); err != nil || len(identities) != 5 {
		t.Fatalf("expected admin plus four test identities, count=%d err=%v", len(identities), err)
	}
}
