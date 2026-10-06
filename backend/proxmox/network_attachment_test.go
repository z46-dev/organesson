package proxmox

import (
	"testing"

	pve "github.com/luthermonson/go-proxmox"
)

func TestNetworkAttachmentMACIsStableAndLocallyAdministered(t *testing.T) {
	var first string = networkAttachmentMAC("deployment:vm:default")
	var second string = networkAttachmentMAC("deployment:vm:default")
	if first != second || first[:3] != "02:" || first == networkAttachmentMAC("deployment:vm:other") {
		t.Fatalf("unexpected deterministic network MAC: %q, %q", first, second)
	}
}

func TestNetworkOptionMapReadsProxmoxDeviceMACAndBridge(t *testing.T) {
	var options map[string]string = networkOptionMap("virtio=02:11:22:33:44:55,bridge=on123456,firewall=0")
	if options["macaddr"] != "02:11:22:33:44:55" || options["bridge"] != "on123456" {
		t.Fatalf("failed to parse Proxmox NIC options: %#v", options)
	}
}

func TestNextNetworkDeviceChoosesFirstFreeSlot(t *testing.T) {
	var device string
	var err error
	if device, err = nextNetworkDevice(map[string]string{"net0": "", "net2": ""}); err != nil {
		t.Fatalf("select network device: %v", err)
	}
	if device != "net1" {
		t.Fatalf("expected net1, got %q", device)
	}
}

func TestFindAttachmentDeviceRecoversMarkedNICAndRejectsForeignBridge(t *testing.T) {
	var request NetworkAttachmentRequest = NetworkAttachmentRequest{
		Bridge: "on123456", AttachmentOperationKey: "attachment-operation",
	}
	var markedMAC string = networkAttachmentMAC(request.AttachmentOperationKey)
	var device string
	var found bool
	var err error
	if device, found, err = findAttachmentDevice(map[string]string{
		"net0": "virtio=02:11:22:33:44:55,bridge=vmbr0",
		"net2": "virtio=" + markedMAC + ",bridge=on123456",
	}, request); err != nil || !found || device != "net2" {
		t.Fatalf("failed to recover marked NIC: device=%q found=%v err=%v", device, found, err)
	}
	if _, _, err = findAttachmentDevice(map[string]string{
		"net2": "virtio=" + markedMAC + ",bridge=vmbr0",
	}, request); err == nil {
		t.Fatal("accepted an Organesson-marked NIC on a different bridge")
	}
	if _, found, err = findAttachmentDevice(map[string]string{
		"net2": "virtio=02:11:22:33:44:55,bridge=on123456",
	}, request); err != nil || found {
		t.Fatalf("treated an unrelated NIC as the marked attachment: found=%v err=%v", found, err)
	}
}

func TestValidateNetworkAttachmentRequestRequiresMarkedVNet(t *testing.T) {
	var request NetworkAttachmentRequest = NetworkAttachmentRequest{
		Node: "pve1", VMID: "100", VMOperationKey: "vm-operation", Bridge: "on123456", AttachmentOperationKey: "attachment-operation",
	}
	if err := validateNetworkAttachmentRequest(request); err == nil {
		t.Fatal("accepted a managed VNet without its ownership operation key")
	}
	request.NetworkOperationKey = "network-operation"
	if err := validateNetworkAttachmentRequest(request); err != nil {
		t.Fatalf("rejected marked VNet attachment: %v", err)
	}
	request.NetworkOperationKey = ""
	request.Bridge = "vmbr0,tag=4"
	if err := validateNetworkAttachmentRequest(request); err == nil {
		t.Fatal("accepted a bridge value containing Proxmox configuration delimiters")
	}
}

func TestValidateNetworkAttachmentRequestRequiresUsableFilteredAddresses(t *testing.T) {
	var request NetworkAttachmentRequest = NetworkAttachmentRequest{
		Node: "pve1", VMID: "100", VMOperationKey: "vm-operation", Bridge: "vmbr0",
		AttachmentOperationKey: "attachment-operation", EnforceAddressFilter: true,
	}
	if err := validateNetworkAttachmentRequest(request); err == nil {
		t.Fatal("IP filtering without allocated addresses was accepted")
	}
	request.AllowedAddresses = []string{"192.0.2.10", "2001:db8::10"}
	if err := validateNetworkAttachmentRequest(request); err != nil {
		t.Fatalf("valid allocated IPv4 and IPv6 hosts were rejected: %v", err)
	}
	request.AllowedAddresses = []string{"192.0.2.0/24", "2001:db8::/64"}
	if err := validateNetworkAttachmentRequest(request); err != nil {
		t.Fatalf("managed-network subnet scopes were rejected: %v", err)
	}
}

func TestDHCPv6ServerFirewallRuleIsRestrictedToTheRouterLANAndClientTraffic(t *testing.T) {
	var request NetworkAttachmentRequest = NetworkAttachmentRequest{
		Node: "pve1", VMID: "100", VMOperationKey: "router-vm", Bridge: "on123456", NetworkOperationKey: "network-operation",
		AttachmentOperationKey: "router-lan-operation", EnforceAddressFilter: true, AllowDHCPv6Server: true, AllowedAddresses: []string{"192.0.2.1", "2001:db8::1"},
	}
	var rule *pve.FirewallRule = dhcpv6ServerFirewallRule("net0", request)
	if rule.Type != "in" || rule.Action != "ACCEPT" || rule.Enable != 1 || rule.Iface != "net0" || rule.Proto != "udp" || rule.Sport != "546" || rule.Dport != "547" || rule.Source != "" || rule.Comment != dhcpv6ServerFirewallRuleComment(request) {
		t.Fatalf("DHCPv6 server rule is broader or differently scoped than expected: %+v", rule)
	}
	var legacy pve.FirewallRule = *rule
	legacy.Source = "fe80::/10"
	if !isLegacyDHCPv6ServerFirewallRule(&legacy, rule) {
		t.Fatal("prior link-local-only DHCPv6 rule was not recognized for safe reconciliation")
	}
	legacy.Dport = "53"
	if isLegacyDHCPv6ServerFirewallRule(&legacy, rule) {
		t.Fatal("unrelated DHCPv6 rule drift was incorrectly treated as a known legacy rule")
	}
	if err := validateNetworkAttachmentRequest(request); err != nil {
		t.Fatalf("valid managed router DHCPv6 attachment rejected: %v", err)
	}
	request.NetworkOperationKey = ""
	if err := validateNetworkAttachmentRequest(request); err == nil {
		t.Fatal("DHCPv6 server exception was accepted on a non-managed network")
	}
}

func TestDHCPv4ServerFirewallRuleIsRestrictedToTheRouterLANAndClientTraffic(t *testing.T) {
	var request NetworkAttachmentRequest = NetworkAttachmentRequest{
		Node: "pve1", VMID: "100", VMOperationKey: "router-vm", Bridge: "on123456", NetworkOperationKey: "network-operation",
		AttachmentOperationKey: "router-lan-operation", EnforceAddressFilter: true, AllowDHCPv4Server: true, AllowedAddresses: []string{"192.0.2.1"},
	}
	var rule *pve.FirewallRule = dhcpv4ServerFirewallRule("net0", request)
	if rule.Type != "in" || rule.Action != "ACCEPT" || rule.Enable != 1 || rule.Iface != "net0" || rule.Proto != "udp" || rule.Sport != "68" || rule.Dport != "67" || rule.Source != "" || rule.Comment != dhcpv4ServerFirewallRuleComment(request) {
		t.Fatalf("DHCPv4 server rule is broader or differently scoped than expected: %+v", rule)
	}
	if err := validateNetworkAttachmentRequest(request); err != nil {
		t.Fatalf("valid managed router DHCPv4 attachment rejected: %v", err)
	}
	request.NetworkOperationKey = ""
	if err := validateNetworkAttachmentRequest(request); err == nil {
		t.Fatal("DHCPv4 server exception was accepted on a non-managed network")
	}
}

func TestRouterDNSFirewallRulesAreLimitedToLANSubnetsAndDNS(t *testing.T) {
	var request NetworkAttachmentRequest = NetworkAttachmentRequest{
		Node: "pve1", VMID: "100", VMOperationKey: "router-vm", Bridge: "on123456", NetworkOperationKey: "network-operation", AttachmentOperationKey: "router-lan-operation",
		EnforceAddressFilter: true, AllowedAddresses: []string{"192.0.2.1", "2001:db8::1"},
		AllowedClientSubnets: []string{"192.0.2.0/24", "2001:db8:2::/64"},
	}
	var rules []*pve.FirewallRule = routerDNSFirewallRules("net1", request)
	if len(rules) != 4 {
		t.Fatalf("expected one TCP and UDP DNS rule per address family, got %d", len(rules))
	}
	var seen map[string]bool = make(map[string]bool)
	for _, rule := range rules {
		if rule.Type != "in" || rule.Action != "ACCEPT" || rule.Enable != 1 || rule.Iface != "net1" || (rule.Proto != "tcp" && rule.Proto != "udp") || rule.Dport != "53" || rule.Source == "" || rule.Comment == "" {
			t.Fatalf("router DNS rule is broader or differently scoped than expected: %+v", rule)
		}
		seen[rule.Source+"/"+rule.Proto] = true
	}
	for _, key := range []string{"192.0.2.0/24/tcp", "192.0.2.0/24/udp", "2001:db8:2::/64/tcp", "2001:db8:2::/64/udp"} {
		if !seen[key] {
			t.Errorf("missing router DNS rule %q", key)
		}
	}
	if err := validateNetworkAttachmentRequest(request); err != nil {
		t.Fatalf("valid router LAN DNS allowances rejected: %v", err)
	}
	request.AllowedClientSubnets = []string{"192.0.2.1/24"}
	if err := validateNetworkAttachmentRequest(request); err == nil {
		t.Fatal("non-canonical client subnet was accepted")
	}
	request.AllowedClientSubnets = []string{"192.0.2.0/24", "198.51.100.0/24"}
	if err := validateNetworkAttachmentRequest(request); err == nil {
		t.Fatal("duplicate address family was accepted")
	}
}

func TestParseIPSetEntryAcceptsHostsAndCanonicalSubnets(t *testing.T) {
	for _, value := range []string{"192.0.2.10", "192.0.2.10/32", "2001:db8::10", "2001:db8::10/128", "192.0.2.0/24", "2001:db8::/64"} {
		if _, err := parseIPSetEntry(value); err != nil {
			t.Errorf("valid filter entry %q rejected: %v", value, err)
		}
	}
	for _, value := range []string{"192.0.2.10/24", "2001:db8::1/64", "bogus"} {
		if _, err := parseIPSetEntry(value); err == nil {
			t.Errorf("non-canonical/invalid entry %q accepted", value)
		}
	}
}

func TestEnableVMIPFilteringOptionsIncludesIPv6NeighborDiscovery(t *testing.T) {
	var options pve.FirewallVirtualMachineOption
	enableVMIPFilteringOptions(&options)
	if !bool(options.Enable) || !bool(options.Dhcp) || !bool(options.Ipfilter) || !bool(options.NDP) {
		t.Fatalf("address-filter policy omitted a required Proxmox firewall option: %+v", options)
	}
}
