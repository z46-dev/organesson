package proxmox

import (
	"context"
	"testing"
	"time"
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

func (driver *testRouterPollingDriver) Delete(context.Context, string, string, string) error {
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
	mergeRouterNeighbors(observed, "192.168.100.42 dev ens18 lladdr aa:bb:cc:dd:ee:ff REACHABLE\n192.168.100.1 dev ens18 lladdr 00:00:00:00:00:01 PERMANENT\n", "192.168.100.0/24")
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
