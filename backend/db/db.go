package db

import (
	"errors"
	"time"

	"github.com/z46-dev/golog"
	"github.com/z46-dev/gosqlite"
)

type (
	// Store owns the SQLite driver and all registered application tables.
	Store struct {
		logger                  *golog.Logger
		driver                  *gosqlite.Driver
		AuthenticationProviders *gosqlite.RegisteredStruct[AuthenticationProvider]
		Accounts                *gosqlite.RegisteredStruct[Account]
		AccountIdentities       *gosqlite.RegisteredStruct[AccountIdentity]
		LocalCredentials        *gosqlite.RegisteredStruct[LocalCredential]
		PasswordResetTokens     *gosqlite.RegisteredStruct[PasswordResetToken]
		Deployments             *gosqlite.RegisteredStruct[Deployment]
		OwnershipNodes          *gosqlite.RegisteredStruct[OwnershipNode]
		UserGroups              *gosqlite.RegisteredStruct[UserGroup]
		GroupMemberships        *gosqlite.RegisteredStruct[GroupMembership]
		PermissionGrants        *gosqlite.RegisteredStruct[PermissionGrant]
		ManagedResources        *gosqlite.RegisteredStruct[ManagedResource]
		AuditEvents             *gosqlite.RegisteredStruct[AuditEvent]
	}
)

// Open creates the SQLite connection, registers the schema, and applies safe migrations.
func Open(path string, logger *golog.Logger, allowDestructiveMigrations bool) (store *Store, err error) {
	store = &Store{logger: logger}
	if store.driver, err = gosqlite.Begin(path); err != nil {
		return nil, err
	}

	if err = store.registerTables(); err != nil {
		_ = store.driver.Close()
		return nil, err
	}

	var options gosqlite.MigrationOptions = gosqlite.MigrationOptions{AllowDestructive: allowDestructiveMigrations}

	for _, migrate := range store.migrations() {
		if err = migrate(options); err != nil {
			_ = store.driver.Close()
			return nil, err
		}
	}

	if err = store.ensureInitialAdministrator(); err != nil {
		_ = store.driver.Close()
		return nil, err
	}

	return
}

// Close releases the SQLite connection owned by the store.
func (store *Store) Close() (err error) {
	if store == nil || store.driver == nil {
		return
	}

	err = store.driver.Close()
	return
}

// InitialAdministrator returns the built-in administrator and its qualified login name.
func (store *Store) InitialAdministrator() (account *Account, identity *AccountIdentity, err error) {
	var identities []*AccountIdentity
	if identities, err = store.AccountIdentities.SelectAll(); err != nil {
		return
	}

	for _, candidate := range identities {
		if candidate.QualifiedName == "administrator@organesson" {
			identity = candidate
			if account, err = store.Accounts.Select(candidate.AccountID); err != nil {
				return
			}
			return
		}
	}

	err = errors.New("initial administrator identity is missing")
	return
}

// ensureInitialAdministrator creates the reserved local provider and pending administrator once.
func (store *Store) ensureInitialAdministrator() (err error) {
	var providers []*AuthenticationProvider
	if providers, err = store.AuthenticationProviders.SelectAll(); err != nil {
		return
	}

	var localProvider *AuthenticationProvider
	for _, provider := range providers {
		if provider.Alias == "organesson" {
			localProvider = provider
			break
		}
	}

	if localProvider == nil {
		localProvider = &AuthenticationProvider{
			Alias:                "organesson",
			Kind:                 AuthenticationProviderKindLocal,
			Enabled:              true,
			SystemManaged:        true,
			ConfigurationJSON:    "{}",
			EncryptedSecretsJSON: "{}",
		}
		if err = store.AuthenticationProviders.Insert(localProvider); err != nil {
			return
		}
	}
	if localProvider.Kind != AuthenticationProviderKindLocal || !localProvider.Enabled || !localProvider.SystemManaged {
		err = errors.New("reserved organesson authentication provider has invalid settings")
		return
	}

	var identities []*AccountIdentity
	if identities, err = store.AccountIdentities.SelectAll(); err != nil {
		return
	}

	for _, identity := range identities {
		if identity.QualifiedName == "administrator@organesson" {
			return
		}
	}

	var administrator *Account = &Account{
		DisplayName:           "Administrator",
		PlatformAdministrator: true,
		CreatedAt:             time.Now(),
	}
	if err = store.Accounts.Insert(administrator); err != nil {
		return
	}

	var identity *AccountIdentity = &AccountIdentity{
		AccountID:                administrator.ID,
		AuthenticationProviderID: localProvider.ID,
		ProviderSubject:          "administrator",
		ProviderSubjectKey:       "organesson:administrator",
		QualifiedName:            "administrator@organesson",
		CreatedAt:                time.Now(),
	}
	err = store.AccountIdentities.Insert(identity)
	return
}

// registerTables registers each model before migrations use it.
func (store *Store) registerTables() (err error) {
	if store.AuthenticationProviders, err = gosqlite.Register(store.driver, AuthenticationProvider{}); err != nil {
		return
	}
	if store.Accounts, err = gosqlite.Register(store.driver, Account{}); err != nil {
		return
	}
	if store.AccountIdentities, err = gosqlite.Register(store.driver, AccountIdentity{}); err != nil {
		return
	}
	if store.LocalCredentials, err = gosqlite.Register(store.driver, LocalCredential{}); err != nil {
		return
	}
	if store.PasswordResetTokens, err = gosqlite.Register(store.driver, PasswordResetToken{}); err != nil {
		return
	}
	if store.Deployments, err = gosqlite.Register(store.driver, Deployment{}); err != nil {
		return
	}
	if store.OwnershipNodes, err = gosqlite.Register(store.driver, OwnershipNode{}); err != nil {
		return
	}
	if store.UserGroups, err = gosqlite.Register(store.driver, UserGroup{}); err != nil {
		return
	}
	if store.GroupMemberships, err = gosqlite.Register(store.driver, GroupMembership{}); err != nil {
		return
	}
	if store.PermissionGrants, err = gosqlite.Register(store.driver, PermissionGrant{}); err != nil {
		return
	}
	if store.ManagedResources, err = gosqlite.Register(store.driver, ManagedResource{}); err != nil {
		return
	}
	if store.AuditEvents, err = gosqlite.Register(store.driver, AuditEvent{}); err != nil {
		return
	}
	return
}

// migrations returns migrations in foreign-key dependency order.
func (store *Store) migrations() (migrations []func(gosqlite.MigrationOptions) error) {
	migrations = []func(gosqlite.MigrationOptions) error{
		func(options gosqlite.MigrationOptions) (err error) {
			return migrateTable(store.AuthenticationProviders, options, store.logger)
		},
		func(options gosqlite.MigrationOptions) (err error) {
			return migrateTable(store.Accounts, options, store.logger)
		},
		func(options gosqlite.MigrationOptions) (err error) {
			return migrateTable(store.AccountIdentities, options, store.logger)
		},
		func(options gosqlite.MigrationOptions) (err error) {
			return migrateTable(store.LocalCredentials, options, store.logger)
		},
		func(options gosqlite.MigrationOptions) (err error) {
			return migrateTable(store.PasswordResetTokens, options, store.logger)
		},
		func(options gosqlite.MigrationOptions) (err error) {
			return migrateTable(store.Deployments, options, store.logger)
		},
		func(options gosqlite.MigrationOptions) (err error) {
			return migrateTable(store.OwnershipNodes, options, store.logger)
		},
		func(options gosqlite.MigrationOptions) (err error) {
			return migrateTable(store.UserGroups, options, store.logger)
		},
		func(options gosqlite.MigrationOptions) (err error) {
			return migrateTable(store.GroupMemberships, options, store.logger)
		},
		func(options gosqlite.MigrationOptions) (err error) {
			return migrateTable(store.PermissionGrants, options, store.logger)
		},
		func(options gosqlite.MigrationOptions) (err error) {
			return migrateTable(store.ManagedResources, options, store.logger)
		},
		func(options gosqlite.MigrationOptions) (err error) {
			return migrateTable(store.AuditEvents, options, store.logger)
		},
	}
	return
}
