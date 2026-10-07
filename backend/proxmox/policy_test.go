package proxmox

import "testing"

func TestValidateResourcePolicyRequiresPoolAndStorage(t *testing.T) {
	var result ResourcePolicyValidation = ValidateResourcePolicy(ResourcePolicy{}, nil)
	if result.Valid || len(result.Issues) != 2 {
		t.Fatalf("expected missing pool and storage errors, got %#v", result)
	}
}

func TestValidateResourcePolicyRejectsNegativeScopedLimits(t *testing.T) {
	var policy ResourcePolicy = ResourcePolicy{
		ResourcePools:    []string{"students"},
		Storages:         []string{"laas"},
		DeploymentLimits: DeploymentLimits{MaxResources: -1},
		VMLimits:         VMLimits{VirtualCPUs: -1},
	}
	if result := ValidateResourcePolicy(policy, nil); result.Valid {
		t.Fatal("negative deployment and VM limits were accepted")
	}
}

func TestValidateResourcePolicyChecksPVEInventoryAndAddressRanges(t *testing.T) {
	var policy ResourcePolicy = ResourcePolicy{
		ResourcePools: []string{"students"},
		Storages:      []string{"local-lvm"},
		Networks: []PolicyNetwork{{
			Name: "student-lan", Kind: "vnet", PVEName: "vnet-students",
			AddressPools: []AddressPool{{Name: "lab", Prefix: "192.0.2.0/24", AllocationPrefix: "192.0.2.0/27", Gateway: "192.0.2.1", DNS: []string{"192.0.2.2"}}},
		}},
	}
	var inventory ResourceInventory = ResourceInventory{
		Pools: []string{"students"}, Storages: []string{"local-lvm"}, VNets: []string{"vnet-students"},
		VNetSubnets: map[string][]PolicySubnet{"vnet-students": {{Prefix: "192.0.2.0/24", Gateway: "192.0.2.1"}}},
	}
	if result := ValidateResourcePolicy(policy, &inventory); !result.Valid {
		t.Fatalf("valid policy rejected: %#v", result.Issues)
	}
	policy.Networks[0].AddressPools[0].AllocationPrefix = "192.0.3.0/27"
	if result := ValidateResourcePolicy(policy, &inventory); result.Valid {
		t.Fatal("address range outside its prefix was accepted")
	}
	policy.Networks[0].AddressPools[0].AllocationPrefix = "192.0.2.0/27"
	policy.Storages[0] = "missing-storage"
	if result := ValidateResourcePolicy(policy, &inventory); result.Valid {
		t.Fatal("storage missing from inventory was accepted")
	}
}

func TestValidateResourcePolicyAllowsCreatedVNetAndConstrainsItsAddressPools(t *testing.T) {
	var policy ResourcePolicy = ResourcePolicy{
		ResourcePools:  []string{"students"},
		Storages:       []string{"laas"},
		VNetSourceZone: "ogvxlan",
		Networks: []PolicyNetwork{{
			Name: "student-lan", Kind: "vnet", TargetMode: "create", Subnets: []PolicySubnet{{Prefix: "192.0.2.0/24", Gateway: "192.0.2.1", DHCPEnabled: true}},
			AddressPools: []AddressPool{{Name: "students", Prefix: "192.0.2.0/24", AllocationPrefix: "192.0.2.0/27"}},
		}},
	}
	var inventory ResourceInventory = ResourceInventory{Pools: []string{"students"}, Storages: []string{"laas"}, VNetSources: []string{"ogvxlan"}}
	if result := ValidateResourcePolicy(policy, &inventory); !result.Valid {
		t.Fatalf("valid created VNet policy rejected: %#v", result.Issues)
	}
	policy.Networks[0].AddressPools[0].Prefix = "192.0.3.0/24"
	if result := ValidateResourcePolicy(policy, &inventory); result.Valid {
		t.Fatal("address pool outside created VNet subnet was accepted")
	}
	policy.Networks[0].AddressPools[0].Prefix = "192.0.2.0/24"
	policy.VNetSourceZone = "missing-zone"
	if result := ValidateResourcePolicy(policy, &inventory); result.Valid {
		t.Fatal("unavailable VNet source zone was accepted")
	}
}

func TestValidateResourcePolicyValidatesNodeScopedVLANTrunks(t *testing.T) {
	var policy ResourcePolicy = ResourcePolicy{
		ResourcePools: []string{"students"},
		Storages:      []string{"laas"},
		VLANTrunks: []VLANTrunk{
			{Node: "tungsten", Bridge: "ogtrunk", AllowedVLANRanges: []VLANRange{{Start: 4091, End: 4091}}},
			{Node: "osmium", Bridge: "ogtrunk", AllowedVLANRanges: []VLANRange{{Start: 4000, End: 4094}}},
		},
	}
	var inventory ResourceInventory = ResourceInventory{
		Pools:    []string{"students"},
		Storages: []string{"laas"},
		Nodes: []NodeNetworkInventory{
			{Name: "tungsten", Status: "online", Bridges: []BridgeNetworkInventory{{Name: "ogtrunk", VLANAware: true, HasPhysicalPorts: true}}},
			{Name: "osmium", Status: "online", Bridges: []BridgeNetworkInventory{{Name: "ogtrunk", VLANAware: true, HasPhysicalPorts: true}}},
		},
	}
	if result := ValidateResourcePolicy(policy, &inventory); !result.Valid {
		t.Fatalf("same bridge name on separate nodes should be valid: %#v", result.Issues)
	}
	policy.VLANTrunks = append(policy.VLANTrunks, policy.VLANTrunks[0])
	if result := ValidateResourcePolicy(policy, &inventory); result.Valid {
		t.Fatal("duplicate node and bridge trunk was accepted")
	}
	policy.VLANTrunks = policy.VLANTrunks[:2]
	policy.VLANTrunks[0].AllowedVLANRanges[0] = VLANRange{Start: 0, End: 4095}
	if result := ValidateResourcePolicy(policy, &inventory); result.Valid {
		t.Fatal("reserved or out-of-range VLAN IDs were accepted")
	}
	policy.VLANTrunks[0].AllowedVLANRanges[0] = VLANRange{Start: 4091, End: 4091}
	inventory.Nodes[0].Bridges[0].VLANAware = false
	if result := ValidateResourcePolicy(policy, &inventory); result.Valid {
		t.Fatal("non-VLAN-aware trunk bridge was accepted")
	}
	if len(inventory.Nodes[0].Bridges) > 0 {
		inventory.Nodes[0].Bridges[0].VLANAware = true
		inventory.Nodes[0].Bridges[0].HasIPConfig = true
	}
	if result := ValidateResourcePolicy(policy, &inventory); result.Valid {
		t.Fatal("trunk bridge with host IP configuration was accepted")
	}
}

func TestAuthorizeExternalVLANUsesOnlySelectedTrunkNode(t *testing.T) {
	var policy ResourcePolicy = ResourcePolicy{VLANTrunks: []VLANTrunk{
		{Node: "tungsten", Bridge: "ogtrunk", AllowedVLANRanges: []VLANRange{{Start: 2000, End: 2100}}},
		{Node: "osmium", Bridge: "ogtrunk", AllowedVLANRanges: []VLANRange{{Start: 2048, End: 2048}}},
		{Node: "other", Bridge: "other-trunk", AllowedVLANRanges: []VLANRange{{Start: 2000, End: 2100}}},
	}}
	var exposure *SDNExternalVLANExposure = &SDNExternalVLANExposure{TrunkNode: "tungsten", TrunkBridge: "ogtrunk", VLANID: 2048}
	var nodes []string
	var err error
	if nodes, err = AuthorizeExternalVLAN(policy, exposure); err != nil {
		t.Fatalf("authorize VLAN exposure: %v", err)
	}
	if len(nodes) != 1 || nodes[0] != "tungsten" {
		t.Fatalf("expected only the selected trunk node, got %#v", nodes)
	}
	exposure.TrunkNode = "osmium"
	if nodes, err = AuthorizeExternalVLAN(policy, exposure); err != nil || len(nodes) != 1 || nodes[0] != "osmium" {
		t.Fatalf("expected separately selected trunk node to be authorized, got nodes=%#v err=%v", nodes, err)
	}
	exposure.VLANID = 3000
	if _, err = AuthorizeExternalVLAN(policy, exposure); err == nil {
		t.Fatal("VLAN outside the authorized range was accepted")
	}
}

func TestValidateResourcePolicyAcceptsIPv6SubnetsAndPools(t *testing.T) {
	var policy ResourcePolicy = ResourcePolicy{
		ResourcePools:  []string{"students"},
		Storages:       []string{"laas"},
		VNetSourceZone: "ogvxlan",
		Networks: []PolicyNetwork{{
			Name: "ipv6-lan", Kind: "vnet", TargetMode: "create",
			Subnets:      []PolicySubnet{{Prefix: "2001:db8:1234::/64", Gateway: "2001:db8:1234::1"}},
			AddressPools: []AddressPool{{Name: "hosts", Prefix: "2001:db8:1234::/64", AllocationPrefix: "2001:db8:1234::/120", Gateway: "2001:db8:1234::1", DNS: []string{"2001:db8:1234::53"}}},
		}},
	}
	var inventory ResourceInventory = ResourceInventory{Pools: []string{"students"}, Storages: []string{"laas"}, VNetSources: []string{"ogvxlan"}}
	if result := ValidateResourcePolicy(policy, &inventory); !result.Valid {
		t.Fatalf("valid IPv6 network policy rejected: %#v", result.Issues)
	}
	policy.Networks[0].Subnets[0].Gateway = "192.0.2.1"
	if result := ValidateResourcePolicy(policy, &inventory); result.Valid {
		t.Fatal("mixed-family subnet gateway was accepted")
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
		Name: "cyber-lab", Prefix: "10.0.0.0/8", AllocationPrefix: "10.192.0.0/29",
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
		Name: "invalid", Prefix: "10.0.0.0/8", AllocationPrefix: "172.16.0.0/12", Gateway: "10.0.0.1",
	}); err == nil {
		t.Fatal("allocation subnet outside the configured guest network was accepted")
	}
}

func TestAllocateAddressesRejectsExhaustedPool(t *testing.T) {
	var _, err = AllocateAddresses(AddressPool{
		Name: "tiny", Prefix: "192.0.2.0/29",
	}, 2, []string{"192.0.2.1", "192.0.2.2", "192.0.2.3", "192.0.2.4", "192.0.2.5", "192.0.2.6"})
	if err == nil {
		t.Fatal("expected an exhausted address pool to fail")
	}
}

func TestAllocateAddressesExcludesIPv4NetworkAndBroadcastAddresses(t *testing.T) {
	var addresses []string
	var err error
	if addresses, err = AllocateAddresses(AddressPool{Name: "small", Prefix: "192.0.2.0/29"}, 6, nil); err != nil {
		t.Fatalf("allocate all usable IPv4 addresses: %v", err)
	}
	if len(addresses) != 6 || addresses[0] != "192.0.2.1" || addresses[5] != "192.0.2.6" {
		t.Fatalf("unexpected usable IPv4 range: %v", addresses)
	}
	if _, err = AllocateAddresses(AddressPool{Name: "small", Prefix: "192.0.2.0/29"}, 7, nil); err == nil {
		t.Fatal("IPv4 network and broadcast addresses were allocated")
	}
}

func TestAllocateAddressesSupportsIPv6Hosts(t *testing.T) {
	var addresses []string
	var err error
	if addresses, err = AllocateAddresses(AddressPool{
		Name: "v6-lan", Prefix: "2001:db8:1234::/64", AllocationPrefix: "2001:db8:1234::/125",
		Gateway: "2001:db8:1234::1", DNS: []string{"2001:db8:1234::2"},
	}, 3, []string{"2001:db8:1234::4"}); err != nil {
		t.Fatalf("allocate IPv6 addresses: %v", err)
	}
	if len(addresses) != 3 || addresses[0] != "2001:db8:1234::3" || addresses[1] != "2001:db8:1234::5" || addresses[2] != "2001:db8:1234::6" {
		t.Fatalf("unexpected IPv6 allocation: %v", addresses)
	}
}
