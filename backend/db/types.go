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

	// APIToken stores only a digest of an opaque non-browser API credential.
	APIToken struct {
		ID         int        `gosqlite:"id,primary,increment"`
		AccountID  int        `gosqlite:"account_id,notnull,fkey:Account.id,ondelete:cascade"`
		Name       string     `gosqlite:"name,notnull"`
		Prefix     string     `gosqlite:"prefix,notnull"`
		TokenHash  []byte     `gosqlite:"token_hash,unique,notnull"`
		CreatedAt  time.Time  `gosqlite:"created_at,notnull"`
		ExpiresAt  *time.Time `gosqlite:"expires_at"`
		LastUsedAt *time.Time `gosqlite:"last_used_at"`
		RevokedAt  *time.Time `gosqlite:"revoked_at"`
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
		ID           int       `gosqlite:"id,primary,increment" json:"id"`
		DeploymentID int       `gosqlite:"deployment_id,notnull,fkey:Deployment.id,ondelete:cascade" json:"deployment_id"`
		Name         string    `gosqlite:"name,notnull" json:"name"`
		CreatedAt    time.Time `gosqlite:"created_at,notnull" json:"created_at"`
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
		ID                 int              `gosqlite:"id,primary,increment" json:"id"`
		SubjectKind        GrantSubjectKind `gosqlite:"subject_kind,notnull" json:"subject_kind"`
		SubjectID          int              `gosqlite:"subject_id,notnull" json:"subject_id"`
		Permission         string           `gosqlite:"permission,notnull" json:"permission"`
		InheritDescendants bool             `gosqlite:"inherit_descendants,notnull" json:"inherit_descendants"`
		TargetNodeID       int              `gosqlite:"target_node_id,notnull,fkey:OwnershipNode.id,ondelete:cascade" json:"target_node_id"`
		CreatedByID        int              `gosqlite:"created_by_id,notnull,fkey:Account.id" json:"created_by_id"`
		CreatedAt          time.Time        `gosqlite:"created_at,notnull" json:"created_at"`
	}

	// ManagedResource stores a resource whose owner is its unique ownership-tree node.
	ManagedResource struct {
		ID                int       `gosqlite:"id,primary,increment" json:"id"`
		DeploymentID      int       `gosqlite:"deployment_id,notnull,fkey:Deployment.id,ondelete:cascade" json:"deployment_id"`
		OwnershipID       int       `gosqlite:"ownership_id,unique,notnull,fkey:OwnershipNode.id,ondelete:cascade" json:"ownership_id"`
		Kind              string    `gosqlite:"kind,notnull" json:"kind"`
		Name              string    `gosqlite:"name,notnull" json:"name"`
		PowerState        string    `gosqlite:"power_state,notnull" json:"power_state"`
		ExternalID        string    `gosqlite:"external_id" json:"external_id,omitempty"`
		ExternalNode      string    `gosqlite:"external_node" json:"external_node,omitempty"`
		OperationKey      string    `gosqlite:"operation_key" json:"-"`
		ConfigurationJSON string    `gosqlite:"configuration_json" json:"-"`
		CreatedAt         time.Time `gosqlite:"created_at,notnull" json:"created_at"`
	}

	// ManagedVMSnapshot records an Organesson-owned snapshot and its reserved capacity.
	ManagedVMSnapshot struct {
		ID          int       `gosqlite:"id,primary,increment" json:"id"`
		ResourceID  int       `gosqlite:"resource_id,notnull,fkey:ManagedResource.id,ondelete:cascade" json:"resource_id"`
		SnapshotKey string    `gosqlite:"snapshot_key,unique,notnull" json:"-"`
		Name        string    `gosqlite:"name,notnull" json:"name"`
		Description string    `gosqlite:"description,notnull" json:"description"`
		ReservedGiB int       `gosqlite:"reserved_gib,notnull" json:"reserved_gib"`
		State       string    `gosqlite:"state,notnull" json:"state"`
		CreatedAt   time.Time `gosqlite:"created_at,notnull" json:"created_at"`
	}

	// VMTemplate is an administrator-managed source VM that can be selected for provisioning.
	VMTemplate struct {
		ID                         int        `gosqlite:"id,primary,increment" json:"id"`
		DisplayName                string     `gosqlite:"display_name,notnull" json:"display_name"`
		Description                string     `gosqlite:"description,notnull" json:"description"`
		SourcePlatform             string     `gosqlite:"source_platform,notnull" json:"source_platform"`
		SourceID                   string     `gosqlite:"source_id,unique,notnull" json:"source_id"`
		GuestOS                    string     `gosqlite:"guest_os,notnull" json:"guest_os"`
		GuestOSVersion             string     `gosqlite:"guest_os_version,notnull" json:"guest_os_version"`
		Edition                    string     `gosqlite:"edition,notnull" json:"edition"`
		Architecture               string     `gosqlite:"architecture,notnull" json:"architecture"`
		ExecutionMethod            string     `gosqlite:"execution_method,notnull" json:"execution_method"`
		ProvisioningReady          bool       `gosqlite:"provisioning_ready,notnull" json:"provisioning_ready"`
		GuestAgentRootVerified     bool       `gosqlite:"guest_agent_root_verified,notnull" json:"guest_agent_root_verified"`
		ProvisioningAccountRemoved bool       `gosqlite:"provisioning_account_removed,notnull" json:"provisioning_account_removed"`
		LastPreflightAt            *time.Time `gosqlite:"last_preflight_at" json:"last_preflight_at,omitempty"`
		LastPreflightJSON          string     `gosqlite:"last_preflight_json,notnull" json:"last_preflight_json"`
		CreatedAt                  time.Time  `gosqlite:"created_at,notnull" json:"created_at"`
		UpdatedAt                  time.Time  `gosqlite:"updated_at,notnull" json:"updated_at"`
	}

	// VMTemplateAlias maps a stable selector to one source VM catalog record.
	VMTemplateAlias struct {
		ID           int       `gosqlite:"id,primary,increment" json:"id"`
		VMTemplateID int       `gosqlite:"vm_template_id,notnull,fkey:VMTemplate.id,ondelete:cascade" json:"vm_template_id"`
		Alias        string    `gosqlite:"alias,unique,notnull" json:"alias"`
		CreatedAt    time.Time `gosqlite:"created_at,notnull" json:"created_at"`
	}

	// ProxmoxResourcePolicy is the singleton administrator-defined capacity and allocation policy.
	ProxmoxResourcePolicy struct {
		ID                  int        `gosqlite:"id,primary,increment"`
		ConfigurationJSON   string     `gosqlite:"configuration_json,notnull"`
		ValidationJSON      string     `gosqlite:"validation_json,notnull"`
		ValidatedConfigHash string     `gosqlite:"validated_config_hash,notnull"`
		ValidatedAt         *time.Time `gosqlite:"validated_at"`
		UpdatedAt           time.Time  `gosqlite:"updated_at,notnull"`
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
	OwnershipNodeKindInternal
)

const (
	GrantSubjectKindAccount GrantSubjectKind = iota
	GrantSubjectKindGroup
)

const (
	PermissionDeploymentView              = "deployment.view"
	PermissionDeploymentManage            = "deployment.manage_configuration"
	PermissionDeploymentManageGroups      = "deployment.manage_groups"
	PermissionDeploymentManagePermissions = "deployment.manage_permissions"
	PermissionDeploymentManageUsers       = "deployment.manage_users"
	PermissionResourceView                = "resource.view"
	PermissionResourceCreate              = "resource.create"
	PermissionResourcePower               = "vm.power_control"
	PermissionVMConsole                   = "vm.console_control"
	PermissionVMSnapshot                  = "vm.snapshot_control"
	PermissionPermissionManage            = PermissionDeploymentManagePermissions
)

// PermissionCatalog is the fixed set accepted by both the API and provider.
var PermissionCatalog = []string{
	PermissionDeploymentView,
	PermissionDeploymentManage,
	PermissionDeploymentManageGroups,
	PermissionDeploymentManagePermissions,
	PermissionDeploymentManageUsers,
	PermissionResourceView,
	PermissionResourceCreate,
	PermissionResourcePower,
	PermissionVMConsole,
	PermissionVMSnapshot,
}
