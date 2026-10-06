package proxmox

import (
	"context"
	"errors"
	"fmt"
	"net/netip"
	"sort"
	"strconv"
	"strings"
	"time"

	pve "github.com/luthermonson/go-proxmox"
	"github.com/z46-dev/organesson/backend/config"
)

const routerLeaseFile = "/var/lib/misc/dnsmasq.leases"
const routerDHCPConfigFile = "/etc/dnsmasq.d/organesson-router.conf"
const routerFirewallConfigFile = "/etc/nftables.conf"

type (
	apiSDNRouterPoller struct {
		settings config.ProxmoxConfiguration
	}

	cachedRouterPollingResult struct {
		result   SDNRouterPollingResult
		cachedAt time.Time
		cacheTTL time.Duration
	}
)

// PollRouter reads dnsmasq leases and the router's neighbor table through QEMU Guest Agent.
func (driver *apiSDNNetworkDriver) PollRouter(ctx context.Context, request SDNNetworkRequest, placement SDNNetworkPlacement) (result SDNRouterPollingResult, err error) {
	return (&apiSDNRouterPoller{settings: driver.settings}).PollRouter(ctx, request, placement)
}

// PollRouter validates the configured router identity and returns LAN address observations.
func (driver *apiSDNRouterPoller) PollRouter(ctx context.Context, request SDNNetworkRequest, placement SDNNetworkPlacement) (result SDNRouterPollingResult, err error) {
	result = SDNRouterPollingResult{State: "unavailable", RouterVMID: request.RouterVMID, ObservedAddresses: []SDNRouterObservedAddress{}}
	if request.Mode != "managed" || request.RouterVMID < 1 {
		err = errors.New("router polling requires a managed network and a configured router VMID")
		return
	}
	if err = validateSDNNetworkRequest(request); err != nil {
		return
	}
	var expected SDNNetworkPlacement = placementForSDNNetwork(request)
	if placement.Zone != expected.Zone || placement.VNet != expected.VNet {
		err = errors.New("stored Proxmox SDN placement does not match its operation key")
		return
	}
	var client *pve.Client
	if client, err = newAPIClient(driver.settings); err != nil {
		return
	}
	var cluster *pve.Cluster
	if cluster, err = client.Cluster(ctx); err != nil {
		return
	}
	var resources pve.ClusterResources
	if resources, err = cluster.Resources(ctx, "vm"); err != nil {
		return
	}
	var vnets []*pve.VNet
	if vnets, err = cluster.SDNVNets(ctx); err != nil {
		return
	}
	var networkOwned bool
	for _, vnet := range vnets {
		if vnet != nil && vnet.Name == placement.VNet && vnet.Zone == placement.Zone && vnet.Alias == "organesson:"+request.OperationKey {
			networkOwned = true
			break
		}
	}
	if !networkOwned {
		err = ErrSDNNetworkNotFound
		return
	}
	var vmFound bool
	for _, resource := range resources {
		if resource == nil || resource.Type != "qemu" || resource.VMID != uint64(request.RouterVMID) {
			continue
		}
		if resource.Pool != "organesson" || resource.Status != "running" {
			err = errors.New("configured router must be a running QEMU VM in the organesson pool")
			return
		}
		vmFound = true
		break
	}
	if !vmFound {
		err = errClusterVMNotFound
		return
	}
	var vm *pve.VirtualMachine
	if _, vm, _, err = locateManagedVM(ctx, client, request.RouterVMID); err != nil {
		return
	}
	if vm.VirtualMachineConfig == nil {
		err = errors.New("router VM configuration could not be read")
		return
	}
	var routerMACs map[string]bool = make(map[string]bool)
	for _, value := range vm.VirtualMachineConfig.Nets {
		var options map[string]string = networkOptionMap(value)
		if options["bridge"] == placement.VNet && options["macaddr"] != "" {
			routerMACs[strings.ToLower(options["macaddr"])] = true
		}
	}
	if len(routerMACs) == 0 {
		err = fmt.Errorf("router VM %d has no interface on managed VNet %q", request.RouterVMID, placement.VNet)
		return
	}
	var bridgeByMAC map[string]string = make(map[string]string)
	for _, value := range vm.VirtualMachineConfig.Nets {
		var options map[string]string = networkOptionMap(value)
		if options["macaddr"] != "" {
			bridgeByMAC[strings.ToLower(options["macaddr"])] = options["bridge"]
		}
	}
	var interfaces []*pve.AgentNetworkIface
	if interfaces, err = vm.AgentGetNetworkIFaces(ctx); err != nil {
		return
	}
	var lanInterface string
	for _, iface := range interfaces {
		if iface != nil && routerMACs[strings.ToLower(iface.HardwareAddress)] {
			lanInterface = iface.Name
			break
		}
	}
	if lanInterface == "" {
		err = errors.New("QEMU Guest Agent did not report the configured router LAN interface")
		return
	}
	var dhcpConfig *pve.AgentFileRead
	if dhcpConfig, err = vm.AgentFileRead(ctx, routerDHCPConfigFile); err == nil && dhcpConfig != nil && !bool(dhcpConfig.Truncated) {
		result.DHCPRangeStart, result.DHCPRangeEnd = parseRouterDHCPRange(dhcpConfig.Content)
		result.DHCPv6RangeStart, result.DHCPv6RangeEnd = parseRouterDHCPv6Range(dhcpConfig.Content)
	} else {
		err = nil
	}
	var firewallConfig *pve.AgentFileRead
	if firewallConfig, err = vm.AgentFileRead(ctx, routerFirewallConfigFile); err == nil && firewallConfig != nil && !bool(firewallConfig.Truncated) {
		var egressInterface string = parseRouterEgressInterface(firewallConfig.Content)
		for _, iface := range interfaces {
			if iface == nil || iface.Name != egressInterface {
				continue
			}
			result.Egress = &SDNRouterEgress{
				Interface: iface.Name, MAC: strings.ToLower(iface.HardwareAddress), Bridge: bridgeByMAC[strings.ToLower(iface.HardwareAddress)],
				Addresses:     routerInterfaceIPv4Addresses(iface),
				IPv6Addresses: routerInterfaceIPv6Addresses(iface),
			}
			var gatewayPID int
			if gatewayPID, err = vm.AgentExec(ctx, []string{"ip", "-4", "route", "show", "default", "dev", iface.Name}, ""); err == nil {
				var gatewayResult guestAgentExecStatus
				if gatewayResult, err = waitForGuestExecExit(ctx, driver.settings, vm.Node, request.RouterVMID, gatewayPID, 15); err == nil && gatewayResult.ExitCode == 0 {
					result.Egress.Gateway = parseRouterDefaultGateway(gatewayResult.OutData)
				}
			}
			var gatewayIPv6PID int
			if gatewayIPv6PID, err = vm.AgentExec(ctx, []string{"ip", "-6", "route", "show", "default", "dev", iface.Name}, ""); err == nil {
				var gatewayIPv6Result guestAgentExecStatus
				if gatewayIPv6Result, err = waitForGuestExecExit(ctx, driver.settings, vm.Node, request.RouterVMID, gatewayIPv6PID, 15); err == nil && gatewayIPv6Result.ExitCode == 0 {
					result.Egress.IPv6Gateway = parseRouterDefaultGateway(gatewayIPv6Result.OutData)
				}
			}
			err = nil
			break
		}
	} else {
		err = nil
	}
	var observed map[string]SDNRouterObservedAddress = make(map[string]SDNRouterObservedAddress)
	for _, iface := range interfaces {
		if iface == nil || iface.Name != lanInterface {
			continue
		}
		mergeRouterInterfaceAddresses(observed, iface, request.Subnet)
		mergeRouterInterfaceAddresses(observed, iface, request.IPv6Subnet)
		break
	}
	var leaseFile *pve.AgentFileRead
	if leaseFile, err = vm.AgentFileRead(ctx, routerLeaseFile); err == nil && leaseFile != nil && !bool(leaseFile.Truncated) {
		mergeRouterLeases(observed, leaseFile.Content, request.Subnet)
		mergeRouterLeases(observed, leaseFile.Content, request.IPv6Subnet)
	} else if err != nil {
		err = nil
	}
	var pid int
	if pid, err = vm.AgentExec(ctx, []string{"ip", "neigh", "show", "dev", lanInterface}, ""); err != nil {
		return
	}
	var commandResult guestAgentExecStatus
	if commandResult, err = waitForGuestExecExit(ctx, driver.settings, vm.Node, request.RouterVMID, pid, 15); err != nil {
		return
	}
	if commandResult.ExitCode != 0 {
		err = errors.New("router neighbor-table query failed")
		return
	}
	mergeRouterNeighbors(observed, commandResult.OutData, request.Subnet)
	if request.IPv6Subnet != "" {
		var ipv6PID int
		if ipv6PID, err = vm.AgentExec(ctx, []string{"ip", "-6", "neigh", "show", "dev", lanInterface}, ""); err != nil {
			return
		}
		var ipv6Result guestAgentExecStatus
		if ipv6Result, err = waitForGuestExecExit(ctx, driver.settings, vm.Node, request.RouterVMID, ipv6PID, 15); err != nil {
			return
		}
		if ipv6Result.ExitCode != 0 {
			err = errors.New("router IPv6 neighbor-table query failed")
			return
		}
		mergeRouterNeighbors(observed, ipv6Result.OutData, request.IPv6Subnet)
	}
	for _, entry := range observed {
		result.ObservedAddresses = append(result.ObservedAddresses, entry)
	}
	sortRouterAddresses(result.ObservedAddresses)
	result.State = "available"
	result.LastPolledAt = time.Now().UTC()
	return
}

func parseRouterDHCPv6Range(content string) (start string, end string) {
	for _, line := range strings.Split(content, "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "dhcp-range=") {
			continue
		}
		var fields []string = strings.Split(strings.TrimPrefix(line, "dhcp-range="), ",")
		if len(fields) < 2 {
			continue
		}
		var first netip.Addr
		var last netip.Addr
		if first, _ = netip.ParseAddr(strings.TrimSpace(fields[0])); first.Is6() && first.IsValid() {
			if last, _ = netip.ParseAddr(strings.TrimSpace(fields[1])); last.Is6() && last.IsValid() {
				start, end = first.String(), last.String()
				return
			}
		}
	}
	return
}

// parseRouterDHCPRange extracts the configured start and end hosts from dnsmasq settings.
func parseRouterDHCPRange(content string) (start string, end string) {
	for _, line := range strings.Split(content, "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "dhcp-range=") {
			continue
		}
		var fields []string = strings.Split(strings.TrimPrefix(line, "dhcp-range="), ",")
		if len(fields) >= 2 {
			start = strings.TrimSpace(fields[0])
			end = strings.TrimSpace(fields[1])
		}
		return
	}
	return
}

// parseRouterEgressInterface finds the NAT uplink named by the router's nftables configuration.
func parseRouterEgressInterface(content string) (name string) {
	for _, line := range strings.Split(content, "\n") {
		var fields []string = strings.Fields(line)
		for index, field := range fields {
			if field == "oifname" && index+1 < len(fields) && strings.Contains(line, "masquerade") {
				name = strings.Trim(fields[index+1], "\"'")
				return
			}
		}
	}
	return
}

// routerInterfaceIPv4Addresses returns usable IPv4 CIDRs reported by QEMU Guest Agent.
func routerInterfaceIPv4Addresses(iface *pve.AgentNetworkIface) (addresses []string) {
	if iface == nil {
		return
	}
	for _, address := range iface.IPAddresses {
		if address == nil || address.IPAddressType != "ipv4" {
			continue
		}
		addresses = append(addresses, fmt.Sprintf("%s/%d", address.IPAddress, address.Prefix))
	}
	return
}

func routerInterfaceIPv6Addresses(iface *pve.AgentNetworkIface) (addresses []string) {
	if iface == nil {
		return
	}
	for _, address := range iface.IPAddresses {
		if address == nil || address.IPAddressType != "ipv6" {
			continue
		}
		addresses = append(addresses, fmt.Sprintf("%s/%d", address.IPAddress, address.Prefix))
	}
	return
}

// parseRouterDefaultGateway extracts the default route's next hop for one egress NIC.
func parseRouterDefaultGateway(content string) (gateway string) {
	for _, line := range strings.Split(content, "\n") {
		var fields []string = strings.Fields(line)
		for index, field := range fields {
			if field == "via" && index+1 < len(fields) {
				gateway = fields[index+1]
				return
			}
		}
	}
	return
}

// mergeRouterInterfaceAddresses includes the router's own configured LAN addresses and MAC.
func mergeRouterInterfaceAddresses(observed map[string]SDNRouterObservedAddress, iface *pve.AgentNetworkIface, subnet string) {
	var prefix netip.Prefix
	if iface == nil {
		return
	}
	if prefix, _ = netip.ParsePrefix(subnet); !prefix.IsValid() {
		return
	}
	for _, ip := range iface.IPAddresses {
		if ip == nil {
			continue
		}
		var address netip.Addr
		if address, _ = netip.ParseAddr(ip.IPAddress); !address.IsValid() || !prefix.Contains(address) {
			continue
		}
		var entry SDNRouterObservedAddress = observed[address.String()]
		entry.Address = address.String()
		entry.MAC = strings.ToLower(iface.HardwareAddress)
		entry.Source = mergeRouterSource(entry.Source, "interface")
		observed[entry.Address] = entry
	}
}

// mergeRouterLeases parses dnsmasq's standard lease-file records within the managed subnet.
func mergeRouterLeases(observed map[string]SDNRouterObservedAddress, content string, subnet string) {
	var prefix netip.Prefix
	if prefix, _ = netip.ParsePrefix(subnet); !prefix.IsValid() {
		return
	}
	for _, line := range strings.Split(content, "\n") {
		var fields []string = strings.Fields(line)
		if len(fields) < 4 {
			continue
		}
		var address netip.Addr
		if address, _ = netip.ParseAddr(fields[2]); !address.IsValid() || !prefix.Contains(address) {
			continue
		}
		var expires time.Time
		if seconds, parseErr := strconv.ParseInt(fields[0], 10, 64); parseErr == nil && seconds > 0 {
			expires = time.Unix(seconds, 0).UTC()
		}
		var entry SDNRouterObservedAddress = observed[address.String()]
		entry.Address = address.String()
		if address.Is4() {
			entry.MAC = strings.ToLower(fields[1])
		} else {
			if len(fields) >= 5 {
				entry.MAC = macFromDHCPv6DUID(fields[4])
			}
		}
		if fields[3] != "*" {
			entry.Hostname = fields[3]
		}
		entry.Source = mergeRouterSource(entry.Source, "lease")
		if !expires.IsZero() {
			entry.LeaseExpiresAt = &expires
		}
		observed[entry.Address] = entry
	}
}

// macFromDHCPv6DUID extracts the hardware address from an Ethernet DUID-LL lease identity.
func macFromDHCPv6DUID(value string) (mac string) {
	var fields []string = strings.Split(strings.ToLower(value), ":")
	if len(fields) != 10 || fields[0] != "00" || fields[1] != "03" || fields[2] != "00" || fields[3] != "01" {
		return
	}
	mac = strings.Join(fields[4:], ":")
	if !validNetworkMAC(mac) {
		mac = ""
	}
	return
}

// mergeRouterNeighbors parses a fixed ip-neighbor command output and joins matching leases.
func mergeRouterNeighbors(observed map[string]SDNRouterObservedAddress, content string, subnet string) {
	var prefix netip.Prefix
	if prefix, _ = netip.ParsePrefix(subnet); !prefix.IsValid() {
		return
	}
	for _, line := range strings.Split(content, "\n") {
		var fields []string = strings.Fields(line)
		if len(fields) < 1 {
			continue
		}
		var address netip.Addr
		if address, _ = netip.ParseAddr(strings.Trim(fields[0], "[]")); !address.IsValid() || !prefix.Contains(address) {
			continue
		}
		var entry SDNRouterObservedAddress = observed[address.String()]
		var hasMAC bool
		for index, field := range fields {
			if field == "lladdr" && index+1 < len(fields) {
				entry.MAC = strings.ToLower(fields[index+1])
				hasMAC = true
			}
		}
		if !hasMAC && entry.MAC == "" {
			continue
		}
		entry.Address = address.String()
		entry.Source = mergeRouterSource(entry.Source, "neighbor")
		observed[entry.Address] = entry
	}
}

// mergeRouterSource preserves both sources when an address appears in leases and neighbors.
func mergeRouterSource(current string, incoming string) (result string) {
	if current == "" || current == incoming {
		result = incoming
		return
	}
	if strings.Contains(current, incoming) {
		result = current
		return
	}
	result = current + ", " + incoming
	return
}

// sortRouterAddresses sorts observed entries by their IPv4 address.
func sortRouterAddresses(entries []SDNRouterObservedAddress) {
	sort.Slice(entries, func(leftIndex int, rightIndex int) (less bool) {
		var left netip.Addr
		var right netip.Addr
		left, _ = netip.ParseAddr(entries[leftIndex].Address)
		right, _ = netip.ParseAddr(entries[rightIndex].Address)
		less = left.Compare(right) < 0
		return
	})
}
