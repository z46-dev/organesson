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

// resourceNetwork defines a virtual network owned by one deployment or logical group.
func resourceNetwork() (resource *schema.Resource) {
	resource = localResource("network", map[string]*schema.Schema{
		"deployment_id": {
			Type:         schema.TypeString,
			Optional:     true,
			ExactlyOneOf: []string{"deployment_id", "logical_group_id"},
			Description:  "The owning deployment identifier for a shared network.",
		},
		"dhcp_enabled": {
			Type:        schema.TypeBool,
			Required:    true,
			Description: "Whether Organesson provides DHCP on this virtual network.",
		},
		"egress_policy": {
			Type:         schema.TypeString,
			Required:     true,
			ValidateFunc: validation.StringInSlice([]string{"isolated", "environment-network", "platform-route"}, false),
			Description:  "The network's explicit egress policy.",
		},
		"ipv4_gateway": optionalStringSchema("The managed IPv4 gateway address."),
		"ipv4_subnet":  optionalStringSchema("The managed IPv4 subnet in CIDR notation."),
		"logical_group_id": {
			Type:         schema.TypeString,
			Optional:     true,
			ExactlyOneOf: []string{"deployment_id", "logical_group_id"},
			Description:  "The owning logical group identifier for a private network.",
		},
		"mode": {
			Type:         schema.TypeString,
			Required:     true,
			ValidateFunc: validation.StringInSlice([]string{"managed", "unmanaged-layer-2"}, false),
			Description:  "The requested network mode.",
		},
		"name":    requiredStringSchema("The virtual network name."),
		"summary": summarySchema(),
	}, func(data *schema.ResourceData) (description string, err error) {
		var (
			dhcpEnabled  bool   = data.Get("dhcp_enabled").(bool)
			egressPolicy string = data.Get("egress_policy").(string)
			gateway      string = data.Get("ipv4_gateway").(string)
			mode         string = data.Get("mode").(string)
			subnet       string = data.Get("ipv4_subnet").(string)
			owner        string
		)

		if value := data.Get("deployment_id").(string); value != "" {
			owner = fmt.Sprintf("deployment %q", value)
		} else {
			owner = fmt.Sprintf("logical group %q", data.Get("logical_group_id").(string))
		}

		if mode == "managed" && (subnet == "" || gateway == "") {
			err = fmt.Errorf("managed network requires ipv4_subnet and ipv4_gateway")

			return
		}

		if mode == "unmanaged-layer-2" && (dhcpEnabled || subnet != "" || gateway != "") {
			err = fmt.Errorf("unmanaged-layer-2 network cannot define DHCP, ipv4_subnet, or ipv4_gateway")

			return
		}

		description = fmt.Sprintf("create %q virtual network %q in %s with egress policy %q", mode, data.Get("name").(string), owner, egressPolicy)
		if mode == "managed" {
			description += fmt.Sprintf(" using subnet %q, gateway %q, and DHCP %t", subnet, gateway, dhcpEnabled)
		}

		return
	})

	return
}

// resourceAddressPoolRequest defines a deployment request for addresses from one environment network.
func resourceAddressPoolRequest() (resource *schema.Resource) {
	resource = localResource("address-pool-request", map[string]*schema.Schema{
		"address_count": {
			Type:         schema.TypeInt,
			Required:     true,
			ValidateFunc: validation.IntAtLeast(1),
			Description:  "The number of addresses requested from the environment network.",
		},
		"address_family": {
			Type:         schema.TypeString,
			Required:     true,
			ValidateFunc: validation.StringInSlice([]string{"ipv4", "ipv6"}, false),
			Description:  "The requested address family.",
		},
		"deployment_id":       requiredStringSchema("The owning deployment identifier."),
		"environment_network": requiredStringSchema("The environment network supplying addresses."),
		"name":                requiredStringSchema("The deployment-local address pool request name."),
		"summary":             summarySchema(),
	}, func(data *schema.ResourceData) (description string, err error) {
		description = fmt.Sprintf("request %d %s addresses from environment network %q as pool %q for deployment %q", data.Get("address_count").(int), data.Get("address_family").(string), data.Get("environment_network").(string), data.Get("name").(string), data.Get("deployment_id").(string))

		return
	})

	return
}

// resourceVirtualMachine defines a catalog-template virtual machine.
func resourceVirtualMachine() (resource *schema.Resource) {
	resource = apiResource(map[string]*schema.Schema{
		"boot_disk_gib":     {Type: schema.TypeInt, Required: true, ForceNew: true, Description: "Requested boot disk size in GiB (recorded, not provisioned in simulation)."},
		"cpu_cores":         {Type: schema.TypeInt, Required: true, ForceNew: true, Description: "Requested CPU cores (recorded, not provisioned in simulation)."},
		"logical_group_id":  {Type: schema.TypeString, Required: true, ForceNew: true, Description: "Parent logical ownership node identifier."},
		"memory_mib":        {Type: schema.TypeInt, Required: true, ForceNew: true, Description: "Requested memory in MiB (recorded, not provisioned in simulation)."},
		"name":              {Type: schema.TypeString, Required: true, ForceNew: true, Description: "VM name."},
		"ownership_node_id": {Type: schema.TypeInt, Computed: true, Description: "The VM's ownership node identifier."},
		"power_state":       {Type: schema.TypeString, Computed: true, Description: "Simulated VM power state."},
		"template":          {Type: schema.TypeString, Required: true, ForceNew: true, Description: "Template catalog identifier (recorded, not provisioned in simulation)."},
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
		description = fmt.Sprintf("attach %d GiB disk %q from storage class %q to VM %q", data.Get("size_gib").(int), data.Get("name").(string), data.Get("storage_class").(string), data.Get("virtual_machine_id").(string))

		return
	})

	return
}

// resourceNetworkAttachment defines a virtual-machine network attachment.
func resourceNetworkAttachment() (resource *schema.Resource) {
	resource = localResource("network-attachment", map[string]*schema.Schema{
		"address_pool_request_id": {
			Type:         schema.TypeString,
			Optional:     true,
			RequiredWith: []string{"requested_address_count"},
			Description:  "The single environment address pool request supplying this interface.",
		},
		"environment_network": {
			Type:         schema.TypeString,
			Optional:     true,
			ExactlyOneOf: []string{"environment_network", "logical_network_id"},
			Description:  "The authorized environment network name.",
		},
		"logical_network_id": {
			Type:         schema.TypeString,
			Optional:     true,
			ExactlyOneOf: []string{"environment_network", "logical_network_id"},
			Description:  "The deployment virtual network identifier.",
		},
		"name": requiredStringSchema("The attachment name."),
		"requested_address_count": {
			Type:         schema.TypeInt,
			Optional:     true,
			RequiredWith: []string{"address_pool_request_id"},
			ValidateFunc: validation.IntAtLeast(1),
			Description:  "The number of addresses this interface consumes from its one address pool.",
		},
		"virtual_machine_id": requiredStringSchema("The attached virtual machine identifier."),
		"summary":            summarySchema(),
	}, func(data *schema.ResourceData) (description string, err error) {
		var target string

		if value := data.Get("environment_network").(string); value != "" {
			target = fmt.Sprintf("environment network %q", value)
		} else if value := data.Get("logical_network_id").(string); value != "" {
			target = fmt.Sprintf("logical network %q", value)
		} else {
			err = fmt.Errorf("one of environment_network or logical_network_id must be set")

			return
		}

		description = fmt.Sprintf("attach VM %q to %s as %q", data.Get("virtual_machine_id").(string), target, data.Get("name").(string))
		if poolRequestID, exists := data.GetOk("address_pool_request_id"); exists {
			description += fmt.Sprintf(" and request %d address(es) from pool %q", data.Get("requested_address_count").(int), poolRequestID.(string))
		}

		return
	})

	return
}

// resourceGuestNetworkConfiguration defines guest-side configuration for one attached NIC.
func resourceGuestNetworkConfiguration() (resource *schema.Resource) {
	resource = localResource("guest-network-configuration", map[string]*schema.Schema{
		"ipv4_address":          optionalStringSchema("The static IPv4 address and prefix."),
		"ipv4_gateway":          optionalStringSchema("The optional IPv4 gateway."),
		"ipv4_method":           requiredStringSchema("The IPv4 configuration method: dhcp or static."),
		"network_attachment_id": requiredStringSchema("The target network attachment identifier."),
		"summary":               summarySchema(),
	}, func(data *schema.ResourceData) (description string, err error) {
		var (
			ipv4Address string = data.Get("ipv4_address").(string)
			ipv4Gateway string = data.Get("ipv4_gateway").(string)
			ipv4Method  string = data.Get("ipv4_method").(string)
		)

		switch ipv4Method {
		case "dhcp":
			if ipv4Address != "" || ipv4Gateway != "" {
				err = fmt.Errorf("DHCP network configuration cannot define ipv4_address or ipv4_gateway")

				return
			}

			description = fmt.Sprintf("configure attachment %q for IPv4 DHCP", data.Get("network_attachment_id").(string))
		case "static":
			if ipv4Address == "" {
				err = fmt.Errorf("static network configuration requires ipv4_address")

				return
			}

			description = fmt.Sprintf("configure attachment %q with static IPv4 %q", data.Get("network_attachment_id").(string), ipv4Address)
			if ipv4Gateway != "" {
				description += fmt.Sprintf(" and gateway %q", ipv4Gateway)
			}
		default:
			err = fmt.Errorf("ipv4_method must be dhcp or static")
		}

		return
	})

	return
}

// resourceArtifact defines an immutable package built from an artifact source directory.
func resourceArtifact() (resource *schema.Resource) {
	resource = localResource("artifact", map[string]*schema.Schema{
		"deployment_id":    requiredStringSchema("The owning deployment identifier."),
		"entrypoint":       requiredStringSchema("The executable path within the source directory."),
		"source_directory": requiredStringSchema("The local source directory to package immutably."),
		"summary":          summarySchema(),
	}, func(data *schema.ResourceData) (description string, err error) {
		description = fmt.Sprintf("package source directory %q with entrypoint %q into an immutable deployment artifact for deployment %q", data.Get("source_directory").(string), data.Get("entrypoint").(string), data.Get("deployment_id").(string))

		return
	})

	return
}

// resourceGuestSetup defines a guest-scoped first-time setup operation.
func resourceGuestSetup() (resource *schema.Resource) {
	resource = localResource("guest-setup", map[string]*schema.Schema{
		"artifact_id":        requiredStringSchema("The setup artifact identifier."),
		"virtual_machine_id": requiredStringSchema("The target virtual machine identifier."),
		"summary":            summarySchema(),
	}, func(data *schema.ResourceData) (description string, err error) {
		description = fmt.Sprintf("build temporary read-only media from artifact %q, run it on VM %q, verify readiness, detach media, and delete temporary media", data.Get("artifact_id").(string), data.Get("virtual_machine_id").(string))

		return
	})

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
