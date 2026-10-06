package organesson

import (
	"context"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/hashicorp/terraform-plugin-sdk/v2/helper/schema"
)

type (
	deploymentResult struct {
		Deployment struct {
			ID         int  `json:"id"`
			RootNodeID *int `json:"root_node_id"`
		} `json:"deployment"`
	}
	nodeResult struct {
		Node struct {
			ID           int    `json:"id"`
			DeploymentID int    `json:"deployment_id"`
			ParentID     *int   `json:"parent_id"`
			Name         string `json:"name"`
		} `json:"ownership_node"`
	}
	vmResult struct {
		Resource struct {
			ID           int    `json:"id"`
			OwnershipID  int    `json:"ownership_id"`
			ExternalID   string `json:"external_id"`
			ExternalNode string `json:"external_node"`
			Name         string `json:"name"`
			PowerState   string `json:"power_state"`
		} `json:"resource"`
	}
	userGroupResult struct {
		Group struct {
			ID           int    `json:"id"`
			DeploymentID int    `json:"deployment_id"`
			Name         string `json:"name"`
		} `json:"user_group"`
		Members []string `json:"members"`
	}
	grantResult struct {
		Grant struct {
			ID                 int    `json:"id"`
			SubjectKind        int    `json:"subject_kind"`
			SubjectID          int    `json:"subject_id"`
			Permission         string `json:"permission"`
			TargetNodeID       int    `json:"target_node_id"`
			InheritDescendants bool   `json:"inherit_descendants"`
		} `json:"permission_grant"`
		SubjectName string `json:"subject_name"`
	}
	addressPoolRequestResult struct {
		Resource struct {
			ID          int    `json:"id"`
			OwnershipID int    `json:"ownership_id"`
			Name        string `json:"name"`
		} `json:"resource"`
		Allocation struct {
			LogicalNetworkID   int      `json:"logical_network_id"`
			RangeStart         string   `json:"range_start"`
			RangeEnd           string   `json:"range_end"`
			EnvironmentNetwork string   `json:"environment_network"`
			Addresses          []string `json:"addresses"`
			PoolName           string   `json:"pool_name"`
			Prefix             string   `json:"prefix"`
			Gateway            string   `json:"gateway"`
			DNS                []string `json:"dns"`
		} `json:"allocation"`
	}
	networkResult struct {
		Resource struct {
			ID           int    `json:"id"`
			OwnershipID  int    `json:"ownership_id"`
			ExternalID   string `json:"external_id"`
			ExternalNode string `json:"external_node"`
			Name         string `json:"name"`
			PowerState   string `json:"power_state"`
		} `json:"resource"`
		Configuration struct {
			Request struct {
				Name         string `json:"name"`
				Mode         string `json:"mode"`
				Subnet       string `json:"subnet"`
				Gateway      string `json:"gateway"`
				IPv6Subnet   string `json:"ipv6_subnet"`
				IPv6Gateway  string `json:"ipv6_gateway"`
				DHCPEnabled  bool   `json:"dhcp_enabled"`
				EgressPolicy string `json:"egress_policy"`
				RouterVMID   int    `json:"router_vmid"`
			} `json:"request"`
		} `json:"configuration"`
	}
	networkAttachmentResult struct {
		Resource struct {
			ID         int    `json:"id"`
			Name       string `json:"name"`
			PowerState string `json:"power_state"`
		} `json:"resource"`
		Configuration struct {
			Addresses          []string `json:"addresses"`
			IPv6Addresses      []string `json:"ipv6_addresses"`
			AddressFamily      string   `json:"address_family"`
			AddressPrefix      string   `json:"address_prefix"`
			AddressGateway     string   `json:"address_gateway"`
			AddressDNS         []string `json:"address_dns"`
			IPv6AddressPrefix  string   `json:"ipv6_address_prefix"`
			IPv6AddressGateway string   `json:"ipv6_address_gateway"`
			IPv6AddressDNS     []string `json:"ipv6_address_dns"`
			Placement          struct {
				Device string `json:"device"`
				MAC    string `json:"mac"`
			} `json:"placement"`
		} `json:"configuration"`
	}
	guestNetworkConfigurationResult struct {
		GuestNetwork struct {
			Method           string   `json:"ipv4_method"`
			Address          string   `json:"ipv4_address,omitempty"`
			Gateway          string   `json:"ipv4_gateway,omitempty"`
			DNS              []string `json:"ipv4_dns,omitempty"`
			NeverDefault     bool     `json:"ipv4_never_default,omitempty"`
			IPv6Method       string   `json:"ipv6_method,omitempty"`
			IPv6Address      string   `json:"ipv6_address,omitempty"`
			IPv6Gateway      string   `json:"ipv6_gateway,omitempty"`
			IPv6DNS          []string `json:"ipv6_dns,omitempty"`
			IPv6NeverDefault bool     `json:"ipv6_never_default,omitempty"`
		} `json:"guest_network"`
	}
)

// addressPoolRequestOperations manages durable environment or managed-VNet address reservations.
func addressPoolRequestOperations() (operations remoteResourceOperations) {
	operations.Create = func(ctx context.Context, data *schema.ResourceData, client *apiClient) (err error) {
		var deploymentID string
		if deploymentID, err = remoteID(data.Get("deployment_id").(string)); err != nil {
			return
		}
		var result addressPoolRequestResult
		var body map[string]any = map[string]any{
			"name":                data.Get("name").(string),
			"environment_network": data.Get("environment_network").(string),
			"address_family":      data.Get("address_family").(string),
			"address_count":       data.Get("address_count").(int),
			"range_start":         data.Get("range_start").(string),
			"range_end":           data.Get("range_end").(string),
			"logical_network_id":  0,
		}
		if value := data.Get("logical_network_id").(string); value != "" {
			if body["logical_network_id"], err = strconv.Atoi(value); err != nil {
				return
			}
		}
		err = client.request(ctx, http.MethodPost, "/api/v1/deployments/"+deploymentID+"/address-pool-requests", body, &result)
		if err != nil {
			return
		}
		data.SetId(strconv.Itoa(result.Resource.ID))
		_ = data.Set("addresses", result.Allocation.Addresses)
		_ = data.Set("prefix", result.Allocation.Prefix)
		_ = data.Set("gateway", result.Allocation.Gateway)
		_ = data.Set("dns", result.Allocation.DNS)
		_ = data.Set("pool_name", result.Allocation.PoolName)
		var logicalNetworkID string
		if result.Allocation.LogicalNetworkID > 0 {
			logicalNetworkID = strconv.Itoa(result.Allocation.LogicalNetworkID)
		}
		_ = data.Set("logical_network_id", logicalNetworkID)
		_ = data.Set("environment_network", result.Allocation.EnvironmentNetwork)
		_ = data.Set("range_start", result.Allocation.RangeStart)
		_ = data.Set("range_end", result.Allocation.RangeEnd)
		var source string = data.Get("environment_network").(string)
		if source == "" {
			source = fmt.Sprintf("managed VNet %d", result.Allocation.LogicalNetworkID)
		}
		_ = data.Set("summary", fmt.Sprintf("reserved %d addresses from %s pool %q", len(result.Allocation.Addresses), source, result.Allocation.PoolName))
		return
	}
	operations.Read = func(ctx context.Context, data *schema.ResourceData, client *apiClient) (err error) {
		var id string
		if id, err = remoteID(data.Id()); err != nil {
			return
		}
		var result addressPoolRequestResult
		err = client.request(ctx, http.MethodGet, "/api/v1/address-pool-requests/"+id, nil, &result)
		if err == nil {
			_ = data.Set("name", result.Resource.Name)
			_ = data.Set("addresses", result.Allocation.Addresses)
			_ = data.Set("prefix", result.Allocation.Prefix)
			_ = data.Set("gateway", result.Allocation.Gateway)
			_ = data.Set("dns", result.Allocation.DNS)
			_ = data.Set("pool_name", result.Allocation.PoolName)
			var logicalNetworkID string
			if result.Allocation.LogicalNetworkID > 0 {
				logicalNetworkID = strconv.Itoa(result.Allocation.LogicalNetworkID)
			}
			_ = data.Set("logical_network_id", logicalNetworkID)
			_ = data.Set("environment_network", result.Allocation.EnvironmentNetwork)
			_ = data.Set("range_start", result.Allocation.RangeStart)
			_ = data.Set("range_end", result.Allocation.RangeEnd)
		}
		return
	}
	operations.Delete = func(ctx context.Context, data *schema.ResourceData, client *apiClient) (err error) {
		var id string
		if id, err = remoteID(data.Id()); err != nil {
			return
		}
		return client.request(ctx, http.MethodDelete, "/api/v1/address-pool-requests/"+id, nil, nil)
	}
	return
}

// networkOperations manages isolated Proxmox SDN resources through Organesson.
func networkOperations(mode string) (operations remoteResourceOperations) {
	operations.Create = func(ctx context.Context, data *schema.ResourceData, client *apiClient) (err error) {
		var parentID string
		var deploymentID string
		if deploymentID, err = remoteID(data.Get("deployment_id").(string)); err == nil {
			var result deploymentResult
			if err = client.request(ctx, http.MethodGet, "/api/v1/deployments/"+deploymentID, nil, &result); err != nil {
				return
			}
			if result.Deployment.RootNodeID == nil {
				return fmt.Errorf("Organesson deployment has no ownership root")
			}
			parentID = strconv.Itoa(*result.Deployment.RootNodeID)
		} else if data.Get("logical_group_id").(string) != "" {
			err = nil
			if parentID, err = remoteID(data.Get("logical_group_id").(string)); err != nil {
				return
			}
			var parent nodeResult
			if err = client.request(ctx, http.MethodGet, "/api/v1/ownership-nodes/"+parentID, nil, &parent); err != nil {
				return
			}
			deploymentID = strconv.Itoa(parent.Node.DeploymentID)
		} else {
			return fmt.Errorf("one of deployment_id or logical_group_id is required")
		}
		var parentNodeID int
		if parentNodeID, err = strconv.Atoi(parentID); err != nil {
			return
		}
		var result networkResult
		err = client.request(ctx, http.MethodPost, "/api/v1/deployments/"+deploymentID+"/networks", map[string]any{
			"parent_node_id": parentNodeID, "name": data.Get("name").(string), "mode": mode,
			"ipv4_subnet": data.Get("ipv4_subnet").(string), "ipv4_gateway": data.Get("ipv4_gateway").(string),
			"ipv6_subnet": data.Get("ipv6_subnet").(string), "ipv6_gateway": data.Get("ipv6_gateway").(string),
			"dhcp_enabled": data.Get("dhcp_enabled").(bool), "egress_policy": data.Get("egress_policy").(string),
			"router_vmid": data.Get("router_vmid").(int),
		}, &result)
		if err != nil {
			return
		}
		data.SetId(strconv.Itoa(result.Resource.ID))
		_ = data.Set("power_state", result.Resource.PowerState)
		_ = data.Set("proxmox_vnet", result.Resource.ExternalID)
		_ = data.Set("proxmox_zone", result.Resource.ExternalNode)
		_ = data.Set("router_vmid", result.Configuration.Request.RouterVMID)
		_ = data.Set("summary", fmt.Sprintf("created isolated Proxmox SDN VNet %q in zone %q", result.Resource.ExternalID, result.Resource.ExternalNode))
		return
	}
	operations.Read = func(ctx context.Context, data *schema.ResourceData, client *apiClient) (err error) {
		var id string
		if id, err = remoteID(data.Id()); err != nil {
			return
		}
		var result networkResult
		err = client.request(ctx, http.MethodGet, "/api/v1/networks/"+id, nil, &result)
		if err == nil {
			_ = data.Set("name", result.Resource.Name)
			_ = data.Set("ipv4_subnet", result.Configuration.Request.Subnet)
			_ = data.Set("ipv4_gateway", result.Configuration.Request.Gateway)
			_ = data.Set("ipv6_subnet", result.Configuration.Request.IPv6Subnet)
			_ = data.Set("ipv6_gateway", result.Configuration.Request.IPv6Gateway)
			_ = data.Set("dhcp_enabled", result.Configuration.Request.DHCPEnabled)
			_ = data.Set("egress_policy", result.Configuration.Request.EgressPolicy)
			_ = data.Set("router_vmid", result.Configuration.Request.RouterVMID)
			_ = data.Set("power_state", result.Resource.PowerState)
			_ = data.Set("proxmox_vnet", result.Resource.ExternalID)
			_ = data.Set("proxmox_zone", result.Resource.ExternalNode)
		}
		return
	}
	operations.Delete = func(ctx context.Context, data *schema.ResourceData, client *apiClient) (err error) {
		var id string
		if id, err = remoteID(data.Id()); err != nil {
			return
		}
		return client.request(ctx, http.MethodDelete, "/api/v1/networks/"+id, nil, nil)
	}
	return
}

// networkAttachmentOperations manages a VM's PVE NIC and environment address claims.
func networkAttachmentOperations() (operations remoteResourceOperations) {
	operations.Create = func(ctx context.Context, data *schema.ResourceData, client *apiClient) (err error) {
		var vmID string
		if vmID, err = remoteID(data.Get("virtual_machine_id").(string)); err != nil {
			return
		}
		var body map[string]any = map[string]any{
			"name":                         data.Get("name").(string),
			"environment_network":          data.Get("environment_network").(string),
			"address_pool_request_id":      0,
			"requested_address_count":      data.Get("requested_address_count").(int),
			"ipv6_address_pool_request_id": 0,
			"requested_ipv6_address_count": data.Get("requested_ipv6_address_count").(int),
			"logical_network_id":           0,
		}
		if value := data.Get("logical_network_id").(string); value != "" {
			if body["logical_network_id"], err = strconv.Atoi(value); err != nil {
				return
			}
		}
		if value := data.Get("address_pool_request_id").(string); value != "" {
			if body["address_pool_request_id"], err = strconv.Atoi(value); err != nil {
				return
			}
		}
		if value := data.Get("ipv6_address_pool_request_id").(string); value != "" {
			if body["ipv6_address_pool_request_id"], err = strconv.Atoi(value); err != nil {
				return
			}
		}
		var result networkAttachmentResult
		err = client.request(ctx, http.MethodPost, "/api/v1/virtual-machines/"+vmID+"/network-attachments", body, &result)
		if err != nil {
			return
		}
		data.SetId(strconv.Itoa(result.Resource.ID))
		_ = data.Set("addresses", result.Configuration.Addresses)
		_ = data.Set("ipv6_addresses", result.Configuration.IPv6Addresses)
		_ = data.Set("address_family", result.Configuration.AddressFamily)
		_ = data.Set("address_prefix", result.Configuration.AddressPrefix)
		_ = data.Set("address_gateway", result.Configuration.AddressGateway)
		_ = data.Set("address_dns", result.Configuration.AddressDNS)
		_ = data.Set("ipv6_address_prefix", result.Configuration.IPv6AddressPrefix)
		_ = data.Set("ipv6_address_gateway", result.Configuration.IPv6AddressGateway)
		_ = data.Set("ipv6_address_dns", result.Configuration.IPv6AddressDNS)
		_ = data.Set("net_device", result.Configuration.Placement.Device)
		_ = data.Set("mac_address", result.Configuration.Placement.MAC)
		_ = data.Set("summary", fmt.Sprintf("attached %s to %s with MAC %s", result.Configuration.Placement.Device, data.Get("virtual_machine_id").(string), result.Configuration.Placement.MAC))
		return
	}
	operations.Read = func(ctx context.Context, data *schema.ResourceData, client *apiClient) (err error) {
		var id string
		if id, err = remoteID(data.Id()); err != nil {
			return
		}
		var result networkAttachmentResult
		err = client.request(ctx, http.MethodGet, "/api/v1/network-attachments/"+id, nil, &result)
		if err == nil {
			_ = data.Set("name", result.Resource.Name)
			_ = data.Set("addresses", result.Configuration.Addresses)
			_ = data.Set("ipv6_addresses", result.Configuration.IPv6Addresses)
			_ = data.Set("address_family", result.Configuration.AddressFamily)
			_ = data.Set("address_prefix", result.Configuration.AddressPrefix)
			_ = data.Set("address_gateway", result.Configuration.AddressGateway)
			_ = data.Set("address_dns", result.Configuration.AddressDNS)
			_ = data.Set("ipv6_address_prefix", result.Configuration.IPv6AddressPrefix)
			_ = data.Set("ipv6_address_gateway", result.Configuration.IPv6AddressGateway)
			_ = data.Set("ipv6_address_dns", result.Configuration.IPv6AddressDNS)
			_ = data.Set("net_device", result.Configuration.Placement.Device)
			_ = data.Set("mac_address", result.Configuration.Placement.MAC)
		}
		return
	}
	operations.Delete = func(ctx context.Context, data *schema.ResourceData, client *apiClient) (err error) {
		var id string
		if id, err = remoteID(data.Id()); err != nil {
			return
		}
		return client.request(ctx, http.MethodDelete, "/api/v1/network-attachments/"+id, nil, nil)
	}
	return
}

// guestNetworkConfigurationOperations applies and refreshes guest IPv4 setup through the API/QGA path.
func guestNetworkConfigurationOperations() (operations remoteResourceOperations) {
	operations.Create = func(ctx context.Context, data *schema.ResourceData, client *apiClient) (err error) {
		var attachmentID string
		if attachmentID, err = remoteID(data.Get("network_attachment_id").(string)); err != nil {
			return
		}
		var dns []string
		for _, entry := range data.Get("ipv4_dns").([]interface{}) {
			dns = append(dns, entry.(string))
		}
		var ipv6DNS []string
		for _, entry := range data.Get("ipv6_dns").([]interface{}) {
			ipv6DNS = append(ipv6DNS, entry.(string))
		}
		var result guestNetworkConfigurationResult
		err = client.request(ctx, http.MethodPost, "/api/v1/network-attachments/"+attachmentID+"/guest-network-configuration", map[string]any{
			"ipv4_method": data.Get("ipv4_method").(string), "ipv4_address": data.Get("ipv4_address").(string),
			"ipv4_gateway": data.Get("ipv4_gateway").(string), "ipv4_dns": dns,
			"ipv4_never_default": data.Get("ipv4_never_default").(bool),
			"ipv6_method":        data.Get("ipv6_method").(string), "ipv6_address": data.Get("ipv6_address").(string),
			"ipv6_gateway": data.Get("ipv6_gateway").(string), "ipv6_dns": ipv6DNS,
			"ipv6_never_default": data.Get("ipv6_never_default").(bool),
		}, &result)
		if err != nil {
			return
		}
		data.SetId(attachmentID)
		setGuestNetworkConfiguration(data, result.GuestNetwork)
		return
	}
	operations.Read = func(ctx context.Context, data *schema.ResourceData, client *apiClient) (err error) {
		var attachmentID string
		if attachmentID, err = remoteID(data.Id()); err != nil {
			return
		}
		var result guestNetworkConfigurationResult
		err = client.request(ctx, http.MethodGet, "/api/v1/network-attachments/"+attachmentID+"/guest-network-configuration", nil, &result)
		if err == nil {
			_ = data.Set("network_attachment_id", attachmentID)
			setGuestNetworkConfiguration(data, result.GuestNetwork)
		}
		return
	}
	operations.Delete = func(ctx context.Context, data *schema.ResourceData, client *apiClient) (err error) {
		var attachmentID string
		if attachmentID, err = remoteID(data.Id()); err != nil {
			return
		}
		return client.request(ctx, http.MethodDelete, "/api/v1/network-attachments/"+attachmentID+"/guest-network-configuration", nil, nil)
	}
	return
}

func setGuestNetworkConfiguration(data *schema.ResourceData, configuration struct {
	Method           string   `json:"ipv4_method"`
	Address          string   `json:"ipv4_address,omitempty"`
	Gateway          string   `json:"ipv4_gateway,omitempty"`
	DNS              []string `json:"ipv4_dns,omitempty"`
	NeverDefault     bool     `json:"ipv4_never_default,omitempty"`
	IPv6Method       string   `json:"ipv6_method,omitempty"`
	IPv6Address      string   `json:"ipv6_address,omitempty"`
	IPv6Gateway      string   `json:"ipv6_gateway,omitempty"`
	IPv6DNS          []string `json:"ipv6_dns,omitempty"`
	IPv6NeverDefault bool     `json:"ipv6_never_default,omitempty"`
}) {
	_ = data.Set("ipv4_method", configuration.Method)
	_ = data.Set("ipv4_address", configuration.Address)
	_ = data.Set("ipv4_gateway", configuration.Gateway)
	_ = data.Set("ipv4_dns", configuration.DNS)
	_ = data.Set("ipv4_never_default", configuration.NeverDefault)
	_ = data.Set("ipv6_method", configuration.IPv6Method)
	_ = data.Set("ipv6_address", configuration.IPv6Address)
	_ = data.Set("ipv6_gateway", configuration.IPv6Gateway)
	_ = data.Set("ipv6_dns", configuration.IPv6DNS)
	_ = data.Set("ipv6_never_default", configuration.IPv6NeverDefault)
	_ = data.Set("summary", fmt.Sprintf("configured %s IPv4 on attachment %q", configuration.Method, data.Get("network_attachment_id")))
}

// deploymentOperations supplies the API lifecycle for a managed deployment root.
func deploymentOperations() (operations remoteResourceOperations) {
	operations.Create = func(ctx context.Context, data *schema.ResourceData, client *apiClient) (err error) {
		var result deploymentResult
		if err = client.request(ctx, http.MethodPost, "/api/v1/deployments", map[string]string{
			"name":        data.Get("name").(string),
			"description": data.Get("description").(string),
		}, &result); err != nil {
			return
		}
		if result.Deployment.ID < 1 || result.Deployment.RootNodeID == nil {
			return fmt.Errorf("Organesson returned an incomplete deployment")
		}
		data.SetId(strconv.Itoa(result.Deployment.ID))
		_ = data.Set("root_node_id", *result.Deployment.RootNodeID)
		return
	}
	operations.Read = func(ctx context.Context, data *schema.ResourceData, client *apiClient) (err error) {
		var id string
		if id, err = remoteID(data.Id()); err != nil {
			return
		}
		var result deploymentResult
		err = client.request(ctx, http.MethodGet, "/api/v1/deployments/"+id, nil, &result)
		if err == nil && result.Deployment.RootNodeID != nil {
			_ = data.Set("root_node_id", *result.Deployment.RootNodeID)
		}
		return
	}
	operations.Update = func(ctx context.Context, data *schema.ResourceData, client *apiClient) (err error) {
		var id string
		if id, err = remoteID(data.Id()); err != nil {
			return
		}
		var result deploymentResult
		err = client.request(ctx, http.MethodPut, "/api/v1/deployments/"+id, map[string]string{
			"name":        data.Get("name").(string),
			"description": data.Get("description").(string),
		}, &result)
		return
	}
	operations.Delete = func(ctx context.Context, data *schema.ResourceData, client *apiClient) (err error) {
		var id string
		if id, err = remoteID(data.Id()); err != nil {
			return
		}
		return client.request(ctx, http.MethodDelete, "/api/v1/deployments/"+id, nil, nil)
	}
	return
}

// logicalGroupOperations supplies remote ownership-node operations.
func logicalGroupOperations() (operations remoteResourceOperations) {
	return createLogicalGroupOperations(false)
}

// internalGroupOperations creates an administrator-only ownership branch.
func internalGroupOperations() (operations remoteResourceOperations) {
	return createLogicalGroupOperations(true)
}

// createLogicalGroupOperations supplies lifecycle operations for visible and internal groups.
func createLogicalGroupOperations(internal bool) (operations remoteResourceOperations) {
	operations.Create = func(ctx context.Context, data *schema.ResourceData, client *apiClient) (err error) {
		var deploymentID string
		if deploymentID, err = remoteID(data.Get("deployment_id").(string)); err != nil {
			return
		}
		var result nodeResult
		err = client.request(ctx, http.MethodPost, "/api/v1/deployments/"+deploymentID+"/logical-groups", map[string]any{
			"name":           data.Get("name").(string),
			"parent_node_id": data.Get("parent_node_id").(int),
			"internal":       internal,
		}, &result)
		if err != nil {
			return
		}
		data.SetId(strconv.Itoa(result.Node.ID))
		_ = data.Set("deployment_id", strconv.Itoa(result.Node.DeploymentID))
		_ = data.Set("parent_node_id", parentID(result.Node.ParentID))
		return
	}
	operations.Read = func(ctx context.Context, data *schema.ResourceData, client *apiClient) (err error) {
		var id string
		if id, err = remoteID(data.Id()); err != nil {
			return
		}
		var result nodeResult
		err = client.request(ctx, http.MethodGet, "/api/v1/ownership-nodes/"+id, nil, &result)
		if err == nil {
			_ = data.Set("deployment_id", strconv.Itoa(result.Node.DeploymentID))
			_ = data.Set("name", result.Node.Name)
			_ = data.Set("parent_node_id", parentID(result.Node.ParentID))
		}
		return
	}
	operations.Delete = func(ctx context.Context, data *schema.ResourceData, client *apiClient) (err error) {
		var id string
		if id, err = remoteID(data.Id()); err != nil {
			return
		}
		return client.request(ctx, http.MethodDelete, "/api/v1/ownership-nodes/"+id, nil, nil)
	}
	return
}

// userGroupOperations creates deployment-local groups and their initial memberships.
func userGroupOperations() (operations remoteResourceOperations) {
	operations.Create = func(ctx context.Context, data *schema.ResourceData, client *apiClient) (err error) {
		var deploymentID string
		if deploymentID, err = remoteID(data.Get("deployment_id").(string)); err != nil {
			return
		}
		var result userGroupResult
		err = client.request(ctx, http.MethodPost, "/api/v1/deployments/"+deploymentID+"/user-groups", map[string]any{
			"name":    data.Get("name").(string),
			"members": stringSetValues(data.Get("members").(*schema.Set)),
		}, &result)
		if err != nil {
			return
		}
		data.SetId(strconv.Itoa(result.Group.ID))
		_ = data.Set("deployment_id", strconv.Itoa(result.Group.DeploymentID))
		_ = data.Set("members", result.Members)
		return
	}
	operations.Read = func(ctx context.Context, data *schema.ResourceData, client *apiClient) (err error) {
		var id string
		if id, err = remoteID(data.Id()); err != nil {
			return
		}
		var result userGroupResult
		err = client.request(ctx, http.MethodGet, "/api/v1/user-groups/"+id, nil, &result)
		if err == nil {
			_ = data.Set("deployment_id", strconv.Itoa(result.Group.DeploymentID))
			_ = data.Set("name", result.Group.Name)
			_ = data.Set("members", result.Members)
		}
		return
	}
	operations.Update = func(ctx context.Context, data *schema.ResourceData, client *apiClient) (err error) {
		var id string
		if id, err = remoteID(data.Id()); err != nil {
			return
		}
		return client.request(ctx, http.MethodPut, "/api/v1/user-groups/"+id+"/members", map[string][]string{
			"members": stringSetValues(data.Get("members").(*schema.Set)),
		}, nil)
	}
	operations.Delete = func(ctx context.Context, data *schema.ResourceData, client *apiClient) (err error) {
		var id string
		if id, err = remoteID(data.Id()); err != nil {
			return
		}
		return client.request(ctx, http.MethodDelete, "/api/v1/user-groups/"+id, nil, nil)
	}
	return
}

// virtualMachineOperations manages simulated or Proxmox-backed VM records through Organesson.
func virtualMachineOperations() (operations remoteResourceOperations) {
	operations.Create = func(ctx context.Context, data *schema.ResourceData, client *apiClient) (err error) {
		var parentID string
		if parentID, err = remoteID(data.Get("logical_group_id").(string)); err != nil {
			return
		}
		var parentResult nodeResult
		if err = client.request(ctx, http.MethodGet, "/api/v1/ownership-nodes/"+parentID, nil, &parentResult); err != nil {
			return
		}
		var result vmResult
		err = client.request(ctx, http.MethodPost, "/api/v1/deployments/"+strconv.Itoa(parentResult.Node.DeploymentID)+"/virtual-machines", map[string]any{
			"parent_node_id":    parentResult.Node.ID,
			"name":              data.Get("name").(string),
			"provisioning_mode": data.Get("provisioning_mode").(string),
			"template":          data.Get("template").(string),
			"pool":              data.Get("pool").(string),
			"storage":           data.Get("storage").(string),
			"cpu_cores":         data.Get("cpu_cores").(int),
			"memory_mib":        data.Get("memory_mib").(int),
			"boot_disk_gib":     data.Get("boot_disk_gib").(int),
		}, &result)
		if err == nil {
			data.SetId(strconv.Itoa(result.Resource.ID))
			_ = data.Set("ownership_node_id", result.Resource.OwnershipID)
			_ = data.Set("power_state", result.Resource.PowerState)
			_ = data.Set("proxmox_vmid", result.Resource.ExternalID)
			_ = data.Set("proxmox_node", result.Resource.ExternalNode)
		}
		return
	}
	operations.Read = func(ctx context.Context, data *schema.ResourceData, client *apiClient) (err error) {
		var id string
		if id, err = remoteID(data.Id()); err != nil {
			return
		}
		var result vmResult
		err = client.request(ctx, http.MethodGet, "/api/v1/virtual-machines/"+id, nil, &result)
		if err == nil {
			_ = data.Set("name", result.Resource.Name)
			_ = data.Set("ownership_node_id", result.Resource.OwnershipID)
			_ = data.Set("power_state", result.Resource.PowerState)
			_ = data.Set("proxmox_vmid", result.Resource.ExternalID)
			_ = data.Set("proxmox_node", result.Resource.ExternalNode)
		}
		return
	}
	operations.Delete = func(ctx context.Context, data *schema.ResourceData, client *apiClient) (err error) {
		var id string
		if id, err = remoteID(data.Id()); err != nil {
			return
		}
		return client.request(ctx, http.MethodDelete, "/api/v1/virtual-machines/"+id, nil, nil)
	}
	return
}

// permissionGrantOperations manages fixed account and group grants in the ownership tree.
func permissionGrantOperations() (operations remoteResourceOperations) {
	operations.Create = func(ctx context.Context, data *schema.ResourceData, client *apiClient) (err error) {
		var targetID string
		if targetID, err = remoteID(data.Get("target_id").(string)); err != nil {
			return
		}
		var subject string = data.Get("subject_id").(string)
		var subjectKind int
		var subjectID int
		var subjectName string
		if subjectID, err = strconv.Atoi(subject); err == nil {
			subjectKind = 1
		} else {
			subjectKind = 0
			subjectName = subject
		}
		var result grantResult
		err = client.request(ctx, http.MethodPost, "/api/v1/ownership-nodes/"+targetID+"/grants", map[string]any{
			"subject_kind":        subjectKind,
			"subject_id":          subjectID,
			"subject_name":        subjectName,
			"permission":          data.Get("permission").(string),
			"inherit_descendants": data.Get("scope").(string) == "descendants",
		}, &result)
		if err == nil {
			data.SetId(strconv.Itoa(result.Grant.ID))
			_ = data.Set("subject_id", grantSubjectID(result))
			_ = data.Set("target_id", strconv.Itoa(result.Grant.TargetNodeID))
			_ = data.Set("scope", grantScope(result.Grant.InheritDescendants))
		}
		return
	}
	operations.Read = func(ctx context.Context, data *schema.ResourceData, client *apiClient) (err error) {
		var id string
		if id, err = remoteID(data.Id()); err != nil {
			return
		}
		var result grantResult
		err = client.request(ctx, http.MethodGet, "/api/v1/permission-grants/"+id, nil, &result)
		if err == nil {
			_ = data.Set("permission", result.Grant.Permission)
			_ = data.Set("subject_id", grantSubjectID(result))
			_ = data.Set("target_id", strconv.Itoa(result.Grant.TargetNodeID))
			_ = data.Set("scope", grantScope(result.Grant.InheritDescendants))
		}
		return
	}
	operations.Delete = func(ctx context.Context, data *schema.ResourceData, client *apiClient) (err error) {
		var id string
		if id, err = remoteID(data.Id()); err != nil {
			return
		}
		return client.request(ctx, http.MethodDelete, "/api/v1/permission-grants/"+id, nil, nil)
	}
	return
}

// remoteID validates the numeric resource identifiers used by this API slice.
func remoteID(value string) (id string, err error) {
	var parsed int
	if parsed, err = strconv.Atoi(strings.TrimSpace(value)); err != nil || parsed < 1 {
		err = errRemoteNotFound
		return
	}
	id = strconv.Itoa(parsed)
	return
}

func parentID(value *int) (id int) {
	if value != nil {
		id = *value
	}
	return
}

func grantSubjectID(result grantResult) (id string) {
	if result.Grant.SubjectKind == 0 {
		id = result.SubjectName
	} else {
		id = strconv.Itoa(result.Grant.SubjectID)
	}
	return
}

func grantScope(inheritDescendants bool) (scope string) {
	scope = "self"
	if inheritDescendants {
		scope = "descendants"
	}
	return
}
