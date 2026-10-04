package proxmox

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/netip"
	"strings"
)

type (
	// ResourcePolicy defines global capacity limits and the PVE resources available to Organesson.
	ResourcePolicy struct {
		Limits           CapacityLimits   `json:"limits"`
		DeploymentLimits DeploymentLimits `json:"deployment_limits"`
		VMLimits         VMLimits         `json:"vm_limits"`
		ResourcePools    []string         `json:"resource_pools"`
		Storages         []string         `json:"storages"`
		VNetSourceZone   string           `json:"vnet_source_zone"`
		Networks         []PolicyNetwork  `json:"networks"`
	}

	// CapacityLimits uses zero to mean that Organesson does not impose that global limit.
	CapacityLimits struct {
		MaxDeployments     int   `json:"max_deployments"`
		MaxSDNNetworks     int   `json:"max_sdn_networks"`
		VirtualCPUs        int   `json:"virtual_cpus"`
		MemoryMiB          int64 `json:"memory_mib"`
		StorageGiB         int64 `json:"storage_gib"`
		SnapshotStorageGiB int64 `json:"snapshot_storage_gib"`
	}

	// DeploymentLimits define optional aggregate caps for each deployment.
	DeploymentLimits struct {
		MaxResources       int   `json:"max_resources"`
		MaxSDNNetworks     int   `json:"max_sdn_networks"`
		VirtualCPUs        int   `json:"virtual_cpus"`
		MemoryMiB          int64 `json:"memory_mib"`
		StorageGiB         int64 `json:"storage_gib"`
		SnapshotStorageGiB int64 `json:"snapshot_storage_gib"`
	}

	// VMLimits define optional caps for each virtual machine.
	VMLimits struct {
		VirtualCPUs        int   `json:"virtual_cpus"`
		MemoryMiB          int64 `json:"memory_mib"`
		StorageGiB         int64 `json:"storage_gib"`
		SnapshotStorageGiB int64 `json:"snapshot_storage_gib"`
	}

	// PolicyNetwork maps an Organesson label to a bridge or Proxmox SDN VNet.
	PolicyNetwork struct {
		Name         string         `json:"name"`
		Kind         string         `json:"kind"`
		TargetMode   string         `json:"target_mode"`
		PVEName      string         `json:"pve_name"`
		Subnets      []PolicySubnet `json:"subnets"`
		AddressPools []AddressPool  `json:"address_pools"`
	}

	// PolicySubnet describes an Organesson-managed subnet attached to a created VNet.
	PolicySubnet struct {
		Prefix      string `json:"prefix"`
		Gateway     string `json:"gateway"`
		DHCPEnabled bool   `json:"dhcp_enabled"`
	}

	// AddressPool is an administrator-declared subnet and network configuration for allocations.
	AddressPool struct {
		Name             string   `json:"name"`
		Prefix           string   `json:"prefix"`
		AllocationPrefix string   `json:"allocation_prefix,omitempty"`
		Gateway          string   `json:"gateway"`
		DNS              []string `json:"dns"`
	}

	// ResourceInventory contains the read-only resource names discovered from PVE.
	ResourceInventory struct {
		Pools       []string                  `json:"pools"`
		Storages    []string                  `json:"storages"`
		Bridges     []string                  `json:"bridges"`
		VNets       []string                  `json:"vnets"`
		VNetSources []string                  `json:"vnet_sources"`
		VNetSubnets map[string][]PolicySubnet `json:"vnet_subnets,omitempty"`
	}

	// ResourcePolicyValidation identifies local issues and PVE inventory mismatches.
	ResourcePolicyValidation struct {
		Valid  bool     `json:"valid"`
		Issues []string `json:"issues"`
	}
)

// ValidateResourcePolicy checks policy syntax and, when provided, selected resources against PVE inventory.
func ValidateResourcePolicy(policy ResourcePolicy, inventory *ResourceInventory) (result ResourcePolicyValidation) {
	result.Valid = true
	addIssue := func(message string) {
		result.Valid = false
		result.Issues = append(result.Issues, message)
	}
	if policy.Limits.MaxDeployments < 0 || policy.Limits.MaxSDNNetworks < 0 || policy.Limits.VirtualCPUs < 0 || policy.Limits.MemoryMiB < 0 || policy.Limits.StorageGiB < 0 || policy.Limits.SnapshotStorageGiB < 0 ||
		policy.DeploymentLimits.MaxResources < 0 || policy.DeploymentLimits.MaxSDNNetworks < 0 || policy.DeploymentLimits.VirtualCPUs < 0 || policy.DeploymentLimits.MemoryMiB < 0 || policy.DeploymentLimits.StorageGiB < 0 || policy.DeploymentLimits.SnapshotStorageGiB < 0 ||
		policy.VMLimits.VirtualCPUs < 0 || policy.VMLimits.MemoryMiB < 0 || policy.VMLimits.StorageGiB < 0 || policy.VMLimits.SnapshotStorageGiB < 0 {
		addIssue("Capacity limits must be zero (unlimited) or a positive value.")
	}
	if len(policy.ResourcePools) == 0 {
		addIssue("At least one Proxmox resource pool is required.")
	}
	if len(policy.Storages) == 0 {
		addIssue("At least one Proxmox storage is required.")
	}
	checkNames := func(label string, values []string, available []string) {
		seen := make(map[string]bool)
		for _, value := range values {
			value = strings.TrimSpace(value)
			if value == "" || seen[value] {
				addIssue(fmt.Sprintf("%s must contain non-empty unique values.", label))
				continue
			}
			seen[value] = true
			if inventory != nil && !contains(available, value) {
				addIssue(fmt.Sprintf("Selected %s %q was not found in the Proxmox inventory.", strings.TrimSuffix(label, "s"), value))
			}
		}
	}
	if inventory == nil {
		checkNames("resource pools", policy.ResourcePools, nil)
		checkNames("storages", policy.Storages, nil)
	} else {
		checkNames("resource pools", policy.ResourcePools, inventory.Pools)
		checkNames("storages", policy.Storages, inventory.Storages)
		if policy.VNetSourceZone != "" && !contains(inventory.VNetSources, policy.VNetSourceZone) {
			addIssue(fmt.Sprintf("Selected VNet source zone %q was not found or is not a VXLAN zone in the Proxmox inventory.", policy.VNetSourceZone))
		}
	}
	seenNetworks := make(map[string]bool)
	for _, network := range policy.Networks {
		if strings.TrimSpace(network.Name) == "" || seenNetworks[network.Name] {
			addIssue("Network labels must be non-empty and unique.")
		}
		seenNetworks[network.Name] = true
		if network.Kind != "bridge" && network.Kind != "vnet" {
			addIssue(fmt.Sprintf("Network %q must use kind bridge or vnet.", network.Name))
		}
		var targetMode string = network.TargetMode
		if targetMode == "" && strings.TrimSpace(network.PVEName) != "" {
			targetMode = "existing"
		}
		if targetMode != "existing" && targetMode != "create" {
			addIssue(fmt.Sprintf("Network %q must use an existing Proxmox target or create its own VNet.", network.Name))
		}
		if targetMode == "create" {
			if network.Kind != "vnet" {
				addIssue(fmt.Sprintf("Network %q can only create its own SDN VNet.", network.Name))
			}
			if strings.TrimSpace(network.PVEName) != "" {
				addIssue(fmt.Sprintf("Network %q cannot select a Proxmox target when creating its own VNet.", network.Name))
			}
			if strings.TrimSpace(policy.VNetSourceZone) == "" {
				addIssue(fmt.Sprintf("Network %q needs a configured Proxmox VNet source zone.", network.Name))
			}
		} else if strings.TrimSpace(network.PVEName) == "" {
			addIssue(fmt.Sprintf("Network %q needs a Proxmox network target.", network.Name))
		} else if inventory != nil {
			available := inventory.Bridges
			if network.Kind == "vnet" {
				available = inventory.VNets
			}
			if !contains(available, network.PVEName) {
				addIssue(fmt.Sprintf("Proxmox network %q for Organesson network %q was not found.", network.PVEName, network.Name))
			}
		}
		var configuredSubnets []PolicySubnet = network.Subnets
		if network.Kind == "vnet" && targetMode == "existing" && inventory != nil {
			configuredSubnets = inventory.VNetSubnets[network.PVEName]
		}
		var subnets []netip.Prefix
		for _, configuredSubnet := range configuredSubnets {
			var subnet netip.Prefix
			var parseErr error
			if subnet, parseErr = netip.ParsePrefix(configuredSubnet.Prefix); parseErr != nil || subnet != subnet.Masked() || subnet.Addr().Is6() {
				addIssue(fmt.Sprintf("Network %q subnet %q must be a canonical IPv4 CIDR.", network.Name, configuredSubnet.Prefix))
				continue
			}
			var gateway netip.Addr
			if configuredSubnet.Gateway == "" && targetMode == "create" {
				addIssue(fmt.Sprintf("Network %q subnet %q needs an IPv4 gateway inside that subnet.", network.Name, configuredSubnet.Prefix))
			} else if configuredSubnet.Gateway != "" {
				if gateway, parseErr = netip.ParseAddr(configuredSubnet.Gateway); parseErr != nil || gateway.Is6() || !subnet.Contains(gateway) {
					addIssue(fmt.Sprintf("Network %q subnet %q needs an IPv4 gateway inside that subnet.", network.Name, configuredSubnet.Prefix))
				}
			}
			subnets = append(subnets, subnet)
		}
		if targetMode == "create" && len(subnets) == 0 {
			addIssue(fmt.Sprintf("Network %q must define at least one subnet when creating its own VNet.", network.Name))
		}
		if network.Kind == "vnet" && targetMode == "existing" && len(network.AddressPools) > 0 && len(subnets) == 0 {
			addIssue(fmt.Sprintf("Network %q has address pools but its Proxmox VNet has no configured subnets to validate them against.", network.Name))
		}
		for _, pool := range network.AddressPools {
			if err := validateAddressPool(pool); err != nil {
				addIssue(fmt.Sprintf("Network %q address pool %q: %v", network.Name, pool.Name, err))
				continue
			}
			if network.Kind == "vnet" {
				var poolPrefix netip.Prefix
				poolPrefix, _ = netip.ParsePrefix(pool.Prefix)
				var contained bool
				for _, subnet := range subnets {
					if subnet.Addr().Is4() == poolPrefix.Addr().Is4() && subnet.Bits() <= poolPrefix.Bits() && subnet.Contains(poolPrefix.Masked().Addr()) {
						contained = true
						break
					}
				}
				if !contained {
					addIssue(fmt.Sprintf("Network %q address pool %q must fit inside one of its configured VNet subnets.", network.Name, pool.Name))
				}
			}
		}
	}
	return
}

func validateAddressPool(pool AddressPool) (err error) {
	if strings.TrimSpace(pool.Name) == "" {
		err = errors.New("a name is required")
		return
	}
	var sourcePrefix netip.Prefix
	if sourcePrefix, err = netip.ParsePrefix(pool.Prefix); err != nil {
		err = errors.New("prefix must be a valid IPv4 or IPv6 CIDR")
		return
	}
	var allocationPrefix netip.Prefix = sourcePrefix
	if pool.AllocationPrefix != "" {
		if allocationPrefix, err = netip.ParsePrefix(pool.AllocationPrefix); err != nil {
			err = errors.New("allocation_prefix must be a valid IPv4 or IPv6 CIDR")
			return
		}
	}
	if allocationPrefix.Addr().Is4() != sourcePrefix.Addr().Is4() || allocationPrefix.Bits() < sourcePrefix.Bits() || !sourcePrefix.Contains(allocationPrefix.Masked().Addr()) {
		err = errors.New("allocation_prefix must be a subnet within the source network prefix")
		return
	}
	if allocationPrefix.Addr().Is4() && allocationPrefix.Bits() > 30 || allocationPrefix.Addr().Is6() && allocationPrefix.Bits() > 127 {
		err = errors.New("allocation subnet must contain at least one usable address")
		return
	}
	for _, address := range append([]string{pool.Gateway}, pool.DNS...) {
		if address == "" {
			continue
		}
		var parsed netip.Addr
		if parsed, err = netip.ParseAddr(address); err != nil || !sourcePrefix.Contains(parsed) || parsed.Is4() != sourcePrefix.Addr().Is4() {
			err = errors.New("gateway and DNS addresses must match the source network family and be within its prefix")
			return
		}
	}
	return
}

// AllocateAddresses returns the first available addresses in an administrator-defined pool.
func AllocateAddresses(pool AddressPool, count int, allocated []string) (addresses []string, err error) {
	if err = validateAddressPool(pool); err != nil {
		return
	}
	if count < 1 {
		err = errors.New("address count must be positive")
		return
	}
	var allocationPrefix netip.Prefix
	if allocationPrefix, err = addressAllocationPrefix(pool); err != nil {
		return
	}
	var current netip.Addr = allocationPrefix.Masked().Addr().Next()
	var lastIPv4 netip.Addr
	if allocationPrefix.Addr().Is4() {
		if allocationPrefix.Bits() > 30 {
			err = errors.New("IPv4 allocation subnet must have at least two usable addresses")
			return
		}
		var network [4]byte = allocationPrefix.Masked().Addr().As4()
		var networkNumber uint32 = binary.BigEndian.Uint32(network[:])
		var hostBits uint = uint(32 - allocationPrefix.Bits())
		var hostMask uint32 = uint32(1<<hostBits) - 1
		var lastAddress uint32 = (networkNumber | hostMask) - 1
		lastIPv4 = netip.AddrFrom4([4]byte{byte(lastAddress >> 24), byte(lastAddress >> 16), byte(lastAddress >> 8), byte(lastAddress)})
	}
	var reserved map[netip.Addr]bool = make(map[netip.Addr]bool, len(allocated)+len(pool.DNS)+1)
	for _, value := range append(append([]string{pool.Gateway}, pool.DNS...), allocated...) {
		if value == "" {
			continue
		}
		var address netip.Addr
		if address, err = netip.ParseAddr(value); err != nil {
			return
		}
		reserved[address] = true
	}
	for current.IsValid() && allocationPrefix.Contains(current) && (!lastIPv4.IsValid() || current.Compare(lastIPv4) <= 0) {
		var address netip.Addr = current
		current = current.Next()
		if reserved[address] {
			continue
		}
		addresses = append(addresses, address.String())
		if len(addresses) == count {
			return
		}
	}
	addresses = nil
	err = errors.New("address pool does not have enough unallocated addresses")
	return
}

func addressAllocationPrefix(pool AddressPool) (prefix netip.Prefix, err error) {
	var value string = pool.AllocationPrefix
	if value == "" {
		value = pool.Prefix
	}
	if prefix, err = netip.ParsePrefix(value); err != nil {
		err = errors.New("allocation subnet must be a valid IPv4 or IPv6 CIDR")
	}
	return
}

// ResourcePolicyHash gives validation a stable binding to the exact serialized configuration.
func ResourcePolicyHash(policy ResourcePolicy) (hash string, err error) {
	var encoded []byte
	if encoded, err = json.Marshal(policy); err != nil {
		return
	}
	var digest [32]byte = sha256.Sum256(encoded)
	hash = hex.EncodeToString(digest[:])
	return
}

func contains(values []string, selected string) (found bool) {
	for _, value := range values {
		if value == selected {
			found = true
			return
		}
	}
	return
}
