package proxmox

import (
	"os/exec"
	"strings"
	"testing"
	"time"
)

func TestGuestNetworkScriptsAreValidAndNICScoped(t *testing.T) {
	var request GuestNetworkRequest = GuestNetworkRequest{
		Node: "pve1", VMID: "901", VMOperationKey: "og-vm",
		AttachmentKey: "og-nic", Bridge: "vmbr0", Placement: NetworkAttachmentPlacement{Device: "net1", MAC: "02:11:22:33:44:55"},
		Method: "static", Address: "10.0.0.100/8", Gateway: "10.0.0.1", DNS: []string{"10.0.0.2"},
	}
	for _, script := range []string{guestNetworkScript(request, false), guestNetworkScript(request, true), guestNetworkReadScript(request)} {
		var command *exec.Cmd = exec.Command("bash", "-n")
		command.Stdin = strings.NewReader(script)
		if output, err := command.CombinedOutput(); err != nil {
			t.Fatalf("guest script is not valid shell: %v: %s\n%s", err, output, script)
		}
		if !strings.Contains(script, "organesson-021122334455") || !strings.Contains(script, "02:11:22:33:44:55") && strings.Contains(script, "connection add") {
			t.Fatalf("guest script is not scoped to its managed NIC: %s", script)
		}
	}
	if !strings.Contains(guestNetworkScript(request, false), "trap 'rm -f \"$0\"' EXIT") {
		t.Fatal("ephemeral guest setup script must remove itself after execution")
	}
	if !strings.Contains(guestNetworkScript(request, false), "systemctl start NetworkManager.service") || !strings.Contains(guestNetworkScript(request, false), "nmcli device set \"$interface\" managed yes") || !strings.Contains(guestNetworkScript(request, false), "GENERAL.NM-MANAGED") {
		t.Fatal("guest network configuration does not wait for and manage its cloned NetworkManager NIC")
	}
	if !strings.Contains(guestNetworkScript(request, false), "systemd-networkd.service") || !strings.Contains(guestNetworkScript(request, false), "MACAddress=02:11:22:33:44:55") {
		t.Fatal("guest network configuration does not support MAC-bound systemd-networkd profiles")
	}
	if !strings.Contains(guestNetworkScript(request, false), "nmcli device set \"$interface\" managed no") {
		t.Fatal("systemd-networkd fallback does not release the NIC from NetworkManager")
	}
	if !strings.Contains(guestNetworkScript(request, false), "netconfig modify --service 'organesson-021122334455'") || !strings.Contains(guestNetworkScript(request, true), "netconfig remove --service 'organesson-021122334455'") {
		t.Fatal("guest network configuration does not publish and clean up per-interface DNS for netconfig guests")
	}
	if !strings.Contains(guestNetworkScript(request, false), "connection.autoconnect no") || !strings.Contains(guestNetworkScript(request, false), "nmcli connection down \"$active_uuid\"") {
		t.Fatal("guest network configuration does not retire competing profiles on the managed NIC")
	}
	request.NeverDefault = true
	if !strings.Contains(guestNetworkScript(request, false), "ipv4.never-default yes") || !strings.Contains(guestNetworkReadScript(request), "guest IPv4 route preference drift detected") {
		t.Fatal("guest setup does not preserve and verify the non-default-route preference")
	}
}

func TestGuestNetworkReadScriptSupportsSystemdNetworkd(t *testing.T) {
	var request GuestNetworkRequest = GuestNetworkRequest{
		Node: "pve1", VMID: "901", VMOperationKey: "og-vm",
		AttachmentKey: "og-nic", Bridge: "vmbr0", Placement: NetworkAttachmentPlacement{Device: "net1", MAC: "02:11:22:33:44:55"},
		Method: "static", Address: "172.24.172.34/19", Gateway: "172.24.160.1", DNS: []string{"172.24.160.1"},
	}
	var script string = guestNetworkReadScript(request)
	for _, expected := range []string{
		"/etc/systemd/network/05-organesson-021122334455.network",
		"systemctl is-active --quiet systemd-networkd.service",
		"cmp -s \"$network_file\" \"$expected_file\"",
		"networkctl status --no-pager \"$interface\"",
		"Address=172.24.172.34/19",
		"Gateway=172.24.160.1",
	} {
		if !strings.Contains(script, expected) {
			t.Fatalf("systemd-networkd guest network verification is missing %q: %s", expected, script)
		}
	}
	var command *exec.Cmd = exec.Command("bash", "-n")
	command.Stdin = strings.NewReader(script)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("systemd-networkd guest read script is invalid shell: %v: %s\n%s", err, output, script)
	}
}

func TestSystemdNetworkdReadScriptKeepsDynamicAddressFiltering(t *testing.T) {
	var request GuestNetworkRequest = GuestNetworkRequest{
		Node: "pve1", VMID: "903", VMOperationKey: "og-vm",
		AttachmentKey: "og-nic", Bridge: "vnet123", Placement: NetworkAttachmentPlacement{Device: "net1", MAC: "02:11:22:33:44:55"},
		Method: "dhcp", IPv6Method: "slaac", EnforceAddressFilter: true,
		AllowedAddresses: []string{"192.0.2.10", "2001:db8::10"}, IPv6Prefix: "2001:db8::/64",
	}
	var script string = guestNetworkReadScript(request)
	if !strings.Contains(script, "ip -o -\"$address_family\" address show dev \"$interface\"") || !strings.Contains(script, "guest acquired an address outside its Organesson allocation") {
		t.Fatalf("systemd-networkd read script does not retain allocation enforcement: %s", script)
	}
	var command *exec.Cmd = exec.Command("bash", "-n")
	command.Stdin = strings.NewReader(script)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("systemd-networkd allocation read script is invalid shell: %v: %s\n%s", err, output, script)
	}
}

func TestGuestOperationLocksArePerVM(t *testing.T) {
	var service Service
	var unlockFirst func() = service.lockGuestOperation("pve1", "901")
	var secondGuestReady chan struct{} = make(chan struct{})
	go func() {
		var unlockSecond func() = service.lockGuestOperation("pve1", "902")
		unlockSecond()
		close(secondGuestReady)
	}()
	select {
	case <-secondGuestReady:
	case <-time.After(time.Second):
		unlockFirst()
		t.Fatal("an operation on another VM was serialized behind the first guest")
	}
	var sameGuestReady chan struct{} = make(chan struct{})
	go func() {
		var unlockSame func() = service.lockGuestOperation("pve1", "901")
		unlockSame()
		close(sameGuestReady)
	}()
	select {
	case <-sameGuestReady:
		unlockFirst()
		t.Fatal("overlapping operations on the same VM were not serialized")
	case <-time.After(25 * time.Millisecond):
	}
	unlockFirst()
	select {
	case <-sameGuestReady:
	case <-time.After(time.Second):
		t.Fatal("the waiting operation did not continue after its guest lock was released")
	}
}

func TestValidateGuestNetworkRequest(t *testing.T) {
	var request GuestNetworkRequest = GuestNetworkRequest{
		Node: "pve1", VMID: "901", VMOperationKey: "og-vm", AttachmentKey: "og-nic", Bridge: "vmbr0",
		Placement: NetworkAttachmentPlacement{Device: "net1", MAC: "02:11:22:33:44:55"},
		Method:    "static", Address: "192.0.2.5/24", Gateway: "192.0.2.1",
	}
	if err := validateGuestNetworkRequest(request); err != nil {
		t.Fatalf("valid static guest configuration rejected: %v", err)
	}
	request.Address = "192.0.2.5"
	if err := validateGuestNetworkRequest(request); err == nil {
		t.Fatal("static configuration without a prefix was accepted")
	}
	request.Address = "192.0.2.5/24"
	request.Method = "dhcp"
	if err := validateGuestNetworkRequest(request); err == nil {
		t.Fatal("DHCP configuration with static values was accepted")
	}
}

func TestOmittedGuestIPv6MethodDefaultsToDisabled(t *testing.T) {
	var request GuestNetworkRequest = GuestNetworkRequest{Method: "dhcp"}
	var normalized GuestNetworkRequest = normalizeGuestNetworkRequest(request)
	if normalized.IPv6Method != "disabled" || !strings.Contains(guestNetworkScript(normalized, false), "ipv6.method 'disabled'") {
		t.Fatalf("omitted IPv6 mode did not become an explicit disabled configuration: %#v", normalized)
	}
}

func TestGuestNetworkSupportsStaticIPv6AndSLAAC(t *testing.T) {
	var request GuestNetworkRequest = GuestNetworkRequest{
		Node: "pve1", VMID: "901", VMOperationKey: "og-vm", AttachmentKey: "og-nic", Bridge: "vnet123",
		Placement: NetworkAttachmentPlacement{Device: "net1", MAC: "02:11:22:33:44:55"},
		Method:    "disabled", IPv6Method: "static", IPv6Address: "2001:db8:1234::5/64", IPv6Gateway: "2001:db8:1234::1", IPv6DNS: []string{"2001:db8::53"},
	}
	if err := validateGuestNetworkRequest(request); err != nil {
		t.Fatalf("valid static IPv6 guest configuration rejected: %v", err)
	}
	var script string = guestNetworkScript(request, false)
	if !strings.Contains(script, "ipv6.method 'manual'") || !strings.Contains(script, "ipv6.addresses '2001:db8:1234::5/64'") || !strings.Contains(guestNetworkReadScript(request), "guest IPv6 address drift detected") || !strings.Contains(guestNetworkReadScript(request), `tr -d '\\'`) {
		t.Fatalf("IPv6 guest configuration was not generated and verified: %s", script)
	}
	request.IPv6Method = "slaac"
	request.IPv6Address = ""
	request.IPv6Gateway = ""
	request.IPv6DNS = nil
	if err := validateGuestNetworkRequest(request); err != nil {
		t.Fatalf("valid SLAAC guest configuration rejected: %v", err)
	}
	if !strings.Contains(guestNetworkScript(request, false), "ipv6.method 'auto'") || !strings.Contains(guestNetworkScript(request, false), "ipv6.addr-gen-mode eui64 ipv6.ip6-privacy 0") {
		t.Fatal("SLAAC was not mapped to NetworkManager automatic IPv6 configuration")
	}
	request.Method = "disabled"
	request.IPv6Method = "disabled"
	if err := validateGuestNetworkRequest(request); err == nil {
		t.Fatal("guest configuration with both address families disabled was accepted")
	}
}

func TestFilteredDynamicGuestAddressesMustMatchTheAllocation(t *testing.T) {
	var request GuestNetworkRequest = GuestNetworkRequest{
		Node: "pve1", VMID: "901", VMOperationKey: "og-vm", AttachmentKey: "og-nic", Bridge: "vmbr0",
		Placement: NetworkAttachmentPlacement{Device: "net1", MAC: "02:11:22:33:44:55"},
		Method:    "dhcp", IPv6Method: "slaac", IPv6Prefix: "2001:db8::/64", EnforceAddressFilter: true,
		AllowedAddresses: []string{"192.0.2.10", "2001:db8::11:22ff:fe33:4455"},
	}
	if err := validateGuestNetworkRequest(request); err != nil {
		t.Fatalf("dynamic configuration with family-matched reservations rejected: %v", err)
	}
	for _, script := range []string{guestNetworkScript(request, false), guestNetworkReadScript(request)} {
		if !strings.Contains(script, "guest acquired an address outside its Organesson allocation") {
			t.Fatalf("dynamic address verification missing from guest script: %s", script)
		}
		var command *exec.Cmd = exec.Command("bash", "-n")
		command.Stdin = strings.NewReader(script)
		if output, err := command.CombinedOutput(); err != nil {
			t.Fatalf("filtered dynamic guest script is invalid: %v: %s", err, output)
		}
	}
	request.IPv6Method = "dhcp"
	request.AllowedAddresses = []string{"192.0.2.10"}
	if err := validateGuestNetworkRequest(request); err == nil {
		t.Fatal("DHCPv6 without an allocated IPv6 host was accepted")
	}
}

func TestFilteredDynamicGuestNetworkAllowsManagedSubnetScopes(t *testing.T) {
	var request GuestNetworkRequest = GuestNetworkRequest{
		Node: "pve1", VMID: "902", VMOperationKey: "og-vm", AttachmentKey: "og-nic", Bridge: "on123456",
		NetworkOperationKey: "og-network", Placement: NetworkAttachmentPlacement{Device: "net1", MAC: "02:11:22:33:44:55"},
		Method: "dhcp", IPv6Method: "dhcp", EnforceAddressFilter: true,
		AllowedAddresses: []string{"192.0.2.0/24", "2001:db8::/64"},
	}
	if err := validateGuestNetworkRequest(request); err != nil {
		t.Fatalf("managed subnet scopes rejected for dynamic guest configuration: %v", err)
	}
	var checkScript string = guestAllocatedAddressCheck(request, "interface")
	if strings.Contains(checkScript, "verify_allocated_family IP4.ADDRESS") || strings.Contains(checkScript, "verify_allocated_family IP6.ADDRESS") {
		t.Fatalf("subnet-scoped dynamic configuration incorrectly expects one exact host reservation: %s", checkScript)
	}
}

func TestFilteredSLAACRequiresTheAllocatedStableEUI64Address(t *testing.T) {
	const allocated string = "2001:db8::11:22ff:fe33:4455"
	if err := ValidateSLAACAllocation("2001:db8::/64", "02:11:22:33:44:55", []string{allocated}); err != nil {
		t.Fatalf("matching stable SLAAC allocation rejected: %v", err)
	}
	if err := ValidateSLAACAllocation("2001:db8::/64", "02:11:22:33:44:55", []string{"2001:db8::10"}); err == nil {
		t.Fatal("SLAAC address that was not allocated to the NIC was accepted")
	}
	if err := ValidateSLAACAllocation("2001:db8::/80", "02:11:22:33:44:55", []string{allocated}); err == nil {
		t.Fatal("non-/64 filtered SLAAC prefix was accepted")
	}
}

func TestAllocatedDynamicAddressCheckAcceptsOnlyTheExactReservedHost(t *testing.T) {
	var request GuestNetworkRequest = GuestNetworkRequest{
		Method: "disabled", IPv6Method: "dhcp", EnforceAddressFilter: true,
		AllowedAddresses: []string{"fd42:2::9"},
	}
	var allowedScript string = "interface=ens22\nnmcli() { printf '%s\\n' 'fd42:2::9/128'; }\n" + guestAllocatedAddressCheck(request, "interface")
	var command *exec.Cmd = exec.Command("bash", "-c", allowedScript)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("allocated dynamic address was rejected: %v: %s", err, output)
	}
	var spoofedScript string = "interface=ens22\nnmcli() { printf '%s\\n' 'fd42:2::35/128'; }\n" + guestAllocatedAddressCheck(request, "interface")
	command = exec.Command("bash", "-c", spoofedScript)
	if output, err := command.CombinedOutput(); err == nil || !strings.Contains(string(output), "guest acquired an address outside its Organesson allocation") {
		t.Fatalf("unallocated dynamic address was not rejected: %v: %s", err, output)
	}
	var missingScript string = "interface=ens22\nsleep() { :; }\nnmcli() { printf '%s\\n' 'fe80::1/64'; }\n" + guestAllocatedAddressCheck(request, "interface")
	command = exec.Command("bash", "-c", missingScript)
	if output, err := command.CombinedOutput(); err == nil || !strings.Contains(string(output), "last observed: fe80::1") {
		t.Fatalf("missing allocated address did not report the last observed address: %v: %s", err, output)
	}
}

func TestFilteredDHCPv6UsesStableMACDerivedDUID(t *testing.T) {
	var request GuestNetworkRequest = GuestNetworkRequest{
		Node: "pve1", VMID: "901", VMOperationKey: "og-vm", AttachmentKey: "og-nic", Bridge: "vnet123",
		Placement: NetworkAttachmentPlacement{Device: "net1", MAC: "02:11:22:33:44:55"},
		Method:    "disabled", IPv6Method: "dhcp", EnforceAddressFilter: true, AllowedAddresses: []string{"2001:db8::10"},
	}
	if err := validateGuestNetworkRequest(request); err != nil {
		t.Fatalf("allocated DHCPv6 configuration rejected: %v", err)
	}
	var script string = guestNetworkScript(request, false)
	if !strings.Contains(script, "ipv6.dhcp-duid ll ipv6.dhcp-iaid mac") || !strings.Contains(script, "ipv6.addr-gen-mode eui64 ipv6.ip6-privacy 0") || !strings.Contains(guestNetworkReadScript(request), "guest DHCPv6 DUID drift detected") || !strings.Contains(guestNetworkReadScript(request), "guest IPv6 link-local address generation drift detected") || !strings.Contains(guestNetworkReadScript(request), "guest acquired an address outside its Organesson allocation") {
		t.Fatalf("filtered DHCPv6 lacks deterministic link-local identity, drift checks, or exact address validation: %s", script)
	}
	var command *exec.Cmd = exec.Command("bash", "-n")
	command.Stdin = strings.NewReader(script)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("filtered DHCPv6 shell script is invalid: %v: %s", err, output)
	}
}

func TestFilteredStaticIPv6UsesStableLinkLocalAddress(t *testing.T) {
	var request GuestNetworkRequest = GuestNetworkRequest{
		Method: "disabled", IPv6Method: "static", IPv6Address: "2001:db8::10/64", EnforceAddressFilter: true,
		Placement: NetworkAttachmentPlacement{Device: "net1", MAC: "02:11:22:33:44:55"},
	}
	var script string = guestNetworkScript(request, false)
	var readScript string = guestNetworkReadScript(request)
	if !strings.Contains(script, "ipv6.addr-gen-mode eui64 ipv6.ip6-privacy 0") || !strings.Contains(readScript, "guest IPv6 link-local address generation drift detected") {
		t.Fatalf("filtered static IPv6 does not enforce a stable MAC-derived link-local address: %s", script)
	}
}

func TestGuestAgentCommandUsesOptionalSELinuxWrapper(t *testing.T) {
	var command []string = guestAgentCommand("/usr/bin/bash", "/run/organesson-network.sh")
	if len(command) != 6 || command[0] != "/bin/sh" || command[1] != "-c" || !strings.Contains(command[2], "/usr/libexec/qemu-ga/fsfreeze-hook.d/organesson-qga-exec") || command[4] != "/usr/bin/bash" || command[5] != "/run/organesson-network.sh" {
		t.Fatalf("guest command did not preserve arguments through the optional SELinux wrapper: %#v", command)
	}
}
