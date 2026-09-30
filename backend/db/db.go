package db

import (
	"os"
	"slices"

	"github.com/z46-dev/golog"
	"github.com/z46-dev/gosqlite"
)

var (
	log *golog.Logger

	driver *gosqlite.Driver

	AuthenticationProviders *gosqlite.RegisteredStruct[AuthenticationProvider]
	Accounts                *gosqlite.RegisteredStruct[Account]
	AccountIdentities       *gosqlite.RegisteredStruct[AccountIdentity]
	LocalCredentials        *gosqlite.RegisteredStruct[LocalCredential]
	PasswordResetTokens     *gosqlite.RegisteredStruct[PasswordResetToken]
)

func Init(logger *golog.Logger) (err error) {
	log = logger

	var opts gosqlite.MigrationOptions = gosqlite.MigrationOptions{
		AllowDestructive: slices.Contains(os.Args, "--allow-destructive-migrations"),
	}

	if AuthenticationProviders, err = gosqlite.Register(driver, AuthenticationProvider{}); err != nil {
		return
	}

	if Accounts, err = gosqlite.Register(driver, Account{}); err != nil {
		return
	}

	if AccountIdentities, err = gosqlite.Register(driver, AccountIdentity{}); err != nil {
		return
	}

	if LocalCredentials, err = gosqlite.Register(driver, LocalCredential{}); err != nil {
		return
	}

	if PasswordResetTokens, err = gosqlite.Register(driver, PasswordResetToken{}); err != nil {
		return
	}

	if err = migrateTable(AuthenticationProviders, opts); err != nil {
		return
	}

	if err = migrateTable(Accounts, opts); err != nil {
		return
	}

	if err = migrateTable(AccountIdentities, opts); err != nil {
		return
	}

	if err = migrateTable(LocalCredentials, opts); err != nil {
		return
	}

	if err = migrateTable(PasswordResetTokens, opts); err != nil {
		return
	}

	return
}
