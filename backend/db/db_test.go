package db

import (
	"path/filepath"
	"testing"

	"github.com/z46-dev/golog"
)

// TestOpenMigratesSchemaAndSeedsTheAdministratorOnce checks first-run and reopen behavior.
func TestOpenMigratesSchemaAndSeedsTheAdministratorOnce(t *testing.T) {
	var path string = filepath.Join(t.TempDir(), "organesson.db")
	var store *Store
	var err error
	if store, err = Open(path, golog.New(), false); err != nil {
		t.Fatalf("open database: %v", err)
	}
	var account *Account
	var identity *AccountIdentity
	if account, identity, err = store.InitialAdministrator(); err != nil {
		t.Fatalf("find initial administrator: %v", err)
	}
	if account == nil || account.ID < 1 || !account.PlatformAdministrator || account.ActivatedAt != nil {
		t.Fatalf("unexpected initial administrator: %#v", account)
	}
	if identity == nil || identity.QualifiedName != "administrator@organesson" {
		t.Fatalf("unexpected initial identity: %#v", identity)
	}
	if err = store.Close(); err != nil {
		t.Fatalf("close first database: %v", err)
	}

	if store, err = Open(path, golog.New(), false); err != nil {
		t.Fatalf("reopen database: %v", err)
	}
	defer store.Close()
	var accounts []*Account
	if accounts, err = store.Accounts.SelectAll(); err != nil {
		t.Fatalf("select accounts after reopen: %v", err)
	}
	if len(accounts) != 1 {
		t.Fatalf("expected exactly one seeded account, got %d", len(accounts))
	}
	var providers []*AuthenticationProvider
	if providers, err = store.AuthenticationProviders.SelectAll(); err != nil {
		t.Fatalf("select providers after reopen: %v", err)
	}
	if len(providers) != 1 || providers[0].Alias != "organesson" {
		t.Fatalf("expected exactly one built-in provider, got %#v", providers)
	}
}
