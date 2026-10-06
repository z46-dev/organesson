package domain

import (
	"testing"

	"github.com/z46-dev/organesson/backend/proxmox"
)

func TestFilterStaleRouterLeasesKeepsCurrentAddressAndOtherNICs(t *testing.T) {
	var staticAddresses []StaticNetworkAddress = []StaticNetworkAddress{
		{Address: "192.168.2.4/24", MAC: "02:AA:BB:CC:DD:EE"},
	}
	var observed []proxmox.SDNRouterObservedAddress = []proxmox.SDNRouterObservedAddress{
		{Address: "192.168.2.32", MAC: "02:aa:bb:cc:dd:ee", Source: "lease"},
		{Address: "192.168.2.4", MAC: "02:aa:bb:cc:dd:ee", Source: "lease"},
		{Address: "192.168.2.30", MAC: "02:11:22:33:44:55", Source: "lease"},
		{Address: "192.168.2.31", MAC: "02:aa:bb:cc:dd:ee", Source: "neighbor"},
	}
	var filtered []proxmox.SDNRouterObservedAddress = FilterStaleRouterLeases(staticAddresses, observed)
	if len(filtered) != 2 {
		t.Fatalf("filtered addresses = %#v, want only the other NIC lease and neighbor", filtered)
	}
	for _, address := range filtered {
		if address.MAC == "02:aa:bb:cc:dd:ee" && address.Source == "lease" {
			t.Fatal("DHCP lease for statically configured NIC was retained")
		}
	}
}

func TestFilterStaleRouterLeasesPreservesOtherAddressFamily(t *testing.T) {
	var staticAddresses []StaticNetworkAddress = []StaticNetworkAddress{
		{Address: "fd42:1::20/64", MAC: "02:aa:bb:cc:dd:ee"},
	}
	var observed []proxmox.SDNRouterObservedAddress = []proxmox.SDNRouterObservedAddress{
		{Address: "192.168.1.30", MAC: "02:aa:bb:cc:dd:ee", Source: "lease"},
		{Address: "fd42:1::21", MAC: "02:aa:bb:cc:dd:ee", Source: "lease"},
	}
	var filtered []proxmox.SDNRouterObservedAddress = FilterStaleRouterLeases(staticAddresses, observed)
	if len(filtered) != 1 || filtered[0].Address != "192.168.1.30" {
		t.Fatalf("filtered addresses = %#v, want IPv4 lease retained and stale IPv6 lease removed", filtered)
	}
}
