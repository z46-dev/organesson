package v1

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/gofiber/fiber/v3"
	"github.com/z46-dev/golog"
	"github.com/z46-dev/organesson/backend/app/api/common"
	"github.com/z46-dev/organesson/backend/db"
	"github.com/z46-dev/organesson/backend/domain"
	"github.com/z46-dev/organesson/backend/proxmox"
)

type (
	testGuestNetworkVMDriver struct {
		powerState string
		missing    bool
	}

	testGuestNetworkDriver struct {
		readCount int
		readErr   error
	}
)

func (driver *testGuestNetworkVMDriver) Clone(context.Context, proxmox.VMCloneRequest) (proxmox.VMPlacement, error) {
	return proxmox.VMPlacement{}, nil
}

func (driver *testGuestNetworkVMDriver) Read(_ context.Context, node string, vmid string, _ string) (placement proxmox.VMPlacement, err error) {
	if driver.missing {
		err = proxmox.ErrVMNotFound
		return
	}
	placement = proxmox.VMPlacement{Node: node, VMID: vmid, PowerState: driver.powerState}
	return
}

func (driver *testGuestNetworkVMDriver) Power(_ context.Context, node string, vmid string, _ string, action string) (placement proxmox.VMPlacement, err error) {
	driver.powerState = "running"
	if action == "stop" {
		driver.powerState = "stopped"
	}
	placement = proxmox.VMPlacement{Node: node, VMID: vmid, PowerState: driver.powerState}
	return
}

// TestGuestSetupStartsVMWhenPersistedPowerStateIsStale uses live PVE state instead of trusting SQLite.
func TestGuestSetupStartsVMWhenPersistedPowerStateIsStale(t *testing.T) {
	var store *db.Store
	var err error
	if store, err = db.Open(filepath.Join(t.TempDir(), "organesson.db"), golog.New(), false); err != nil {
		t.Fatalf("open database: %v", err)
	}
	defer store.Close()

	var admin *db.Account
	if admin, _, err = store.InitialAdministrator(); err != nil {
		t.Fatalf("get administrator: %v", err)
	}
	var domainService *domain.Service = domain.New(store)
	var deployment *db.Deployment
	if deployment, err = domainService.CreateDeployment(admin.ID, "stopped-guest", ""); err != nil {
		t.Fatalf("create deployment: %v", err)
	}
	var root *db.OwnershipNode
	if root, err = store.OwnershipNodes.Select(*deployment.RootNodeID); err != nil {
		t.Fatalf("load deployment root: %v", err)
	}
	var ownership *db.OwnershipNode = &db.OwnershipNode{
		DeploymentID: deployment.ID, ParentID: &root.ID, Kind: db.OwnershipNodeKindResource,
		Name: "fedora", CreatedAt: time.Now(),
	}
	if err = store.OwnershipNodes.Insert(ownership); err != nil {
		t.Fatalf("create VM ownership node: %v", err)
	}
	var vm *db.ManagedResource = &db.ManagedResource{
		DeploymentID: deployment.ID, OwnershipID: ownership.ID, Kind: "virtual_machine", Name: "fedora",
		PowerState: "running", ExternalID: "101", ExternalNode: "pve1", OperationKey: "og-vm-test", CreatedAt: time.Now(),
	}
	if err = store.ManagedResources.Insert(vm); err != nil {
		t.Fatalf("create managed VM record: %v", err)
	}

	var vmDriver *testGuestNetworkVMDriver = &testGuestNetworkVMDriver{powerState: "stopped"}
	var service *proxmox.Service = proxmox.NewWithVMDriver(vmDriver)
	var application *fiber.App = fiber.New()
	application.Post("/start", func(ctx fiber.Ctx) (err error) {
		return ensureGuestVMRunning(ctx, common.Services{Store: store, Proxmox: service}, vm.ID)
	})
	var response *http.Response
	if response, err = application.Test(httptest.NewRequest(http.MethodPost, "/start", nil)); err != nil {
		t.Fatalf("start stale guest: %v", err)
	}
	response.Body.Close()
	if response.StatusCode != fiber.StatusOK || vmDriver.powerState != "running" {
		t.Fatalf("stopped guest was not started using live state: status=%d pve_state=%q", response.StatusCode, vmDriver.powerState)
	}
	if vm, err = store.ManagedResources.Select(vm.ID); err != nil {
		t.Fatalf("reload VM state: %v", err)
	}
	if vm.PowerState != "running" {
		t.Fatalf("started guest's persisted power state is %q, want running", vm.PowerState)
	}
}

func TestGetVirtualMachineKeepsMissingProxmoxVMDeletable(t *testing.T) {
	var store *db.Store
	var err error
	if store, err = db.Open(filepath.Join(t.TempDir(), "organesson.db"), golog.New(), false); err != nil {
		t.Fatalf("open database: %v", err)
	}
	defer store.Close()

	var admin *db.Account
	if admin, _, err = store.InitialAdministrator(); err != nil {
		t.Fatalf("get administrator: %v", err)
	}
	var domainService *domain.Service = domain.New(store)
	var deployment *db.Deployment
	if deployment, err = domainService.CreateDeployment(admin.ID, "missing-vm", ""); err != nil {
		t.Fatalf("create deployment: %v", err)
	}
	var root *db.OwnershipNode
	if root, err = store.OwnershipNodes.Select(*deployment.RootNodeID); err != nil {
		t.Fatalf("load deployment root: %v", err)
	}
	var ownership *db.OwnershipNode = &db.OwnershipNode{
		DeploymentID: deployment.ID, ParentID: &root.ID, Kind: db.OwnershipNodeKindResource,
		Name: "missing-vm", CreatedAt: time.Now(),
	}
	if err = store.OwnershipNodes.Insert(ownership); err != nil {
		t.Fatalf("create VM ownership node: %v", err)
	}
	var vm *db.ManagedResource = &db.ManagedResource{
		DeploymentID: deployment.ID, OwnershipID: ownership.ID, Kind: "virtual_machine", Name: "missing-vm",
		PowerState: "running", ExternalID: "101", ExternalNode: "pve1", OperationKey: "og-vm-test", CreatedAt: time.Now(),
	}
	if err = store.ManagedResources.Insert(vm); err != nil {
		t.Fatalf("create managed VM record: %v", err)
	}

	var service *proxmox.Service = proxmox.NewWithVMDriver(&testGuestNetworkVMDriver{missing: true})
	var application *fiber.App = fiber.New()
	application.Get("/virtual-machines/:resource_id", func(ctx fiber.Ctx) (err error) {
		ctx.Locals("account_id", admin.ID)
		return getVirtualMachine(common.Services{Store: store, Domain: domainService, Proxmox: service})(ctx)
	})
	var response *http.Response
	if response, err = application.Test(httptest.NewRequest(http.MethodGet, "/virtual-machines/"+strconv.Itoa(vm.ID), nil)); err != nil {
		t.Fatalf("refresh missing VM: %v", err)
	}
	defer response.Body.Close()
	var body []byte
	if body, err = io.ReadAll(response.Body); err != nil {
		t.Fatalf("read VM response: %v", err)
	}
	if response.StatusCode != fiber.StatusOK || !strings.Contains(string(body), `"power_state":"missing"`) {
		t.Fatalf("missing Proxmox VM should remain available for the subsequent DELETE call: status=%d body=%s", response.StatusCode, body)
	}
}

func (driver *testGuestNetworkVMDriver) Delete(context.Context, string, string, string) error {
	return nil
}

func (driver *testGuestNetworkVMDriver) DeleteUnplaced(context.Context, string, string) error {
	return nil
}

func (driver *testGuestNetworkDriver) Configure(context.Context, proxmox.GuestNetworkRequest) error {
	return nil
}

func (driver *testGuestNetworkDriver) Read(context.Context, proxmox.GuestNetworkRequest) (err error) {
	driver.readCount++
	err = driver.readErr
	return
}

func (driver *testGuestNetworkDriver) Remove(context.Context, proxmox.GuestNetworkRequest) error {
	return nil
}

// TestStoppedGuestNetworkRefreshDefersQGAAndStillRefreshesRunningGuests protects provider destroy refresh.
func TestStoppedGuestNetworkRefreshDefersQGAAndStillRefreshesRunningGuests(t *testing.T) {
	var guestNetworkDriver *testGuestNetworkDriver = &testGuestNetworkDriver{}
	var vmDriver *testGuestNetworkVMDriver = &testGuestNetworkVMDriver{powerState: "stopped"}
	var service *proxmox.Service = proxmox.NewWithProvisioningDrivers(vmDriver, nil, nil, nil, guestNetworkDriver)
	var application *fiber.App = fiber.New()
	application.Get("/verify", func(ctx fiber.Ctx) (err error) {
		var deferred bool
		if deferred, err = verifyGuestNetworkConfiguration(ctx, common.Services{Proxmox: service}, proxmox.GuestNetworkRequest{
			Node: "pve1", VMID: "101", VMOperationKey: "og-vm-test", AttachmentKey: "og-nic-test",
			Bridge: "vmbr0", Placement: proxmox.NetworkAttachmentPlacement{Device: "net0", MAC: "02:00:00:00:00:01"}, Method: "dhcp",
		}); err != nil {
			return ctx.Status(fiber.StatusBadGateway).SendString(err.Error())
		}
		return ctx.JSON(fiber.Map{"verification_deferred": deferred})
	})

	var response *http.Response
	var err error
	if response, err = application.Test(httptest.NewRequest(http.MethodGet, "/verify", nil)); err != nil {
		t.Fatalf("refresh stopped guest: %v", err)
	}
	response.Body.Close()
	if response.StatusCode != fiber.StatusOK || guestNetworkDriver.readCount != 0 {
		t.Fatalf("stopped guest should defer QGA verification: status=%d guest reads=%d", response.StatusCode, guestNetworkDriver.readCount)
	}

	vmDriver.powerState = "running"
	if response, err = application.Test(httptest.NewRequest(http.MethodGet, "/verify", nil)); err != nil {
		t.Fatalf("refresh running guest: %v", err)
	}
	if response.StatusCode != fiber.StatusOK || guestNetworkDriver.readCount != 1 {
		var body []byte
		body, _ = io.ReadAll(response.Body)
		t.Fatalf("running guest should verify through QGA: status=%d guest reads=%d body=%s", response.StatusCode, guestNetworkDriver.readCount, body)
	}
	response.Body.Close()
	guestNetworkDriver.readErr = errors.New("QEMU Guest Agent is unavailable")
	if response, err = application.Test(httptest.NewRequest(http.MethodGet, "/verify", nil)); err != nil {
		t.Fatalf("refresh guest without QGA: %v", err)
	}
	response.Body.Close()
	if response.StatusCode != fiber.StatusOK || guestNetworkDriver.readCount != 2 {
		t.Fatalf("unavailable QGA should defer verification: status=%d guest reads=%d", response.StatusCode, guestNetworkDriver.readCount)
	}
	guestNetworkDriver.readErr = nil

	vmDriver.powerState = "unknown"
	if response, err = application.Test(httptest.NewRequest(http.MethodGet, "/verify", nil)); err != nil {
		t.Fatalf("refresh guest with unknown power state: %v", err)
	}
	response.Body.Close()
	if response.StatusCode != fiber.StatusBadGateway || guestNetworkDriver.readCount != 2 {
		t.Fatalf("unknown guest state must fail closed: status=%d guest reads=%d", response.StatusCode, guestNetworkDriver.readCount)
	}

	vmDriver.missing = true
	if response, err = application.Test(httptest.NewRequest(http.MethodGet, "/verify", nil)); err != nil {
		t.Fatalf("refresh missing guest: %v", err)
	}
	response.Body.Close()
	if response.StatusCode != fiber.StatusOK || guestNetworkDriver.readCount != 2 {
		t.Fatalf("missing guest should defer QGA verification: status=%d guest reads=%d", response.StatusCode, guestNetworkDriver.readCount)
	}
}
