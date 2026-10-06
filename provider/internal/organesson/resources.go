package organesson

import (
	"fmt"
	"sort"

	"github.com/hashicorp/terraform-plugin-sdk/v2/helper/schema"
	"github.com/hashicorp/terraform-plugin-sdk/v2/helper/validation"
	"github.com/z46-dev/organesson/backend/db"
)

var supportedPermissions []string = db.PermissionCatalog

// resourceDeployment defines a deployment root.
func resourceDeployment() (resource *schema.Resource) {
	resource = apiResource(map[string]*schema.Schema{
		"description":  {Type: schema.TypeString, Optional: true, Description: "Deployment description."},
		"environment":  {Type: schema.TypeString, Optional: true, ForceNew: true, Description: "Authorized Organesson environment label."},
		"name":         {Type: schema.TypeString, Required: true, ForceNew: true, Description: "Deployment name."},
		"owner":        {Type: schema.TypeString, Optional: true, ForceNew: true, Description: "Informational owner label; this first slice assigns the deployment to its creator."},
		"root_node_id": {Type: schema.TypeInt, Computed: true, Description: "The deployment ownership-tree root identifier."},
		"summary":      summarySchema(),
	}, deploymentOperations())

	return
}

// resourceLogicalGroup defines a deployment-owned logical resource group.
func resourceLogicalGroup() (resource *schema.Resource) {
	resource = apiResource(map[string]*schema.Schema{
		"deployment_id":  {Type: schema.TypeString, Required: true, ForceNew: true, Description: "Parent deployment identifier."},
		"name":           {Type: schema.TypeString, Required: true, ForceNew: true, Description: "Logical group name."},
		"owner":          {Type: schema.TypeString, Optional: true, ForceNew: true, Description: "Owner group identifier."},
		"parent_node_id": {Type: schema.TypeInt, Optional: true, Computed: true, ForceNew: true, Description: "Parent ownership node; defaults to the deployment root."},
		"summary":        summarySchema(),
	}, logicalGroupOperations())

	return
}

// resourceInternalGroup defines a platform-owned branch hidden from deployment users.
func resourceInternalGroup() (resource *schema.Resource) {
	resource = apiResource(map[string]*schema.Schema{
		"deployment_id":  {Type: schema.TypeString, Required: true, ForceNew: true, Description: "Parent deployment identifier."},
		"name":           {Type: schema.TypeString, Required: true, ForceNew: true, Description: "Platform-owned implementation group name."},
		"parent_node_id": {Type: schema.TypeInt, Optional: true, Computed: true, ForceNew: true, Description: "Parent ownership node; defaults to the deployment root."},
		"summary":        summarySchema(),
	}, internalGroupOperations())

	return
}

// resourceUserGroup defines a deployment-local group of Organesson-resolved users.
func resourceUserGroup() (resource *schema.Resource) {
	resource = apiResource(map[string]*schema.Schema{
		"deployment_id": {Type: schema.TypeString, Required: true, ForceNew: true, Description: "Parent deployment identifier."},
		"members":       {Type: schema.TypeSet, Optional: true, Elem: &schema.Schema{Type: schema.TypeString}, Description: "Resolved Organesson account names."},
		"name":          {Type: schema.TypeString, Required: true, ForceNew: true, Description: "Deployment-local group name."},
		"summary":       summarySchema(),
	}, userGroupOperations())

	return
}

// resourcePermissionGrant defines one fixed Organesson permission over a resource-tree target.
func resourcePermissionGrant() (resource *schema.Resource) {
	resource = apiResource(map[string]*schema.Schema{
		"permission": {
			Type:         schema.TypeString,
			Required:     true,
			ForceNew:     true,
			ValidateFunc: validation.StringInSlice(supportedPermissions, false),
			Description:  "A fixed Organesson permission.",
		},
		"scope": {
			Type:         schema.TypeString,
			Required:     true,
			ForceNew:     true,
			ValidateFunc: validation.StringInSlice([]string{"self", "descendants"}, false),
			Description:  "Whether the grant applies only to its target or inherited descendants.",
		},
		"subject_id": {Type: schema.TypeString, Required: true, ForceNew: true, Description: "An Organesson qualified user name or deployment-local group ID."},
		"target_id":  {Type: schema.TypeString, Required: true, ForceNew: true, Description: "The ownership node receiving the grant."},
		"summary":    summarySchema(),
	}, permissionGrantOperations())

	return
}

// resourceNetworkMode keeps managed and unmanaged network schemas distinct at the Tofu boundary.
func resourceNetworkMode(mode string) (resource *schema.Resource) {
	var fields map[string]*schema.Schema = map[string]*schema.Schema{
		"deployment_id": {
			Type:         schema.TypeString,
			Optional:     true,
			ForceNew:     true,
			ExactlyOneOf: []string{"deployment_id", "logical_group_id"},
			Description:  "The owning deployment identifier for a shared network.",
		},
		"dhcp_enabled": {
			Type:        schema.TypeBool,
			Required:    true,
			ForceNew:    true,
			Description: "Whether Organesson provides DHCP on this virtual network.",
		},
		"egress_policy": {
			Type:         schema.TypeString,
			Required:     true,
			ForceNew:     true,
			ValidateFunc: validation.StringInSlice([]string{"isolated"}, false),
			Description:  "The network's explicit egress policy.",
		},
		"external_vlan": externalVLANExposureSchema(),
		"ipv4_gateway":  {Type: schema.TypeString, Optional: true, ForceNew: true, Description: "The managed IPv4 gateway address."},
		"ipv4_subnet":   {Type: schema.TypeString, Optional: true, ForceNew: true, Description: "The managed IPv4 subnet in CIDR notation."},
		"ipv6_gateway":  {Type: schema.TypeString, Optional: true, ForceNew: true, Description: "The managed IPv6 gateway address."},
		"ipv6_subnet":   {Type: schema.TypeString, Optional: true, ForceNew: true, Description: "The managed IPv6 subnet in CIDR notation."},
		"logical_group_id": {
			Type:         schema.TypeString,
			Optional:     true,
			ForceNew:     true,
			ExactlyOneOf: []string{"deployment_id", "logical_group_id"},
			Description:  "The owning logical group identifier for a private network.",
		},
		"name":         {Type: schema.TypeString, Required: true, ForceNew: true, Description: "The virtual network name."},
		"power_state":  {Type: schema.TypeString, Computed: true, Description: "Proxmox SDN provisioning state."},
		"router_vmid":  {Type: schema.TypeInt, Optional: true, ForceNew: true, Description: "Deployment-managed router VM queried through QEMU Guest Agent for Router Polling."},
		"proxmox_vnet": {Type: schema.TypeString, Computed: true, Description: "The Organesson-owned Proxmox SDN VNet identifier."},
		"proxmox_zone": {Type: schema.TypeString, Computed: true, Description: "The Proxmox SDN zone containing this VNet."},
		"summary":      summarySchema(),
	}
	resource = apiResource(fields, networkOperations(mode))

	return
}

// externalVLANExposureSchema declares an optional single VLAN exposure over an authorized physical trunk.
func externalVLANExposureSchema() (field *schema.Schema) {
	field = &schema.Schema{
		Type:     schema.TypeList,
		Optional: true,
		ForceNew: true,
		MaxItems: 1,
		Elem: &schema.Resource{Schema: map[string]*schema.Schema{
			"trunk_node":   {Type: schema.TypeString, Required: true, Description: "Proxmox node hosting the authorized trunk."},
			"trunk_bridge": {Type: schema.TypeString, Required: true, Description: "Authorized host bridge on that node."},
			"vlan_id":      {Type: schema.TypeInt, Required: true, ValidateFunc: validation.IntBetween(1, 4094), Description: "VLAN tag to expose on the trunk."},
		}},
		Description: "Optional external VLAN exposure over an administrator-authorized trunk. The VNet is attached to the Proxmox VLAN zone for that bridge and tag.",
	}

	return
}

// resourceAddressPoolRequest reserves addresses from a validated platform pool or a managed VNet range.
func resourceAddressPoolRequest() (resource *schema.Resource) {
	resource = apiResource(map[string]*schema.Schema{
		"address_count": {
			Type:         schema.TypeInt,
			Required:     true,
			ForceNew:     true,
			ValidateFunc: validation.IntAtLeast(1),
			Description:  "The number of addresses reserved from the selected environment or managed-VNet range.",
		},
		"address_family": {
			Type:         schema.TypeString,
			Required:     true,
			ForceNew:     true,
			ValidateFunc: validation.StringInSlice([]string{"ipv4", "ipv6"}, false),
			Description:  "The requested address family.",
		},
		"deployment_id":       {Type: schema.TypeString, Required: true, ForceNew: true, Description: "The owning deployment identifier."},
		"environment_network": {Type: schema.TypeString, Optional: true, ForceNew: true, ExactlyOneOf: []string{"environment_network", "logical_network_id"}, Description: "The environment network supplying addresses."},
		"logical_network_id":  {Type: schema.TypeString, Optional: true, ForceNew: true, ExactlyOneOf: []string{"environment_network", "logical_network_id"}, Description: "Managed Organesson VNet supplying addresses."},
		"range_start":         {Type: schema.TypeString, Optional: true, ForceNew: true, RequiredWith: []string{"range_end", "logical_network_id"}, Description: "First IPv4 host in a managed VNet's reservable range."},
		"range_end":           {Type: schema.TypeString, Optional: true, ForceNew: true, RequiredWith: []string{"range_start", "logical_network_id"}, Description: "Last IPv4 host in a managed VNet's reservable range."},
		"name":                {Type: schema.TypeString, Required: true, ForceNew: true, Description: "The deployment-local address pool request name."},
		"addresses":           {Type: schema.TypeList, Computed: true, Elem: &schema.Schema{Type: schema.TypeString}, Description: "Persistently allocated addresses supplied by the selected network range."},
		"dns":                 {Type: schema.TypeList, Computed: true, Elem: &schema.Schema{Type: schema.TypeString}, Description: "DNS servers associated with the source network."},
		"gateway":             {Type: schema.TypeString, Computed: true, Description: "Gateway associated with the source network."},
		"prefix":              {Type: schema.TypeString, Computed: true, Description: "Source network prefix; this request does not create a subnet."},
		"pool_name":           {Type: schema.TypeString, Computed: true, Description: "Administrator-configured address pool that supplied this request."},
		"summary":             summarySchema(),
	}, addressPoolRequestOperations())

	return
}

// resourceVirtualMachine defines a catalog-template virtual machine.
func resourceVirtualMachine() (resource *schema.Resource) {
	resource = apiResource(map[string]*schema.Schema{
		"boot_disk_gib":     {Type: schema.TypeInt, Required: true, ForceNew: true, Description: "Requested boot disk size in GiB."},
		"cpu_cores":         {Type: schema.TypeInt, Required: true, ForceNew: true, Description: "Requested virtual CPU cores."},
		"logical_group_id":  {Type: schema.TypeString, Required: true, ForceNew: true, Description: "Parent logical ownership node identifier."},
		"memory_mib":        {Type: schema.TypeInt, Required: true, ForceNew: true, Description: "Requested memory in MiB."},
		"name":              {Type: schema.TypeString, Required: true, ForceNew: true, Description: "VM name."},
		"pool":              {Type: schema.TypeString, Optional: true, ForceNew: true, Description: "Authorized Proxmox resource pool; omitted when policy allows exactly one."},
		"provisioning_mode": {Type: schema.TypeString, Optional: true, Default: "simulated", ForceNew: true, ValidateFunc: validation.StringInSlice([]string{"simulated", "proxmox"}, false), Description: "Use simulated lifecycle for smoke tests or clone a real QEMU VM through Organesson."},
		"proxmox_node":      {Type: schema.TypeString, Computed: true, Description: "The Proxmox node hosting this VM."},
		"proxmox_vmid":      {Type: schema.TypeString, Computed: true, Description: "The Proxmox VMID assigned to this resource."},
		"ownership_node_id": {Type: schema.TypeInt, Computed: true, Description: "The VM's ownership-tree node identifier."},
		"power_state":       {Type: schema.TypeString, Computed: true, Description: "Current VM power state."},
		"storage":           {Type: schema.TypeString, Optional: true, ForceNew: true, Description: "Authorized Proxmox storage; omitted when policy allows exactly one."},
		"template":          {Type: schema.TypeString, Required: true, ForceNew: true, Description: "Ready Organesson VM template alias."},
		"summary":           summarySchema(),
	}, virtualMachineOperations())

	return
}

// resourceVirtualDisk defines a non-boot disk attached to one virtual machine.
func resourceVirtualDisk() (resource *schema.Resource) {
	resource = localResource("virtual-disk", map[string]*schema.Schema{
		"name":               requiredStringSchema("The virtual disk name."),
		"size_gib":           requiredIntSchema("Requested disk size in GiB."),
		"storage_class":      requiredStringSchema("The approved storage class."),
		"virtual_machine_id": requiredStringSchema("The attached virtual machine identifier."),
		"summary":            summarySchema(),
	}, func(data *schema.ResourceData) (description string, err error) {
		description = fmt.Sprintf("declare %d GiB disk %q from storage class %q for VM %q; Proxmox disk lifecycle is not implemented", data.Get("size_gib").(int), data.Get("name").(string), data.Get("storage_class").(string), data.Get("virtual_machine_id").(string))

		return
	})

	return
}

// resourceNetworkAttachment defines a virtual-machine network attachment.
func resourceNetworkAttachment() (resource *schema.Resource) {
	resource = apiResource(map[string]*schema.Schema{
		"address_pool_request_id": {
			Type:         schema.TypeString,
			Optional:     true,
			ForceNew:     true,
			RequiredWith: []string{"requested_address_count"},
			Description:  "The IPv4 environment-network or managed-VNet address request supplying this interface.",
		},
		"ipv6_address_pool_request_id": {Type: schema.TypeString, Optional: true, ForceNew: true, RequiredWith: []string{"requested_ipv6_address_count"}, Description: "The IPv6 environment-network or managed-VNet address request supplying this interface."},
		"environment_network": {
			Type:         schema.TypeString,
			Optional:     true,
			ForceNew:     true,
			ExactlyOneOf: []string{"environment_network", "logical_network_id"},
			Description:  "The authorized environment network name.",
		},
		"logical_network_id": {
			Type:         schema.TypeString,
			Optional:     true,
			ForceNew:     true,
			ExactlyOneOf: []string{"environment_network", "logical_network_id"},
			Description:  "The deployment virtual network identifier.",
		},
		"addresses":            {Type: schema.TypeList, Computed: true, Elem: &schema.Schema{Type: schema.TypeString}, Description: "IPv4 addresses claimed from the referenced address pool."},
		"ipv6_addresses":       {Type: schema.TypeList, Computed: true, Elem: &schema.Schema{Type: schema.TypeString}, Description: "IPv6 addresses claimed from the referenced address pool."},
		"address_family":       {Type: schema.TypeString, Computed: true, Description: "Address family of this interface's allocated pool."},
		"address_prefix":       {Type: schema.TypeString, Computed: true, Description: "Original prefix for the claimed environment addresses."},
		"address_gateway":      {Type: schema.TypeString, Computed: true, Description: "Original gateway for the claimed environment addresses."},
		"address_dns":          {Type: schema.TypeList, Computed: true, Elem: &schema.Schema{Type: schema.TypeString}, Description: "Original DNS servers for the claimed environment addresses."},
		"ipv6_address_prefix":  {Type: schema.TypeString, Computed: true, Description: "Original IPv6 prefix for the claimed addresses."},
		"ipv6_address_gateway": {Type: schema.TypeString, Computed: true, Description: "Original IPv6 gateway for the claimed addresses."},
		"ipv6_address_dns":     {Type: schema.TypeList, Computed: true, Elem: &schema.Schema{Type: schema.TypeString}, Description: "Original IPv6 DNS servers for the claimed addresses."},
		"mac_address":          {Type: schema.TypeString, Computed: true, Description: "Deterministic Proxmox NIC MAC address."},
		"name":                 {Type: schema.TypeString, Required: true, ForceNew: true, Description: "The attachment name."},
		"net_device":           {Type: schema.TypeString, Computed: true, Description: "Proxmox network device slot, such as net0."},
		"requested_address_count": {
			Type:         schema.TypeInt,
			Optional:     true,
			ForceNew:     true,
			RequiredWith: []string{"address_pool_request_id"},
			ValidateFunc: validation.IntAtLeast(1),
			Description:  "The number of IPv4 addresses this interface consumes from its IPv4 address pool.",
		},
		"requested_ipv6_address_count": {Type: schema.TypeInt, Optional: true, ForceNew: true, RequiredWith: []string{"ipv6_address_pool_request_id"}, ValidateFunc: validation.IntAtLeast(1), Description: "The number of IPv6 addresses this interface consumes from its IPv6 address pool."},
		"virtual_machine_id":           {Type: schema.TypeString, Required: true, ForceNew: true, Description: "The attached Proxmox-backed VM resource identifier."},
		"summary":                      summarySchema(),
	}, networkAttachmentOperations())

	return
}

// resourceGuestNetworkConfiguration defines guest-side configuration for one attached NIC.
func resourceGuestNetworkConfiguration() (resource *schema.Resource) {
	resource = apiResource(map[string]*schema.Schema{
		"ipv4_address":          {Type: schema.TypeString, Optional: true, ForceNew: true, Description: "The static IPv4 address and prefix."},
		"ipv4_dns":              {Type: schema.TypeList, Optional: true, ForceNew: true, Elem: &schema.Schema{Type: schema.TypeString}, Description: "DNS servers to configure on this interface."},
		"ipv4_gateway":          {Type: schema.TypeString, Optional: true, ForceNew: true, Description: "The optional IPv4 gateway."},
		"ipv4_method":           {Type: schema.TypeString, Optional: true, Default: "disabled", ForceNew: true, ValidateFunc: validation.StringInSlice([]string{"disabled", "dhcp", "static"}, false), Description: "The IPv4 configuration method: disabled, dhcp, or static."},
		"ipv4_never_default":    {Type: schema.TypeBool, Optional: true, ForceNew: true, Description: "Ignore this NIC as a default route, while still allowing local subnet traffic."},
		"ipv6_address":          {Type: schema.TypeString, Optional: true, ForceNew: true, Description: "The static IPv6 address and prefix."},
		"ipv6_dns":              {Type: schema.TypeList, Optional: true, ForceNew: true, Elem: &schema.Schema{Type: schema.TypeString}, Description: "IPv6 DNS servers to configure on this interface."},
		"ipv6_gateway":          {Type: schema.TypeString, Optional: true, ForceNew: true, Description: "The optional IPv6 gateway."},
		"ipv6_method":           {Type: schema.TypeString, Optional: true, Default: "disabled", ForceNew: true, ValidateFunc: validation.StringInSlice([]string{"disabled", "dhcp", "slaac", "static"}, false), Description: "The IPv6 configuration method: disabled, dhcp, slaac, or static. Filtered SLAAC requires the pool to reserve the NIC's stable EUI-64 address from its /64."},
		"ipv6_never_default":    {Type: schema.TypeBool, Optional: true, ForceNew: true, Description: "Ignore this NIC as an IPv6 default route, while still allowing local subnet traffic."},
		"network_attachment_id": {Type: schema.TypeString, Required: true, ForceNew: true, Description: "The target network attachment identifier."},
		"summary":               summarySchema(),
	}, guestNetworkConfigurationOperations())

	return
}

// resourceArtifact defines an immutable package built from an artifact source directory.
func resourceArtifact() (resource *schema.Resource) {
	resource = artifactProviderResource()

	return
}

// artifactProviderResource validates and fingerprints an immutable local artifact package.
func artifactProviderResource() (resource *schema.Resource) {
	resource = artifactResource(map[string]*schema.Schema{
		"deployment_id":    {Type: schema.TypeString, Required: true, ForceNew: true, Description: "The deployment using this local artifact."},
		"entrypoint":       {Type: schema.TypeString, Required: true, ForceNew: true, Description: "The executable path within the source directory."},
		"inline_files":     {Type: schema.TypeMap, Optional: true, ForceNew: true, Sensitive: true, Elem: &schema.Schema{Type: schema.TypeString}, Description: "Generated non-secret regular files packaged with the artifact."},
		"source_directory": {Type: schema.TypeString, Required: true, ForceNew: true, Description: "The local source directory to package immutably."},
		"file_count":       {Type: schema.TypeInt, Computed: true, Description: "Number of regular files in the package."},
		"sha256":           {Type: schema.TypeString, Computed: true, Description: "SHA-256 digest of the deterministic compressed package."},
		"size_bytes":       {Type: schema.TypeInt, Computed: true, Description: "Compressed package size in bytes."},
		"summary":          summarySchema(),
	})

	return
}

// resourceGuestSetup defines a guest-scoped first-time setup operation.
func resourceGuestSetup() (resource *schema.Resource) {
	resource = guestSetupResource()

	return
}

// stringSetValues returns sorted string values from an OpenTofu set.
func stringSetValues(values *schema.Set) (members []string) {
	var rawMembers []interface{} = values.List()

	members = make([]string, 0, len(rawMembers))
	for _, rawMember := range rawMembers {
		members = append(members, rawMember.(string))
	}

	sort.Strings(members)

	return
}
