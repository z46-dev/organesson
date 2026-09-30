package auth

import (
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
	if service, err = New(store); err != nil {
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

// TestPasswordLinkExpirationAndReset checks reset invalidates a pending activation link.
func TestPasswordLinkExpirationAndReset(t *testing.T) {
	var store *db.Store
	var err error
	if store, err = db.Open(filepath.Join(t.TempDir(), "organesson.db"), golog.New(), false); err != nil {
		t.Fatalf("open database: %v", err)
	}
	defer store.Close()

	var service *Service
	if service, err = New(store); err != nil {
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
	if service, err = New(store); err != nil {
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
