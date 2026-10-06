package domain

import (
	"testing"

	"github.com/z46-dev/organesson/backend/proxmox"
)

func TestFilteredAttachmentsRequireAnAllocationOrManagedNetworkScope(t *testing.T) {
	var request NetworkAttachmentRequest
	if err := validateNetworkAttachmentAddressPools(request, true, false, false); err == nil {
		t.Fatal("filtered environment attachment without an address-pool request was accepted")
	}
	request.IPv6AddressPoolRequestID = 42
	request.RequestedIPv6AddressCount = 1
	if err := validateNetworkAttachmentAddressPools(request, true, false, false); err != nil {
		t.Fatalf("IPv6-only filtered attachment was rejected: %v", err)
	}
	request.AddressPoolRequestID = 41
	request.RequestedAddressCount = 1
	if err := validateNetworkAttachmentAddressPools(request, true, false, false); err != nil {
		t.Fatalf("dual-stack filtered attachment was rejected: %v", err)
	}
	request.RequestedAddressCount = 0
	if err := validateNetworkAttachmentAddressPools(request, true, false, false); err == nil {
		t.Fatal("pool ID without its address count was accepted")
	}
	request = NetworkAttachmentRequest{}
	if err := validateNetworkAttachmentAddressPools(request, true, false, true); err != nil {
		t.Fatalf("managed VNet attachment without a reservation was rejected: %v", err)
	}
	request = NetworkAttachmentRequest{}
	if err := validateNetworkAttachmentAddressPools(request, true, true, false); err != nil {
		t.Fatalf("managed router's own gateway NIC exception was rejected: %v", err)
	}
}

func TestGuestNetworkInputMayUseTheManagedNetworkSubnetWithoutAReservation(t *testing.T) {
	var configuration ManagedNetworkAttachmentConfiguration = ManagedNetworkAttachmentConfiguration{
		LogicalNetworkID:       42,
		NetworkDHCPEnabled:     true,
		NetworkIPv6DHCPEnabled: true,
		Request: proxmox.NetworkAttachmentRequest{
			EnforceAddressFilter: true,
			AllowedAddresses:     []string{"192.0.2.0/24", "2001:db8::/64"},
		},
	}
	for _, input := range []GuestNetworkInput{
		{Method: "static", Address: "192.0.2.20/24"},
		{Method: "dhcp"},
		{Method: "disabled", IPv6Method: "static", IPv6Address: "2001:db8::20/64"},
		{Method: "disabled", IPv6Method: "dhcp"},
	} {
		if err := validateGuestNetworkInput(configuration, input); err != nil {
			t.Errorf("managed subnet guest configuration was rejected: %v", err)
		}
	}
	if err := validateGuestNetworkInput(configuration, GuestNetworkInput{Method: "static", Address: "198.51.100.20/24"}); err == nil {
		t.Fatal("static address outside the managed VNet subnet was accepted")
	}
}

func TestGuestNetworkInputMustUseAddressesAllocatedToFilteredInterface(t *testing.T) {
	var configuration ManagedNetworkAttachmentConfiguration = ManagedNetworkAttachmentConfiguration{
		Request: proxmox.NetworkAttachmentRequest{
			EnforceAddressFilter: true,
			AllowedAddresses:     []string{"192.0.2.10", "2001:db8::10"},
		},
	}
	for _, input := range []GuestNetworkInput{
		{Method: "static", Address: "192.0.2.10/24"},
		{Method: "disabled", IPv6Method: "static", IPv6Address: "2001:db8::10/64"},
	} {
		if err := validateGuestNetworkInput(configuration, input); err != nil {
			t.Errorf("allocated static address rejected: %v", err)
		}
	}
	for _, input := range []GuestNetworkInput{
		{Method: "static", Address: "192.0.2.11/24"},
		{Method: "disabled", IPv6Method: "static", IPv6Address: "2001:db8::11/64"},
	} {
		if err := validateGuestNetworkInput(configuration, input); err == nil {
			t.Errorf("unallocated or dynamic address configuration accepted: %#v", input)
		}
	}
	var ipv6Only ManagedNetworkAttachmentConfiguration = configuration
	ipv6Only.Request.AllowedAddresses = []string{"2001:db8::10"}
	if err := validateGuestNetworkInput(ipv6Only, GuestNetworkInput{Method: "dhcp"}); err == nil {
		t.Fatal("IPv4 DHCP without an allocated IPv4 address was accepted")
	}
	var ipv4Only ManagedNetworkAttachmentConfiguration = configuration
	ipv4Only.Request.AllowedAddresses = []string{"192.0.2.10"}
	if err := validateGuestNetworkInput(ipv4Only, GuestNetworkInput{Method: "disabled", IPv6Method: "slaac"}); err == nil {
		t.Fatal("IPv6 SLAAC without an allocated IPv6 address was accepted")
	}
}

func TestManagedNetworkDynamicAddressMethodsRequireDHCPFamilyEnabled(t *testing.T) {
	var configuration ManagedNetworkAttachmentConfiguration = ManagedNetworkAttachmentConfiguration{
		LogicalNetworkID: 42,
		Request:          proxmox.NetworkAttachmentRequest{EnforceAddressFilter: true, AllowedAddresses: []string{"192.0.2.10", "2001:db8::10"}},
	}
	if err := validateGuestNetworkInput(configuration, GuestNetworkInput{Method: "dhcp"}); err == nil {
		t.Fatal("IPv4 DHCP was accepted while disabled on the managed network")
	}
	if err := validateGuestNetworkInput(configuration, GuestNetworkInput{Method: "disabled", IPv6Method: "dhcp"}); err == nil {
		t.Fatal("DHCPv6 was accepted while disabled on the managed network")
	}
	configuration.NetworkDHCPEnabled = true
	configuration.NetworkIPv6DHCPEnabled = true
	if err := validateGuestNetworkInput(configuration, GuestNetworkInput{Method: "dhcp", IPv6Method: "dhcp"}); err != nil {
		t.Fatalf("enabled dual-stack DHCP configuration rejected: %v", err)
	}
}

func TestFilteredSLAACMustMatchItsNICDerivedAllocation(t *testing.T) {
	var configuration ManagedNetworkAttachmentConfiguration = ManagedNetworkAttachmentConfiguration{
		IPv6AddressPrefix: "2001:db8::/64",
		Placement:         proxmox.NetworkAttachmentPlacement{MAC: "02:11:22:33:44:55"},
		Request: proxmox.NetworkAttachmentRequest{
			EnforceAddressFilter: true,
			AllowedAddresses:     []string{"2001:db8::11:22ff:fe33:4455"},
		},
	}
	if err := validateGuestNetworkInput(configuration, GuestNetworkInput{Method: "disabled", IPv6Method: "slaac"}); err != nil {
		t.Fatalf("matching EUI-64 SLAAC allocation was rejected: %v", err)
	}
	configuration.Request.AllowedAddresses = []string{"2001:db8::10"}
	if err := validateGuestNetworkInput(configuration, GuestNetworkInput{Method: "disabled", IPv6Method: "slaac"}); err == nil {
		t.Fatal("SLAAC configuration with an unrelated allocated IPv6 host was accepted")
	}
}
