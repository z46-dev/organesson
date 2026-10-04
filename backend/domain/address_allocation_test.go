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
				Name: "classroom", Prefix: "10.0.0.0/8", AllocationPrefix: "10.192.0.8/29",
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
	if len(allocation.Addresses) != 2 || allocation.Addresses[0] != "10.192.0.9" || allocation.Addresses[1] != "10.192.0.10" {
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
	if second.Addresses[0] != "10.192.0.11" {
		t.Fatalf("second request reused an allocated address: %v", second.Addresses)
	}
	var vmOwner *db.OwnershipNode = &db.OwnershipNode{
		DeploymentID: deployment.ID, ParentID: deployment.RootNodeID, Kind: db.OwnershipNodeKindResource,
		Name: "student-vm", CreatedAt: time.Now().UTC(),
	}
	if err = store.OwnershipNodes.Insert(vmOwner); err != nil {
		t.Fatalf("insert VM ownership: %v", err)
	}
	var vm *db.ManagedResource = &db.ManagedResource{
		DeploymentID: deployment.ID, OwnershipID: vmOwner.ID, Kind: "virtual_machine", Name: "student-vm", PowerState: "running", CreatedAt: time.Now().UTC(),
	}
	if err = store.ManagedResources.Insert(vm); err != nil {
		t.Fatalf("insert VM: %v", err)
	}
	var attachmentOwner *db.OwnershipNode = &db.OwnershipNode{
		DeploymentID: deployment.ID, ParentID: &vmOwner.ID, Kind: db.OwnershipNodeKindResource,
		Name: "internet-nic", CreatedAt: time.Now().UTC(),
	}
	if err = store.OwnershipNodes.Insert(attachmentOwner); err != nil {
		t.Fatalf("insert attachment ownership: %v", err)
	}
	var attachmentConfiguration ManagedNetworkAttachmentConfiguration = ManagedNetworkAttachmentConfiguration{
		VirtualMachineID: vm.ID, AddressPoolRequestID: first.ID, Addresses: []string{allocation.Addresses[0]},
	}
	var attachmentJSON []byte
	if attachmentJSON, err = json.Marshal(attachmentConfiguration); err != nil {
		t.Fatalf("encode attachment: %v", err)
	}
	var attachment *db.ManagedResource = &db.ManagedResource{
		DeploymentID: deployment.ID, OwnershipID: attachmentOwner.ID, Kind: "network_attachment", Name: "internet-nic",
		PowerState: "ready", ConfigurationJSON: string(attachmentJSON), CreatedAt: time.Now().UTC(),
	}
	if err = store.ManagedResources.Insert(attachment); err != nil {
		t.Fatalf("insert attachment: %v", err)
	}
	var visibleAllocation AddressPoolRequest
	if _, visibleAllocation, err = service.GetAddressPoolRequest(admin.ID, first.ID); err != nil {
		t.Fatalf("get allocation usage as admin: %v", err)
	}
	if !visibleAllocation.AddressUsage[0].InUse || visibleAllocation.AddressUsage[0].VirtualMachineID != vm.ID || visibleAllocation.AddressUsage[0].VirtualMachineName != vm.Name {
		t.Fatalf("allocation did not identify visible VM usage: %#v", visibleAllocation.AddressUsage[0])
	}
	var allocationViewer *db.Account = &db.Account{DisplayName: "Allocation viewer", CreatedAt: time.Now().UTC()}
	if err = store.Accounts.Insert(allocationViewer); err != nil {
		t.Fatalf("insert allocation viewer: %v", err)
	}
	if _, err = service.CreatePermissionGrant(admin.ID, db.GrantSubjectKindAccount, allocationViewer.ID, db.PermissionResourceView, first.OwnershipID); err != nil {
		t.Fatalf("grant allocation visibility: %v", err)
	}
	var privateAllocation AddressPoolRequest
	if _, privateAllocation, err = service.GetAddressPoolRequest(allocationViewer.ID, first.ID); err != nil {
		t.Fatalf("get allocation usage as viewer: %v", err)
	}
	if !privateAllocation.AddressUsage[0].InUse || privateAllocation.AddressUsage[0].VirtualMachineID != 0 || privateAllocation.AddressUsage[0].VirtualMachineName != "" {
		t.Fatalf("allocation usage leaked an inaccessible VM: %#v", privateAllocation.AddressUsage[0])
	}
	if err = store.ManagedResources.Delete(attachment.ID); err != nil {
		t.Fatalf("remove test attachment: %v", err)
	}
	if err = store.OwnershipNodes.Delete(attachmentOwner.ID); err != nil {
		t.Fatalf("remove test attachment ownership: %v", err)
	}
	if err = store.ManagedResources.Delete(vm.ID); err != nil {
		t.Fatalf("remove test VM: %v", err)
	}
	if err = store.OwnershipNodes.Delete(vmOwner.ID); err != nil {
		t.Fatalf("remove test VM ownership: %v", err)
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
	if released.Addresses[0] != "10.192.0.9" {
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

func TestManagedNetworkAddressAllocationStaysInsideDeclaredRange(t *testing.T) {
	var addresses []string
	var err error
	if addresses, err = allocateManagedNetworkAddresses(proxmox.AddressPool{Prefix: "192.168.2.0/24"}, "192.168.2.2", "192.168.2.10", 3, []string{"192.168.2.2", "192.168.2.3"}); err != nil {
		t.Fatalf("allocate managed-network addresses: %v", err)
	}
	if len(addresses) != 3 || addresses[0] != "192.168.2.4" || addresses[1] != "192.168.2.5" || addresses[2] != "192.168.2.6" {
		t.Fatalf("unexpected managed-network allocation: %v", addresses)
	}
	if _, err = allocateManagedNetworkAddresses(proxmox.AddressPool{Prefix: "192.168.2.0/24"}, "192.168.2.2", "192.168.2.3", 2, []string{"192.168.2.2", "192.168.2.3"}); err == nil {
		t.Fatal("expected exhausted range to fail")
	}
}

func TestManagedNetworkAddressPoolRequestsReserveUniqueHosts(t *testing.T) {
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
	if deployment, err = service.CreateDeployment(admin.ID, "managed-address-test", "managed address range test"); err != nil {
		t.Fatalf("create deployment: %v", err)
	}
	var networkOwner *db.OwnershipNode = &db.OwnershipNode{
		DeploymentID: deployment.ID, ParentID: deployment.RootNodeID, Kind: db.OwnershipNodeKindResource,
		Name: "private-network", CreatedAt: time.Now().UTC(),
	}
	if err = store.OwnershipNodes.Insert(networkOwner); err != nil {
		t.Fatalf("insert network ownership: %v", err)
	}
	var networkConfiguration []byte
	if networkConfiguration, err = json.Marshal(ManagedNetworkConfiguration{
		Request: proxmox.SDNNetworkRequest{Name: "private", Mode: "managed", Subnet: "192.168.2.0/24", Gateway: "192.168.2.1"},
	}); err != nil {
		t.Fatalf("encode network configuration: %v", err)
	}
	var network *db.ManagedResource = &db.ManagedResource{
		DeploymentID: deployment.ID, OwnershipID: networkOwner.ID, Kind: "virtual_network", Name: "private",
		PowerState: "ready", ConfigurationJSON: string(networkConfiguration), CreatedAt: time.Now().UTC(),
	}
	if err = store.ManagedResources.Insert(network); err != nil {
		t.Fatalf("insert managed network: %v", err)
	}
	var request AddressPoolRequest = AddressPoolRequest{
		DeploymentID: deployment.ID, LogicalNetworkID: network.ID, Name: "student-addresses",
		AddressFamily: "ipv4", AddressCount: 3, RangeStart: "192.168.2.2", RangeEnd: "192.168.2.10",
	}
	var allocation AddressPoolRequest
	if _, allocation, err = service.ReserveAddressPoolRequest(admin.ID, request); err != nil {
		t.Fatalf("reserve student addresses: %v", err)
	}
	if len(allocation.Addresses) != 3 || allocation.Addresses[0] != "192.168.2.2" || allocation.Addresses[2] != "192.168.2.4" || allocation.Prefix != "192.168.2.0/24" || allocation.Gateway != "192.168.2.1" {
		t.Fatalf("unexpected managed address allocation: %#v", allocation)
	}
	request.Name = "second-student-addresses"
	var next AddressPoolRequest
	if _, next, err = service.ReserveAddressPoolRequest(admin.ID, request); err != nil {
		t.Fatalf("reserve next student addresses: %v", err)
	}
	if next.Addresses[0] != "192.168.2.5" {
		t.Fatalf("managed address request reused a reserved address: %v", next.Addresses)
	}
}
