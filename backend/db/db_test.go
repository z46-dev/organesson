package db

import (
	"path/filepath"
	"testing"
	"time"

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

// TestOpenMigratesTemplateCatalog stores one source VM with multiple selectors.
func TestOpenMigratesTemplateCatalog(t *testing.T) {
	var path string = filepath.Join(t.TempDir(), "organesson.db")
	var store *Store
	var err error
	if store, err = Open(path, golog.New(), false); err != nil {
		t.Fatalf("open database: %v", err)
	}
	defer store.Close()

	var now time.Time = time.Now()
	var template *VMTemplate = &VMTemplate{
		DisplayName:       "Fedora Server",
		Description:       "Fedora server source VM",
		SourcePlatform:    "proxmox",
		SourceID:          "test-source-vm",
		GuestOS:           "Fedora Linux",
		GuestOSVersion:    "",
		Edition:           "server",
		Architecture:      "x86_64",
		ExecutionMethod:   "qemu_guest_agent",
		ProvisioningReady: false,
		CreatedAt:         now,
		UpdatedAt:         now,
	}
	if err = store.VMTemplates.Insert(template); err != nil {
		t.Fatalf("insert VM template: %v", err)
	}

	for _, alias := range []string{"og-template-fedora-server-latest", "fedora-server"} {
		if err = store.VMTemplateAliases.Insert(&VMTemplateAlias{
			VMTemplateID: template.ID,
			Alias:        alias,
			CreatedAt:    now,
		}); err != nil {
			t.Fatalf("insert VM template alias %q: %v", alias, err)
		}
	}

	var aliases []*VMTemplateAlias
	if aliases, err = store.VMTemplateAliases.SelectAll(); err != nil {
		t.Fatalf("select VM template aliases: %v", err)
	}
	if len(aliases) != 2 || aliases[0].VMTemplateID != template.ID || aliases[1].VMTemplateID != template.ID {
		t.Fatalf("expected two aliases for template %d, got %#v", template.ID, aliases)
	}
}

// TestOpenAddsPlatformPermissionStorageWithoutLosingExistingDeploymentData checks additive migration safety.
func TestOpenAddsPlatformPermissionStorageWithoutLosingExistingDeploymentData(t *testing.T) {
	var path string = filepath.Join(t.TempDir(), "organesson.db")
	var store *Store
	var err error
	if store, err = Open(path, golog.New(), false); err != nil {
		t.Fatalf("open database: %v", err)
	}
	var administrator *Account
	if administrator, _, err = store.InitialAdministrator(); err != nil {
		t.Fatalf("load administrator: %v", err)
	}
	var deployment *Deployment = &Deployment{Name: "existing-lab", CreatedByID: administrator.ID, CreatedAt: time.Now()}
	if err = store.Deployments.Insert(deployment); err != nil {
		t.Fatalf("insert existing deployment: %v", err)
	}
	var root *OwnershipNode = &OwnershipNode{DeploymentID: deployment.ID, Kind: OwnershipNodeKindDeployment, Name: deployment.Name, CreatedAt: time.Now()}
	if err = store.OwnershipNodes.Insert(root); err != nil {
		t.Fatalf("insert existing ownership root: %v", err)
	}
	deployment.RootNodeID = &root.ID
	if err = store.Deployments.Update(deployment); err != nil {
		t.Fatalf("link existing ownership root: %v", err)
	}
	var grant *PermissionGrant = &PermissionGrant{
		SubjectKind: GrantSubjectKindAccount, SubjectID: administrator.ID,
		Permission: PermissionDeploymentView, InheritDescendants: true,
		TargetNodeID: root.ID, CreatedByID: administrator.ID, CreatedAt: time.Now(),
	}
	if err = store.PermissionGrants.Insert(grant); err != nil {
		t.Fatalf("insert existing permission grant: %v", err)
	}
	if err = store.PlatformPermissionGrants.Insert(&PlatformPermissionGrant{
		AccountID: administrator.ID, Permission: PlatformPermissionDeploymentCreate,
		GrantedByID: administrator.ID, CreatedAt: time.Now(),
	}); err != nil {
		t.Fatalf("insert platform permission: %v", err)
	}
	if err = store.Close(); err != nil {
		t.Fatalf("close database before reopen: %v", err)
	}
	if store, err = Open(path, golog.New(), false); err != nil {
		t.Fatalf("reopen database with additive migration: %v", err)
	}
	defer store.Close()
	var loadedDeployment *Deployment
	if loadedDeployment, err = store.Deployments.Select(deployment.ID); err != nil || loadedDeployment == nil || loadedDeployment.Name != deployment.Name {
		t.Fatalf("existing deployment was not preserved: deployment=%#v err=%v", loadedDeployment, err)
	}
	var loadedGrant *PermissionGrant
	if loadedGrant, err = store.PermissionGrants.Select(grant.ID); err != nil || loadedGrant == nil || loadedGrant.Permission != PermissionDeploymentView {
		t.Fatalf("existing permission grant was not preserved: grant=%#v err=%v", loadedGrant, err)
	}
	var permissions []*PlatformPermissionGrant
	if permissions, err = store.PlatformPermissionGrants.SelectAll(); err != nil || len(permissions) != 1 || permissions[0].Permission != PlatformPermissionDeploymentCreate {
		t.Fatalf("platform permission storage did not persist: permissions=%#v err=%v", permissions, err)
	}
}

// TestOpenMigratesProxmoxPolicy supports the singleton admin policy across process restarts.
func TestOpenMigratesProxmoxPolicy(t *testing.T) {
	var path string = filepath.Join(t.TempDir(), "organesson.db")
	var store *Store
	var err error
	if store, err = Open(path, golog.New(), false); err != nil {
		t.Fatalf("open database: %v", err)
	}
	var policy *ProxmoxResourcePolicy = &ProxmoxResourcePolicy{ID: 1, ConfigurationJSON: `{"resource_pools":["lab"]}`, ValidationJSON: `{"valid":true}`, ValidatedConfigHash: "abc", UpdatedAt: time.Now()}
	if err = store.ProxmoxResourcePolicies.Insert(policy); err != nil {
		t.Fatalf("insert Proxmox resource policy: %v", err)
	}
	if err = store.Close(); err != nil {
		t.Fatalf("close database: %v", err)
	}
	if store, err = Open(path, golog.New(), false); err != nil {
		t.Fatalf("reopen database: %v", err)
	}
	defer store.Close()
	var loaded *ProxmoxResourcePolicy
	if loaded, err = store.ProxmoxResourcePolicies.Select(1); err != nil {
		t.Fatalf("read Proxmox resource policy: %v", err)
	}
	if loaded == nil || loaded.ValidatedConfigHash != "abc" {
		t.Fatalf("resource policy did not persist: %#v", loaded)
	}
}
