package domain

import (
	"encoding/json"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/z46-dev/golog"
	"github.com/z46-dev/organesson/backend/db"
	"github.com/z46-dev/organesson/backend/proxmox"
)

func TestAddressPoolRequestsPersistAndUniquelyAllocate(t *testing.T) {
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
	var deployment *db.Deployment
	if deployment, err = service.CreateDeployment(admin.ID, "address-test", "allocation test"); err != nil {
		t.Fatalf("create deployment: %v", err)
	}
	var policy proxmox.ResourcePolicy = proxmox.ResourcePolicy{
		ResourcePools: []string{"students"},
		Storages:      []string{"local-lvm"},
		Networks: []proxmox.PolicyNetwork{{
			Name: "cyber.lab", Kind: "bridge", PVEName: "vmbr0",
			AddressPools: []proxmox.AddressPool{{
				Name: "classroom", Prefix: "10.0.0.0/8", AllocationPrefix: "10.192.0.0/12", Start: "10.192.0.10", End: "10.192.0.20",
				Gateway: "10.0.0.1", DNS: []string{"10.0.0.2"},
			}},
		}},
	}
	var encoded []byte
	if encoded, err = json.Marshal(policy); err != nil {
		t.Fatalf("encode policy: %v", err)
	}
	var configHash string
	if configHash, err = proxmox.ResourcePolicyHash(policy); err != nil {
		t.Fatalf("hash policy: %v", err)
	}
	var now time.Time = time.Now().UTC()
	if err = store.ProxmoxResourcePolicies.Insert(&db.ProxmoxResourcePolicy{
		ID: 1, ConfigurationJSON: string(encoded), ValidationJSON: `{"valid":true}`,
		ValidatedConfigHash: configHash, ValidatedAt: &now, UpdatedAt: now,
	}); err != nil {
		t.Fatalf("insert validated policy: %v", err)
	}
	var request AddressPoolRequest = AddressPoolRequest{
		DeploymentID: deployment.ID, Name: "student-internet", EnvironmentNetwork: "cyber.lab",
		AddressFamily: "ipv4", AddressCount: 2,
	}
	var first *db.ManagedResource
	var allocation AddressPoolRequest
	if first, allocation, err = service.ReserveAddressPoolRequest(admin.ID, request); err != nil {
		t.Fatalf("reserve first request: %v", err)
	}
	if len(allocation.Addresses) != 2 || allocation.Addresses[0] != "10.192.0.10" || allocation.Addresses[1] != "10.192.0.11" {
		t.Fatalf("unexpected first allocation: %v", allocation.Addresses)
	}
	if allocation.Prefix != "10.0.0.0/8" || allocation.Gateway != "10.0.0.1" || len(allocation.DNS) != 1 || allocation.DNS[0] != "10.0.0.2" {
		t.Fatalf("allocation did not preserve source network configuration: %#v", allocation)
	}
	var repeated *db.ManagedResource
	var repeatedAllocation AddressPoolRequest
	if repeated, repeatedAllocation, err = service.ReserveAddressPoolRequest(admin.ID, request); err != nil {
		t.Fatalf("repeat same request: %v", err)
	}
	if repeated.ID != first.ID || repeatedAllocation.Addresses[0] != allocation.Addresses[0] {
		t.Fatal("repeated apply did not preserve its allocation")
	}
	request.Name = "student-two"
	var second AddressPoolRequest
	if _, second, err = service.ReserveAddressPoolRequest(admin.ID, request); err != nil {
		t.Fatalf("reserve second request: %v", err)
	}
	if second.Addresses[0] != "10.192.0.12" {
		t.Fatalf("second request reused an allocated address: %v", second.Addresses)
	}
	request.Name = "student-internet"
	request.AddressCount = 3
	if _, _, err = service.ReserveAddressPoolRequest(admin.ID, request); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("changed existing request should fail, got %v", err)
	}
	if err = service.DeleteAddressPoolRequest(admin.ID, first.ID); err != nil {
		t.Fatalf("release first request: %v", err)
	}
	request.Name = "released-address"
	request.AddressCount = 1
	var released AddressPoolRequest
	if _, released, err = service.ReserveAddressPoolRequest(admin.ID, request); err != nil {
		t.Fatalf("reuse released address: %v", err)
	}
	if released.Addresses[0] != "10.192.0.10" {
		t.Fatalf("expected deleted reservation to release address, got %v", released.Addresses)
	}
	var unauthorized *db.Account = &db.Account{DisplayName: "Unauthorized", CreatedAt: time.Now().UTC()}
	if err = store.Accounts.Insert(unauthorized); err != nil {
		t.Fatalf("insert unauthorized account: %v", err)
	}
	request.Name = "unauthorized-request"
	if _, _, err = service.ReserveAddressPoolRequest(unauthorized.ID, request); !errors.Is(err, ErrForbidden) {
		t.Fatalf("ungranted account must not allocate deployment addresses, got %v", err)
	}
}
