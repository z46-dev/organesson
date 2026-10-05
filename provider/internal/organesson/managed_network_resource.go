package organesson

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/netip"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/hashicorp/terraform-plugin-sdk/v2/diag"
	"github.com/hashicorp/terraform-plugin-sdk/v2/helper/schema"
	"github.com/hashicorp/terraform-plugin-sdk/v2/helper/validation"
)

// resourceManagedNetwork creates and owns the complete Organesson managed-network stack.
func resourceManagedNetwork() (resource *schema.Resource) {
	resource = &schema.Resource{
		CreateContext: managedNetworkCreate,
		ReadContext:   managedNetworkRead,
		DeleteContext: managedNetworkDelete,
		Importer:      &schema.ResourceImporter{StateContext: schema.ImportStatePassthroughContext},
		Schema: map[string]*schema.Schema{
			"deployment_id":                  {Type: schema.TypeString, Optional: true, ForceNew: true, ExactlyOneOf: []string{"deployment_id", "logical_group_id"}, Description: "The owning deployment identifier."},
			"logical_group_id":               {Type: schema.TypeString, Optional: true, ForceNew: true, ExactlyOneOf: []string{"deployment_id", "logical_group_id"}, Description: "The owning logical group identifier."},
			"name":                           {Type: schema.TypeString, Required: true, ForceNew: true, Description: "Managed network name."},
			"ipv4_subnet":                    {Type: schema.TypeString, Required: true, ForceNew: true, ValidateFunc: validation.IsCIDR, Description: "Managed IPv4 subnet in CIDR notation."},
			"ipv4_gateway":                   {Type: schema.TypeString, Required: true, ForceNew: true, Description: "Router address inside the managed subnet."},
			"dhcp_start":                     {Type: schema.TypeString, Required: true, ForceNew: true, Description: "First IPv4 address offered by the router DHCP service."},
			"dhcp_end":                       {Type: schema.TypeString, Required: true, ForceNew: true, Description: "Last IPv4 address offered by the router DHCP service."},
			"dns_servers":                    {Type: schema.TypeSet, Required: true, ForceNew: true, MinItems: 1, Elem: &schema.Schema{Type: schema.TypeString}, Description: "DNS servers advertised to DHCP clients."},
			"router_template":                {Type: schema.TypeString, Required: true, ForceNew: true, Description: "Ready Organesson Debian router template alias."},
			"router_pool":                    {Type: schema.TypeString, Required: true, ForceNew: true, Description: "Authorized Proxmox resource pool for the hidden router VM."},
			"router_storage":                 {Type: schema.TypeString, Required: true, ForceNew: true, Description: "Authorized Proxmox storage for the hidden router VM."},
			"router_cpu_cores":               {Type: schema.TypeInt, Optional: true, Default: 1, ForceNew: true, ValidateFunc: validation.IntAtLeast(1), Description: "Virtual CPU cores for the hidden router VM."},
			"router_memory_mib":              {Type: schema.TypeInt, Optional: true, Default: 2048, ForceNew: true, ValidateFunc: validation.IntAtLeast(512), Description: "Memory in MiB for the hidden router VM."},
			"router_boot_disk_gib":           {Type: schema.TypeInt, Optional: true, Default: 16, ForceNew: true, ValidateFunc: validation.IntAtLeast(8), Description: "Boot disk size in GiB for the hidden router VM."},
			"egress_enabled":                 {Type: schema.TypeBool, Optional: true, Default: false, ForceNew: true, Description: "Attach the router to an authorized environment network and enable filtered NAT egress."},
			"egress_environment_network":     {Type: schema.TypeString, Optional: true, ForceNew: true, Description: "Authorized environment network used as the router's WAN."},
			"egress_address_pool_request_id": {Type: schema.TypeString, Optional: true, ForceNew: true, Description: "Optional reserved address request for static router egress."},
			"egress_ipv4_method":             {Type: schema.TypeString, Optional: true, Default: "dhcp", ForceNew: true, ValidateFunc: validation.StringInSlice([]string{"dhcp", "static"}, false), Description: "IPv4 configuration method for the router WAN."},
			"power_state":                    {Type: schema.TypeString, Computed: true, Description: "Proxmox SDN provisioning state."},
			"proxmox_vnet":                   {Type: schema.TypeString, Computed: true, Description: "Organesson-owned Proxmox SDN VNet identifier."},
			"proxmox_zone":                   {Type: schema.TypeString, Computed: true, Description: "Proxmox SDN zone containing this VNet."},
			"router_vmid":                    {Type: schema.TypeInt, Computed: true, Description: "Platform-only router VMID, retained for Router Polling."},
			"router_resource_id":             {Type: schema.TypeString, Computed: true, Description: "Platform-owned Organesson VM record for the router."},
			"internal_group_id":              {Type: schema.TypeString, Computed: true, Description: "Platform-owned hidden ownership branch for the router."},
			"lan_attachment_id":              {Type: schema.TypeString, Computed: true, Description: "Platform-owned router LAN attachment."},
			"egress_attachment_id":           {Type: schema.TypeString, Computed: true, Description: "Platform-owned router WAN attachment, if enabled."},
			"summary":                        summarySchema(),
		},
	}
	return
}

// managedNetworkCreate provisions the VNet, hidden router, interfaces, and guest network services as one resource.
func managedNetworkCreate(ctx context.Context, data *schema.ResourceData, meta interface{}) (diagnostics diag.Diagnostics) {
	var client *apiClient = meta.(*apiClient)
	var deploymentID string
	var networkParentID int
	var err error
	if deploymentID, networkParentID, err = resolveManagedNetworkParent(ctx, client, data); err != nil {
		diagnostics = diag.FromErr(err)
		return
	}
	var subnet netip.Prefix
	if subnet, err = netip.ParsePrefix(data.Get("ipv4_subnet").(string)); err != nil || !subnet.Addr().Is4() || subnet != subnet.Masked() {
		diagnostics = diag.Errorf("managed network subnet must be a canonical IPv4 CIDR")
		return
	}
	var gateway netip.Addr
	if gateway, err = netip.ParseAddr(data.Get("ipv4_gateway").(string)); err != nil || !gateway.Is4() || !subnet.Contains(gateway) {
		diagnostics = diag.Errorf("managed network gateway must be an IPv4 host within its subnet")
		return
	}
	var dhcpStart netip.Addr
	if dhcpStart, err = netip.ParseAddr(data.Get("dhcp_start").(string)); err != nil {
		diagnostics = diag.Errorf("DHCP start must be a valid IPv4 address")
		return
	}
	var dhcpEnd netip.Addr
	if dhcpEnd, err = netip.ParseAddr(data.Get("dhcp_end").(string)); err != nil || !subnet.Contains(dhcpStart) || !subnet.Contains(dhcpEnd) || dhcpStart.Compare(dhcpEnd) > 0 || dhcpStart.Compare(gateway) <= 0 && dhcpEnd.Compare(gateway) >= 0 || dhcpStart == subnet.Masked().Addr() || dhcpEnd == routerBroadcastAddress(subnet) {
		diagnostics = diag.Errorf("DHCP range must be ordered IPv4 hosts in the subnet, excluding the gateway, network, and broadcast addresses")
		return
	}
	if data.Get("egress_enabled").(bool) && data.Get("egress_environment_network").(string) == "" {
		diagnostics = diag.Errorf("egress_environment_network is required when egress_enabled is true")
		return
	}
	if data.Get("egress_enabled").(bool) && data.Get("egress_ipv4_method").(string) == "static" && data.Get("egress_address_pool_request_id").(string) == "" {
		diagnostics = diag.Errorf("static egress requires egress_address_pool_request_id")
		return
	}
	var dnsServers []string
	for _, entry := range data.Get("dns_servers").(*schema.Set).List() {
		var address netip.Addr
		if address, err = netip.ParseAddr(entry.(string)); err != nil || !address.Is4() {
			diagnostics = diag.Errorf("DNS servers must be valid IPv4 addresses")
			return
		}
		dnsServers = append(dnsServers, address.String())
	}

	var group nodeResult
	var routerParentID int
	if routerParentID, err = managedDeploymentRootID(ctx, client, deploymentID); err != nil {
		diagnostics = diag.FromErr(err)
		return
	}
	if err = client.request(ctx, http.MethodPost, "/api/v1/deployments/"+deploymentID+"/logical-groups", map[string]any{
		"name": data.Get("name").(string) + "-router", "parent_node_id": routerParentID, "internal": true,
	}, &group); err != nil {
		diagnostics = diag.FromErr(err)
		return
	}
	var groupID string = strconv.Itoa(group.Node.ID)
	var vm vmResult
	if err = client.request(ctx, http.MethodPost, "/api/v1/deployments/"+deploymentID+"/virtual-machines", map[string]any{
		"parent_node_id": group.Node.ID, "name": data.Get("name").(string) + "-router", "provisioning_mode": "proxmox",
		"template": data.Get("router_template").(string), "pool": data.Get("router_pool").(string), "storage": data.Get("router_storage").(string),
		"cpu_cores": data.Get("router_cpu_cores").(int), "memory_mib": data.Get("router_memory_mib").(int), "boot_disk_gib": data.Get("router_boot_disk_gib").(int),
	}, &vm); err != nil {
		_ = client.request(ctx, http.MethodDelete, "/api/v1/ownership-nodes/"+groupID, nil, nil)
		diagnostics = diag.FromErr(err)
		return
	}
	var vmResourceID string = strconv.Itoa(vm.Resource.ID)
	var vmID int
	if vmID, err = strconv.Atoi(vm.Resource.ExternalID); err != nil {
		diagnostics = diag.Errorf("Organesson returned an invalid router VMID")
		managedNetworkRollback(ctx, client, "", "", "", vmResourceID, groupID)
		return
	}
	var network networkResult
	if err = client.request(ctx, http.MethodPost, "/api/v1/deployments/"+deploymentID+"/networks", map[string]any{
		"parent_node_id": networkParentID, "name": data.Get("name").(string), "mode": "managed",
		"ipv4_subnet": subnet.String(), "ipv4_gateway": gateway.String(), "dhcp_enabled": true,
		"egress_policy": "isolated", "router_vmid": vmID,
	}, &network); err != nil {
		managedNetworkRollback(ctx, client, "", "", "", vmResourceID, groupID)
		diagnostics = diag.FromErr(err)
		return
	}
	var networkID string = strconv.Itoa(network.Resource.ID)
	var lan networkAttachmentResult
	if err = client.request(ctx, http.MethodPost, "/api/v1/virtual-machines/"+vmResourceID+"/network-attachments", map[string]any{
		"name": "lan", "environment_network": "", "address_pool_request_id": 0, "requested_address_count": 0, "logical_network_id": network.Resource.ID,
	}, &lan); err != nil {
		managedNetworkRollback(ctx, client, "", "", networkID, vmResourceID, groupID)
		diagnostics = diag.FromErr(err)
		return
	}
	var lanID string = strconv.Itoa(lan.Resource.ID)
	if err = createGuestNetworkConfiguration(ctx, client, lanID, map[string]any{
		"ipv4_method": "static", "ipv4_address": gateway.String() + "/" + strconv.Itoa(subnet.Bits()), "ipv4_gateway": "", "ipv4_dns": []string{}, "ipv4_never_default": true,
	}); err != nil {
		managedNetworkRollback(ctx, client, lanID, "", networkID, vmResourceID, groupID)
		diagnostics = diag.FromErr(err)
		return
	}
	var wanID string
	if data.Get("egress_enabled").(bool) {
		var wan networkAttachmentResult
		var poolID int
		if raw := data.Get("egress_address_pool_request_id").(string); raw != "" {
			if poolID, err = strconv.Atoi(raw); err != nil {
				managedNetworkRollback(ctx, client, lanID, "", networkID, vmResourceID, groupID)
				diagnostics = diag.FromErr(err)
				return
			}
		}
		var requested int
		if data.Get("egress_ipv4_method").(string) == "static" {
			requested = 1
		}
		if err = client.request(ctx, http.MethodPost, "/api/v1/virtual-machines/"+vmResourceID+"/network-attachments", map[string]any{
			"name": "egress", "environment_network": data.Get("egress_environment_network").(string), "address_pool_request_id": poolID, "requested_address_count": requested, "logical_network_id": 0,
		}, &wan); err != nil {
			managedNetworkRollback(ctx, client, lanID, "", networkID, vmResourceID, groupID)
			diagnostics = diag.FromErr(err)
			return
		}
		wanID = strconv.Itoa(wan.Resource.ID)
		var guestConfig map[string]any = map[string]any{"ipv4_method": data.Get("egress_ipv4_method").(string), "ipv4_address": "", "ipv4_gateway": "", "ipv4_dns": []string{}, "ipv4_never_default": false}
		if data.Get("egress_ipv4_method").(string) == "static" {
			if len(wan.Configuration.Addresses) != 1 || wan.Configuration.AddressPrefix == "" {
				managedNetworkRollback(ctx, client, lanID, wanID, networkID, vmResourceID, groupID)
				diagnostics = diag.Errorf("static router egress did not receive one address and its network prefix")
				return
			}
			prefix, prefixErr := netip.ParsePrefix(wan.Configuration.AddressPrefix)
			if prefixErr != nil {
				managedNetworkRollback(ctx, client, lanID, wanID, networkID, vmResourceID, groupID)
				diagnostics = diag.Errorf("router egress source returned an invalid IPv4 prefix")
				return
			}
			guestConfig["ipv4_address"] = wan.Configuration.Addresses[0] + "/" + strconv.Itoa(prefix.Bits())
			guestConfig["ipv4_gateway"] = wan.Configuration.AddressGateway
			guestConfig["ipv4_dns"] = wan.Configuration.AddressDNS
		}
		if err = createGuestNetworkConfiguration(ctx, client, wanID, guestConfig); err != nil {
			managedNetworkRollback(ctx, client, lanID, wanID, networkID, vmResourceID, groupID)
			diagnostics = diag.FromErr(err)
			return
		}
	}
	if err = executeManagedRouter(ctx, client, vmResourceID, lanID, wanID, gateway.String(), subnet.String(), dhcpStart.String(), dhcpEnd.String(), dnsServers); err != nil {
		managedNetworkRollback(ctx, client, lanID, wanID, networkID, vmResourceID, groupID)
		diagnostics = diag.FromErr(err)
		return
	}
	data.SetId(networkID)
	_ = data.Set("router_vmid", vmID)
	_ = data.Set("router_resource_id", vmResourceID)
	_ = data.Set("internal_group_id", groupID)
	_ = data.Set("lan_attachment_id", lanID)
	_ = data.Set("egress_attachment_id", wanID)
	_ = data.Set("power_state", network.Resource.PowerState)
	_ = data.Set("proxmox_vnet", network.Resource.ExternalID)
	_ = data.Set("proxmox_zone", network.Resource.ExternalNode)
	_ = data.Set("summary", fmt.Sprintf("managed VNet %q with platform-owned router, DHCP, DNS, and configured egress", data.Get("name")))
	return
}

// resolveManagedNetworkParent finds the deployment API route and network ownership parent.
func resolveManagedNetworkParent(ctx context.Context, client *apiClient, data *schema.ResourceData) (deploymentID string, parentID int, err error) {
	if raw := data.Get("deployment_id").(string); raw != "" {
		if deploymentID, err = remoteID(raw); err != nil {
			return
		}
		var result deploymentResult
		if err = client.request(ctx, http.MethodGet, "/api/v1/deployments/"+deploymentID, nil, &result); err != nil {
			return
		}
		if result.Deployment.RootNodeID == nil {
			err = fmt.Errorf("Organesson deployment has no ownership root")
			return
		}
		parentID = *result.Deployment.RootNodeID
		return
	}
	var logicalGroupID string
	if logicalGroupID, err = remoteID(data.Get("logical_group_id").(string)); err != nil {
		return
	}
	var parent nodeResult
	if err = client.request(ctx, http.MethodGet, "/api/v1/ownership-nodes/"+logicalGroupID, nil, &parent); err != nil {
		return
	}
	deploymentID = strconv.Itoa(parent.Node.DeploymentID)
	parentID = parent.Node.ID
	return
}

func managedDeploymentRootID(ctx context.Context, client *apiClient, deploymentID string) (id int, err error) {
	var result deploymentResult
	if err = client.request(ctx, http.MethodGet, "/api/v1/deployments/"+deploymentID, nil, &result); err != nil {
		return
	}
	if result.Deployment.RootNodeID == nil {
		err = fmt.Errorf("Organesson deployment has no ownership root")
		return
	}
	id = *result.Deployment.RootNodeID
	return
}

func createGuestNetworkConfiguration(ctx context.Context, client *apiClient, attachmentID string, body map[string]any) (err error) {
	var id string
	if id, err = remoteID(attachmentID); err != nil {
		return
	}
	return client.request(ctx, http.MethodPost, "/api/v1/network-attachments/"+id+"/guest-network-configuration", body, nil)
}

// executeManagedRouter packages and applies the hidden router's DHCP/DNS and firewall configuration.
func executeManagedRouter(ctx context.Context, client *apiClient, vmResourceID string, lanID string, wanID string, gateway string, subnet string, dhcpStart string, dhcpEnd string, dns []string) (err error) {
	var vmID string
	if vmID, err = remoteID(vmResourceID); err != nil {
		return
	}
	var lanMAC string
	if lanMAC, err = routerAttachmentMAC(ctx, client, lanID); err != nil {
		return
	}
	var wanMAC string
	if wanID != "" {
		if wanMAC, err = routerAttachmentMAC(ctx, client, wanID); err != nil {
			return
		}
	}
	var script []byte
	if script, err = routerAssets.ReadFile("assets/router-linux.sh"); err != nil {
		return
	}
	var directory string
	if directory, err = os.MkdirTemp("", "organesson-router-"); err != nil {
		return
	}
	defer os.RemoveAll(directory)
	if err = os.WriteFile(filepath.Join(directory, "router.sh"), script, 0755); err != nil {
		return
	}
	var configuration string = strings.Join([]string{
		"LAN_MAC=" + shellQuoted(lanMAC), "WAN_MAC=" + shellQuoted(wanMAC), "LAN_ADDRESS=" + shellQuoted(gateway),
		"LAN_SUBNET=" + shellQuoted(subnet), "DHCP_START=" + shellQuoted(dhcpStart), "DHCP_END=" + shellQuoted(dhcpEnd),
		"DNS_SERVERS=(" + strings.Join(dns, " ") + ")",
	}, "\n") + "\n"
	if err = os.WriteFile(filepath.Join(directory, "organesson-router.conf"), []byte(configuration), 0600); err != nil {
		return
	}
	var artifact artifactPackage
	if artifact, err = packageArtifact(directory, "router.sh"); err != nil {
		return
	}
	var result struct {
		Execution guestSetupResult `json:"execution"`
	}
	if err = client.requestBytes(ctx, http.MethodPost, "/api/v1/virtual-machines/"+vmID+"/guest-setup", "application/vnd.organesson.artifact+gzip", map[string]string{
		"X-Organesson-Artifact-SHA256": artifact.SHA256, "X-Organesson-Artifact-Entrypoint": "router.sh",
	}, artifact.Archive, &result); err != nil {
		return
	}
	if result.Execution.SHA256 != artifact.SHA256 || result.Execution.Status != "succeeded" || result.Execution.ExitCode != 0 {
		err = fmt.Errorf("Organesson did not confirm successful router configuration")
	}
	return
}

// managedNetworkRollback best-effort removes every child already created during a failed composite apply.
func managedNetworkRollback(ctx context.Context, client *apiClient, lanID string, wanID string, networkID string, vmID string, groupID string) {
	var operations []struct {
		path string
	}
	for _, attachmentID := range []string{wanID, lanID} {
		if attachmentID != "" {
			operations = append(operations,
				struct{ path string }{"/api/v1/network-attachments/" + attachmentID + "/guest-network-configuration"},
				struct{ path string }{"/api/v1/network-attachments/" + attachmentID},
			)
		}
	}
	if networkID != "" {
		operations = append(operations, struct{ path string }{"/api/v1/networks/" + networkID})
	}
	if vmID != "" {
		operations = append(operations, struct{ path string }{"/api/v1/virtual-machines/" + vmID})
	}
	if groupID != "" {
		operations = append(operations, struct{ path string }{"/api/v1/ownership-nodes/" + groupID})
	}
	for _, operation := range operations {
		_ = client.request(ctx, http.MethodDelete, operation.path, nil, nil)
	}
}

// managedNetworkRead refreshes the network and Proxmox presentation fields without exposing router ownership.
func managedNetworkRead(ctx context.Context, data *schema.ResourceData, meta interface{}) (diagnostics diag.Diagnostics) {
	var client *apiClient = meta.(*apiClient)
	var id string
	var err error
	if id, err = remoteID(data.Id()); err != nil {
		diagnostics = diag.FromErr(err)
		return
	}
	var result networkResult
	if err = client.request(ctx, http.MethodGet, "/api/v1/networks/"+id, nil, &result); err != nil {
		if errors.Is(err, errRemoteNotFound) {
			managedNetworkRollback(ctx, client, data.Get("lan_attachment_id").(string), data.Get("egress_attachment_id").(string), data.Id(), data.Get("router_resource_id").(string), data.Get("internal_group_id").(string))
			data.SetId("")
		} else {
			diagnostics = diag.FromErr(err)
		}
		return
	}
	_ = data.Set("name", result.Resource.Name)
	_ = data.Set("ipv4_subnet", result.Configuration.Request.Subnet)
	_ = data.Set("ipv4_gateway", result.Configuration.Request.Gateway)
	_ = data.Set("power_state", result.Resource.PowerState)
	_ = data.Set("proxmox_vnet", result.Resource.ExternalID)
	_ = data.Set("proxmox_zone", result.Resource.ExternalNode)
	_ = data.Set("router_vmid", result.Configuration.Request.RouterVMID)
	return
}

// managedNetworkDelete removes network attachments, VNet, router VM, and hidden ownership branch.
func managedNetworkDelete(ctx context.Context, data *schema.ResourceData, meta interface{}) (diagnostics diag.Diagnostics) {
	var client *apiClient = meta.(*apiClient)
	var operations []string
	var wanID string = data.Get("egress_attachment_id").(string)
	var lanID string = data.Get("lan_attachment_id").(string)
	for _, attachmentID := range []string{wanID, lanID} {
		if attachmentID != "" {
			operations = append(operations, "/api/v1/network-attachments/"+attachmentID+"/guest-network-configuration", "/api/v1/network-attachments/"+attachmentID)
		}
	}
	operations = append(operations, "/api/v1/networks/"+data.Id())
	if routerID := data.Get("router_resource_id").(string); routerID != "" {
		operations = append(operations, "/api/v1/virtual-machines/"+routerID)
	}
	if groupID := data.Get("internal_group_id").(string); groupID != "" {
		operations = append(operations, "/api/v1/ownership-nodes/"+groupID)
	}
	for _, path := range operations {
		if err := client.request(ctx, http.MethodDelete, path, nil, nil); err != nil && err != errRemoteNotFound {
			diagnostics = diag.FromErr(err)
			return
		}
	}
	data.SetId("")
	return
}
