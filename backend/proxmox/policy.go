package proxmox

import (
	"crypto/sha256"
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
		Limits                   CapacityLimits  `json:"limits"`
		ResourcePools            []string        `json:"resource_pools"`
		Storages                 []string        `json:"storages"`
		Networks                 []PolicyNetwork `json:"networks"`
		AllowIsolatedSDNNetworks bool            `json:"allow_isolated_sdn_networks"`
	}

	// CapacityLimits uses zero to mean that Organesson does not impose that global limit.
	CapacityLimits struct {
		VirtualCPUs        int   `json:"virtual_cpus"`
		MemoryMiB          int64 `json:"memory_mib"`
		StorageGiB         int64 `json:"storage_gib"`
		SnapshotStorageGiB int64 `json:"snapshot_storage_gib"`
	}

	// PolicyNetwork maps an Organesson label to a bridge or Proxmox SDN VNet.
	PolicyNetwork struct {
		Name         string        `json:"name"`
		Kind         string        `json:"kind"`
		PVEName      string        `json:"pve_name"`
		AddressPools []AddressPool `json:"address_pools"`
	}

	// AddressPool is an administrator-declared range and network configuration for allocations.
	AddressPool struct {
		Name             string   `json:"name"`
		Prefix           string   `json:"prefix"`
		AllocationPrefix string   `json:"allocation_prefix,omitempty"`
		Start            string   `json:"start"`
		End              string   `json:"end"`
		Gateway          string   `json:"gateway"`
		DNS              []string `json:"dns"`
	}

	// ResourceInventory contains the read-only resource names discovered from PVE.
	ResourceInventory struct {
		Pools    []string `json:"pools"`
		Storages []string `json:"storages"`
		Bridges  []string `json:"bridges"`
		VNets    []string `json:"vnets"`
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
	if policy.Limits.VirtualCPUs < 0 || policy.Limits.MemoryMiB < 0 || policy.Limits.StorageGiB < 0 || policy.Limits.SnapshotStorageGiB < 0 {
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
		if strings.TrimSpace(network.PVEName) == "" {
			addIssue(fmt.Sprintf("Network %q needs a Proxmox network name.", network.Name))
		} else if inventory != nil {
			available := inventory.Bridges
			if network.Kind == "vnet" {
				available = inventory.VNets
			}
			if !contains(available, network.PVEName) {
				addIssue(fmt.Sprintf("Proxmox network %q for Organesson network %q was not found.", network.PVEName, network.Name))
			}
		}
		for _, pool := range network.AddressPools {
			if err := validateAddressPool(pool); err != nil {
				addIssue(fmt.Sprintf("Network %q address pool %q: %v", network.Name, pool.Name, err))
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
	var start, end netip.Addr
	if start, err = netip.ParseAddr(pool.Start); err != nil {
		err = errors.New("start must be a valid IP address")
		return
	}
	if end, err = netip.ParseAddr(pool.End); err != nil {
		err = errors.New("end must be a valid IP address")
		return
	}
	if !allocationPrefix.Contains(start) || !allocationPrefix.Contains(end) || start.Is4() != end.Is4() || start.Is4() != sourcePrefix.Addr().Is4() || start.Compare(end) > 0 {
		err = errors.New("start and end must be ordered addresses within the allocation prefix and address family")
		return
	}
	for _, address := range append([]string{pool.Gateway}, pool.DNS...) {
		if address == "" {
			continue
		}
		var parsed netip.Addr
		if parsed, err = netip.ParseAddr(address); err != nil || !sourcePrefix.Contains(parsed) || parsed.Is4() != start.Is4() {
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
	var start, end netip.Addr
	if start, err = netip.ParseAddr(pool.Start); err != nil {
		return
	}
	if end, err = netip.ParseAddr(pool.End); err != nil {
		return
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
	for current := start; current.IsValid() && current.Compare(end) <= 0; current = current.Next() {
		if reserved[current] {
			continue
		}
		addresses = append(addresses, current.String())
		if len(addresses) == count {
			return
		}
	}
	addresses = nil
	err = errors.New("address pool does not have enough unallocated addresses")
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
