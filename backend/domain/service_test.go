package domain

import (
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/z46-dev/golog"
	"github.com/z46-dev/organesson/backend/db"
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

func timePointer(value time.Time) (pointer *time.Time) {
	pointer = &value
	return
}
