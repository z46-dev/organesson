package domain

import (
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/z46-dev/golog"
	"github.com/z46-dev/organesson/backend/db"
	"github.com/z46-dev/organesson/backend/proxmox"
)

// TestInheritedGroupGrantsScopeViewsAndVMOperations exercises the core access path.
func TestInheritedGroupGrantsScopeViewsAndVMOperations(t *testing.T) {
	var store *db.Store
	var err error
	if store, err = db.Open(filepath.Join(t.TempDir(), "organesson.db"), golog.New(), false); err != nil {
		t.Fatalf("open database: %v", err)
	}
	defer store.Close()

	var service *Service = New(store)
	var admin *db.Account
	if admin, _, err = store.InitialAdministrator(); err != nil {
		t.Fatalf("get administrator: %v", err)
	}
	var student *db.Account = &db.Account{DisplayName: "Student", ActivatedAt: timePointer(time.Now()), CreatedAt: time.Now()}
	if err = store.Accounts.Insert(student); err != nil {
		t.Fatalf("insert student: %v", err)
	}
	var outsider *db.Account = &db.Account{DisplayName: "Outsider", ActivatedAt: timePointer(time.Now()), CreatedAt: time.Now()}
	if err = store.Accounts.Insert(outsider); err != nil {
		t.Fatalf("insert outsider: %v", err)
	}
	var deploymentViewer *db.Account = &db.Account{DisplayName: "Deployment viewer", ActivatedAt: timePointer(time.Now()), CreatedAt: time.Now()}
	if err = store.Accounts.Insert(deploymentViewer); err != nil {
		t.Fatalf("insert deployment viewer: %v", err)
	}

	var deployment *db.Deployment
	if deployment, err = service.CreateDeployment(admin.ID, "class-lab", "A test class lab"); err != nil {
		t.Fatalf("create deployment: %v", err)
	}
	var studentOwner *db.OwnershipNode
	if studentOwner, err = service.CreateLogicalGroup(admin.ID, deployment.ID, *deployment.RootNodeID, "student-lab"); err != nil {
		t.Fatalf("create student ownership group: %v", err)
	}
	var otherOwner *db.OwnershipNode
	if otherOwner, err = service.CreateLogicalGroup(admin.ID, deployment.ID, *deployment.RootNodeID, "other-lab"); err != nil {
		t.Fatalf("create other ownership group: %v", err)
	}
	var studentVM, otherVM *db.ManagedResource
	if studentVM, err = service.CreateVirtualMachine(admin.ID, deployment.ID, studentOwner.ID, "student-fedora"); err != nil {
		t.Fatalf("create student VM: %v", err)
	}
	if otherVM, err = service.CreateVirtualMachine(admin.ID, deployment.ID, otherOwner.ID, "other-fedora"); err != nil {
		t.Fatalf("create other VM: %v", err)
	}

	var accessGroup *db.UserGroup
	if accessGroup, err = service.CreateUserGroup(admin.ID, deployment.ID, "students"); err != nil {
		t.Fatalf("create user group: %v", err)
	}
	if err = service.AddGroupMember(admin.ID, accessGroup.ID, student.ID); err != nil {
		t.Fatalf("add student to group: %v", err)
	}
	if _, err = service.CreatePermissionGrant(admin.ID, db.GrantSubjectKindGroup, accessGroup.ID, db.PermissionResourceView, studentOwner.ID); err != nil {
		t.Fatalf("grant inherited view: %v", err)
	}
	if _, err = service.CreatePermissionGrant(admin.ID, db.GrantSubjectKindGroup, accessGroup.ID, db.PermissionResourcePower, studentOwner.ID); err != nil {
		t.Fatalf("grant inherited power: %v", err)
	}
	if _, err = service.CreatePermissionGrant(admin.ID, db.GrantSubjectKindGroup, accessGroup.ID, db.PermissionVMSnapshot, studentOwner.ID); err != nil {
		t.Fatalf("grant inherited snapshot control: %v", err)
	}
	if _, err = service.CreatePermissionGrant(admin.ID, db.GrantSubjectKindGroup, accessGroup.ID, db.PermissionVMConsole, studentOwner.ID); err != nil {
		t.Fatalf("grant inherited console control: %v", err)
	}
	if _, err = service.CreatePermissionGrant(admin.ID, db.GrantSubjectKindAccount, deploymentViewer.ID, db.PermissionDeploymentView, *deployment.RootNodeID); err != nil {
		t.Fatalf("grant deployment view: %v", err)
	}

	var allowed bool
	if allowed, err = service.Can(student.ID, db.PermissionResourcePower, studentVM.OwnershipID); err != nil || !allowed {
		t.Fatalf("student should inherit VM power grant: allowed=%t err=%v", allowed, err)
	}
	if allowed, err = service.Can(student.ID, db.PermissionResourcePower, otherVM.OwnershipID); err != nil || allowed {
		t.Fatalf("student should not access another lab: allowed=%t err=%v", allowed, err)
	}
	if err = service.Require(outsider.ID, db.PermissionResourcePower, studentVM.OwnershipID); !errors.Is(err, ErrForbidden) {
		t.Fatalf("ungranted account should be denied: %v", err)
	}

	var summary *DeploymentSummary
	if summary, err = service.GetDeployment(student.ID, deployment.ID); err != nil {
		t.Fatalf("student deployment view: %v", err)
	}
	if len(summary.Resources) != 1 || summary.Resources[0].ID != studentVM.ID {
		t.Fatalf("deployment view should only include granted resource: %#v", summary.Resources)
	}
	if !summary.Resources[0].CanSnapshotControl || !summary.Resources[0].CanConsoleControl {
		t.Fatalf("VM summary did not expose the granted controls: %#v", summary.Resources[0])
	}
	if len(summary.OwnershipNodes) != 3 {
		t.Fatalf("deployment view should include only the visible resource and its ownership ancestors: %#v", summary.OwnershipNodes)
	}
	var networkNode *db.OwnershipNode = &db.OwnershipNode{DeploymentID: deployment.ID, ParentID: deployment.RootNodeID, Kind: db.OwnershipNodeKindResource, Name: "isolated-net", CreatedAt: time.Now()}
	if err = store.OwnershipNodes.Insert(networkNode); err != nil {
		t.Fatalf("insert network ownership node: %v", err)
	}
	var networkConfig []byte
	if networkConfig, err = json.Marshal(ManagedNetworkConfiguration{Request: proxmox.SDNNetworkRequest{Name: "isolated-net", Mode: "isolated", EgressPolicy: "none"}, Placement: proxmox.SDNNetworkPlacement{Zone: "og-zone", VNet: "og-vnet"}}); err != nil {
		t.Fatalf("encode network config: %v", err)
	}
	var network *db.ManagedResource = &db.ManagedResource{DeploymentID: deployment.ID, OwnershipID: networkNode.ID, Kind: "virtual_network", Name: networkNode.Name, PowerState: "ready", ConfigurationJSON: string(networkConfig), CreatedAt: time.Now()}
	if err = store.ManagedResources.Insert(network); err != nil {
		t.Fatalf("insert network resource: %v", err)
	}
	if _, configuration, getErr := service.GetSDNNetwork(deploymentViewer.ID, network.ID); getErr != nil || configuration.Request.Name != "isolated-net" {
		t.Fatalf("deployment viewer could not open network details: config=%#v err=%v", configuration, getErr)
	}
	for _, node := range summary.OwnershipNodes {
		if node.ID == otherOwner.ID {
			t.Fatalf("deployment view leaked another student's ownership group: %#v", summary.OwnershipNodes)
		}
	}
	var changed *db.ManagedResource
	if changed, err = service.SetVirtualMachinePower(student.ID, studentVM.ID, "start"); err != nil {
		t.Fatalf("permitted VM start: %v", err)
	}
	if changed.PowerState != "running" {
		t.Fatalf("expected running state, got %q", changed.PowerState)
	}
	if _, err = service.SetVirtualMachinePower(outsider.ID, studentVM.ID, "stop"); !errors.Is(err, ErrForbidden) {
		t.Fatalf("ungranted VM power action should be denied, got %v", err)
	}
	if _, err = service.CreatePermissionGrant(admin.ID, db.GrantSubjectKindAccount, student.ID, "vm.root_shell", studentOwner.ID); !errors.Is(err, ErrInvalidPermission) {
		t.Fatalf("arbitrary permission should be rejected, got %v", err)
	}
	var grants []*db.PermissionGrant
	if grants, err = store.PermissionGrants.SelectAll(); err != nil {
		t.Fatalf("read permission grants: %v", err)
	}
	for _, grant := range grants {
		if grant.Permission == db.PermissionResourcePower {
			if err = service.DeletePermissionGrant(admin.ID, grant.ID); err != nil {
				t.Fatalf("revoke power grant: %v", err)
			}
			break
		}
	}
	if err = service.Require(student.ID, db.PermissionResourcePower, studentVM.OwnershipID); !errors.Is(err, ErrForbidden) {
		t.Fatalf("revoked power grant should no longer apply: %v", err)
	}
	if err = service.RemoveGroupMember(admin.ID, accessGroup.ID, student.ID); err != nil {
		t.Fatalf("remove student from group: %v", err)
	}
	if allowed, err = service.Can(student.ID, db.PermissionResourceView, studentVM.OwnershipID); err != nil || allowed {
		t.Fatalf("removed student should lose inherited view: allowed=%t err=%v", allowed, err)
	}

	var events []*db.AuditEvent
	if events, err = store.AuditEvents.SelectAll(); err != nil {
		t.Fatalf("read audit events: %v", err)
	}
	if len(events) < 5 {
		t.Fatalf("expected audited resource and power operations, got %d events", len(events))
	}
}

// TestDeploymentAccessSeparatesManagersFromDeploymentAdministrators verifies the access workspace boundary.
func TestDeploymentAccessSeparatesManagersFromDeploymentAdministrators(t *testing.T) {
	var store *db.Store
	var err error
	if store, err = db.Open(filepath.Join(t.TempDir(), "organesson.db"), golog.New(), false); err != nil {
		t.Fatalf("open database: %v", err)
	}
	defer store.Close()

	var service *Service = New(store)
	var admin *db.Account
	if admin, _, err = store.InitialAdministrator(); err != nil {
		t.Fatalf("get administrator: %v", err)
	}
	var manager *db.Account = &db.Account{DisplayName: "Manager", ActivatedAt: timePointer(time.Now()), CreatedAt: time.Now()}
	if err = store.Accounts.Insert(manager); err != nil {
		t.Fatalf("insert manager: %v", err)
	}
	var charlie *db.Account = &db.Account{DisplayName: "Charlie", ActivatedAt: timePointer(time.Now()), CreatedAt: time.Now()}
	if err = store.Accounts.Insert(charlie); err != nil {
		t.Fatalf("insert Charlie: %v", err)
	}
	var permissionObserver *db.Account = &db.Account{DisplayName: "Permission observer", ActivatedAt: timePointer(time.Now()), CreatedAt: time.Now()}
	if err = store.Accounts.Insert(permissionObserver); err != nil {
		t.Fatalf("insert permission observer: %v", err)
	}
	var localIdentity *db.AccountIdentity
	if _, localIdentity, err = store.InitialAdministrator(); err != nil {
		t.Fatalf("get local identity provider: %v", err)
	}
	for _, account := range []*db.Account{manager, charlie, permissionObserver} {
		var username string = strings.ToLower(account.DisplayName)
		if err = store.AccountIdentities.Insert(&db.AccountIdentity{
			AccountID: account.ID, AuthenticationProviderID: localIdentity.AuthenticationProviderID,
			ProviderSubject: username, ProviderSubjectKey: "organesson:" + username,
			QualifiedName: username + "@organesson", CreatedAt: time.Now(),
		}); err != nil {
			t.Fatalf("insert %s identity: %v", username, err)
		}
	}
	var deployment *db.Deployment
	if deployment, err = service.CreateDeployment(admin.ID, "managed-class", ""); err != nil {
		t.Fatalf("create deployment: %v", err)
	}
	var group *db.UserGroup
	if group, err = service.CreateUserGroup(admin.ID, deployment.ID, "students"); err != nil {
		t.Fatalf("create user group: %v", err)
	}
	if err = service.AddGroupMember(admin.ID, group.ID, charlie.ID); err != nil {
		t.Fatalf("add Charlie to group: %v", err)
	}
	if _, err = service.CreatePermissionGrant(admin.ID, db.GrantSubjectKindAccount, manager.ID, db.PermissionDeploymentManagePermissions, *deployment.RootNodeID); err != nil {
		t.Fatalf("grant manager permission-management access: %v", err)
	}
	if _, err = service.CreatePermissionGrant(admin.ID, db.GrantSubjectKindAccount, manager.ID, db.PermissionDeploymentManageGroups, *deployment.RootNodeID); err != nil {
		t.Fatalf("grant manager group-management access: %v", err)
	}
	if _, err = service.CreatePermissionGrant(admin.ID, db.GrantSubjectKindAccount, manager.ID, db.PermissionResourceView, *deployment.RootNodeID); err != nil {
		t.Fatalf("grant manager resource view: %v", err)
	}
	if _, err = service.CreatePermissionGrant(admin.ID, db.GrantSubjectKindAccount, permissionObserver.ID, db.PermissionDeploymentManagePermissions, *deployment.RootNodeID); err != nil {
		t.Fatalf("grant permission observer access: %v", err)
	}
	var protectedGrant *db.PermissionGrant
	if protectedGrant, err = service.CreatePermissionGrant(admin.ID, db.GrantSubjectKindGroup, group.ID, db.PermissionDeploymentManage, *deployment.RootNodeID); err != nil {
		t.Fatalf("create protected administrator grant: %v", err)
	}
	if _, err = service.CreatePermissionGrant(manager.ID, db.GrantSubjectKindGroup, group.ID, db.PermissionResourcePower, *deployment.RootNodeID); err != nil {
		t.Fatalf("manager should manage operational grants: %v", err)
	}
	if _, err = service.CreatePermissionGrant(manager.ID, db.GrantSubjectKindGroup, group.ID, db.PermissionDeploymentManageGroups, *deployment.RootNodeID); err != nil {
		t.Fatalf("manager should be able to delegate manager permissions: %v", err)
	}
	if _, err = service.CreatePermissionGrant(manager.ID, db.GrantSubjectKindGroup, group.ID, db.PermissionDeploymentManage, *deployment.RootNodeID); !errors.Is(err, ErrForbidden) {
		t.Fatalf("manager must not grant Deployment Admin: %v", err)
	}
	if err = service.DeletePermissionGrant(manager.ID, protectedGrant.ID); !errors.Is(err, ErrForbidden) {
		t.Fatalf("manager must not revoke Deployment Admin: %v", err)
	}

	var access *DeploymentAccessSummary
	if access, err = service.GetDeploymentAccess(manager.ID, deployment.ID); err != nil {
		t.Fatalf("load manager access workspace: %v", err)
	}
	if !access.CanManagePermissions || !access.CanManageGroups || access.CanManageConfiguration || len(access.PermissionGrants) != 7 || len(access.Groups) != 1 || len(access.Groups[0].Members) != 1 || access.Groups[0].Members[0].QualifiedName != "charlie@organesson" || len(access.Accounts) != 3 {
		t.Fatalf("unexpected scoped manager access workspace: %#v", access)
	}
	if access, err = service.GetDeploymentAccess(permissionObserver.ID, deployment.ID); err != nil {
		t.Fatalf("load permission-only observer access: %v", err)
	}
	if access.CanManageGroups || len(access.Groups) != 1 || len(access.Groups[0].Members) != 1 || access.Groups[0].Members[0].QualifiedName != "charlie@organesson" {
		t.Fatalf("permission managers must be able to inspect group membership: %#v", access)
	}
}

func timePointer(value time.Time) (pointer *time.Time) {
	pointer = &value
	return
}
