package db

import "time"

type (
	AuthenticationProviderKind uint8
	PasswordResetPurpose       uint8

	// AuthenticationProvider is an administrator-configured local, LDAP, or OIDC identity source.
	AuthenticationProvider struct {
		ID                   int                        `gosqlite:"id,primary,increment"`
		Alias                string                     `gosqlite:"alias,unique,notnull"`
		Kind                 AuthenticationProviderKind `gosqlite:"kind,notnull"`
		Enabled              bool                       `gosqlite:"enabled,notnull"`
		SystemManaged        bool                       `gosqlite:"system_managed,notnull"`
		ConfigurationJSON    string                     `gosqlite:"configuration_json,notnull"`
		EncryptedSecretsJSON string                     `gosqlite:"encrypted_secrets_json,notnull"`
	}

	// Account is Organesson's local person record, independent of authentication method.
	Account struct {
		ID          int        `gosqlite:"id,primary,increment"`
		DisplayName string     `gosqlite:"display_name,notnull"`
		ActivatedAt *time.Time `gosqlite:"activated_at"`
		Disabled    bool       `gosqlite:"disabled,notnull"`
		CreatedAt   time.Time  `gosqlite:"created_at,notnull"`
	}

	// AccountIdentity maps an external provider subject to one local account.
	AccountIdentity struct {
		ID                       int       `gosqlite:"id,primary,increment"`
		AccountID                int       `gosqlite:"account_id,notnull,fkey:Account.id,ondelete:cascade"`
		AuthenticationProviderID int       `gosqlite:"authentication_provider_id,notnull,fkey:AuthenticationProvider.id,ondelete:cascade"`
		ProviderSubject          string    `gosqlite:"provider_subject,notnull"`
		ProviderSubjectKey       string    `gosqlite:"provider_subject_key,unique,notnull"`
		QualifiedName            string    `gosqlite:"qualified_name,unique,notnull"`
		CreatedAt                time.Time `gosqlite:"created_at,notnull"`
	}

	// LocalCredential holds an Argon2id password hash only for a local-provider identity.
	LocalCredential struct {
		AccountID     int       `gosqlite:"account_id,primary,unique,notnull,fkey:Account.id,ondelete:cascade"`
		PasswordHash  string    `gosqlite:"password_hash,notnull"`
		PasswordSetAt time.Time `gosqlite:"password_set_at,notnull"`
	}

	// PasswordResetToken stores a hashed, single-use activation or reset token.
	PasswordResetToken struct {
		ID        int                  `gosqlite:"id,primary,increment"`
		AccountID int                  `gosqlite:"account_id,notnull,fkey:Account.id,ondelete:cascade"`
		Purpose   PasswordResetPurpose `gosqlite:"purpose,notnull"`
		TokenHash []byte               `gosqlite:"token_hash,unique,notnull"`
		ExpiresAt time.Time            `gosqlite:"expires_at,notnull"`
		UsedAt    *time.Time           `gosqlite:"used_at"`
		CreatedAt time.Time            `gosqlite:"created_at,notnull"`
	}
)

const (
	AuthenticationProviderKindLocal AuthenticationProviderKind = iota
	AuthenticationProviderKindLDAP
	AuthenticationProviderKindOIDC
)

const (
	PasswordResetPurposeActivation PasswordResetPurpose = iota
	PasswordResetPurposePasswordReset
)
