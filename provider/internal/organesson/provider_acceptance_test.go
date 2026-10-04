package organesson

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"sync/atomic"
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
	"github.com/z46-dev/organesson/backend/proxmox"
)

func TestGuestSetupProviderUploadsOnlyVerifiedArtifactDuringApply(t *testing.T) {
	var sourceDirectory string = t.TempDir()
	if err := os.WriteFile(filepath.Join(sourceDirectory, "entrypoint.sh"), []byte("#!/bin/bash\necho ok\n"), 0755); err != nil {
		t.Fatal(err)
	}
	var artifact artifactPackage
	var err error
	var inlineFiles map[string]string = map[string]string{"router.conf": "DHCP_START=192.168.100.100\n"}
	if artifact, err = packageArtifactWithFiles(sourceDirectory, "entrypoint.sh", inlineFiles); err != nil {
		t.Fatal(err)
	}
	var requestCount atomic.Int32
	var server *httptest.Server = httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		requestCount.Add(1)
		var body []byte
		var readErr error
		if body, readErr = io.ReadAll(request.Body); readErr != nil {
			t.Errorf("read artifact request body: %v", readErr)
		}
		if request.Method != http.MethodPost || request.URL.Path != "/api/v1/virtual-machines/42/guest-setup" || request.Header.Get("Content-Type") != "application/vnd.organesson.artifact+gzip" || request.Header.Get("X-Organesson-Artifact-SHA256") != artifact.SHA256 || request.Header.Get("X-Organesson-Artifact-Entrypoint") != "entrypoint.sh" || !bytes.Equal(body, artifact.Archive) {
			t.Errorf("provider did not send the exact verified archive and metadata: method=%s path=%s headers=%v bytes=%d", request.Method, request.URL.Path, request.Header, len(body))
		}
		response.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(response).Encode(map[string]any{"execution": map[string]any{"sha256": artifact.SHA256, "status": "succeeded", "exit_code": 0}})
	}))
	defer server.Close()
	var client *apiClient
	if client, err = configuredClient(server.URL, "test-token"); err != nil {
		t.Fatal(err)
	}
	var resource *schema.Resource = guestSetupResource()
	var data *schema.ResourceData = schema.TestResourceDataRaw(t, resource.Schema, map[string]interface{}{
		"artifact_id":        "local:artifact:test",
		"entrypoint":         "entrypoint.sh",
		"inline_files":       map[string]interface{}{"router.conf": "DHCP_START=192.168.100.100\n"},
		"sha256":             artifact.SHA256,
		"source_directory":   sourceDirectory,
		"virtual_machine_id": "42",
	})
	if diagnostics := resource.CreateContext(context.Background(), data, client); diagnostics.HasError() {
		t.Fatalf("guest setup apply failed: %v", diagnostics)
	}
	if requestCount.Load() != 1 || data.Get("execution_status") != "succeeded" || data.Get("exit_code") != 0 || data.Id() == "" {
		t.Fatalf("guest setup result was not recorded: calls=%d status=%v exit=%v id=%q", requestCount.Load(), data.Get("execution_status"), data.Get("exit_code"), data.Id())
	}
	if diagnostics := resource.ReadContext(context.Background(), data, client); diagnostics.HasError() || requestCount.Load() != 1 {
		t.Fatalf("refresh reran one-shot guest setup: diagnostics=%v calls=%d", diagnostics, requestCount.Load())
	}
}

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

	var networkDriver *acceptanceSDNNetworkDriver = &acceptanceSDNNetworkDriver{}
	var proxmoxService *proxmox.Service = proxmox.NewWithSDNNetworkDriver(networkDriver)
	var fiberApp *fiber.App = organessonapp.New(api.Services{Authentication: authentication, Domain: domain.New(store), Store: store, Proxmox: proxmoxService}, false, nil)
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
	var addressPolicy proxmox.ResourcePolicy = proxmox.ResourcePolicy{
		ResourcePools: []string{"students"}, Storages: []string{"local-lvm"},
		DeploymentLimits: proxmox.DeploymentLimits{MaxSDNNetworks: 1},
		Networks: []proxmox.PolicyNetwork{{Name: "cyber.lab", Kind: "bridge", PVEName: "vmbr0", AddressPools: []proxmox.AddressPool{{
			Name: "test-pool", Prefix: "10.0.0.0/8", AllocationPrefix: "10.192.0.0/29", Gateway: "10.0.0.1", DNS: []string{"10.0.0.2"},
		}}}},
	}
	var addressPolicyJSON []byte
	if addressPolicyJSON, err = json.Marshal(addressPolicy); err != nil {
		t.Fatalf("encode address policy: %v", err)
	}
	var addressPolicyHash string
	if addressPolicyHash, err = proxmox.ResourcePolicyHash(addressPolicy); err != nil {
		t.Fatalf("hash address policy: %v", err)
	}
	var validatedAt time.Time = time.Now().UTC()
	if err = store.ProxmoxResourcePolicies.Insert(&db.ProxmoxResourcePolicy{
		ID: 1, ConfigurationJSON: string(addressPolicyJSON), ValidationJSON: `{"valid":true}`,
		ValidatedConfigHash: addressPolicyHash, ValidatedAt: &validatedAt, UpdatedAt: validatedAt,
	}); err != nil {
		t.Fatalf("save test address policy: %v", err)
	}
	var addressRequest *schema.ResourceData = schema.TestResourceDataRaw(t, provider.ResourcesMap["organesson_address_pool_request"].Schema, map[string]interface{}{
		"deployment_id": deployment.Id(), "name": "internet-addresses", "environment_network": "cyber.lab",
		"address_family": "ipv4", "address_count": 2,
	})
	createRemoteResource(t, ctx, provider.ResourcesMap["organesson_address_pool_request"], addressRequest, client)
	readRemoteResource(t, ctx, provider.ResourcesMap["organesson_address_pool_request"], addressRequest, client)
	if addressRequest.Get("pool_name") != "test-pool" || len(addressRequest.Get("addresses").([]interface{})) != 2 {
		t.Fatalf("address allocation was not refreshed into provider state: %#v", addressRequest.Get("addresses"))
	}
	var network *schema.ResourceData = schema.TestResourceDataRaw(t, provider.ResourcesMap["organesson_network"].Schema, map[string]interface{}{
		"deployment_id": deployment.Id(), "name": "shared-lan", "mode": "managed", "ipv4_subnet": "192.168.100.0/24",
		"ipv4_gateway": "192.168.100.1", "dhcp_enabled": true, "egress_policy": "isolated",
		"router_vmid": 158,
	})
	createRemoteResource(t, ctx, provider.ResourcesMap["organesson_network"], network, client)
	readRemoteResource(t, ctx, provider.ResourcesMap["organesson_network"], network, client)
	if network.Get("proxmox_vnet") == "" || network.Get("power_state") != "ready" || network.Get("router_vmid") != 158 || networkDriver.request.RouterVMID != 158 {
		t.Fatalf("network refresh did not restore Proxmox placement: %#v", network.Get("proxmox_vnet"))
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
	deleteRemoteResource(t, ctx, provider.ResourcesMap["organesson_address_pool_request"], addressRequest, client)
	deleteRemoteResource(t, ctx, provider.ResourcesMap["organesson_network"], network, client)
	if !networkDriver.deleted {
		t.Fatal("network destroy did not call the Proxmox SDN driver")
	}
	deleteRemoteResource(t, ctx, provider.ResourcesMap["organesson_virtual_machine"], daveVM, client)
	deleteRemoteResource(t, ctx, provider.ResourcesMap["organesson_virtual_machine"], charlieVM, client)
	deleteRemoteResource(t, ctx, provider.ResourcesMap["organesson_logical_group"], daveLab, client)
	deleteRemoteResource(t, ctx, provider.ResourcesMap["organesson_logical_group"], charlieLab, client)
	deleteRemoteResource(t, ctx, provider.ResourcesMap["organesson_user_group"], daveGroup, client)
	deleteRemoteResource(t, ctx, provider.ResourcesMap["organesson_user_group"], charlieGroup, client)
	deleteRemoteResource(t, ctx, provider.ResourcesMap["organesson_deployment"], deployment, client)
}

// TestProviderProxmoxVMCreateRefreshDelete verifies provider CRUD against an authenticated app and fake PVE lifecycle.
func TestProviderProxmoxVMCreateRefreshDelete(t *testing.T) {
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
	var activation string
	if activation, _, err = authentication.EnsureInitialActivationLink(); err != nil {
		t.Fatalf("create administrator activation: %v", err)
	}
	var administrator *db.Account
	if administrator, err = authentication.RedeemPasswordLink(activation, "provider-lifecycle-password"); err != nil {
		t.Fatalf("activate administrator: %v", err)
	}
	var credential *localauth.APITokenCredential
	if credential, err = authentication.CreateAPIToken(administrator.ID, "Proxmox provider lifecycle", 24*time.Hour); err != nil {
		t.Fatalf("create administrator API token: %v", err)
	}
	var service *domain.Service = domain.New(store)
	var template *domain.VMTemplateRecord
	if template, err = service.CreateVMTemplate(administrator.ID, domain.VMTemplateInput{
		DisplayName: "Fedora Server", Description: "Provider acceptance source", SourceID: "157", GuestOS: "fedora",
		GuestOSVersion: "44", Edition: "server", Architecture: "x86_64", ExecutionMethod: "qemu_guest_agent",
	}, []string{"fedora-server-latest"}); err != nil {
		t.Fatalf("register ready source metadata: %v", err)
	}
	var preflight proxmox.PreflightResult = proxmox.PreflightResult{
		SourceID: "157", PowerState: "running", GuestOSID: "fedora", AgentReachable: true,
		GuestAgentRootVerified: true, Passed: true, CheckedAt: time.Now().UTC(),
		Checks: []proxmox.Check{{Name: "guest_agent_root_execution", Passed: true, Required: true}},
	}
	if _, err = service.RecordVMTemplatePreflight(administrator.ID, template.Template.ID, preflight, nil); err != nil {
		t.Fatalf("record successful source preflight: %v", err)
	}
	if _, err = service.SetVMTemplateReadiness(administrator.ID, template.Template.ID, true, true); err != nil {
		t.Fatalf("mark source provisioning-ready: %v", err)
	}
	var policy proxmox.ResourcePolicy = proxmox.ResourcePolicy{
		Limits:        proxmox.CapacityLimits{VirtualCPUs: 8, MemoryMiB: 16384, StorageGiB: 256},
		ResourcePools: []string{"class-labs"},
		Storages:      []string{"local-lvm"},
		Networks: []proxmox.PolicyNetwork{{Name: "cyber.lab", Kind: "bridge", PVEName: "vmbr0", AddressPools: []proxmox.AddressPool{{
			Name: "cyber-lab", Prefix: "10.0.0.0/8", AllocationPrefix: "10.192.0.0/29", Gateway: "10.0.0.1", DNS: []string{"10.0.0.2"},
		}}}},
	}
	var policyJSON []byte
	if policyJSON, err = json.Marshal(policy); err != nil {
		t.Fatalf("encode policy: %v", err)
	}
	var policyValidation proxmox.ResourcePolicyValidation = proxmox.ValidateResourcePolicy(policy, &proxmox.ResourceInventory{
		Pools: []string{"class-labs"}, Storages: []string{"local-lvm"}, Bridges: []string{"vmbr0"},
	})
	var validationJSON []byte
	if validationJSON, err = json.Marshal(policyValidation); err != nil {
		t.Fatalf("encode policy validation: %v", err)
	}
	var policyHash string
	if policyHash, err = proxmox.ResourcePolicyHash(policy); err != nil {
		t.Fatalf("hash policy: %v", err)
	}
	var validatedAt time.Time = time.Now().UTC()
	if err = store.ProxmoxResourcePolicies.Insert(&db.ProxmoxResourcePolicy{
		ID: 1, ConfigurationJSON: string(policyJSON), ValidationJSON: string(validationJSON),
		ValidatedConfigHash: policyHash, ValidatedAt: &validatedAt, UpdatedAt: validatedAt,
	}); err != nil {
		t.Fatalf("save validated test policy: %v", err)
	}
	var fakeDriver *providerLifecycleVMDriver = &providerLifecycleVMDriver{}
	var attachmentDriver *acceptanceNetworkAttachmentDriver = &acceptanceNetworkAttachmentDriver{}
	var guestNetworkDriver *acceptanceGuestNetworkDriver = &acceptanceGuestNetworkDriver{}
	var application *fiber.App = organessonapp.New(api.Services{
		Authentication: authentication,
		Domain:         service,
		Store:          store,
		Proxmox:        proxmox.NewWithProvisioningDrivers(fakeDriver, nil, attachmentDriver, providerLifecycleInventory{}, guestNetworkDriver),
	}, false, nil)
	var server *httptest.Server = httptest.NewServer(adaptor.FiberApp(application))
	defer server.Close()
	var client *apiClient
	if client, err = configuredClient(server.URL, credential.Secret); err != nil {
		t.Fatalf("configure provider client: %v", err)
	}
	var provider *schema.Provider = Provider()
	if err = provider.InternalValidate(); err != nil {
		t.Fatalf("validate provider schema: %v", err)
	}
	var ctx context.Context = context.Background()
	var deployment *schema.ResourceData = schema.TestResourceDataRaw(t, provider.ResourcesMap["organesson_deployment"].Schema, map[string]interface{}{
		"name": "proxmox-provider-lifecycle", "description": "single cloned VM acceptance test",
	})
	createRemoteResource(t, ctx, provider.ResourcesMap["organesson_deployment"], deployment, client)
	var group *schema.ResourceData = schema.TestResourceDataRaw(t, provider.ResourcesMap["organesson_logical_group"].Schema, map[string]interface{}{
		"deployment_id": deployment.Id(), "name": "single-vm",
	})
	createRemoteResource(t, ctx, provider.ResourcesMap["organesson_logical_group"], group, client)
	var vm *schema.ResourceData = schema.TestResourceDataRaw(t, provider.ResourcesMap["organesson_virtual_machine"].Schema, map[string]interface{}{
		"boot_disk_gib": 64, "cpu_cores": 2, "logical_group_id": group.Id(), "memory_mib": 4096,
		"name": "provider-fedora", "provisioning_mode": "proxmox", "template": "fedora-server-latest",
		"pool": "class-labs", "storage": "local-lvm",
	})
	createRemoteResource(t, ctx, provider.ResourcesMap["organesson_virtual_machine"], vm, client)
	if fakeDriver.cloneCount != 1 || vm.Get("proxmox_vmid").(string) != "901" || vm.Get("proxmox_node").(string) != "pve1" {
		t.Fatalf("provider create did not retain the Proxmox mapping: clones=%d vmid=%v node=%v", fakeDriver.cloneCount, vm.Get("proxmox_vmid"), vm.Get("proxmox_node"))
	}
	readRemoteResource(t, ctx, provider.ResourcesMap["organesson_virtual_machine"], vm, client)
	if fakeDriver.cloneCount != 1 || vm.Get("proxmox_vmid").(string) != "901" {
		t.Fatalf("provider refresh lost mapping or created another clone: clones=%d vmid=%v", fakeDriver.cloneCount, vm.Get("proxmox_vmid"))
	}
	var addressRequest *schema.ResourceData = schema.TestResourceDataRaw(t, provider.ResourcesMap["organesson_address_pool_request"].Schema, map[string]interface{}{
		"deployment_id": deployment.Id(), "name": "provider-nic-address", "environment_network": "cyber.lab",
		"address_family": "ipv4", "address_count": 1,
	})
	createRemoteResource(t, ctx, provider.ResourcesMap["organesson_address_pool_request"], addressRequest, client)
	var attachment *schema.ResourceData = schema.TestResourceDataRaw(t, provider.ResourcesMap["organesson_network_attachment"].Schema, map[string]interface{}{
		"address_pool_request_id": addressRequest.Id(), "environment_network": "cyber.lab", "name": "internet",
		"requested_address_count": 1, "virtual_machine_id": vm.Id(),
	})
	createRemoteResource(t, ctx, provider.ResourcesMap["organesson_network_attachment"], attachment, client)
	if attachment.Get("net_device") != "net1" || attachment.Get("mac_address") != "02:11:22:33:44:55" || len(attachment.Get("addresses").([]interface{})) != 1 {
		t.Fatalf("provider NIC create did not retain placement and claimed address: device=%v mac=%v addresses=%v", attachment.Get("net_device"), attachment.Get("mac_address"), attachment.Get("addresses"))
	}
	readRemoteResource(t, ctx, provider.ResourcesMap["organesson_network_attachment"], attachment, client)
	var guestNetwork *schema.ResourceData = schema.TestResourceDataRaw(t, provider.ResourcesMap["organesson_guest_network_configuration"].Schema, map[string]interface{}{
		"network_attachment_id": attachment.Id(), "ipv4_method": "static", "ipv4_address": "10.0.0.100/8",
		"ipv4_gateway": "10.0.0.1", "ipv4_dns": []interface{}{"10.0.0.2"},
	})
	createRemoteResource(t, ctx, provider.ResourcesMap["organesson_guest_network_configuration"], guestNetwork, client)
	readRemoteResource(t, ctx, provider.ResourcesMap["organesson_guest_network_configuration"], guestNetwork, client)
	if guestNetworkDriver.configureCount != 1 || guestNetworkDriver.readCount != 1 || guestNetwork.Get("ipv4_address") != "10.0.0.100/8" {
		t.Fatalf("guest network apply/refresh did not use its QGA driver: configure=%d read=%d address=%v", guestNetworkDriver.configureCount, guestNetworkDriver.readCount, guestNetwork.Get("ipv4_address"))
	}
	deleteRemoteResource(t, ctx, provider.ResourcesMap["organesson_guest_network_configuration"], guestNetwork, client)
	if guestNetworkDriver.removeCount != 1 {
		t.Fatalf("guest network destroy did not remove the managed guest profile: removes=%d", guestNetworkDriver.removeCount)
	}
	deleteRemoteResource(t, ctx, provider.ResourcesMap["organesson_network_attachment"], attachment, client)
	if !attachmentDriver.detached {
		t.Fatal("provider NIC destroy did not call the Proxmox detach driver")
	}
	deleteRemoteResource(t, ctx, provider.ResourcesMap["organesson_address_pool_request"], addressRequest, client)
	deleteRemoteResource(t, ctx, provider.ResourcesMap["organesson_virtual_machine"], vm, client)
	if fakeDriver.deleteCount != 1 || fakeDriver.deletedVMID != "901" {
		t.Fatalf("provider destroy did not remove exactly the managed VM: deletes=%d vmid=%s", fakeDriver.deleteCount, fakeDriver.deletedVMID)
	}
	deleteRemoteResource(t, ctx, provider.ResourcesMap["organesson_logical_group"], group, client)
	deleteRemoteResource(t, ctx, provider.ResourcesMap["organesson_deployment"], deployment, client)
}

type providerLifecycleInventory struct{}

func (providerLifecycleInventory) ReadResourceInventory(context.Context) (inventory proxmox.ResourceInventory, err error) {
	inventory = proxmox.ResourceInventory{Pools: []string{"class-labs"}, Storages: []string{"local-lvm"}, Bridges: []string{"vmbr0"}}
	return
}

type providerLifecycleInspector struct{}

func (providerLifecycleInspector) Inspect(_ context.Context, sourceID string, _ string) (result proxmox.PreflightResult, err error) {
	result = proxmox.PreflightResult{
		SourceID: sourceID, PowerState: "running", GuestOSID: "fedora", AgentReachable: true,
		GuestAgentRootVerified: true, Passed: true, IsQEMU: true,
		Checks: []proxmox.Check{{Name: "guest_agent_root_execution", Passed: true, Required: true}},
	}
	return
}

type providerLifecycleVMDriver struct {
	cloneCount  int
	deleteCount int
	deletedVMID string
	powerState  string
}

type acceptanceSDNNetworkDriver struct {
	deleted bool
	request proxmox.SDNNetworkRequest
}

type acceptanceNetworkAttachmentDriver struct {
	detached bool
}

type acceptanceGuestNetworkDriver struct {
	configureCount int
	readCount      int
	removeCount    int
}

func (driver *acceptanceGuestNetworkDriver) Configure(_ context.Context, _ proxmox.GuestNetworkRequest) (err error) {
	driver.configureCount++
	return
}

func (driver *acceptanceGuestNetworkDriver) Read(_ context.Context, _ proxmox.GuestNetworkRequest) (err error) {
	driver.readCount++
	return
}

func (driver *acceptanceGuestNetworkDriver) Remove(_ context.Context, _ proxmox.GuestNetworkRequest) (err error) {
	driver.removeCount++
	return
}

func (*acceptanceNetworkAttachmentDriver) Attach(_ context.Context, _ proxmox.NetworkAttachmentRequest) (placement proxmox.NetworkAttachmentPlacement, err error) {
	placement = proxmox.NetworkAttachmentPlacement{Device: "net1", MAC: "02:11:22:33:44:55"}
	return
}

func (*acceptanceNetworkAttachmentDriver) Read(_ context.Context, _ proxmox.NetworkAttachmentRequest, _ proxmox.NetworkAttachmentPlacement) (err error) {
	return
}

func (driver *acceptanceNetworkAttachmentDriver) Detach(_ context.Context, _ proxmox.NetworkAttachmentRequest, _ proxmox.NetworkAttachmentPlacement) (err error) {
	driver.detached = true
	return
}

func (driver *acceptanceSDNNetworkDriver) Create(_ context.Context, request proxmox.SDNNetworkRequest) (placement proxmox.SDNNetworkPlacement, err error) {
	driver.request = request
	placement = proxmox.SDNNetworkNames(request.OperationKey)
	return
}

func (driver *acceptanceSDNNetworkDriver) Read(_ context.Context, _ proxmox.SDNNetworkRequest, _ proxmox.SDNNetworkPlacement) (err error) {
	return
}

func (driver *acceptanceSDNNetworkDriver) Delete(_ context.Context, _ string, _ string, _ string) (err error) {
	driver.deleted = true
	return
}

func (driver *providerLifecycleVMDriver) Clone(_ context.Context, request proxmox.VMCloneRequest) (placement proxmox.VMPlacement, err error) {
	driver.cloneCount++
	driver.powerState = "stopped"
	placement = proxmox.VMPlacement{VMID: "901", Node: "pve1", Name: request.Name, PowerState: "stopped"}
	return
}

func (driver *providerLifecycleVMDriver) Read(_ context.Context, node string, vmid string, _ string) (placement proxmox.VMPlacement, err error) {
	placement = proxmox.VMPlacement{VMID: vmid, Node: node, Name: "provider-fedora", PowerState: driver.powerState}
	return
}

func (driver *providerLifecycleVMDriver) Power(_ context.Context, node string, vmid string, _ string, action string) (placement proxmox.VMPlacement, err error) {
	var state string = action
	if action == "start" || action == "resume" || action == "restart" {
		state = "running"
	}
	driver.powerState = state
	placement = proxmox.VMPlacement{VMID: vmid, Node: node, Name: "provider-fedora", PowerState: state}
	return
}

func (driver *providerLifecycleVMDriver) Delete(_ context.Context, _ string, vmid string, _ string) (err error) {
	driver.deleteCount++
	driver.deletedVMID = vmid
	return
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
