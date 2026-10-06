package proxmox

import (
	"context"
	"testing"
	"time"

	pve "github.com/luthermonson/go-proxmox"
)

type testRouterPollingDriver struct {
	polls int
}

func (driver *testRouterPollingDriver) Create(context.Context, SDNNetworkRequest) (SDNNetworkPlacement, error) {
	return SDNNetworkPlacement{}, nil
}

func (driver *testRouterPollingDriver) Read(context.Context, SDNNetworkRequest, SDNNetworkPlacement) error {
	return nil
}

func (driver *testRouterPollingDriver) Delete(context.Context, SDNNetworkRequest, SDNNetworkPlacement) error {
	return nil
}

func (driver *testRouterPollingDriver) PollRouter(_ context.Context, request SDNNetworkRequest, _ SDNNetworkPlacement) (result SDNRouterPollingResult, err error) {
	driver.polls++
	result = SDNRouterPollingResult{
		State: "available", RouterVMID: request.RouterVMID, LastPolledAt: time.Now().UTC(),
		ObservedAddresses: []SDNRouterObservedAddress{{Address: "192.168.100.42", Source: "lease"}},
	}
	return
}

func TestRouterPollingMergesLeaseAndNeighborSources(t *testing.T) {
	var observed map[string]SDNRouterObservedAddress = make(map[string]SDNRouterObservedAddress)
	mergeRouterLeases(observed, "1791133200 aa:bb:cc:dd:ee:ff 192.168.100.42 fedora *\n0 11:22:33:44:55:66 10.0.0.5 outside *\n", "192.168.100.0/24")
	mergeRouterNeighbors(observed, "192.168.100.42 dev ens18 lladdr aa:bb:cc:dd:ee:ff REACHABLE\n192.168.100.1 dev ens18 lladdr 00:00:00:00:00:01 PERMANENT\n192.168.100.31 dev ens18 FAILED\n", "192.168.100.0/24")
	var entry SDNRouterObservedAddress = observed["192.168.100.42"]
	if entry.Hostname != "fedora" || entry.MAC != "aa:bb:cc:dd:ee:ff" || entry.Source != "lease, neighbor" || entry.LeaseExpiresAt == nil {
		t.Fatalf("merged router observation = %#v", entry)
	}
	if _, exists := observed["10.0.0.5"]; exists {
		t.Fatal("address outside the managed network was included")
	}
	if _, exists := observed["192.168.100.1"]; !exists {
		t.Fatal("neighbor without a DHCP lease should be included")
	}
	if _, exists := observed["192.168.100.31"]; exists {
		t.Fatal("incomplete neighbor without a MAC should not be included")
	}
}

func TestRouterPollingIncludesRouterLANAddressAndMAC(t *testing.T) {
	var observed map[string]SDNRouterObservedAddress = make(map[string]SDNRouterObservedAddress)
	var iface *pve.AgentNetworkIface = &pve.AgentNetworkIface{
		Name: "ens19", HardwareAddress: "02:aa:bb:cc:dd:ee",
		IPAddresses: []*pve.AgentNetworkIPAddress{
			{IPAddressType: "ipv4", IPAddress: "192.168.2.1", Prefix: 24},
			{IPAddressType: "ipv4", IPAddress: "10.0.0.24", Prefix: 8},
		},
	}
	mergeRouterInterfaceAddresses(observed, iface, "192.168.2.0/24")
	if observed["192.168.2.1"].MAC != "02:aa:bb:cc:dd:ee" {
		t.Fatalf("router LAN address did not include interface MAC: %#v", observed["192.168.2.1"])
	}
	if _, exists := observed["10.0.0.24"]; exists {
		t.Fatal("router address outside the managed subnet was included")
	}
}

func TestRouterPollingSupportsIPv6ObservationsAndDHCPRange(t *testing.T) {
	var observed map[string]SDNRouterObservedAddress = make(map[string]SDNRouterObservedAddress)
	var iface *pve.AgentNetworkIface = &pve.AgentNetworkIface{
		Name: "ens19", HardwareAddress: "02:aa:bb:cc:dd:ee",
		IPAddresses: []*pve.AgentNetworkIPAddress{{IPAddressType: "ipv6", IPAddress: "fd42:1::1", Prefix: 64}},
	}
	mergeRouterInterfaceAddresses(observed, iface, "fd42:1::/64")
	mergeRouterLeases(observed, "1791133200 00000001 fd42:1::42 fedora 00:03:00:01:02:11:22:33:44:55\n", "fd42:1::/64")
	mergeRouterNeighbors(observed, "fd42:1::42 dev ens19 lladdr 02:11:22:33:44:55 REACHABLE\n", "fd42:1::/64")
	if observed["fd42:1::1"].MAC != iface.HardwareAddress || observed["fd42:1::42"].MAC != "02:11:22:33:44:55" || observed["fd42:1::42"].Source != "lease, neighbor" {
		t.Fatalf("IPv6 router or neighbor observation missing MAC: %#v", observed)
	}
	start, end := parseRouterDHCPv6Range("dhcp-range=192.0.2.20,192.0.2.40,12h\ndhcp-range=fd42:1::20,fd42:1::40,slaac,12h\n")
	if start != "fd42:1::20" || end != "fd42:1::40" {
		t.Fatalf("unexpected DHCPv6 range: %q–%q", start, end)
	}
}

func TestDHCPv6DUIDMapsToEthernetMAC(t *testing.T) {
	if mac := macFromDHCPv6DUID("00:03:00:01:02:11:22:33:44:55"); mac != "02:11:22:33:44:55" {
		t.Fatalf("DUID-LL mapped to MAC %q", mac)
	}
	if mac := macFromDHCPv6DUID("00:04:00:01:02:11:22:33:44:55"); mac != "" {
		t.Fatalf("unsupported DUID mapped to MAC %q", mac)
	}
}

func TestRouterPollingParsesDHCPAndEgressDetails(t *testing.T) {
	var start string
	var end string
	start, end = parseRouterDHCPRange("interface=ens18\ndhcp-range=192.168.1.30,192.168.1.40,12h\n")
	if start != "192.168.1.30" || end != "192.168.1.40" {
		t.Fatalf("DHCP range = %q-%q", start, end)
	}
	if name := parseRouterEgressInterface("table ip organesson_nat { chain postrouting { oifname \"ens19\" ip saddr 192.168.1.0/24 masquerade } }"); name != "ens19" {
		t.Fatalf("egress interface = %q, want ens19", name)
	}
	if gateway := parseRouterDefaultGateway("default via 10.0.0.1 dev ens19 proto dhcp"); gateway != "10.0.0.1" {
		t.Fatalf("default gateway = %q, want 10.0.0.1", gateway)
	}
}

func TestPollSDNRouterCachesResultsForThirtySeconds(t *testing.T) {
	var driver *testRouterPollingDriver = &testRouterPollingDriver{}
	var service *Service = NewWithSDNNetworkDriver(driver)
	var request SDNNetworkRequest = SDNNetworkRequest{Mode: "managed", OperationKey: "router-cache-test", RouterVMID: 158}
	var placement SDNNetworkPlacement = SDNNetworkPlacement{Zone: "ogvxlan", VNet: "oncache"}
	for range 2 {
		if _, err := service.PollSDNRouter(context.Background(), request, placement); err != nil {
			t.Fatalf("poll router: %v", err)
		}
	}
	if driver.polls != 1 {
		t.Fatalf("poll count = %d, want 1 cached poll", driver.polls)
	}
}
