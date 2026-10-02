package proxmox

import (
	"context"
	"sort"

	pve "github.com/luthermonson/go-proxmox"
	"github.com/z46-dev/organesson/backend/config"
)

type apiResourceInventory struct {
	settings config.ProxmoxConfiguration
}

// ResourceInventory reads available resource pools, storages, bridges, and SDN VNets from PVE.
func (service *Service) ResourceInventory(ctx context.Context) (inventory ResourceInventory, err error) {
	if service == nil || !service.Configured() || service.inventoryReader == nil {
		err = ErrNotConfigured
		return
	}
	inventory, err = service.inventoryReader.ReadResourceInventory(ctx)
	return
}

// ReadResourceInventory queries available resource pools, storages, bridges, and SDN VNets.
func (reader *apiResourceInventory) ReadResourceInventory(ctx context.Context) (inventory ResourceInventory, err error) {
	var client *pve.Client
	if client, err = newAPIClient(reader.settings); err != nil {
		return
	}
	var pools pve.Pools
	if pools, err = client.Pools(ctx); err != nil {
		return
	}
	for _, pool := range pools {
		if pool != nil && pool.PoolID != "" {
			inventory.Pools = append(inventory.Pools, pool.PoolID)
		}
	}
	var storages pve.ClusterStorages
	if storages, err = client.ClusterStorages(ctx); err != nil {
		return
	}
	for _, storage := range storages {
		if storage != nil && storage.Storage != "" {
			inventory.Storages = append(inventory.Storages, storage.Storage)
		}
	}
	var nodes pve.NodeStatuses
	if nodes, err = client.Nodes(ctx); err != nil {
		return
	}
	var bridges = make(map[string]bool)
	for _, status := range nodes {
		if status == nil || status.Node == "" || status.Status != "online" {
			continue
		}
		var node *pve.Node
		if node, err = client.Node(ctx, status.Node); err != nil {
			return
		}
		var networks pve.NodeNetworks
		if networks, err = node.Networks(ctx, "bridge"); err != nil {
			return
		}
		for _, network := range networks {
			if network != nil && network.Iface != "" {
				bridges[network.Iface] = true
			}
		}
	}
	for bridge := range bridges {
		inventory.Bridges = append(inventory.Bridges, bridge)
	}
	sort.Strings(inventory.Pools)
	sort.Strings(inventory.Storages)
	sort.Strings(inventory.Bridges)
	var cluster *pve.Cluster
	if cluster, err = client.Cluster(ctx); err != nil {
		return
	}
	var vnets []*pve.VNet
	if vnets, err = cluster.SDNVNets(ctx); err != nil {
		return
	}
	for _, vnet := range vnets {
		if vnet != nil && vnet.Name != "" {
			inventory.VNets = append(inventory.VNets, vnet.Name)
		}
	}
	sort.Strings(inventory.VNets)
	return
}
