package organesson

import (
	"context"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/gofiber/fiber/v3"
	"github.com/gofiber/fiber/v3/middleware/adaptor"
	"github.com/hashicorp/terraform-plugin-sdk/v2/helper/schema"
	"github.com/z46-dev/golog"
	organessonapp "github.com/z46-dev/organesson/backend/app"
	"github.com/z46-dev/organesson/backend/app/api"
	localauth "github.com/z46-dev/organesson/backend/auth"
	"github.com/z46-dev/organesson/backend/db"
	"github.com/z46-dev/organesson/backend/domain"
)

// TestProviderAPIApplyRefreshAndPermissionRevocation exercises the real API without Proxmox.
func TestProviderAPIApplyRefreshAndPermissionRevocation(t *testing.T) {
	var store *db.Store
	var err error
	if store, err = db.Open(filepath.Join(t.TempDir(), "organesson.db"), golog.New(), false); err != nil {
		t.Fatalf("open database: %v", err)
	}
	defer store.Close()
	var authentication *localauth.Service
	if authentication, err = localauth.New(store); err != nil {
		t.Fatalf("create authentication service: %v", err)
	}
	var bootstrapToken string
	var created bool
	if bootstrapToken, created, err = authentication.EnsureInitialActivationLink(); err != nil || !created {
		t.Fatalf("create administrator activation: created=%t err=%v", created, err)
	}
	var administrator *db.Account
	if administrator, err = authentication.RedeemPasswordLink(bootstrapToken, "admin-acceptance-password"); err != nil {
		t.Fatalf("activate administrator: %v", err)
	}
	var setups []*localauth.LocalAccountSetup
	if setups, err = authentication.CreateDevelopmentTestUsers(administrator.ID); err != nil || len(setups) != 4 {
		t.Fatalf("seed development users: count=%d err=%v", len(setups), err)
	}
	var accounts map[string]*db.Account = make(map[string]*db.Account, len(setups))
	for _, setup := range setups {
		var account *db.Account
		if account, err = authentication.RedeemPasswordLink(setup.SetupToken, "fixture-acceptance-password"); err != nil {
			t.Fatalf("activate %s: %v", setup.QualifiedName, err)
		}
		accounts[setup.QualifiedName] = account
	}
	var apiToken *localauth.APITokenCredential
	if apiToken, err = authentication.CreateAPIToken(administrator.ID, "provider integration test", 24*time.Hour); err != nil {
		t.Fatalf("create provider bearer token: %v", err)
	}

	var fiberApp *fiber.App = organessonapp.New(api.Services{Authentication: authentication, Domain: domain.New(store), Store: store}, false, nil)
	var server *httptest.Server = httptest.NewServer(adaptor.FiberApp(fiberApp))
	defer server.Close()
	var client *apiClient
	if client, err = configuredClient(server.URL, apiToken.Secret); err != nil {
		t.Fatalf("configure provider client: %v", err)
	}
	var provider *schema.Provider = Provider()
	if err = provider.InternalValidate(); err != nil {
		t.Fatalf("provider schema validation: %v", err)
	}
	var ctx context.Context = context.Background()
	var deployment *schema.ResourceData = schema.TestResourceDataRaw(t, provider.ResourcesMap["organesson_deployment"].Schema, map[string]interface{}{
		"name":        "provider-acceptance",
		"description": "first description",
	})
	createRemoteResource(t, ctx, provider.ResourcesMap["organesson_deployment"], deployment, client)
	readRemoteResource(t, ctx, provider.ResourcesMap["organesson_deployment"], deployment, client)
	if deployment.Get("root_node_id").(int) < 1 {
		t.Fatal("deployment refresh did not restore its root node ID")
	}
	if err = deployment.Set("description", "updated description"); err != nil {
		t.Fatalf("set deployment description: %v", err)
	}
	updateRemoteResource(t, ctx, provider.ResourcesMap["organesson_deployment"], deployment, client)
	readRemoteResource(t, ctx, provider.ResourcesMap["organesson_deployment"], deployment, client)

	var charlieGroup *schema.ResourceData = schema.TestResourceDataRaw(t, provider.ResourcesMap["organesson_user_group"].Schema, map[string]interface{}{
		"deployment_id": deployment.Id(),
		"name":          "charlie-access",
		"members":       []interface{}{"charlie@organesson"},
	})
	var daveGroup *schema.ResourceData = schema.TestResourceDataRaw(t, provider.ResourcesMap["organesson_user_group"].Schema, map[string]interface{}{
		"deployment_id": deployment.Id(),
		"name":          "dave-access",
		"members":       []interface{}{"dave@organesson"},
	})
	createRemoteResource(t, ctx, provider.ResourcesMap["organesson_user_group"], charlieGroup, client)
	createRemoteResource(t, ctx, provider.ResourcesMap["organesson_user_group"], daveGroup, client)
	readRemoteResource(t, ctx, provider.ResourcesMap["organesson_user_group"], charlieGroup, client)
	readRemoteResource(t, ctx, provider.ResourcesMap["organesson_user_group"], daveGroup, client)

	var charlieLab *schema.ResourceData = createAcceptanceLab(t, provider, ctx, client, deployment.Id(), "charlie-lab")
	var daveLab *schema.ResourceData = createAcceptanceLab(t, provider, ctx, client, deployment.Id(), "dave-lab")
	var charlieVM *schema.ResourceData = createAcceptanceVM(t, provider, ctx, client, charlieLab.Id(), "charlie-vm")
	var daveVM *schema.ResourceData = createAcceptanceVM(t, provider, ctx, client, daveLab.Id(), "dave-vm")
	readRemoteResource(t, ctx, provider.ResourcesMap["organesson_virtual_machine"], charlieVM, client)
	readRemoteResource(t, ctx, provider.ResourcesMap["organesson_virtual_machine"], daveVM, client)

	var charlieView *schema.ResourceData = createAcceptanceGrant(t, provider, ctx, client, "resource.view", charlieGroup.Id(), charlieLab.Id())
	var charliePower *schema.ResourceData = createAcceptanceGrant(t, provider, ctx, client, "vm.power_control", charlieGroup.Id(), charlieLab.Id())
	var daveView *schema.ResourceData = createAcceptanceGrant(t, provider, ctx, client, "resource.view", daveGroup.Id(), daveLab.Id())
	var davePower *schema.ResourceData = createAcceptanceGrant(t, provider, ctx, client, "vm.power_control", daveGroup.Id(), daveLab.Id())
	readRemoteResource(t, ctx, provider.ResourcesMap["organesson_permission_grant"], charlieView, client)
	readRemoteResource(t, ctx, provider.ResourcesMap["organesson_permission_grant"], daveView, client)

	var charlie *db.Account = accounts["charlie@organesson"]
	var dave *db.Account = accounts["dave@organesson"]
	var summary *domain.DeploymentSummary
	if summary, err = domain.New(store).GetDeployment(charlie.ID, mustAtoi(t, deployment.Id())); err != nil || len(summary.Resources) != 1 || summary.Resources[0].Name != "charlie-vm" {
		t.Fatalf("Charlie should only see Charlie's VM: summary=%#v err=%v", summary, err)
	}
	if summary, err = domain.New(store).GetDeployment(dave.ID, mustAtoi(t, deployment.Id())); err != nil || len(summary.Resources) != 1 || summary.Resources[0].Name != "dave-vm" {
		t.Fatalf("Dave should only see Dave's VM: summary=%#v err=%v", summary, err)
	}
	var changed *db.ManagedResource
	if changed, err = domain.New(store).SetVirtualMachinePower(charlie.ID, mustAtoi(t, charlieVM.Id()), "start"); err != nil || changed.PowerState != "running" {
		t.Fatalf("Charlie should power on her VM: resource=%#v err=%v", changed, err)
	}
	if _, err = domain.New(store).SetVirtualMachinePower(charlie.ID, mustAtoi(t, daveVM.Id()), "start"); err == nil {
		t.Fatal("Charlie must not power on Dave's VM")
	}
	if err = charlieGroup.Set("members", []interface{}{}); err != nil {
		t.Fatalf("remove Charlie from access group: %v", err)
	}
	updateRemoteResource(t, ctx, provider.ResourcesMap["organesson_user_group"], charlieGroup, client)
	if _, err = domain.New(store).GetDeployment(charlie.ID, mustAtoi(t, deployment.Id())); err == nil {
		t.Fatal("Charlie should lose deployment visibility after membership revocation")
	}

	deleteRemoteResource(t, ctx, provider.ResourcesMap["organesson_permission_grant"], davePower, client)
	deleteRemoteResource(t, ctx, provider.ResourcesMap["organesson_permission_grant"], daveView, client)
	deleteRemoteResource(t, ctx, provider.ResourcesMap["organesson_permission_grant"], charliePower, client)
	deleteRemoteResource(t, ctx, provider.ResourcesMap["organesson_permission_grant"], charlieView, client)
	deleteRemoteResource(t, ctx, provider.ResourcesMap["organesson_virtual_machine"], daveVM, client)
	deleteRemoteResource(t, ctx, provider.ResourcesMap["organesson_virtual_machine"], charlieVM, client)
	deleteRemoteResource(t, ctx, provider.ResourcesMap["organesson_logical_group"], daveLab, client)
	deleteRemoteResource(t, ctx, provider.ResourcesMap["organesson_logical_group"], charlieLab, client)
	deleteRemoteResource(t, ctx, provider.ResourcesMap["organesson_user_group"], daveGroup, client)
	deleteRemoteResource(t, ctx, provider.ResourcesMap["organesson_user_group"], charlieGroup, client)
	deleteRemoteResource(t, ctx, provider.ResourcesMap["organesson_deployment"], deployment, client)
}

func createAcceptanceLab(t *testing.T, provider *schema.Provider, ctx context.Context, client *apiClient, deploymentID string, name string) (data *schema.ResourceData) {
	t.Helper()
	data = schema.TestResourceDataRaw(t, provider.ResourcesMap["organesson_logical_group"].Schema, map[string]interface{}{
		"deployment_id": deploymentID,
		"name":          name,
	})
	createRemoteResource(t, ctx, provider.ResourcesMap["organesson_logical_group"], data, client)
	readRemoteResource(t, ctx, provider.ResourcesMap["organesson_logical_group"], data, client)
	return
}

func createAcceptanceVM(t *testing.T, provider *schema.Provider, ctx context.Context, client *apiClient, parentID string, name string) (data *schema.ResourceData) {
	t.Helper()
	data = schema.TestResourceDataRaw(t, provider.ResourcesMap["organesson_virtual_machine"].Schema, map[string]interface{}{
		"boot_disk_gib":    64,
		"cpu_cores":        2,
		"logical_group_id": parentID,
		"memory_mib":       4096,
		"name":             name,
		"template":         "fedora-test-template",
	})
	createRemoteResource(t, ctx, provider.ResourcesMap["organesson_virtual_machine"], data, client)
	return
}

func createAcceptanceGrant(t *testing.T, provider *schema.Provider, ctx context.Context, client *apiClient, permission string, groupID string, targetID string) (data *schema.ResourceData) {
	t.Helper()
	data = schema.TestResourceDataRaw(t, provider.ResourcesMap["organesson_permission_grant"].Schema, map[string]interface{}{
		"permission": permission,
		"scope":      "descendants",
		"subject_id": groupID,
		"target_id":  targetID,
	})
	createRemoteResource(t, ctx, provider.ResourcesMap["organesson_permission_grant"], data, client)
	return
}

func createRemoteResource(t *testing.T, ctx context.Context, resource *schema.Resource, data *schema.ResourceData, client *apiClient) {
	t.Helper()
	if diagnostics := resource.CreateContext(ctx, data, client); diagnostics.HasError() {
		t.Fatalf("create %s: %v", data.Id(), diagnostics)
	}
	if data.Id() == "" {
		t.Fatal("provider create did not store the API identifier")
	}
}

func readRemoteResource(t *testing.T, ctx context.Context, resource *schema.Resource, data *schema.ResourceData, client *apiClient) {
	t.Helper()
	if diagnostics := resource.ReadContext(ctx, data, client); diagnostics.HasError() {
		t.Fatalf("refresh %s: %v", data.Id(), diagnostics)
	}
	if data.Id() == "" {
		t.Fatal("provider refresh cleared a live API identifier")
	}
}

func updateRemoteResource(t *testing.T, ctx context.Context, resource *schema.Resource, data *schema.ResourceData, client *apiClient) {
	t.Helper()
	if diagnostics := resource.UpdateContext(ctx, data, client); diagnostics.HasError() {
		t.Fatalf("update %s: %v", data.Id(), diagnostics)
	}
}

func deleteRemoteResource(t *testing.T, ctx context.Context, resource *schema.Resource, data *schema.ResourceData, client *apiClient) {
	t.Helper()
	if diagnostics := resource.DeleteContext(ctx, data, client); diagnostics.HasError() {
		t.Fatalf("delete %s: %v", data.Id(), diagnostics)
	}
	if data.Id() != "" {
		t.Fatalf("provider delete retained state ID %q", data.Id())
	}
}

func mustAtoi(t *testing.T, value string) (result int) {
	t.Helper()
	var err error
	if result, err = strconv.Atoi(value); err != nil {
		t.Fatalf("parse resource identifier %q: %v", value, err)
	}
	return
}
