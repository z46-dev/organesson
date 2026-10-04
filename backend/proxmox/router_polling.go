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
	var expected SDNNetworkPlacement = namesForSDNNetwork(request.OperationKey, request.VNetSourceZone)
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
	var observed map[string]SDNRouterObservedAddress = make(map[string]SDNRouterObservedAddress)
	var leaseFile *pve.AgentFileRead
	if leaseFile, err = vm.AgentFileRead(ctx, routerLeaseFile); err == nil && leaseFile != nil && !bool(leaseFile.Truncated) {
		mergeRouterLeases(observed, leaseFile.Content, request.Subnet)
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
	for _, entry := range observed {
		result.ObservedAddresses = append(result.ObservedAddresses, entry)
	}
	sortRouterAddresses(result.ObservedAddresses)
	result.State = "available"
	result.LastPolledAt = time.Now().UTC()
	return
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
		entry.MAC = strings.ToLower(fields[1])
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
		entry.Address = address.String()
		for index, field := range fields {
			if field == "lladdr" && index+1 < len(fields) {
				entry.MAC = strings.ToLower(fields[index+1])
			}
		}
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
