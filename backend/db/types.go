package db

import "time"

type (
	AuthenticationProviderKind uint8
	PasswordResetPurpose       uint8
	OwnershipNodeKind          uint8
	GrantSubjectKind           uint8

	// AuthenticationProvider identifies a local or externally managed login source.
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
		ID                    int        `gosqlite:"id,primary,increment"`
		DisplayName           string     `gosqlite:"display_name,notnull"`
		PlatformAdministrator bool       `gosqlite:"platform_administrator,notnull"`
		ActivatedAt           *time.Time `gosqlite:"activated_at"`
		Disabled              bool       `gosqlite:"disabled,notnull"`
		CreatedAt             time.Time  `gosqlite:"created_at,notnull"`
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

	// LocalCredential stores a password hash only for a local-provider identity.
	LocalCredential struct {
		AccountID     int       `gosqlite:"account_id,primary,unique,notnull,fkey:Account.id,ondelete:cascade"`
		PasswordHash  string    `gosqlite:"password_hash,notnull"`
		PasswordSetAt time.Time `gosqlite:"password_set_at,notnull"`
	}

	// PasswordResetToken stores a hash of a single-use activation or reset secret.
	PasswordResetToken struct {
		ID        int                  `gosqlite:"id,primary,increment"`
		AccountID int                  `gosqlite:"account_id,notnull,fkey:Account.id,ondelete:cascade"`
		Purpose   PasswordResetPurpose `gosqlite:"purpose,notnull"`
		TokenHash []byte               `gosqlite:"token_hash,unique,notnull"`
		ExpiresAt time.Time            `gosqlite:"expires_at,notnull"`
		UsedAt    *time.Time           `gosqlite:"used_at"`
		CreatedAt time.Time            `gosqlite:"created_at,notnull"`
	}

	// Deployment is the root container for an independently managed resource set.
	Deployment struct {
		ID          int       `gosqlite:"id,primary,increment" json:"id"`
		Name        string    `gosqlite:"name,unique,notnull" json:"name"`
		Description string    `gosqlite:"description,notnull" json:"description"`
		RootNodeID  *int      `gosqlite:"root_node_id,fkey:OwnershipNode.id,ondelete:cascade" json:"root_node_id"`
		CreatedByID int       `gosqlite:"created_by_id,notnull,fkey:Account.id" json:"created_by_id"`
		CreatedAt   time.Time `gosqlite:"created_at,notnull" json:"created_at"`
	}

	// OwnershipNode is one node in a deployment's single-owner resource tree.
	OwnershipNode struct {
		ID           int               `gosqlite:"id,primary,increment" json:"id"`
		DeploymentID int               `gosqlite:"deployment_id,notnull,fkey:Deployment.id,ondelete:cascade" json:"deployment_id"`
		ParentID     *int              `gosqlite:"parent_id,fkey:OwnershipNode.id,ondelete:cascade" json:"parent_id"`
		Kind         OwnershipNodeKind `gosqlite:"kind,notnull" json:"kind"`
		Name         string            `gosqlite:"name,notnull" json:"name"`
		CreatedAt    time.Time         `gosqlite:"created_at,notnull" json:"created_at"`
	}

	// UserGroup contains deployment-local members and is distinct from logical resource groups.
	UserGroup struct {
		ID           int       `gosqlite:"id,primary,increment"`
		DeploymentID int       `gosqlite:"deployment_id,notnull,fkey:Deployment.id,ondelete:cascade"`
		Name         string    `gosqlite:"name,notnull"`
		CreatedAt    time.Time `gosqlite:"created_at,notnull"`
	}

	// GroupMembership links one local account to one deployment-local user group.
	GroupMembership struct {
		ID        int       `gosqlite:"id,primary,increment"`
		GroupID   int       `gosqlite:"group_id,notnull,fkey:UserGroup.id,ondelete:cascade"`
		AccountID int       `gosqlite:"account_id,notnull,fkey:Account.id,ondelete:cascade"`
		CreatedAt time.Time `gosqlite:"created_at,notnull"`
	}

	// PermissionGrant assigns one fixed Organesson permission to an account or group on a tree node.
	PermissionGrant struct {
		ID           int              `gosqlite:"id,primary,increment"`
		SubjectKind  GrantSubjectKind `gosqlite:"subject_kind,notnull"`
		SubjectID    int              `gosqlite:"subject_id,notnull"`
		Permission   string           `gosqlite:"permission,notnull"`
		TargetNodeID int              `gosqlite:"target_node_id,notnull,fkey:OwnershipNode.id,ondelete:cascade"`
		CreatedByID  int              `gosqlite:"created_by_id,notnull,fkey:Account.id"`
		CreatedAt    time.Time        `gosqlite:"created_at,notnull"`
	}

	// ManagedResource stores a resource whose owner is its unique ownership-tree node.
	ManagedResource struct {
		ID           int       `gosqlite:"id,primary,increment" json:"id"`
		DeploymentID int       `gosqlite:"deployment_id,notnull,fkey:Deployment.id,ondelete:cascade" json:"deployment_id"`
		OwnershipID  int       `gosqlite:"ownership_id,unique,notnull,fkey:OwnershipNode.id,ondelete:cascade" json:"ownership_id"`
		Kind         string    `gosqlite:"kind,notnull" json:"kind"`
		Name         string    `gosqlite:"name,notnull" json:"name"`
		PowerState   string    `gosqlite:"power_state,notnull" json:"power_state"`
		ExternalID   string    `gosqlite:"external_id" json:"external_id,omitempty"`
		CreatedAt    time.Time `gosqlite:"created_at,notnull" json:"created_at"`
	}

	// AuditEvent records security-sensitive operations and their outcomes.
	AuditEvent struct {
		ID             int       `gosqlite:"id,primary,increment"`
		ActorAccountID *int      `gosqlite:"actor_account_id,fkey:Account.id"`
		Action         string    `gosqlite:"action,notnull"`
		Target         string    `gosqlite:"target,notnull"`
		Result         string    `gosqlite:"result,notnull"`
		DetailsJSON    string    `gosqlite:"details_json,notnull"`
		CreatedAt      time.Time `gosqlite:"created_at,notnull"`
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

const (
	OwnershipNodeKindDeployment OwnershipNodeKind = iota
	OwnershipNodeKindGroup
	OwnershipNodeKindResource
)

const (
	GrantSubjectKindAccount GrantSubjectKind = iota
	GrantSubjectKindGroup
)

const (
	PermissionDeploymentView   = "deployment.view"
	PermissionDeploymentManage = "deployment.manage"
	PermissionResourceView     = "resource.view"
	PermissionResourceCreate   = "resource.create"
	PermissionResourcePower    = "vm.power"
	PermissionPermissionManage = "permission.manage"
)
