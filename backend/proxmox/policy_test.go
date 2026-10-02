package proxmox

import "testing"

func TestValidateResourcePolicyRequiresPoolAndStorage(t *testing.T) {
	var result ResourcePolicyValidation = ValidateResourcePolicy(ResourcePolicy{}, nil)
	if result.Valid || len(result.Issues) != 2 {
		t.Fatalf("expected missing pool and storage errors, got %#v", result)
	}
}

func TestValidateResourcePolicyChecksPVEInventoryAndAddressRanges(t *testing.T) {
	var policy ResourcePolicy = ResourcePolicy{
		ResourcePools: []string{"students"},
		Storages:      []string{"local-lvm"},
		Networks: []PolicyNetwork{{
			Name: "student-lan", Kind: "vnet", PVEName: "vnet-students",
			AddressPools: []AddressPool{{Name: "lab", Prefix: "192.0.2.0/24", Start: "192.0.2.10", End: "192.0.2.20", Gateway: "192.0.2.1", DNS: []string{"192.0.2.2"}}},
		}},
	}
	var inventory ResourceInventory = ResourceInventory{Pools: []string{"students"}, Storages: []string{"local-lvm"}, VNets: []string{"vnet-students"}}
	if result := ValidateResourcePolicy(policy, &inventory); !result.Valid {
		t.Fatalf("valid policy rejected: %#v", result.Issues)
	}
	policy.Networks[0].AddressPools[0].End = "192.0.3.20"
	if result := ValidateResourcePolicy(policy, &inventory); result.Valid {
		t.Fatal("address range outside its prefix was accepted")
	}
	policy.Networks[0].AddressPools[0].End = "192.0.2.20"
	policy.Storages[0] = "missing-storage"
	if result := ValidateResourcePolicy(policy, &inventory); result.Valid {
		t.Fatal("storage missing from inventory was accepted")
	}
}

func TestResourcePolicyHashIsStableAndChangesWithPolicy(t *testing.T) {
	var first string
	var second string
	var changed string
	var err error
	var policy ResourcePolicy = ResourcePolicy{ResourcePools: []string{"pool"}, Storages: []string{"storage"}}
	if first, err = ResourcePolicyHash(policy); err != nil {
		t.Fatal(err)
	}
	if second, err = ResourcePolicyHash(policy); err != nil {
		t.Fatal(err)
	}
	if first != second {
		t.Fatal("same policy produced different hashes")
	}
	policy.Limits.VirtualCPUs = 12
	if changed, err = ResourcePolicyHash(policy); err != nil {
		t.Fatal(err)
	}
	if changed == first {
		t.Fatal("changed policy kept the original hash")
	}
}

func TestAllocateAddressesPreservesConfiguredNetworkSemantics(t *testing.T) {
	var pool AddressPool = AddressPool{
		Name: "cyber-lab", Prefix: "10.0.0.0/8", AllocationPrefix: "10.192.0.0/12", Start: "10.192.0.1", End: "10.192.0.6",
		Gateway: "10.0.0.1", DNS: []string{"10.0.0.2"},
	}
	var addresses []string
	var err error
	if addresses, err = AllocateAddresses(pool, 3, []string{"10.192.0.4"}); err != nil {
		t.Fatalf("allocate addresses: %v", err)
	}
	if len(addresses) != 3 || addresses[0] != "10.192.0.1" || addresses[1] != "10.192.0.2" || addresses[2] != "10.192.0.3" {
		t.Fatalf("unexpected allocated addresses: %v", addresses)
	}
	if pool.Prefix != "10.0.0.0/8" || pool.Gateway != "10.0.0.1" || len(pool.DNS) != 1 || pool.DNS[0] != "10.0.0.2" {
		t.Fatal("allocation changed the configured source-network semantics")
	}
}

func TestAddressPoolRequiresAllocationSubnetInsideGuestNetwork(t *testing.T) {
	if err := validateAddressPool(AddressPool{
		Name: "invalid", Prefix: "10.0.0.0/8", AllocationPrefix: "172.16.0.0/12",
		Start: "172.16.0.10", End: "172.16.0.20", Gateway: "10.0.0.1",
	}); err == nil {
		t.Fatal("allocation subnet outside the configured guest network was accepted")
	}
}

func TestAllocateAddressesRejectsExhaustedPool(t *testing.T) {
	var _, err = AllocateAddresses(AddressPool{
		Name: "tiny", Prefix: "192.0.2.0/29", Start: "192.0.2.1", End: "192.0.2.2",
	}, 2, []string{"192.0.2.1"})
	if err == nil {
		t.Fatal("expected an exhausted address pool to fail")
	}
}
