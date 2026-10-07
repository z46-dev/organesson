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
	"time"

	pve "github.com/luthermonson/go-proxmox"
	"github.com/z46-dev/organesson/backend/config"
)

const maxVXLANVNI uint32 = 16777215

type (
	// SDNNetworkRequest describes an isolated Proxmox SDN VNet and optional subnet.
	SDNNetworkRequest struct {
		Name            string                   `json:"name"`
		Mode            string                   `json:"mode"`
		Subnet          string                   `json:"subnet,omitempty"`
		Gateway         string                   `json:"gateway,omitempty"`
		IPv6Subnet      string                   `json:"ipv6_subnet,omitempty"`
		IPv6Gateway     string                   `json:"ipv6_gateway,omitempty"`
		DHCPEnabled     bool                     `json:"dhcp_enabled"`
		IPv6DHCPEnabled bool                     `json:"ipv6_dhcp_enabled,omitempty"`
		RouterVMID      int                      `json:"router_vmid,omitempty"`
		EgressPolicy    string                   `json:"egress_policy"`
		OperationKey    string                   `json:"operation_key"`
		VNetSourceZone  string                   `json:"vnet_source_zone,omitempty"`
		ExternalVLAN    *SDNExternalVLANExposure `json:"external_vlan,omitempty"`
	}

	// SDNExternalVLANExposure places this VNet on an authorized physical VLAN fabric.
	SDNExternalVLANExposure struct {
		TrunkNode   string   `json:"trunk_node"`
		TrunkBridge string   `json:"trunk_bridge"`
		VLANID      int      `json:"vlan_id"`
		Nodes       []string `json:"nodes,omitempty"` // Internal Proxmox zone placement; always the single selected trunk node.
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

	// SDNRouterObservedAddress describes a leased or recently-neighbored guest address.
	SDNRouterObservedAddress struct {
		Address            string     `json:"address"`
		MAC                string     `json:"mac,omitempty"`
		Hostname           string     `json:"hostname,omitempty"`
		Source             string     `json:"source,omitempty"`
		LeaseExpiresAt     *time.Time `json:"lease_expires_at,omitempty"`
		VirtualMachineID   int        `json:"virtual_machine_id,omitempty"`
		VirtualMachineName string     `json:"virtual_machine_name,omitempty"`
	}

	// SDNRouterPollingResult contains read-only observations collected through QEMU Guest Agent.
	SDNRouterPollingResult struct {
		State             string                     `json:"state"`
		RouterVMID        int                        `json:"router_vmid,omitempty"`
		LastPolledAt      time.Time                  `json:"last_polled_at,omitempty"`
		DHCPRangeStart    string                     `json:"dhcp_range_start,omitempty"`
		DHCPRangeEnd      string                     `json:"dhcp_range_end,omitempty"`
		DHCPv6RangeStart  string                     `json:"dhcpv6_range_start,omitempty"`
		DHCPv6RangeEnd    string                     `json:"dhcpv6_range_end,omitempty"`
		Egress            *SDNRouterEgress           `json:"egress,omitempty"`
		ObservedAddresses []SDNRouterObservedAddress `json:"observed_addresses"`
	}

	// SDNRouterEgress describes the router's configured NAT uplink as observed in the guest.
	SDNRouterEgress struct {
		Interface     string   `json:"interface"`
		MAC           string   `json:"mac,omitempty"`
		Bridge        string   `json:"bridge,omitempty"`
		Addresses     []string `json:"addresses,omitempty"`
		IPv6Addresses []string `json:"ipv6_addresses,omitempty"`
		Gateway       string   `json:"gateway,omitempty"`
		IPv6Gateway   string   `json:"ipv6_gateway,omitempty"`
	}

	// SDNRouterDHCPReservation binds an allocated IPv4/IPv6 address to a VM NIC identity.
	SDNRouterDHCPReservation struct {
		MAC     string `json:"mac"`
		DUID    string `json:"duid,omitempty"`
		Address string `json:"address"`
	}

	SDNRouterPoller interface {
		PollRouter(context.Context, SDNNetworkRequest, SDNNetworkPlacement) (SDNRouterPollingResult, error)
	}
)

// CreateSDNNetwork provisions an isolated Proxmox SDN VNet.
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

// PollSDNRouter returns cached, read-only router observations for a managed network.
func (service *Service) PollSDNRouter(ctx context.Context, request SDNNetworkRequest, placement SDNNetworkPlacement) (result SDNRouterPollingResult, err error) {
	if request.RouterVMID == 0 {
		result = SDNRouterPollingResult{State: "not_configured", ObservedAddresses: []SDNRouterObservedAddress{}}
		return
	}
	if service == nil || service.sdnNetworkDriver == nil || !service.Configured() {
		result = SDNRouterPollingResult{State: "unavailable", RouterVMID: request.RouterVMID, ObservedAddresses: []SDNRouterObservedAddress{}}
		err = ErrNotConfigured
		return
	}
	var cacheKey string = fmt.Sprintf("%s:%d", request.OperationKey, request.RouterVMID)
	service.routerPollingLock.Lock()
	if service.routerPollingCache == nil {
		service.routerPollingCache = make(map[string]cachedRouterPollingResult)
	}
	if cached, supported := service.routerPollingCache[cacheKey]; supported && time.Since(cached.cachedAt) < cached.cacheTTL {
		service.routerPollingLock.Unlock()
		result = cached.result
		return
	}
	service.routerPollingLock.Unlock()
	var poller SDNRouterPoller
	var supported bool
	if poller, supported = service.sdnNetworkDriver.(SDNRouterPoller); !supported {
		result = SDNRouterPollingResult{State: "unavailable", RouterVMID: request.RouterVMID, ObservedAddresses: []SDNRouterObservedAddress{}}
		return
	}
	result, err = poller.PollRouter(ctx, request, placement)
	if result.LastPolledAt.IsZero() {
		result.LastPolledAt = time.Now().UTC()
	}
	service.routerPollingLock.Lock()
	var cacheTTL time.Duration = 30 * time.Second
	if result.State != "available" {
		cacheTTL = 5 * time.Second
	}
	service.routerPollingCache[cacheKey] = cachedRouterPollingResult{result: result, cachedAt: time.Now(), cacheTTL: cacheTTL}
	service.routerPollingLock.Unlock()
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
func (service *Service) DeleteSDNNetwork(ctx context.Context, request SDNNetworkRequest, placement SDNNetworkPlacement) (err error) {
	if service == nil || service.sdnNetworkDriver == nil || !service.Configured() {
		err = ErrNotConfigured
		return
	}
	service.lifecycleLock.Lock()
	defer service.lifecycleLock.Unlock()
	err = service.sdnNetworkDriver.Delete(ctx, request, placement)
	return
}

// Create idempotently creates an Organesson VNet and optional subnet, using a shared source zone when configured.
func (driver *apiSDNNetworkDriver) Create(ctx context.Context, request SDNNetworkRequest) (placement SDNNetworkPlacement, err error) {
	if err = validateSDNNetworkRequest(request); err != nil {
		return
	}
	placement = placementForSDNNetwork(request)
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
	if request.ExternalVLAN != nil {
		if existingZone != nil {
			if existingZone.Type != "vlan" || strings.Join([]string(existingZone.Nodes), ",") != strings.Join(request.ExternalVLAN.Nodes, ",") {
				err = fmt.Errorf("Proxmox VLAN zone %q exists with different trunk settings", placement.Zone)
				return
			}
			if err = verifyVLANZoneBridge(ctx, client, placement.Zone, request.ExternalVLAN.TrunkBridge); err != nil {
				return
			}
		}
	} else if request.VNetSourceZone != "" {
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
	if request.ExternalVLAN != nil {
		placement.Tag = uint32(request.ExternalVLAN.VLANID)
		if existingVNet != nil && existingVNet.Tag != placement.Tag {
			err = fmt.Errorf("Proxmox VNet %q exists with a different VLAN tag", placement.VNet)
			return
		}
	} else if request.VNetSourceZone != "" {
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
	if request.ExternalVLAN != nil && existingZone == nil {
		if err = cluster.NewSDNZone(ctx, &pve.SDNZoneOptions{
			Name: placement.Zone, Type: "vlan", Bridge: request.ExternalVLAN.TrunkBridge,
			Nodes: strings.Join(request.ExternalVLAN.Nodes, ","),
		}); err != nil {
			return
		}
		changed = true
	} else if request.ExternalVLAN == nil && request.VNetSourceZone == "" && existingZone == nil {
		var zoneOptions *pve.SDNZoneOptions = &pve.SDNZoneOptions{Name: placement.Zone, Type: "simple", IPAM: "pve"}
		if request.DHCPEnabled {
			zoneOptions.DHCP = "dnsmasq"
		}
		if err = cluster.NewSDNZone(ctx, zoneOptions); err != nil {
			return
		}
		changed = true
	} else if request.ExternalVLAN == nil && request.VNetSourceZone == "" {
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
	if request.Mode == "managed" && existingVNet != nil {
		var vnet *pve.VNet
		if vnet, err = cluster.SDNVNet(ctx, placement.VNet); err != nil {
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
				changed = true
			}
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
	var zones []*pve.SDNZone
	if zones, err = cluster.SDNZones(ctx); err != nil {
		return
	}
	var zoneFound bool
	for _, zone := range zones {
		if zone == nil || zone.Name != expected.Zone {
			continue
		}
		if request.ExternalVLAN != nil {
			if zone.Type != "vlan" || strings.Join([]string(zone.Nodes), ",") != strings.Join(request.ExternalVLAN.Nodes, ",") {
				err = errors.New("Proxmox VLAN zone no longer matches the authorized external trunk")
				return
			}
			if err = verifyVLANZoneBridge(ctx, client, expected.Zone, request.ExternalVLAN.TrunkBridge); err != nil {
				return
			}
			zoneFound = true
			break
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
		if vnet.Alias != "organesson:"+request.OperationKey || vnet.Zone != expected.Zone || request.ExternalVLAN != nil && vnet.Tag != uint32(request.ExternalVLAN.VLANID) || request.ExternalVLAN == nil && request.VNetSourceZone != "" && (vnet.Tag == 0 || placement.Tag != 0 && vnet.Tag != placement.Tag) {
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
	if len(subnets) != 0 {
		err = errors.New("Organesson VNet unexpectedly has subnet configuration; gateway and DHCP are managed by the attached router")
	}
	return
}

// ReadIPAM lists only address assignments attached to the expected Organesson VNet.
func (driver *apiSDNNetworkDriver) ReadIPAM(ctx context.Context, request SDNNetworkRequest, placement SDNNetworkPlacement) (entries []SDNIPAMEntry, state string, err error) {
	if err = validateSDNNetworkRequest(request); err != nil {
		state = "unavailable"
		return
	}
	var expected SDNNetworkPlacement = placementForSDNNetwork(request)
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

// verifyVLANZoneBridge confirms the bridge stored by Proxmox because go-proxmox omits it from SDNZone.
func verifyVLANZoneBridge(ctx context.Context, client *pve.Client, zone string, expectedBridge string) (err error) {
	var configuration map[string]any
	if err = client.Get(ctx, "/cluster/sdn/zones/"+zone, &configuration); err != nil {
		return
	}
	if bridge, _ := configuration["bridge"].(string); bridge != expectedBridge {
		err = fmt.Errorf("Proxmox VLAN zone %q uses bridge %q, not authorized bridge %q", zone, bridge, expectedBridge)
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

func (driver *apiSDNNetworkDriver) Delete(ctx context.Context, request SDNNetworkRequest, placement SDNNetworkPlacement) (err error) {
	var expected SDNNetworkPlacement = placementForSDNNetwork(request)
	if placement.VNet != expected.VNet || placement.Zone != expected.Zone || request.OperationKey == "" {
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
			if vnet.Alias != "organesson:"+request.OperationKey || vnet.Zone != expected.Zone || request.ExternalVLAN != nil && vnet.Tag != uint32(request.ExternalVLAN.VLANID) {
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
	if request.ExternalVLAN != nil {
		if !owned {
			return
		}
		var zones []*pve.SDNZone
		if zones, err = cluster.SDNZones(ctx); err != nil {
			return
		}
		for _, zone := range zones {
			if zone == nil || zone.Name != expected.Zone {
				continue
			}
			if zone.Type != "vlan" || strings.Join([]string(zone.Nodes), ",") != strings.Join(request.ExternalVLAN.Nodes, ",") {
				err = errors.New("refusing to delete a Proxmox VLAN zone with unexpected trunk configuration")
				return
			}
			if err = verifyVLANZoneBridge(ctx, client, expected.Zone, request.ExternalVLAN.TrunkBridge); err != nil {
				return
			}
			if err = cluster.DeleteSDNZone(ctx, expected.Zone); err != nil {
				return
			}
			break
		}
		var task *pve.Task
		if task, err = cluster.SDNApply(ctx); err != nil {
			return
		}
		err = waitTask(ctx, client, task)
		return
	} else if request.VNetSourceZone != "" {
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
	if request.RouterVMID < 0 {
		err = errors.New("router VMID cannot be negative")
		return
	}
	if request.ExternalVLAN != nil {
		var exposure *SDNExternalVLANExposure = request.ExternalVLAN
		if strings.TrimSpace(exposure.TrunkNode) == "" || strings.TrimSpace(exposure.TrunkBridge) == "" || strings.ContainsAny(exposure.TrunkBridge, ",=") || exposure.VLANID < 1 || exposure.VLANID > 4094 {
			err = errors.New("external VLAN requires a trunk node, bridge, and VLAN ID from 1 through 4094")
			return
		}
		if len(exposure.Nodes) != 1 || exposure.Nodes[0] != exposure.TrunkNode {
			err = errors.New("external VLAN must be exposed on only its selected trunk node")
			return
		}
	}
	switch request.Mode {
	case "unmanaged-layer-2":
		if request.Subnet != "" || request.Gateway != "" || request.IPv6Subnet != "" || request.IPv6Gateway != "" || request.DHCPEnabled || request.IPv6DHCPEnabled || request.RouterVMID != 0 {
			err = errors.New("unmanaged layer-2 network cannot configure a subnet, gateway, DHCP, or router polling")
		}
	case "managed":
		if request.Subnet == "" && request.Gateway != "" || request.Subnet != "" && request.Gateway == "" {
			err = errors.New("managed IPv4 subnet and gateway must be configured together")
			return
		}
		if request.IPv6Subnet == "" && request.IPv6Gateway != "" || request.IPv6Subnet != "" && request.IPv6Gateway == "" {
			err = errors.New("managed IPv6 subnet and gateway must be configured together")
			return
		}
		if request.Subnet == "" && request.IPv6Subnet == "" {
			err = errors.New("managed SDN network requires at least one IPv4 or IPv6 subnet")
			return
		}
		if request.DHCPEnabled && request.Subnet == "" {
			err = errors.New("IPv4 DHCP requires an IPv4 subnet")
			return
		}
		if request.IPv6DHCPEnabled && request.IPv6Subnet == "" {
			err = errors.New("DHCPv6 requires an IPv6 subnet")
			return
		}
		if request.Subnet != "" {
			var prefix netip.Prefix
			if prefix, err = netip.ParsePrefix(request.Subnet); err != nil || !prefix.Addr().Is4() || prefix != prefix.Masked() {
				err = errors.New("managed IPv4 subnet must be a canonical IPv4 CIDR")
				return
			}
			var gateway netip.Addr
			if gateway, err = netip.ParseAddr(request.Gateway); err != nil || !gateway.Is4() || !prefix.Contains(gateway) {
				err = errors.New("managed IPv4 gateway must be inside its IPv4 subnet")
				return
			}
		}
		if request.IPv6Subnet != "" {
			var prefix netip.Prefix
			if prefix, err = netip.ParsePrefix(request.IPv6Subnet); err != nil || !prefix.Addr().Is6() || prefix.Addr().Is4In6() || prefix != prefix.Masked() {
				err = errors.New("managed IPv6 subnet must be a canonical IPv6 CIDR")
				return
			}
			var gateway netip.Addr
			if gateway, err = netip.ParseAddr(request.IPv6Gateway); err != nil || !gateway.Is6() || gateway.Is4In6() || !prefix.Contains(gateway) {
				err = errors.New("managed IPv6 gateway must be inside its IPv6 subnet")
			}
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

// placementForSDNNetwork selects a dedicated VLAN zone when external exposure is requested.
func placementForSDNNetwork(request SDNNetworkRequest) (placement SDNNetworkPlacement) {
	placement = namesForSDNNetwork(request.OperationKey, request.VNetSourceZone)
	if request.ExternalVLAN != nil {
		var digest [sha256.Size]byte = sha256.Sum256([]byte("organesson-external-vlan:" + request.OperationKey))
		placement.Zone = "ov" + hex.EncodeToString(digest[:])[:6]
		placement.Tag = uint32(request.ExternalVLAN.VLANID)
	}
	return
}

// SDNNetworkPlacementForRequest returns the deterministic Proxmox placement for a network request.
func SDNNetworkPlacementForRequest(request SDNNetworkRequest) (placement SDNNetworkPlacement) {
	placement = placementForSDNNetwork(request)
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
