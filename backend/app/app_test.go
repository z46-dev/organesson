package app

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/gofiber/fiber/v3"
	"github.com/z46-dev/golog"
	"github.com/z46-dev/organesson/backend/app/api"
	localauth "github.com/z46-dev/organesson/backend/auth"
	"github.com/z46-dev/organesson/backend/db"
	"github.com/z46-dev/organesson/backend/domain"
	"github.com/z46-dev/organesson/backend/proxmox"
)

// TestHTTPBoundaryReportsSetupAndRejectsUnauthenticatedMutations checks the initial API boundary.
func TestHTTPBoundaryReportsSetupAndRejectsUnauthenticatedMutations(t *testing.T) {
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
	var application *fiber.App = New(api.Services{
		Authentication: authentication,
		Domain:         domain.New(store),
		Store:          store,
	}, false, nil)

	var response *http.Response
	if response, err = application.Test(httptest.NewRequest("GET", "/api/v1/auth/status", nil)); err != nil {
		t.Fatalf("request auth status: %v", err)
	}
	defer response.Body.Close()
	if response.StatusCode != fiber.StatusOK {
		t.Fatalf("expected auth status 200, got %d", response.StatusCode)
	}

	if response, err = application.Test(httptest.NewRequest("GET", "/api/v1/deployments", nil)); err != nil {
		t.Fatalf("request protected deployment list: %v", err)
	}
	response.Body.Close()
	if response.StatusCode != fiber.StatusUnauthorized {
		t.Fatalf("expected unauthenticated deployment list to return 401, got %d", response.StatusCode)
	}

	if response, err = application.Test(httptest.NewRequest("POST", "/api/v1/deployments", nil)); err != nil {
		t.Fatalf("request mutation without CSRF token: %v", err)
	}
	response.Body.Close()
	if response.StatusCode != fiber.StatusForbidden {
		t.Fatalf("expected mutation without CSRF token to return 403, got %d", response.StatusCode)
	}
}

// TestBootstrapLoginAndAuthorizedOperations exercises the browser session and API lifecycle.
func TestBootstrapLoginAndAuthorizedOperations(t *testing.T) {
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
		t.Fatalf("create bootstrap token: created=%t err=%v", created, err)
	}
	var application *fiber.App = New(api.Services{
		Authentication: authentication,
		Domain:         domain.New(store),
		Store:          store,
		Proxmox:        proxmox.NewWithInspector(testProxmoxInspector{}),
	}, false, nil)
	var jar *cookiejar.Jar
	if jar, err = cookiejar.New(nil); err != nil {
		t.Fatalf("create cookie jar: %v", err)
	}

	var csrfToken string
	if csrfToken, err = requestCSRFToken(t, application, jar); err != nil {
		t.Fatalf("get bootstrap CSRF token: %v", err)
	}
	var response *http.Response
	response = performRequest(t, application, jar, http.MethodPost, "/api/v1/auth/bootstrap/redeem", `{"token":"`+bootstrapToken+`","password":"Smoke-test-password-2026!"}`, csrfToken)
	response.Body.Close()
	if response.StatusCode != fiber.StatusCreated {
		t.Fatalf("expected bootstrap to return 201, got %d", response.StatusCode)
	}

	if csrfToken, err = requestCSRFToken(t, application, jar); err != nil {
		t.Fatalf("get deployment CSRF token: %v", err)
	}
	response = performRequest(t, application, jar, http.MethodPost, "/api/v1/admin/vm-templates", `{"display_name":"Fedora Workstation","description":"Test source","source_id":"156","guest_os":"fedora","guest_os_version":"44","edition":"workstation","architecture":"x86_64","execution_method":"qemu_guest_agent","aliases":["og-template-fedora-workstation-latest"]}`, csrfToken)
	var sourceResult struct {
		Template struct {
			Template struct {
				ID int `json:"id"`
			} `json:"template"`
			Aliases []struct {
				Alias string `json:"alias"`
			} `json:"aliases"`
		} `json:"template"`
	}
	if response.StatusCode != fiber.StatusCreated {
		var body []byte
		body, _ = io.ReadAll(response.Body)
		response.Body.Close()
		t.Fatalf("expected template create 201, got %d: %s", response.StatusCode, body)
	}
	if err = json.NewDecoder(response.Body).Decode(&sourceResult); err != nil {
		response.Body.Close()
		t.Fatalf("decode source template response: %v", err)
	}
	response.Body.Close()
	if sourceResult.Template.Template.ID < 1 || len(sourceResult.Template.Aliases) != 1 {
		t.Fatalf("template response is missing source record or alias: %#v", sourceResult)
	}
	var preflightPath string = "/api/v1/admin/vm-templates/" + strconv.Itoa(sourceResult.Template.Template.ID) + "/preflight"
	response = performRequest(t, application, jar, http.MethodPost, preflightPath, "{}", csrfToken)
	response.Body.Close()
	if response.StatusCode != fiber.StatusOK {
		t.Fatalf("expected read-only preflight 200, got %d", response.StatusCode)
	}
	var readinessPath string = "/api/v1/admin/vm-templates/" + strconv.Itoa(sourceResult.Template.Template.ID) + "/readiness"
	response = performRequest(t, application, jar, http.MethodPut, readinessPath, `{"guest_agent_root_verified":true,"provisioning_account_removed":true}`, csrfToken)
	response.Body.Close()
	if response.StatusCode != fiber.StatusOK {
		t.Fatalf("expected readiness confirmation 200, got %d", response.StatusCode)
	}

	response = performRequest(t, application, jar, http.MethodPost, "/api/v1/auth/api-tokens", `{"name":"provider acceptance","lifetime_days":7}`, csrfToken)
	var apiTokenResult struct {
		ID    int    `json:"id"`
		Token string `json:"token"`
	}
	if response.StatusCode != fiber.StatusCreated {
		var body []byte
		body, _ = io.ReadAll(response.Body)
		response.Body.Close()
		t.Fatalf("expected API token create 201, got %d: %s", response.StatusCode, body)
	}
	if err = json.NewDecoder(response.Body).Decode(&apiTokenResult); err != nil {
		response.Body.Close()
		t.Fatalf("decode API token response: %v", err)
	}
	response.Body.Close()
	if apiTokenResult.ID < 1 || apiTokenResult.Token == "" {
		t.Fatalf("API token response lacks one-time secret or identifier: %#v", apiTokenResult)
	}
	response = performBearerRequest(t, application, http.MethodPost, "/api/v1/deployments", `{"name":"provider-deployment","description":"Bearer API acceptance"}`, apiTokenResult.Token)
	response.Body.Close()
	if response.StatusCode != fiber.StatusCreated {
		t.Fatalf("expected bearer-authenticated mutation without CSRF to return 201, got %d", response.StatusCode)
	}
	response = performBearerRequest(t, application, http.MethodGet, "/api/v1/deployments", "", "invalid-token")
	response.Body.Close()
	if response.StatusCode != fiber.StatusUnauthorized {
		t.Fatalf("expected invalid bearer credential to return 401, got %d", response.StatusCode)
	}
	if csrfToken, err = requestCSRFToken(t, application, jar); err != nil {
		t.Fatalf("get token revoke CSRF token: %v", err)
	}
	response = performRequest(t, application, jar, http.MethodDelete, "/api/v1/auth/api-tokens/"+strconv.Itoa(apiTokenResult.ID), "", csrfToken)
	response.Body.Close()
	if response.StatusCode != fiber.StatusNoContent {
		t.Fatalf("expected API token revocation to return 204, got %d", response.StatusCode)
	}
	response = performBearerRequest(t, application, http.MethodGet, "/api/v1/deployments", "", apiTokenResult.Token)
	response.Body.Close()
	if response.StatusCode != fiber.StatusUnauthorized {
		t.Fatalf("expected revoked bearer credential to return 401, got %d", response.StatusCode)
	}

	response = performRequest(t, application, jar, http.MethodPost, "/api/v1/deployments", `{"name":"smoke-deployment","description":"API test"}`, csrfToken)
	var deploymentResult struct {
		Deployment struct {
			ID         int `json:"id"`
			RootNodeID int `json:"root_node_id"`
		} `json:"deployment"`
	}
	if response.StatusCode != fiber.StatusCreated {
		var body []byte
		body, _ = io.ReadAll(response.Body)
		response.Body.Close()
		t.Fatalf("expected deployment create 201, got %d: %s", response.StatusCode, body)
	}
	if err = json.NewDecoder(response.Body).Decode(&deploymentResult); err != nil {
		response.Body.Close()
		t.Fatalf("decode deployment response: %v", err)
	}
	response.Body.Close()
	if deploymentResult.Deployment.ID < 1 || deploymentResult.Deployment.RootNodeID < 1 {
		t.Fatalf("deployment response is missing its IDs: %#v", deploymentResult.Deployment)
	}

	var createVMPath string = "/api/v1/deployments/" + strconv.Itoa(deploymentResult.Deployment.ID) + "/virtual-machines"
	var createVMBody string = `{"parent_node_id":` + strconv.Itoa(deploymentResult.Deployment.RootNodeID) + `,"name":"test-vm"}`
	response = performRequest(t, application, jar, http.MethodPost, createVMPath, createVMBody, csrfToken)
	var vmResult struct {
		Resource struct {
			ID         int    `json:"id"`
			PowerState string `json:"power_state"`
		} `json:"resource"`
	}
	if response.StatusCode != fiber.StatusCreated {
		var body []byte
		body, _ = io.ReadAll(response.Body)
		response.Body.Close()
		t.Fatalf("expected VM create 201, got %d: %s", response.StatusCode, body)
	}
	if err = json.NewDecoder(response.Body).Decode(&vmResult); err != nil {
		response.Body.Close()
		t.Fatalf("decode VM response: %v", err)
	}
	response.Body.Close()
	var powerPath string = "/api/v1/virtual-machines/" + strconv.Itoa(vmResult.Resource.ID) + "/power"
	response = performRequest(t, application, jar, http.MethodPost, powerPath, `{"action":"start"}`, csrfToken)
	response.Body.Close()
	if response.StatusCode != fiber.StatusOK {
		t.Fatalf("expected authorized VM power action 200, got %d", response.StatusCode)
	}

	response = performRequest(t, application, jar, http.MethodPost, "/api/v1/deployments", `{"name":"blocked","description":"csrf missing"}`, "")
	response.Body.Close()
	if response.StatusCode != fiber.StatusForbidden {
		t.Fatalf("expected state change without CSRF token to return 403, got %d", response.StatusCode)
	}

	if csrfToken, err = requestCSRFToken(t, application, jar); err != nil {
		t.Fatalf("get logout CSRF token: %v", err)
	}
	response = performRequest(t, application, jar, http.MethodPost, "/api/v1/auth/logout", "", csrfToken)
	response.Body.Close()
	if response.StatusCode != fiber.StatusNoContent {
		t.Fatalf("expected logout to return 204, got %d", response.StatusCode)
	}
	response = performRequest(t, application, jar, http.MethodGet, "/api/v1/deployments", "", "")
	response.Body.Close()
	if response.StatusCode != fiber.StatusUnauthorized {
		t.Fatalf("expected request after logout to return 401, got %d", response.StatusCode)
	}
}

// TestAuthenticatedProxmoxVMLifecycle exercises readiness, policy, permissions, power, and deletion over HTTP.
func TestAuthenticatedProxmoxVMLifecycle(t *testing.T) {
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
	if bootstrapToken, _, err = authentication.EnsureInitialActivationLink(); err != nil {
		t.Fatalf("create bootstrap token: %v", err)
	}
	var fakeDriver *fakeProxmoxVMDriver = &fakeProxmoxVMDriver{state: "stopped"}
	var proxmoxService *proxmox.Service = proxmox.NewWithDrivers(fakeDriver, fakeProxmoxInventory{}, testProxmoxInspector{})
	var application *fiber.App = New(api.Services{
		Authentication: authentication,
		Domain:         domain.New(store),
		Store:          store,
		Proxmox:        proxmoxService,
	}, false, nil)
	var jar *cookiejar.Jar
	if jar, err = cookiejar.New(nil); err != nil {
		t.Fatalf("create cookie jar: %v", err)
	}
	var csrfToken string
	if csrfToken, err = requestCSRFToken(t, application, jar); err != nil {
		t.Fatalf("get setup CSRF token: %v", err)
	}
	var response *http.Response = performRequest(t, application, jar, http.MethodPost, "/api/v1/auth/bootstrap/redeem", `{"token":"`+bootstrapToken+`","password":"Smoke-test-password-2026!"}`, csrfToken)
	response.Body.Close()
	if response.StatusCode != fiber.StatusCreated {
		t.Fatalf("activate administrator: status %d", response.StatusCode)
	}
	var admin *db.Account
	if admin, err = authentication.Authenticate("administrator@organesson", "Smoke-test-password-2026!"); err != nil {
		t.Fatalf("authenticate administrator: %v", err)
	}
	var charlieSetup *localauth.LocalAccountSetup
	if charlieSetup, err = authentication.CreateLocalAccount(admin.ID, "charlie", "Charlie"); err != nil {
		t.Fatalf("create Charlie: %v", err)
	}
	var charlie *db.Account
	if charlie, err = authentication.RedeemPasswordLink(charlieSetup.SetupToken, "Charlie-test-password-2026!"); err != nil {
		t.Fatalf("activate Charlie: %v", err)
	}
	var daveSetup *localauth.LocalAccountSetup
	if daveSetup, err = authentication.CreateLocalAccount(admin.ID, "dave", "Dave"); err != nil {
		t.Fatalf("create Dave: %v", err)
	}
	var dave *db.Account
	if dave, err = authentication.RedeemPasswordLink(daveSetup.SetupToken, "Dave-test-password-2026!"); err != nil {
		t.Fatalf("activate Dave: %v", err)
	}
	var charlieCredential *localauth.APITokenCredential
	if charlieCredential, err = authentication.CreateAPIToken(charlie.ID, "lifecycle test", 24*time.Hour); err != nil {
		t.Fatalf("create Charlie API token: %v", err)
	}
	var daveCredential *localauth.APITokenCredential
	if daveCredential, err = authentication.CreateAPIToken(dave.ID, "lifecycle test", 24*time.Hour); err != nil {
		t.Fatalf("create Dave API token: %v", err)
	}
	if csrfToken, err = requestCSRFToken(t, application, jar); err != nil {
		t.Fatalf("get admin CSRF token: %v", err)
	}
	response = performRequest(t, application, jar, http.MethodPost, "/api/v1/admin/vm-templates", `{"display_name":"Fedora Server","description":"Lifecycle source","source_id":"157","guest_os":"fedora","guest_os_version":"44","edition":"server","architecture":"x86_64","execution_method":"qemu_guest_agent","aliases":["fedora-server-latest"]}`, csrfToken)
	var templateResult struct {
		Template struct {
			Template struct {
				ID int `json:"id"`
			} `json:"template"`
		} `json:"template"`
	}
	if response.StatusCode != fiber.StatusCreated || json.NewDecoder(response.Body).Decode(&templateResult) != nil {
		response.Body.Close()
		t.Fatalf("create source catalog record: status %d", response.StatusCode)
	}
	response.Body.Close()
	var readinessPath string = "/api/v1/admin/vm-templates/" + strconv.Itoa(templateResult.Template.Template.ID)
	response = performRequest(t, application, jar, http.MethodPost, readinessPath+"/preflight", "{}", csrfToken)
	response.Body.Close()
	if response.StatusCode != fiber.StatusOK {
		t.Fatalf("run passing source preflight: status %d", response.StatusCode)
	}
	response = performRequest(t, application, jar, http.MethodPut, readinessPath+"/readiness", `{"guest_agent_root_verified":true,"provisioning_account_removed":true}`, csrfToken)
	response.Body.Close()
	if response.StatusCode != fiber.StatusOK {
		t.Fatalf("mark source ready: status %d", response.StatusCode)
	}
	response = performRequest(t, application, jar, http.MethodPut, "/api/v1/admin/proxmox/resources", `{"limits":{"virtual_cpus":8,"memory_mib":16384,"storage_gib":256},"resource_pools":["class-labs"],"storages":["local-lvm"],"networks":[]}`, csrfToken)
	response.Body.Close()
	if response.StatusCode != fiber.StatusOK {
		t.Fatalf("validate and save resource policy: status %d", response.StatusCode)
	}
	response = performRequest(t, application, jar, http.MethodPost, "/api/v1/deployments", `{"name":"vm-lifecycle","description":"Proxmox integration"}`, csrfToken)
	var deploymentResult struct {
		Deployment struct {
			ID         int `json:"id"`
			RootNodeID int `json:"root_node_id"`
		} `json:"deployment"`
	}
	if response.StatusCode != fiber.StatusCreated || json.NewDecoder(response.Body).Decode(&deploymentResult) != nil {
		response.Body.Close()
		t.Fatalf("create deployment: status %d", response.StatusCode)
	}
	response.Body.Close()
	var createPath string = "/api/v1/deployments/" + strconv.Itoa(deploymentResult.Deployment.ID) + "/virtual-machines"
	var createBody string = `{"parent_node_id":` + strconv.Itoa(deploymentResult.Deployment.RootNodeID) + `,"name":"charlie-fedora","provisioning_mode":"proxmox","template":"fedora-server-latest","pool":"class-labs","storage":"local-lvm","cpu_cores":2,"memory_mib":4096,"boot_disk_gib":64}`
	response = performRequest(t, application, jar, http.MethodPost, createPath, createBody, csrfToken)
	var vmResult struct {
		Resource struct {
			ID          int    `json:"id"`
			OwnershipID int    `json:"ownership_id"`
			ExternalID  string `json:"external_id"`
		} `json:"resource"`
	}
	if response.StatusCode != fiber.StatusCreated || json.NewDecoder(response.Body).Decode(&vmResult) != nil {
		response.Body.Close()
		t.Fatalf("clone ready VM source: status %d", response.StatusCode)
	}
	response.Body.Close()
	if fakeDriver.cloneCount != 1 || vmResult.Resource.ExternalID != "901" {
		t.Fatalf("expected one persisted Proxmox clone mapping, clones=%d resource=%#v", fakeDriver.cloneCount, vmResult.Resource)
	}
	var overCapacityBody string = strings.Replace(strings.Replace(createBody, "charlie-fedora", "over-capacity-vm", 1), `"cpu_cores":2`, `"cpu_cores":7`, 1)
	response = performRequest(t, application, jar, http.MethodPost, createPath, overCapacityBody, csrfToken)
	response.Body.Close()
	if response.StatusCode != fiber.StatusBadRequest || fakeDriver.cloneCount != 1 {
		t.Fatalf("over-capacity request must be rejected before PVE clone: status=%d clones=%d", response.StatusCode, fakeDriver.cloneCount)
	}
	var unauthorizedPoolBody string = strings.Replace(strings.Replace(createBody, "charlie-fedora", "unauthorized-pool-vm", 1), "class-labs", "unapproved-pool", 1)
	response = performRequest(t, application, jar, http.MethodPost, createPath, unauthorizedPoolBody, csrfToken)
	response.Body.Close()
	if response.StatusCode != fiber.StatusBadRequest || fakeDriver.cloneCount != 1 {
		t.Fatalf("unapproved pool must be rejected before PVE clone: status=%d clones=%d", response.StatusCode, fakeDriver.cloneCount)
	}
	var recoveryBody string = strings.Replace(createBody, "charlie-fedora", "recoverable-fedora", 1)
	fakeDriver.failNextClone = true
	response = performRequest(t, application, jar, http.MethodPost, createPath, recoveryBody, csrfToken)
	response.Body.Close()
	if response.StatusCode != fiber.StatusBadGateway || fakeDriver.cloneCount != 2 {
		t.Fatalf("interrupted clone should retain one recoverable PVE VM: status=%d clones=%d", response.StatusCode, fakeDriver.cloneCount)
	}
	var pendingResources []*db.ManagedResource
	if pendingResources, err = store.ManagedResources.SelectAll(); err != nil {
		t.Fatalf("load retry reservation: %v", err)
	}
	var pendingID int
	for _, pending := range pendingResources {
		if pending.Name == "recoverable-fedora" && pending.ExternalID == "" {
			pendingID = pending.ID
		}
	}
	if pendingID < 1 {
		t.Fatal("interrupted provisioning must keep an ownership reservation for retry")
	}
	response = performRequest(t, application, jar, http.MethodPost, createPath, recoveryBody, csrfToken)
	var recoveredResult struct {
		Resource struct {
			ID         int    `json:"id"`
			ExternalID string `json:"external_id"`
		} `json:"resource"`
	}
	if response.StatusCode != fiber.StatusCreated || json.NewDecoder(response.Body).Decode(&recoveredResult) != nil {
		response.Body.Close()
		t.Fatalf("retry should recover the interrupted clone: status %d", response.StatusCode)
	}
	response.Body.Close()
	if recoveredResult.Resource.ID != pendingID || recoveredResult.Resource.ExternalID != "902" || fakeDriver.cloneCount != 2 {
		t.Fatalf("retry duplicated or remapped the interrupted clone: resource=%#v clones=%d", recoveredResult.Resource, fakeDriver.cloneCount)
	}
	for _, permission := range []string{db.PermissionResourceView, db.PermissionResourcePower} {
		var grantBody string = `{"subject_kind":0,"subject_id":` + strconv.Itoa(charlie.ID) + `,"permission":"` + permission + `","inherit_descendants":false}`
		response = performRequest(t, application, jar, http.MethodPost, "/api/v1/ownership-nodes/"+strconv.Itoa(vmResult.Resource.OwnershipID)+"/grants", grantBody, csrfToken)
		response.Body.Close()
		if response.StatusCode != fiber.StatusCreated {
			t.Fatalf("grant Charlie %s: status %d", permission, response.StatusCode)
		}
	}
	response = performBearerRequest(t, application, http.MethodGet, "/api/v1/deployments/"+strconv.Itoa(deploymentResult.Deployment.ID), "", charlieCredential.Secret)
	var detail struct {
		Resources []struct {
			PowerState      string `json:"power_state"`
			CanPowerControl bool   `json:"can_power_control"`
		} `json:"resources"`
	}
	if response.StatusCode != fiber.StatusOK || json.NewDecoder(response.Body).Decode(&detail) != nil {
		response.Body.Close()
		t.Fatalf("read authorized live deployment: status %d", response.StatusCode)
	}
	response.Body.Close()
	if len(detail.Resources) != 1 || detail.Resources[0].PowerState != "stopped" || !detail.Resources[0].CanPowerControl {
		t.Fatalf("deployment summary did not return live state and power capability: %#v", detail)
	}
	var powerPath string = "/api/v1/virtual-machines/" + strconv.Itoa(vmResult.Resource.ID) + "/power"
	response = performBearerRequest(t, application, http.MethodPost, powerPath, `{"action":"start"}`, charlieCredential.Secret)
	response.Body.Close()
	if response.StatusCode != fiber.StatusOK || fakeDriver.state != "running" {
		t.Fatalf("authorized start did not reach Proxmox: status=%d state=%s", response.StatusCode, fakeDriver.state)
	}
	response = performBearerRequest(t, application, http.MethodGet, "/api/v1/deployments/"+strconv.Itoa(deploymentResult.Deployment.ID), "", charlieCredential.Secret)
	if response.StatusCode != fiber.StatusOK || json.NewDecoder(response.Body).Decode(&detail) != nil {
		response.Body.Close()
		t.Fatalf("refresh live VM state after start: status %d", response.StatusCode)
	}
	response.Body.Close()
	if len(detail.Resources) != 1 || detail.Resources[0].PowerState != "running" {
		t.Fatalf("deployment refresh did not reflect Proxmox running state: %#v", detail.Resources)
	}
	response = performBearerRequest(t, application, http.MethodPost, powerPath, `{"action":"stop"}`, daveCredential.Secret)
	response.Body.Close()
	if response.StatusCode != fiber.StatusForbidden || fakeDriver.state != "running" {
		t.Fatalf("unauthorized power request must be 403 without changing PVE: status=%d state=%s", response.StatusCode, fakeDriver.state)
	}
	response = performBearerRequest(t, application, http.MethodPost, powerPath, `{"action":"stop"}`, charlieCredential.Secret)
	response.Body.Close()
	if response.StatusCode != fiber.StatusOK || fakeDriver.state != "stopped" {
		t.Fatalf("authorized stop did not reach Proxmox: status=%d state=%s", response.StatusCode, fakeDriver.state)
	}
	response = performBearerRequest(t, application, http.MethodDelete, "/api/v1/virtual-machines/"+strconv.Itoa(vmResult.Resource.ID), "", charlieCredential.Secret)
	response.Body.Close()
	if response.StatusCode != fiber.StatusForbidden || fakeDriver.deleteCount != 0 {
		t.Fatalf("unauthorized destroy must be rejected without deleting the VM: status=%d deletes=%d", response.StatusCode, fakeDriver.deleteCount)
	}
	response = performRequest(t, application, jar, http.MethodDelete, "/api/v1/virtual-machines/"+strconv.Itoa(recoveredResult.Resource.ID), "", csrfToken)
	response.Body.Close()
	if response.StatusCode != fiber.StatusNoContent || fakeDriver.deletedVMIDs[len(fakeDriver.deletedVMIDs)-1] != "902" {
		t.Fatalf("destroy of recovered VM removed the wrong Proxmox resource: status=%d deleted=%v", response.StatusCode, fakeDriver.deletedVMIDs)
	}
	response = performRequest(t, application, jar, http.MethodDelete, "/api/v1/virtual-machines/"+strconv.Itoa(vmResult.Resource.ID), "", csrfToken)
	response.Body.Close()
	if response.StatusCode != fiber.StatusNoContent || fakeDriver.deleteCount != 2 || fakeDriver.deletedVMIDs[len(fakeDriver.deletedVMIDs)-1] != "901" {
		t.Fatalf("admin destroy must delete only the managed clone: status=%d deletes=%d vmids=%v", response.StatusCode, fakeDriver.deleteCount, fakeDriver.deletedVMIDs)
	}
}

type fakeProxmoxInventory struct{}

func (fakeProxmoxInventory) ReadResourceInventory(context.Context) (inventory proxmox.ResourceInventory, err error) {
	inventory = proxmox.ResourceInventory{Pools: []string{"class-labs"}, Storages: []string{"local-lvm"}}
	return
}

type fakeProxmoxVMDriver struct {
	state           string
	cloneCount      int
	deleteCount     int
	failNextClone   bool
	placementsByKey map[string]proxmox.VMPlacement
	deletedVMIDs    []string
}

func (driver *fakeProxmoxVMDriver) Clone(_ context.Context, request proxmox.VMCloneRequest) (placement proxmox.VMPlacement, err error) {
	if placement, exists := driver.placementsByKey[request.OperationKey]; exists {
		return placement, nil
	}
	driver.cloneCount++
	var vmid string = "901"
	if driver.cloneCount > 1 {
		vmid = "902"
	}
	placement = proxmox.VMPlacement{VMID: vmid, Node: "pve1", Name: request.Name, PowerState: "stopped"}
	if driver.placementsByKey == nil {
		driver.placementsByKey = make(map[string]proxmox.VMPlacement)
	}
	driver.placementsByKey[request.OperationKey] = placement
	if driver.failNextClone {
		driver.failNextClone = false
		err = errors.New("simulated response interruption after Proxmox accepted the clone")
	}
	return
}

func (driver *fakeProxmoxVMDriver) Read(_ context.Context, node string, vmid string, _ string) (placement proxmox.VMPlacement, err error) {
	placement = proxmox.VMPlacement{VMID: vmid, Node: node, Name: "charlie-fedora", PowerState: driver.state}
	return
}

func (driver *fakeProxmoxVMDriver) Power(_ context.Context, node string, vmid string, _ string, action string) (placement proxmox.VMPlacement, err error) {
	if action == "start" {
		driver.state = "running"
	} else {
		driver.state = "stopped"
	}
	placement = proxmox.VMPlacement{VMID: vmid, Node: node, Name: "charlie-fedora", PowerState: driver.state}
	return
}

func (driver *fakeProxmoxVMDriver) Delete(_ context.Context, _ string, vmid string, _ string) (err error) {
	driver.deleteCount++
	driver.deletedVMIDs = append(driver.deletedVMIDs, vmid)
	return
}

type testProxmoxInspector struct{}

func (testProxmoxInspector) Inspect(_ context.Context, sourceID string, _ string) (result proxmox.PreflightResult, err error) {
	result = proxmox.PreflightResult{
		SourceID:               sourceID,
		Node:                   "test-node",
		Name:                   "test-source",
		PowerState:             "running",
		GuestOSID:              "fedora",
		AgentConfigured:        true,
		AgentReachable:         true,
		GuestAgentRootVerified: true,
		IsQEMU:                 true,
		Passed:                 true,
		Checks: []proxmox.Check{
			{Name: "source_exists", Passed: true, Required: true},
			{Name: "guest_agent_root_execution", Passed: true, Required: true},
		},
	}
	return
}

func requestCSRFToken(t *testing.T, application *fiber.App, jar *cookiejar.Jar) (token string, err error) {
	t.Helper()
	var response *http.Response = performRequest(t, application, jar, http.MethodGet, "/api/v1/auth/csrf", "", "")
	defer response.Body.Close()
	var result struct {
		Token string `json:"csrf_token"`
	}
	if err = json.NewDecoder(response.Body).Decode(&result); err != nil {
		return
	}
	token = result.Token
	if response.StatusCode != fiber.StatusOK || token == "" {
		t.Fatalf("expected a CSRF token response, got status %d", response.StatusCode)
	}
	return
}

func performRequest(t *testing.T, application *fiber.App, jar *cookiejar.Jar, method string, path string, body string, csrfToken string) (response *http.Response) {
	return performRequestWithAuthorization(t, application, jar, method, path, body, csrfToken, "")
}

func performBearerRequest(t *testing.T, application *fiber.App, method string, path string, body string, token string) (response *http.Response) {
	return performRequestWithAuthorization(t, application, nil, method, path, body, "", "Bearer "+token)
}

func performRequestWithAuthorization(t *testing.T, application *fiber.App, jar *cookiejar.Jar, method string, path string, body string, csrfToken string, authorization string) (response *http.Response) {
	t.Helper()
	var requestURL *url.URL
	var err error
	if requestURL, err = url.Parse("http://organesson.test" + path); err != nil {
		t.Fatalf("parse request URL: %v", err)
	}
	var requestBody io.Reader
	if body != "" {
		requestBody = strings.NewReader(body)
	}
	var request *http.Request
	if request, err = http.NewRequest(method, requestURL.String(), requestBody); err != nil {
		t.Fatalf("create request: %v", err)
	}
	if jar != nil {
		for _, cookie := range jar.Cookies(requestURL) {
			request.AddCookie(cookie)
		}
	}
	if body != "" {
		request.Header.Set("Content-Type", "application/json")
	}
	if csrfToken != "" {
		request.Header.Set("X-Csrf-Token", csrfToken)
	}
	if authorization != "" {
		request.Header.Set("Authorization", authorization)
	}
	if response, err = application.Test(request); err != nil {
		t.Fatalf("perform %s %s: %v", method, path, err)
	}
	if jar != nil {
		jar.SetCookies(requestURL, response.Cookies())
	}
	return
}
