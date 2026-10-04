package proxmox

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"net/netip"
	"strings"

	pve "github.com/luthermonson/go-proxmox"
	"github.com/z46-dev/organesson/backend/config"
)

const maxVXLANVNI uint32 = 16777215

type (
	// SDNNetworkRequest describes an isolated Proxmox SDN VNet and optional subnet.
	SDNNetworkRequest struct {
		Name           string `json:"name"`
		Mode           string `json:"mode"`
		Subnet         string `json:"subnet,omitempty"`
		Gateway        string `json:"gateway,omitempty"`
		DHCPEnabled    bool   `json:"dhcp_enabled"`
		EgressPolicy   string `json:"egress_policy"`
		OperationKey   string `json:"operation_key"`
		VNetSourceZone string `json:"vnet_source_zone,omitempty"`
	}

	// SDNNetworkPlacement identifies the Organesson-owned PVE SDN configuration.
	SDNNetworkPlacement struct {
		Zone string `json:"zone"`
		VNet string `json:"vnet"`
		Tag  uint32 `json:"tag,omitempty"`
	}

	apiSDNNetworkDriver struct {
		settings config.ProxmoxConfiguration
	}

	// SDNIPAMEntry is a single address assignment reported by a Proxmox IPAM backend.
	SDNIPAMEntry struct {
		IP       string `json:"ip"`
		MAC      string `json:"mac,omitempty"`
		Hostname string `json:"hostname,omitempty"`
		Subnet   string `json:"subnet,omitempty"`
		VMID     string `json:"vmid,omitempty"`
	}
)

// CreateSDNNetwork provisions an isolated VNet and its optional subnet in Proxmox.
func (service *Service) CreateSDNNetwork(ctx context.Context, request SDNNetworkRequest) (placement SDNNetworkPlacement, err error) {
	if service == nil || service.sdnNetworkDriver == nil || !service.Configured() {
		err = ErrNotConfigured
		return
	}
	service.lifecycleLock.Lock()
	defer service.lifecycleLock.Unlock()
	placement, err = service.sdnNetworkDriver.Create(ctx, request)
	return
}

// ReadSDNNetwork verifies the managed network configuration in the current PVE inventory.
func (service *Service) ReadSDNNetwork(ctx context.Context, request SDNNetworkRequest, placement SDNNetworkPlacement) (err error) {
	if service == nil || service.sdnNetworkDriver == nil || !service.Configured() {
		err = ErrNotConfigured
		return
	}
	service.lifecycleLock.Lock()
	defer service.lifecycleLock.Unlock()
	err = service.sdnNetworkDriver.Read(ctx, request, placement)
	return
}

// ReadSDNNetworkIPAM returns address records when the configured driver can read IPAM.
func (service *Service) ReadSDNNetworkIPAM(ctx context.Context, request SDNNetworkRequest, placement SDNNetworkPlacement) (entries []SDNIPAMEntry, state string, err error) {
	if service == nil || service.sdnNetworkDriver == nil || !service.Configured() {
		state = "unavailable"
		err = ErrNotConfigured
		return
	}
	var reader SDNNetworkIPAMReader
	var supported bool
	if reader, supported = service.sdnNetworkDriver.(SDNNetworkIPAMReader); !supported {
		state = "unavailable"
		return
	}
	service.lifecycleLock.Lock()
	defer service.lifecycleLock.Unlock()
	entries, state, err = reader.ReadIPAM(ctx, request, placement)
	return
}

// DeleteSDNNetwork removes a network only when its VNet ownership marker matches.
func (service *Service) DeleteSDNNetwork(ctx context.Context, vnet string, operationKey string, sourceZone string) (err error) {
	if service == nil || service.sdnNetworkDriver == nil || !service.Configured() {
		err = ErrNotConfigured
		return
	}
	service.lifecycleLock.Lock()
	defer service.lifecycleLock.Unlock()
	err = service.sdnNetworkDriver.Delete(ctx, vnet, operationKey, sourceZone)
	return
}

// Create idempotently creates an Organesson VNet and optional subnet, using a shared source zone when configured.
func (driver *apiSDNNetworkDriver) Create(ctx context.Context, request SDNNetworkRequest) (placement SDNNetworkPlacement, err error) {
	if err = validateSDNNetworkRequest(request); err != nil {
		return
	}
	placement = namesForSDNNetwork(request.OperationKey, request.VNetSourceZone)
	var marker string = "organesson:" + request.OperationKey
	var client *pve.Client
	if client, err = newAPIClient(driver.settings); err != nil {
		return
	}
	var cluster *pve.Cluster
	if cluster, err = client.Cluster(ctx); err != nil {
		return
	}
	var zones []*pve.SDNZone
	if zones, err = cluster.SDNZones(ctx); err != nil {
		return
	}
	var existingZone *pve.SDNZone
	for _, zone := range zones {
		if zone != nil && zone.Name == placement.Zone {
			existingZone = zone
			break
		}
	}
	if request.VNetSourceZone != "" {
		if existingZone == nil || existingZone.Type != "vxlan" {
			err = fmt.Errorf("configured Proxmox VNet source zone %q is missing or is not VXLAN", placement.Zone)
			return
		}
	} else if existingZone != nil && (existingZone.Type != "simple" || existingZone.IPAM != "pve") {
		err = fmt.Errorf("Proxmox SDN zone %q exists but is not the expected Simple zone with PVE IPAM", placement.Zone)
		return
	}
	var vnets []*pve.VNet
	if vnets, err = cluster.SDNVNets(ctx); err != nil {
		return
	}
	var existingVNet *pve.VNet
	for _, vnet := range vnets {
		if vnet != nil && vnet.Name == placement.VNet {
			existingVNet = vnet
			break
		}
	}
	if existingVNet != nil && (existingVNet.Alias != marker || existingVNet.Zone != placement.Zone) {
		err = fmt.Errorf("Proxmox VNet %q exists but is not owned by this Organesson resource", placement.VNet)
		return
	}
	if request.VNetSourceZone != "" {
		if existingVNet != nil {
			placement.Tag = existingVNet.Tag
			if placement.Tag == 0 || placement.Tag > maxVXLANVNI {
				err = fmt.Errorf("existing Organesson VNet %q has no valid VXLAN VNI", placement.VNet)
				return
			}
		} else if placement.Tag, err = allocateVXLANVNI(request.OperationKey, placement.Zone, vnets); err != nil {
			return
		}
	}
	var changed bool
	if request.VNetSourceZone == "" && existingZone == nil {
		var zoneOptions *pve.SDNZoneOptions = &pve.SDNZoneOptions{Name: placement.Zone, Type: "simple", IPAM: "pve"}
		if request.DHCPEnabled {
			zoneOptions.DHCP = "dnsmasq"
		}
		if err = cluster.NewSDNZone(ctx, zoneOptions); err != nil {
			return
		}
		changed = true
	} else if request.VNetSourceZone == "" {
		var expectedDHCP string
		if request.DHCPEnabled {
			expectedDHCP = "dnsmasq"
		}
		if existingZone.DHCP != expectedDHCP {
			err = fmt.Errorf("Proxmox SDN zone %q exists with a different DHCP configuration", placement.Zone)
			return
		}
	}
	if existingVNet == nil {
		if err = cluster.NewSDNVNet(ctx, &pve.VNetOptions{Name: placement.VNet, Zone: placement.Zone, Alias: marker, Tag: placement.Tag, Type: "vnet"}); err != nil {
			return
		}
		changed = true
	}
	if request.Mode == "managed" {
		var vnet *pve.VNet
		if vnet, err = cluster.SDNVNet(ctx, placement.VNet); err != nil {
			return
		}
		var subnets []*pve.VNetSubnet
		if subnets, err = vnet.Subnets(ctx); err != nil {
			return
		}
		var subnetExists bool
		for _, subnet := range subnets {
			if subnet != nil && subnet.CIDR == request.Subnet {
				subnetExists = true
				if subnet.Gateway != request.Gateway {
					err = errors.New("existing Proxmox SDN subnet has a different gateway")
					return
				}
			}
		}
		if !subnetExists {
			if err = vnet.NewSubnet(ctx, &pve.SDNSubnetOptions{Subnet: request.Subnet, Gateway: request.Gateway}); err != nil {
				return
			}
			changed = true
		}
	}
	if changed {
		var task *pve.Task
		if task, err = cluster.SDNApply(ctx); err != nil {
			return
		}
		err = waitTask(ctx, client, task)
	}
	return
}

// Read checks ownership markers and ensures the expected isolated topology still exists.
func (driver *apiSDNNetworkDriver) Read(ctx context.Context, request SDNNetworkRequest, placement SDNNetworkPlacement) (err error) {
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
	var zones []*pve.SDNZone
	if zones, err = cluster.SDNZones(ctx); err != nil {
		return
	}
	var zoneFound bool
	for _, zone := range zones {
		if zone == nil || zone.Name != expected.Zone {
			continue
		}
		if request.VNetSourceZone != "" {
			if zone.Type != "vxlan" {
				err = errors.New("configured Proxmox VNet source zone is no longer a VXLAN zone")
				return
			}
			zoneFound = true
			break
		}
		if zone.Type != "simple" || zone.IPAM != "pve" {
			err = errors.New("Proxmox SDN zone no longer matches the Organesson isolated-network configuration")
			return
		}
		var expectedDHCP string
		if request.DHCPEnabled {
			expectedDHCP = "dnsmasq"
		}
		if zone.DHCP != expectedDHCP {
			err = errors.New("Proxmox SDN zone DHCP configuration has drifted")
			return
		}
		zoneFound = true
		break
	}
	if !zoneFound {
		err = ErrSDNNetworkNotFound
		return
	}
	var vnets []*pve.VNet
	if vnets, err = cluster.SDNVNets(ctx); err != nil {
		return
	}
	var vnetFound bool
	for _, vnet := range vnets {
		if vnet == nil || vnet.Name != expected.VNet {
			continue
		}
		if vnet.Alias != "organesson:"+request.OperationKey || vnet.Zone != expected.Zone || request.VNetSourceZone != "" && (vnet.Tag == 0 || placement.Tag != 0 && vnet.Tag != placement.Tag) {
			err = errors.New("Proxmox VNet ownership marker or zone does not match the Organesson resource")
			return
		}
		vnetFound = true
		break
	}
	if !vnetFound {
		err = ErrSDNNetworkNotFound
		return
	}
	var vnet *pve.VNet
	if vnet, err = cluster.SDNVNet(ctx, expected.VNet); err != nil {
		return
	}
	var subnets []*pve.VNetSubnet
	if subnets, err = vnet.Subnets(ctx); err != nil {
		return
	}
	if request.Mode == "managed" {
		var subnetFound bool
		for _, subnet := range subnets {
			if subnet != nil && subnet.CIDR == request.Subnet {
				subnetFound = true
				if subnet.Gateway != request.Gateway {
					err = errors.New("Proxmox SDN gateway no longer matches the Organesson network")
					return
				}
			}
		}
		if !subnetFound {
			err = ErrSDNNetworkNotFound
			return
		}
	} else if len(subnets) != 0 {
		err = errors.New("Proxmox unmanaged layer-2 VNet unexpectedly has subnet configuration")
	}
	return
}

// ReadIPAM lists only address assignments attached to the expected Organesson VNet.
func (driver *apiSDNNetworkDriver) ReadIPAM(ctx context.Context, request SDNNetworkRequest, placement SDNNetworkPlacement) (entries []SDNIPAMEntry, state string, err error) {
	if err = validateSDNNetworkRequest(request); err != nil {
		state = "unavailable"
		return
	}
	var expected SDNNetworkPlacement = namesForSDNNetwork(request.OperationKey, request.VNetSourceZone)
	if placement.Zone != expected.Zone || placement.VNet != expected.VNet {
		state = "unavailable"
		err = errors.New("stored Proxmox SDN placement does not match its operation key")
		return
	}
	var client *pve.Client
	if client, err = newAPIClient(driver.settings); err != nil {
		state = "unavailable"
		return
	}
	var cluster *pve.Cluster
	if cluster, err = client.Cluster(ctx); err != nil {
		state = "unavailable"
		return
	}
	var zones []*pve.SDNZone
	if zones, err = cluster.SDNZones(ctx); err != nil {
		state = "unavailable"
		return
	}
	var ipam string
	for _, zone := range zones {
		if zone != nil && zone.Name == expected.Zone {
			ipam = zone.IPAM
			break
		}
	}
	if ipam == "" {
		state = "unconfigured"
		return
	}
	var records []map[string]any
	if records, err = cluster.SDNIPAM(ipam).Status(ctx); err != nil {
		state = "unavailable"
		return
	}
	state = "available"
	for _, record := range records {
		var recordVNet string = ipamString(record["vnet"])
		var recordZone string = ipamString(record["zone"])
		if recordVNet != expected.VNet || (recordZone != "" && recordZone != expected.Zone) {
			continue
		}
		var entry SDNIPAMEntry = SDNIPAMEntry{
			IP:       ipamString(record["ip"]),
			MAC:      ipamString(record["mac"]),
			Hostname: ipamString(record["hostname"]),
			Subnet:   ipamString(record["subnet"]),
			VMID:     ipamString(record["vmid"]),
		}
		if entry.IP != "" {
			entries = append(entries, entry)
		}
	}
	return
}

// ipamString converts optional API fields to displayable strings.
func ipamString(value any) (result string) {
	if value != nil {
		result = fmt.Sprint(value)
	}
	return
}

// Delete removes the marked VNet and its subnet, and removes a dedicated Simple zone only when Organesson created it.
func (driver *apiSDNNetworkDriver) Delete(ctx context.Context, vnetName string, operationKey string, sourceZone string) (err error) {
	var expected SDNNetworkPlacement = namesForSDNNetwork(operationKey, sourceZone)
	if vnetName != expected.VNet || operationKey == "" {
		err = errors.New("Proxmox VNet identifier does not match its Organesson operation key")
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
	var vnets []*pve.VNet
	if vnets, err = cluster.SDNVNets(ctx); err != nil {
		return
	}
	var owned bool
	for _, vnet := range vnets {
		if vnet != nil && vnet.Name == expected.VNet {
			if vnet.Alias != "organesson:"+operationKey || vnet.Zone != expected.Zone {
				err = errors.New("refusing to delete a Proxmox VNet not owned by this Organesson resource")
				return
			}
			owned = true
			break
		}
	}
	if owned {
		var vnet *pve.VNet
		if vnet, err = cluster.SDNVNet(ctx, expected.VNet); err != nil {
			return
		}
		var subnets []*pve.VNetSubnet
		if subnets, err = vnet.Subnets(ctx); err != nil {
			return
		}
		for _, subnet := range subnets {
			if subnet != nil {
				if err = vnet.Subnet(subnet.ID).Delete(ctx); err != nil {
					return
				}
			}
		}
		if err = cluster.DeleteSDNVNet(ctx, expected.VNet); err != nil {
			return
		}
	}
	if sourceZone != "" {
		if !owned {
			return
		}
		var task *pve.Task
		if task, err = cluster.SDNApply(ctx); err != nil {
			return
		}
		err = waitTask(ctx, client, task)
		return
	}
	var zones []*pve.SDNZone
	if zones, err = cluster.SDNZones(ctx); err != nil {
		return
	}
	var zoneExists bool
	for _, zone := range zones {
		if zone != nil && zone.Name == expected.Zone {
			if zone.Type != "simple" || zone.IPAM != "pve" {
				err = errors.New("refusing to delete a Proxmox SDN zone with unexpected configuration")
				return
			}
			zoneExists = true
			break
		}
	}
	if !zoneExists && !owned {
		return
	}
	if zoneExists {
		if err = cluster.DeleteSDNZone(ctx, expected.Zone); err != nil {
			return
		}
	}
	if !owned && !zoneExists {
		return
	}
	var task *pve.Task
	if task, err = cluster.SDNApply(ctx); err != nil {
		return
	}
	err = waitTask(ctx, client, task)
	return
}

// validateSDNNetworkRequest restricts managed SDN to isolated, internally consistent networks.
func validateSDNNetworkRequest(request SDNNetworkRequest) (err error) {
	if strings.TrimSpace(request.Name) == "" || strings.TrimSpace(request.OperationKey) == "" || request.EgressPolicy != "isolated" {
		err = errors.New("isolated SDN network requires a name, operation key, and isolated egress policy")
		return
	}
	switch request.Mode {
	case "unmanaged-layer-2":
		if request.Subnet != "" || request.Gateway != "" || request.DHCPEnabled {
			err = errors.New("unmanaged layer-2 network cannot configure a subnet, gateway, or DHCP")
		}
	case "managed":
		var prefix netip.Prefix
		if prefix, err = netip.ParsePrefix(request.Subnet); err != nil || prefix.Addr().Is6() || prefix != prefix.Masked() {
			err = errors.New("managed SDN network requires a canonical IPv4 subnet")
			return
		}
		var gateway netip.Addr
		if gateway, err = netip.ParseAddr(request.Gateway); err != nil || !prefix.Contains(gateway) || gateway.Is6() {
			err = errors.New("managed SDN network gateway must be inside its IPv4 subnet")
		}
	default:
		err = fmt.Errorf("unsupported Organesson network mode %q", request.Mode)
	}
	return
}

// ValidateSDNNetworkRequest validates a requested isolated network before reserving ownership.
func ValidateSDNNetworkRequest(request SDNNetworkRequest) (err error) {
	err = validateSDNNetworkRequest(request)
	return
}

// namesForSDNNetwork generates short, deterministic PVE identifiers in Organesson's namespace.
func namesForSDNNetwork(operationKey string, sourceZones ...string) (placement SDNNetworkPlacement) {
	var digest [sha256.Size]byte = sha256.Sum256([]byte(operationKey))
	var suffix string = hex.EncodeToString(digest[:])[:6]
	placement = SDNNetworkPlacement{Zone: "oz" + suffix, VNet: "on" + suffix}
	if len(sourceZones) > 0 && sourceZones[0] != "" {
		placement.Zone = sourceZones[0]
	}
	return
}

// allocateVXLANVNI finds a deterministic free VNI among VNets in the selected VXLAN zone.
func allocateVXLANVNI(operationKey string, zone string, vnets []*pve.VNet) (tag uint32, err error) {
	var digest [sha256.Size]byte = sha256.Sum256([]byte("organesson-vni:" + operationKey))
	var candidate uint32 = binary.BigEndian.Uint32(digest[:4])%maxVXLANVNI + 1
	var used = make(map[uint32]bool)
	for _, vnet := range vnets {
		if vnet != nil && vnet.Zone == zone && vnet.Tag > 0 && vnet.Tag <= maxVXLANVNI {
			used[vnet.Tag] = true
		}
	}
	for range maxVXLANVNI {
		if !used[candidate] {
			tag = candidate
			return
		}
		candidate = candidate%maxVXLANVNI + 1
	}
	err = fmt.Errorf("no VXLAN VNIs remain available in zone %q", zone)
	return
}

// SDNNetworkNames returns the deterministic Proxmox identifiers reserved for an operation key.
func SDNNetworkNames(operationKey string, sourceZones ...string) (placement SDNNetworkPlacement) {
	placement = namesForSDNNetwork(operationKey, sourceZones...)
	return
}
