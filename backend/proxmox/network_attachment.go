package proxmox

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"net/netip"
	"sort"
	"strconv"
	"strings"

	pve "github.com/luthermonson/go-proxmox"
	"github.com/z46-dev/organesson/backend/config"
)

var ErrNetworkAttachmentNotFound = errors.New("managed Proxmox VM network attachment is missing")

type (
	// NetworkAttachmentRequest identifies a managed VM and its approved Proxmox L2 network.
	NetworkAttachmentRequest struct {
		Node                   string   `json:"node"`
		VMID                   string   `json:"vmid"`
		VMOperationKey         string   `json:"vm_operation_key"`
		Bridge                 string   `json:"bridge"`
		NetworkOperationKey    string   `json:"network_operation_key,omitempty"`
		AttachmentOperationKey string   `json:"attachment_operation_key"`
		EnforceAddressFilter   bool     `json:"enforce_address_filter,omitempty"`
		AllowDHCPv4Server      bool     `json:"allow_dhcpv4_server,omitempty"`
		AllowDHCPv6Server      bool     `json:"allow_dhcpv6_server,omitempty"`
		AllowedAddresses       []string `json:"allowed_addresses,omitempty"`
		AllowedClientSubnets   []string `json:"allowed_client_subnets,omitempty"`
	}

	// NetworkAttachmentPlacement is the stable PVE virtual NIC identity.
	NetworkAttachmentPlacement struct {
		Device string `json:"device"`
		MAC    string `json:"mac"`
	}

	apiNetworkAttachmentDriver struct {
		settings config.ProxmoxConfiguration
	}
)

// Attach creates a marked PVE virtual NIC on a bridge or verified Organesson VNet.
func (service *Service) AttachNetwork(ctx context.Context, request NetworkAttachmentRequest) (placement NetworkAttachmentPlacement, err error) {
	if service == nil || service.networkAttachmentDriver == nil || !service.Configured() {
		err = ErrNotConfigured
		return
	}
	service.lifecycleLock.Lock()
	defer service.lifecycleLock.Unlock()
	placement, err = service.networkAttachmentDriver.Attach(ctx, request)
	return
}

// ReadNetworkAttachment verifies the stable NIC still matches its VM and network ownership.
func (service *Service) ReadNetworkAttachment(ctx context.Context, request NetworkAttachmentRequest, placement NetworkAttachmentPlacement) (err error) {
	if service == nil || service.networkAttachmentDriver == nil || !service.Configured() {
		err = ErrNotConfigured
		return
	}
	service.lifecycleLock.Lock()
	defer service.lifecycleLock.Unlock()
	err = service.networkAttachmentDriver.Read(ctx, request, placement)
	return
}

// DetachNetwork removes only the marked NIC from its managed VM.
func (service *Service) DetachNetwork(ctx context.Context, request NetworkAttachmentRequest, placement NetworkAttachmentPlacement) (err error) {
	if service == nil || service.networkAttachmentDriver == nil || !service.Configured() {
		err = ErrNotConfigured
		return
	}
	service.lifecycleLock.Lock()
	defer service.lifecycleLock.Unlock()
	err = service.networkAttachmentDriver.Detach(ctx, request, placement)
	return
}

// Attach locates a safe netN slot and applies an idempotently marked network device.
func (driver *apiNetworkAttachmentDriver) Attach(ctx context.Context, request NetworkAttachmentRequest) (placement NetworkAttachmentPlacement, err error) {
	if err = validateNetworkAttachmentRequest(request); err != nil {
		return
	}
	var client *pve.Client
	if client, err = newAPIClient(driver.settings); err != nil {
		return
	}
	var vmid int
	if vmid, err = parseVMID(request.VMID); err != nil {
		return
	}
	var node *pve.Node
	var vm *pve.VirtualMachine
	if node, vm, _, err = locateManagedVM(ctx, client, vmid); err != nil {
		return
	}
	if err = verifyManagedVM(vm, request.VMOperationKey); err != nil {
		return
	}
	if err = verifyAttachmentTarget(ctx, client, node, request); err != nil {
		return
	}
	var mac string = networkAttachmentMAC(request.AttachmentOperationKey)
	var interfaces map[string]string
	if vm.VirtualMachineConfig != nil {
		interfaces = vm.VirtualMachineConfig.Nets
	}
	for device, value := range interfaces {
		var options map[string]string = networkOptionMap(value)
		if strings.EqualFold(options["macaddr"], mac) {
			if options["bridge"] != request.Bridge {
				err = errors.New("marked Proxmox network device is attached to a different bridge")
				return
			}
			placement = NetworkAttachmentPlacement{Device: device, MAC: mac}
			if request.EnforceAddressFilter {
				if options["firewall"] != "1" {
					if err = enableNetworkDeviceFirewall(ctx, client, vm, device, value); err != nil {
						return
					}
				}
				if err = driver.ensureVMInterfaceIPFilter(ctx, client, vm, device, request); err != nil {
					return
				}
			}
			if request.AllowDHCPv4Server {
				if err = ensureVMInterfaceDHCPv4ServerRule(ctx, vm, device, request); err != nil {
					return
				}
			}
			if request.AllowDHCPv6Server {
				if err = ensureVMInterfaceDHCPv6ServerRule(ctx, vm, device, request); err != nil {
					return
				}
			}
			if len(request.AllowedClientSubnets) > 0 {
				if err = ensureVMInterfaceRouterDNSRules(ctx, vm, device, request); err != nil {
					return
				}
			}
			return
		}
	}
	var device string
	if device, err = nextNetworkDevice(interfaces); err != nil {
		return
	}
	var task *pve.Task
	var firewallOption string
	if request.EnforceAddressFilter {
		if err = driver.enableVMIPFiltering(ctx, client, vm); err != nil {
			return
		}
		firewallOption = ",firewall=1"
	}
	if task, err = vm.Config(ctx, pve.VirtualMachineOption{Name: device, Value: "virtio=" + mac + ",bridge=" + request.Bridge + firewallOption}); err != nil {
		return
	}
	if err = waitTask(ctx, client, task); err != nil {
		return
	}
	placement = NetworkAttachmentPlacement{Device: device, MAC: mac}
	if request.EnforceAddressFilter {
		if err = driver.ensureVMInterfaceIPFilter(ctx, client, vm, device, request); err != nil {
			return
		}
	}
	if request.AllowDHCPv4Server {
		if err = ensureVMInterfaceDHCPv4ServerRule(ctx, vm, device, request); err != nil {
			return
		}
	}
	if request.AllowDHCPv6Server {
		if err = ensureVMInterfaceDHCPv6ServerRule(ctx, vm, device, request); err != nil {
			return
		}
	}
	if len(request.AllowedClientSubnets) > 0 {
		if err = ensureVMInterfaceRouterDNSRules(ctx, vm, device, request); err != nil {
			return
		}
	}
	return
}

// enableNetworkDeviceFirewall enables only the PVE firewall flag for one existing NIC.
func enableNetworkDeviceFirewall(ctx context.Context, client *pve.Client, vm *pve.VirtualMachine, device string, value string) (err error) {
	var options map[string]string = networkOptionMap(value)
	if options["firewall"] == "1" {
		return
	}
	var updated string = value
	if value == "" {
		err = fmt.Errorf("Proxmox network device %q has no configuration", device)
		return
	}
	updated += ",firewall=1"
	var task *pve.Task
	if task, err = vm.Config(ctx, pve.VirtualMachineOption{Name: device, Value: updated}); err != nil {
		return
	}
	err = waitTask(ctx, client, task)
	return
}

// Read confirms the configured netN device still carries the deterministic Organesson MAC.
func (driver *apiNetworkAttachmentDriver) Read(ctx context.Context, request NetworkAttachmentRequest, placement NetworkAttachmentPlacement) (err error) {
	if err = validateNetworkAttachmentRequest(request); err != nil {
		return
	}
	var client *pve.Client
	if client, err = newAPIClient(driver.settings); err != nil {
		return
	}
	var vmid int
	if vmid, err = parseVMID(request.VMID); err != nil {
		return
	}
	var node *pve.Node
	var vm *pve.VirtualMachine
	if node, vm, _, err = locateManagedVM(ctx, client, vmid); err != nil {
		return
	}
	if err = verifyManagedVM(vm, request.VMOperationKey); err != nil {
		return
	}
	var value string
	if vm.VirtualMachineConfig != nil {
		value = vm.VirtualMachineConfig.Nets[placement.Device]
	}
	var options map[string]string = networkOptionMap(value)
	if value == "" || !strings.EqualFold(options["macaddr"], networkAttachmentMAC(request.AttachmentOperationKey)) {
		err = ErrNetworkAttachmentNotFound
		return
	}
	if options["bridge"] != request.Bridge || placement.MAC != networkAttachmentMAC(request.AttachmentOperationKey) {
		err = errors.New("Proxmox virtual NIC differs from its recorded Organesson configuration")
		return
	}
	if request.EnforceAddressFilter {
		if options["firewall"] != "1" {
			err = errors.New("Proxmox virtual NIC firewall is not enabled")
			return
		}
		if err = verifyVMInterfaceIPFilter(ctx, client, vm, placement.Device, request); err != nil {
			return
		}
	}
	if request.AllowDHCPv4Server {
		if err = verifyVMInterfaceDHCPv4ServerRule(ctx, vm, placement.Device, request); err != nil {
			return
		}
	}
	if request.AllowDHCPv6Server {
		if err = verifyVMInterfaceDHCPv6ServerRule(ctx, vm, placement.Device, request); err != nil {
			return
		}
	}
	if len(request.AllowedClientSubnets) > 0 {
		if err = verifyVMInterfaceRouterDNSRules(ctx, vm, placement.Device, request); err != nil {
			return
		}
	}
	err = verifyAttachmentTarget(ctx, client, node, request)
	return
}

// Detach removes a NIC only if its recorded device and ownership MAC still match.
func (driver *apiNetworkAttachmentDriver) Detach(ctx context.Context, request NetworkAttachmentRequest, placement NetworkAttachmentPlacement) (err error) {
	if err = validateNetworkAttachmentRequest(request); err != nil {
		return
	}
	var client *pve.Client
	if client, err = newAPIClient(driver.settings); err != nil {
		return
	}
	var vmid int
	if vmid, err = parseVMID(request.VMID); err != nil {
		return
	}
	var vm *pve.VirtualMachine
	if _, vm, _, err = locateManagedVM(ctx, client, vmid); err != nil {
		return
	}
	if err = verifyManagedVM(vm, request.VMOperationKey); err != nil {
		return
	}
	var interfaces map[string]string
	if vm.VirtualMachineConfig != nil {
		interfaces = vm.VirtualMachineConfig.Nets
	}
	var device string
	var found bool
	if device, found, err = findAttachmentDevice(interfaces, request); err != nil {
		return
	}
	if !found {
		return
	}
	if request.AllowDHCPv4Server {
		if err = removeVMInterfaceDHCPv4ServerRule(ctx, vm, device, request); err != nil {
			return
		}
	}
	if request.AllowDHCPv6Server {
		if err = removeVMInterfaceDHCPv6ServerRule(ctx, vm, device, request); err != nil {
			return
		}
	}
	if len(request.AllowedClientSubnets) > 0 {
		if err = removeVMInterfaceRouterDNSRules(ctx, vm, device, request); err != nil {
			return
		}
	}
	if request.EnforceAddressFilter {
		if err = driver.removeVMInterfaceIPFilter(ctx, vm, device, request); err != nil {
			return
		}
	}
	var task *pve.Task
	if task, err = vm.Config(ctx, pve.VirtualMachineOption{Name: "delete", Value: device}); err != nil {
		return
	}
	err = waitTask(ctx, client, task)
	return
}

// enableVMIPFiltering turns on VM-level Proxmox filtering without changing unrelated firewall policy.
func (driver *apiNetworkAttachmentDriver) enableVMIPFiltering(ctx context.Context, client *pve.Client, vm *pve.VirtualMachine) (err error) {
	if err = verifyProxmoxFirewallPrerequisites(ctx, client, vm.Node); err != nil {
		return
	}
	var options *pve.FirewallVirtualMachineOption
	if options, err = getVMFirewallOptions(ctx, client, vm); err != nil {
		return
	}
	enableVMIPFilteringOptions(options)
	err = vm.FirewallOptionSet(ctx, options)
	return
}

// enableVMIPFilteringOptions enables address filtering, DHCP allowances, and IPv6 neighbor discovery.
func enableVMIPFilteringOptions(options *pve.FirewallVirtualMachineOption) {
	options.Enable = true
	options.Dhcp = true
	options.Ipfilter = true
	options.NDP = true
}

// dhcpv6ServerFirewallRule builds a DHCPv6-only ingress allowance for a managed router LAN.
func dhcpv6ServerFirewallRule(device string, request NetworkAttachmentRequest) (rule *pve.FirewallRule) {
	rule = &pve.FirewallRule{
		Type: "in", Action: "ACCEPT", Enable: 1, Iface: device, Proto: "udp", Sport: "546", Dport: "547",
		Comment: dhcpv6ServerFirewallRuleComment(request),
	}
	return
}

// dhcpv6ServerFirewallRuleComment identifies only the DHCPv6 ingress rule owned by this LAN attachment.
func dhcpv6ServerFirewallRuleComment(request NetworkAttachmentRequest) (comment string) {
	comment = "Organesson DHCPv6 server " + request.AttachmentOperationKey
	return
}

// dhcpv4ServerFirewallRule builds a LAN-interface-only allowance for DHCPv4 client requests and renewals.
func dhcpv4ServerFirewallRule(device string, request NetworkAttachmentRequest) (rule *pve.FirewallRule) {
	rule = &pve.FirewallRule{
		Type: "in", Action: "ACCEPT", Enable: 1, Iface: device, Proto: "udp", Sport: "68", Dport: "67",
		Comment: dhcpv4ServerFirewallRuleComment(request),
	}
	return
}

// dhcpv4ServerFirewallRuleComment identifies the exact DHCPv4 ingress allowance owned by this LAN attachment.
func dhcpv4ServerFirewallRuleComment(request NetworkAttachmentRequest) (comment string) {
	comment = "Organesson DHCPv4 server " + request.AttachmentOperationKey
	return
}

// routerDNSFirewallRules allows DNS queries to one platform-managed router from its LAN subnets.
func routerDNSFirewallRules(device string, request NetworkAttachmentRequest) (rules []*pve.FirewallRule) {
	for _, subnet := range request.AllowedClientSubnets {
		for _, protocol := range []string{"udp", "tcp"} {
			rules = append(rules, &pve.FirewallRule{
				Type: "in", Action: "ACCEPT", Enable: 1, Iface: device, Proto: protocol, Dport: "53", Source: subnet,
				Comment: "Organesson router DNS " + request.AttachmentOperationKey + " " + protocol + " " + subnet,
			})
		}
	}
	return
}

// ensureVMInterfaceRouterDNSRules idempotently permits LAN clients to query router DNS.
func ensureVMInterfaceRouterDNSRules(ctx context.Context, vm *pve.VirtualMachine, device string, request NetworkAttachmentRequest) (err error) {
	var existing []*pve.FirewallRule
	if existing, err = vm.FirewallRules(ctx); err != nil {
		return
	}
	for _, expected := range routerDNSFirewallRules(device, request) {
		var found bool
		for _, actual := range existing {
			if actual == nil || actual.Comment != expected.Comment {
				continue
			}
			if !firewallRulesEqual(actual, expected) {
				err = fmt.Errorf("Organesson router DNS firewall rule %q conflicts with its desired configuration", expected.Comment)
				return
			}
			found = true
			break
		}
		if !found {
			if err = vm.NewFirewallRule(ctx, expected); err != nil {
				return
			}
		}
	}
	return
}

// verifyVMInterfaceRouterDNSRules checks the exact router DNS ingress allowances.
func verifyVMInterfaceRouterDNSRules(ctx context.Context, vm *pve.VirtualMachine, device string, request NetworkAttachmentRequest) (err error) {
	var existing []*pve.FirewallRule
	if existing, err = vm.FirewallRules(ctx); err != nil {
		return
	}
	for _, expected := range routerDNSFirewallRules(device, request) {
		var found bool
		for _, actual := range existing {
			if actual != nil && actual.Comment == expected.Comment {
				if !firewallRulesEqual(actual, expected) {
					err = fmt.Errorf("Organesson router DNS firewall rule %q differs from its expected configuration", expected.Comment)
					return
				}
				found = true
				break
			}
		}
		if !found {
			err = fmt.Errorf("Organesson router DNS firewall rule %q is missing", expected.Comment)
			return
		}
	}
	return
}

// removeVMInterfaceRouterDNSRules removes only the DNS allowances owned by this LAN attachment.
func removeVMInterfaceRouterDNSRules(ctx context.Context, vm *pve.VirtualMachine, device string, request NetworkAttachmentRequest) (err error) {
	var existing []*pve.FirewallRule
	if existing, err = vm.FirewallRules(ctx); err != nil {
		return
	}
	for _, expected := range routerDNSFirewallRules(device, request) {
		for _, actual := range existing {
			if actual == nil || actual.Comment != expected.Comment {
				continue
			}
			if !firewallRulesEqual(actual, expected) {
				err = fmt.Errorf("refusing to remove modified Organesson router DNS rule %q", expected.Comment)
				return
			}
			if err = actual.Delete(ctx); err != nil {
				return
			}
		}
	}
	return
}

// ensureVMInterfaceDHCPv4ServerRule idempotently allows DHCPv4 requests on one managed router LAN.
func ensureVMInterfaceDHCPv4ServerRule(ctx context.Context, vm *pve.VirtualMachine, device string, request NetworkAttachmentRequest) (err error) {
	var rules []*pve.FirewallRule
	if rules, err = vm.FirewallRules(ctx); err != nil {
		return
	}
	var expected *pve.FirewallRule = dhcpv4ServerFirewallRule(device, request)
	for _, rule := range rules {
		if rule == nil || rule.Comment != expected.Comment {
			continue
		}
		if firewallRulesEqual(rule, expected) {
			return
		}
		err = fmt.Errorf("Organesson DHCPv4 firewall rule %q conflicts with its desired configuration", expected.Comment)
		return
	}
	err = vm.NewFirewallRule(ctx, expected)
	return
}

// verifyVMInterfaceDHCPv4ServerRule checks the exact DHCPv4 ingress allowance for a managed router LAN.
func verifyVMInterfaceDHCPv4ServerRule(ctx context.Context, vm *pve.VirtualMachine, device string, request NetworkAttachmentRequest) (err error) {
	var rules []*pve.FirewallRule
	if rules, err = vm.FirewallRules(ctx); err != nil {
		return
	}
	var expected *pve.FirewallRule = dhcpv4ServerFirewallRule(device, request)
	for _, rule := range rules {
		if rule != nil && rule.Comment == expected.Comment {
			if !firewallRulesEqual(rule, expected) {
				err = fmt.Errorf("Organesson DHCPv4 firewall rule %q differs from its expected configuration", expected.Comment)
			}
			return
		}
	}
	err = fmt.Errorf("Organesson DHCPv4 firewall rule %q is missing", expected.Comment)
	return
}

// removeVMInterfaceDHCPv4ServerRule removes only the managed DHCPv4 ingress allowance for one attachment.
func removeVMInterfaceDHCPv4ServerRule(ctx context.Context, vm *pve.VirtualMachine, device string, request NetworkAttachmentRequest) (err error) {
	var rules []*pve.FirewallRule
	if rules, err = vm.FirewallRules(ctx); err != nil {
		return
	}
	var expected *pve.FirewallRule = dhcpv4ServerFirewallRule(device, request)
	for _, rule := range rules {
		if rule != nil && rule.Comment == expected.Comment {
			if !firewallRulesEqual(rule, expected) {
				err = fmt.Errorf("refusing to remove modified Organesson DHCPv4 firewall rule %q", expected.Comment)
				return
			}
			err = rule.Delete(ctx)
			return
		}
	}
	return
}

// ensureVMInterfaceDHCPv6ServerRule idempotently allows DHCPv6 clients to reach a managed router.
func ensureVMInterfaceDHCPv6ServerRule(ctx context.Context, vm *pve.VirtualMachine, device string, request NetworkAttachmentRequest) (err error) {
	var rules []*pve.FirewallRule
	if rules, err = vm.FirewallRules(ctx); err != nil {
		return
	}
	var expected *pve.FirewallRule = dhcpv6ServerFirewallRule(device, request)
	for _, rule := range rules {
		if rule == nil || rule.Comment != expected.Comment {
			continue
		}
		if firewallRulesEqual(rule, expected) {
			return
		}
		if isLegacyDHCPv6ServerFirewallRule(rule, expected) {
			if err = rule.Delete(ctx); err != nil {
				return
			}
			err = vm.NewFirewallRule(ctx, expected)
			return
		}
		err = fmt.Errorf("Organesson DHCPv6 firewall rule %q conflicts with its desired configuration", expected.Comment)
		return
	}
	err = vm.NewFirewallRule(ctx, expected)
	return
}

// isLegacyDHCPv6ServerFirewallRule recognizes the prior link-local-only rule so it can be safely widened for renewals.
func isLegacyDHCPv6ServerFirewallRule(actual *pve.FirewallRule, expected *pve.FirewallRule) (matches bool) {
	var legacy pve.FirewallRule = *expected
	legacy.Source = "fe80::/10"
	matches = firewallRulesEqual(actual, &legacy)
	return
}

// verifyVMInterfaceDHCPv6ServerRule checks the managed router's exact DHCPv6 ingress allowance.
func verifyVMInterfaceDHCPv6ServerRule(ctx context.Context, vm *pve.VirtualMachine, device string, request NetworkAttachmentRequest) (err error) {
	var rules []*pve.FirewallRule
	if rules, err = vm.FirewallRules(ctx); err != nil {
		return
	}
	var expected *pve.FirewallRule = dhcpv6ServerFirewallRule(device, request)
	for _, rule := range rules {
		if rule != nil && rule.Comment == expected.Comment {
			if !firewallRulesEqual(rule, expected) {
				err = fmt.Errorf("Organesson DHCPv6 firewall rule %q differs from its expected configuration", expected.Comment)
			}
			return
		}
	}
	err = fmt.Errorf("Organesson DHCPv6 firewall rule %q is missing", expected.Comment)
	return
}

// removeVMInterfaceDHCPv6ServerRule removes only the marked DHCPv6 ingress rule for one attachment.
func removeVMInterfaceDHCPv6ServerRule(ctx context.Context, vm *pve.VirtualMachine, device string, request NetworkAttachmentRequest) (err error) {
	var rules []*pve.FirewallRule
	if rules, err = vm.FirewallRules(ctx); err != nil {
		return
	}
	var expected *pve.FirewallRule = dhcpv6ServerFirewallRule(device, request)
	for _, rule := range rules {
		if rule != nil && rule.Comment == expected.Comment {
			if !firewallRulesEqual(rule, expected) {
				err = fmt.Errorf("refusing to remove modified Organesson DHCPv6 firewall rule %q", expected.Comment)
				return
			}
			err = rule.Delete(ctx)
			return
		}
	}
	return
}

// firewallRulesEqual compares the fields Organesson owns on a DHCPv6 rule.
func firewallRulesEqual(actual *pve.FirewallRule, expected *pve.FirewallRule) (equal bool) {
	equal = actual.Type == expected.Type && actual.Action == expected.Action && actual.Enable == expected.Enable &&
		actual.Iface == expected.Iface && actual.Proto == expected.Proto && actual.Sport == expected.Sport &&
		actual.Dport == expected.Dport && actual.Source == expected.Source && actual.Comment == expected.Comment
	return
}

// getVMFirewallOptions reads into a non-nil value because go-proxmox v0.8.2 passes nil to its decoder here.
func getVMFirewallOptions(ctx context.Context, client *pve.Client, vm *pve.VirtualMachine) (options *pve.FirewallVirtualMachineOption, err error) {
	options = &pve.FirewallVirtualMachineOption{}
	err = client.Get(ctx, fmt.Sprintf("/nodes/%s/qemu/%d/firewall/options", vm.Node, vm.VMID), options)
	return
}

// ensureVMInterfaceIPFilter owns only Organesson-marked entries in Proxmox's per-interface IP set.
func (driver *apiNetworkAttachmentDriver) ensureVMInterfaceIPFilter(ctx context.Context, client *pve.Client, vm *pve.VirtualMachine, device string, request NetworkAttachmentRequest) (err error) {
	if err = driver.enableVMIPFiltering(ctx, client, vm); err != nil {
		return
	}
	var setName string = "ipfilter-" + device
	var sets []*pve.FirewallIPSet
	if sets, err = vm.GetFirewallIPSet(ctx); err != nil {
		return
	}
	var found bool
	for _, set := range sets {
		if set != nil && set.Name == setName {
			found = true
			break
		}
	}
	if !found {
		if err = vm.NewFirewallIPSet(ctx, pve.FirewallIPSetCreationOption{Name: setName, Comment: "Organesson managed source-address filter"}); err != nil {
			return
		}
	}
	var entries []*pve.FirewallIPSetEntry
	if entries, err = vm.GetFirewallIPSetEntries(ctx, setName); err != nil {
		return
	}
	var marker string = "organesson:" + request.AttachmentOperationKey
	var desired map[string]bool = make(map[string]bool, len(request.AllowedAddresses))
	for _, address := range request.AllowedAddresses {
		var parsed netip.Prefix
		if parsed, err = parseIPSetEntry(address); err != nil {
			return
		}
		desired[parsed.String()] = true
	}
	var present map[string]bool = make(map[string]bool, len(entries))
	for _, entry := range entries {
		if entry == nil {
			continue
		}
		var parsed netip.Prefix
		if parsed, err = parseIPSetEntry(entry.CIDR); err != nil {
			return
		}
		var address string = parsed.String()
		if entry.Comment != marker {
			err = fmt.Errorf("Proxmox IP set %q contains an entry not owned by this Organesson attachment", setName)
			return
		}
		if !desired[address] {
			if err = vm.DeleteFirewallIPSetEntry(ctx, setName, entry.CIDR, entry.Digest); err != nil {
				return
			}
			continue
		}
		present[address] = true
	}
	for address := range desired {
		if present[address] {
			continue
		}
		if err = vm.NewFirewallIPSetEntry(ctx, setName, pve.FirewallIPSetEntryCreationOption{CIDR: address, Comment: marker}); err != nil {
			return
		}
	}
	return
}

// verifyProxmoxFirewallPrerequisites refuses to claim filtering when cluster/node firewalling is inactive.
func verifyProxmoxFirewallPrerequisites(ctx context.Context, client *pve.Client, nodeName string) (err error) {
	var cluster *pve.Cluster
	if cluster, err = client.Cluster(ctx); err != nil {
		return
	}
	var clusterOptions *pve.FirewallClusterOption
	if clusterOptions, err = cluster.FirewallOptions(ctx); err != nil {
		return
	}
	if clusterOptions == nil || clusterOptions.Enable != 1 {
		err = errors.New("Proxmox cluster firewall is disabled; enable it before using Organesson address filtering")
		return
	}
	var node *pve.Node
	if node, err = client.Node(ctx, nodeName); err != nil {
		return
	}
	var nodeOptions *pve.FirewallNodeOption
	if nodeOptions, err = node.FirewallOptionGet(ctx); err != nil {
		return
	}
	if nodeOptions != nil && nodeOptions.Enable != nil && !bool(*nodeOptions.Enable) {
		err = fmt.Errorf("Proxmox node firewall is disabled on %q; enable it before using Organesson address filtering", nodeName)
	}
	return
}

// verifyVMInterfaceIPFilter confirms the Proxmox firewall is enabled and its allow-list matches allocation state.
func verifyVMInterfaceIPFilter(ctx context.Context, client *pve.Client, vm *pve.VirtualMachine, device string, request NetworkAttachmentRequest) (err error) {
	var options *pve.FirewallVirtualMachineOption
	if options, err = getVMFirewallOptions(ctx, client, vm); err != nil {
		return
	}
	if options == nil || !options.Enable || !options.Ipfilter {
		err = errors.New("Proxmox VM firewall IP filtering is not enabled")
		return
	}
	var entries []*pve.FirewallIPSetEntry
	if entries, err = vm.GetFirewallIPSetEntries(ctx, "ipfilter-"+device); err != nil {
		return
	}
	var expected map[string]bool = make(map[string]bool, len(request.AllowedAddresses))
	for _, address := range request.AllowedAddresses {
		var parsed netip.Prefix
		if parsed, err = parseIPSetEntry(address); err != nil {
			return
		}
		expected[parsed.String()] = true
	}
	var marker string = "organesson:" + request.AttachmentOperationKey
	var observed map[string]bool = make(map[string]bool, len(entries))
	for _, entry := range entries {
		if entry == nil {
			continue
		}
		var prefix netip.Prefix
		if prefix, err = parseIPSetEntry(entry.CIDR); err != nil {
			return
		}
		var address string = prefix.String()
		if entry.Comment != marker || !expected[address] {
			err = errors.New("Proxmox interface IP filter contains an unapproved address scope or foreign entry")
			return
		}
		observed[address] = true
	}
	if len(observed) != len(expected) {
		err = errors.New("Proxmox interface IP filter differs from the approved address scopes")
		return
	}
	for address := range expected {
		if !observed[address] {
			err = errors.New("Proxmox interface IP filter is missing an approved address scope")
			return
		}
	}
	return
}

// removeVMInterfaceIPFilter removes only the set entries owned by the detaching attachment.
func (driver *apiNetworkAttachmentDriver) removeVMInterfaceIPFilter(ctx context.Context, vm *pve.VirtualMachine, device string, request NetworkAttachmentRequest) (err error) {
	var setName string = "ipfilter-" + device
	var sets []*pve.FirewallIPSet
	if sets, err = vm.GetFirewallIPSet(ctx); err != nil {
		return
	}
	var found bool
	for _, set := range sets {
		if set != nil && set.Name == setName {
			found = true
			break
		}
	}
	if !found {
		return
	}
	var entries []*pve.FirewallIPSetEntry
	if entries, err = vm.GetFirewallIPSetEntries(ctx, setName); err != nil {
		return
	}
	var marker string = "organesson:" + request.AttachmentOperationKey
	for _, entry := range entries {
		if entry != nil && entry.Comment == marker {
			if err = vm.DeleteFirewallIPSetEntry(ctx, setName, entry.CIDR, entry.Digest); err != nil {
				return
			}
		}
	}
	return
}

// findAttachmentDevice recovers the interface from its deterministic ownership MAC after an interrupted attach.
func findAttachmentDevice(interfaces map[string]string, request NetworkAttachmentRequest) (device string, found bool, err error) {
	var expectedMAC string = networkAttachmentMAC(request.AttachmentOperationKey)
	for candidate, candidateValue := range interfaces {
		var options map[string]string = networkOptionMap(candidateValue)
		if !strings.EqualFold(options["macaddr"], expectedMAC) {
			continue
		}
		if options["bridge"] != request.Bridge {
			err = errors.New("marked Proxmox network device is attached to a different bridge")
			return
		}
		if found {
			err = errors.New("multiple Proxmox NICs carry the same Organesson attachment marker")
			return
		}
		device = candidate
		found = true
	}
	return
}

// parseIPSetEntry canonicalizes a Proxmox filter entry as a host or subnet prefix.
func parseIPSetEntry(value string) (prefix netip.Prefix, err error) {
	if address, parseErr := netip.ParseAddr(value); parseErr == nil {
		var bits int = 128
		if address.Is4() {
			bits = 32
		}
		prefix = netip.PrefixFrom(address, bits)
		return
	}
	if prefix, err = netip.ParsePrefix(value); err != nil || prefix != prefix.Masked() {
		err = fmt.Errorf("Proxmox IP filter entry %q is not a canonical address or subnet", value)
	}
	return
}

func validateNetworkAttachmentRequest(request NetworkAttachmentRequest) (err error) {
	if request.Node == "" || request.VMID == "" || request.VMOperationKey == "" || request.AttachmentOperationKey == "" || request.Bridge == "" || strings.ContainsAny(request.Bridge, ",=") {
		err = errors.New("network attachment requires managed VM identity and a valid approved bridge")
		return
	}
	if request.NetworkOperationKey == "" && strings.HasPrefix(request.Bridge, "on") {
		err = errors.New("Organesson SDN VNets require their ownership operation key")
		return
	}
	if (request.AllowDHCPv4Server || request.AllowDHCPv6Server) && (!request.EnforceAddressFilter || request.NetworkOperationKey == "") {
		err = errors.New("DHCP server allowances require a filtered, Organesson-managed network attachment")
		return
	}
	if len(request.AllowedClientSubnets) > 0 && (!request.EnforceAddressFilter || request.NetworkOperationKey == "") {
		err = errors.New("router client subnets require a filtered, Organesson-managed network attachment")
		return
	}
	var subnetFamilies map[bool]bool = make(map[bool]bool)
	for _, value := range request.AllowedClientSubnets {
		var prefix netip.Prefix
		if prefix, err = netip.ParsePrefix(value); err != nil || prefix != prefix.Masked() {
			err = fmt.Errorf("router client subnet %q is not a canonical network prefix", value)
			return
		}
		if subnetFamilies[prefix.Addr().Is6()] {
			err = fmt.Errorf("router client subnet family is duplicated: %q", value)
			return
		}
		subnetFamilies[prefix.Addr().Is6()] = true
	}
	if request.EnforceAddressFilter {
		if len(request.AllowedAddresses) == 0 {
			err = errors.New("IP filtering requires at least one address or subnet entry")
			return
		}
		for _, value := range request.AllowedAddresses {
			var prefix netip.Prefix
			if prefix, err = parseIPSetEntry(value); err != nil || prefix.Addr().IsUnspecified() || prefix.Addr().IsMulticast() || prefix.Addr().Zone() != "" {
				err = fmt.Errorf("IP filtering entry %q is not a usable host address or canonical subnet", value)
				return
			}
		}
	} else if len(request.AllowedAddresses) > 0 {
		err = errors.New("allowed IPs require address filtering")
	}
	return
}

func verifyAttachmentTarget(ctx context.Context, client *pve.Client, node *pve.Node, request NetworkAttachmentRequest) (err error) {
	if request.NetworkOperationKey != "" {
		var cluster *pve.Cluster
		if cluster, err = client.Cluster(ctx); err != nil {
			return
		}
		var vnets []*pve.VNet
		if vnets, err = cluster.SDNVNets(ctx); err != nil {
			return
		}
		for _, vnet := range vnets {
			if vnet == nil || vnet.Name != request.Bridge {
				continue
			}
			var expected SDNNetworkPlacement = namesForSDNNetwork(request.NetworkOperationKey)
			if vnet.Alias != "organesson:"+request.NetworkOperationKey || vnet.Name != expected.VNet {
				err = errors.New("target Proxmox SDN VNet is not owned by the referenced Organesson network")
				return
			}
			// Cluster SDN VNets are not necessarily exposed by a node's local
			// network-interface inventory; the ownership-checked SDN record is
			// the authoritative attachment target.
			return
		}
		err = ErrSDNNetworkNotFound
		return
	}
	var networks pve.NodeNetworks
	if networks, err = node.Networks(ctx, "bridge"); err != nil {
		return
	}
	for _, network := range networks {
		if network != nil && network.Iface == request.Bridge {
			return
		}
	}
	err = fmt.Errorf("approved Proxmox bridge %q is not present on node %q", request.Bridge, request.Node)
	return
}

func networkAttachmentMAC(operationKey string) (mac string) {
	var digest [sha256.Size]byte = sha256.Sum256([]byte(operationKey))
	mac = fmt.Sprintf("02:%02x:%02x:%02x:%02x:%02x", digest[0], digest[1], digest[2], digest[3], digest[4])
	return
}

func networkOptionMap(value string) (options map[string]string) {
	options = make(map[string]string)
	for _, option := range strings.Split(value, ",") {
		var pair []string = strings.SplitN(option, "=", 2)
		if len(pair) == 2 {
			options[strings.ToLower(pair[0])] = pair[1]
			if (pair[0] == "virtio" || pair[0] == "e1000" || pair[0] == "rtl8139") && strings.Contains(pair[1], ":") {
				options["macaddr"] = pair[1]
			}
		} else if len(pair) == 1 && strings.Contains(pair[0], ":") {
			options["macaddr"] = pair[0]
		}
	}
	if value != "" && !strings.Contains(value, "=") {
		var parts []string = strings.Split(value, ",")
		if len(parts) > 0 {
			options["macaddr"] = parts[0]
		}
	}
	return
}

func nextNetworkDevice(interfaces map[string]string) (device string, err error) {
	var indexes []int
	for name := range interfaces {
		if !strings.HasPrefix(name, "net") {
			continue
		}
		var index int
		if index, err = strconv.Atoi(strings.TrimPrefix(name, "net")); err != nil || index < 0 || index > 31 {
			continue
		}
		indexes = append(indexes, index)
	}
	sort.Ints(indexes)
	for index := 0; index < 32; index++ {
		if index >= len(indexes) || indexes[index] != index {
			device = fmt.Sprintf("net%d", index)
			return
		}
	}
	err = errors.New("managed VM has no free Proxmox network device slot")
	return
}
