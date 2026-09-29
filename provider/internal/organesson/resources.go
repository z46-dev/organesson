package organesson

import (
	"fmt"
	"sort"
	"strings"

	"github.com/hashicorp/terraform-plugin-sdk/v2/helper/schema"
	"github.com/hashicorp/terraform-plugin-sdk/v2/helper/validation"
)

var supportedPermissions = []string{
	"deployment.manage_configuration",
	"deployment.manage_groups",
	"deployment.manage_permissions",
	"deployment.manage_users",
	"resource.view",
	"vm.console_control",
	"vm.power_control",
	"vm.snapshot_control",
}

// resourceDeployment defines a deployment root.
func resourceDeployment() (resource *schema.Resource) {
	resource = localResource("deployment", map[string]*schema.Schema{
		"environment": requiredStringSchema("The authorized Organesson environment."),
		"name":        requiredStringSchema("The deployment name."),
		"owner":       requiredStringSchema("The deployment owner group."),
		"summary":     summarySchema(),
	}, func(data *schema.ResourceData) (description string, err error) {
		description = fmt.Sprintf("create deployment %q in environment %q owned by %q", data.Get("name").(string), data.Get("environment").(string), data.Get("owner").(string))

		return
	})

	return
}

// resourceLogicalGroup defines a deployment-owned logical resource group.
func resourceLogicalGroup() (resource *schema.Resource) {
	resource = localResource("logical-group", map[string]*schema.Schema{
		"deployment_id": requiredStringSchema("The parent deployment identifier."),
		"name":          requiredStringSchema("The logical group name."),
		"owner":         requiredStringSchema("The logical group owner."),
		"summary":       summarySchema(),
	}, func(data *schema.ResourceData) (description string, err error) {
		description = fmt.Sprintf("create logical group %q under deployment %q owned by %q", data.Get("name").(string), data.Get("deployment_id").(string), data.Get("owner").(string))

		return
	})

	return
}

// resourceUserGroup defines a deployment-local group of Organesson-resolved users.
func resourceUserGroup() (resource *schema.Resource) {
	resource = localResource("user-group", map[string]*schema.Schema{
		"deployment_id": requiredStringSchema("The parent deployment identifier."),
		"members":       optionalStringSetSchema("Organesson-resolved user identities in this group."),
		"name":          requiredStringSchema("The deployment-local group name."),
		"summary":       summarySchema(),
	}, func(data *schema.ResourceData) (description string, err error) {
		var members []string = stringSetValues(data.Get("members").(*schema.Set))

		description = fmt.Sprintf("create deployment-local user group %q under deployment %q with members [%s]", data.Get("name").(string), data.Get("deployment_id").(string), strings.Join(members, ", "))

		return
	})

	return
}

// resourcePermissionGrant defines one fixed Organesson permission over a resource-tree target.
func resourcePermissionGrant() (resource *schema.Resource) {
	resource = localResource("permission-grant", map[string]*schema.Schema{
		"permission": {
			Type:         schema.TypeString,
			Required:     true,
			ValidateFunc: validation.StringInSlice(supportedPermissions, false),
			Description:  "A fixed Organesson permission.",
		},
		"scope": {
			Type:         schema.TypeString,
			Required:     true,
			ValidateFunc: validation.StringInSlice([]string{"self", "descendants"}, false),
			Description:  "Whether the grant applies only to its target or inherited descendants.",
		},
		"subject_id": requiredStringSchema("An Organesson user identity or deployment-local group identifier."),
		"target_id":  requiredStringSchema("The deployment, logical group, or resource receiving the grant."),
		"summary":    summarySchema(),
	}, func(data *schema.ResourceData) (description string, err error) {
		description = fmt.Sprintf("grant fixed permission %q to subject %q on target %q with scope %q", data.Get("permission").(string), data.Get("subject_id").(string), data.Get("target_id").(string), data.Get("scope").(string))

		return
	})

	return
}

// resourceNetwork defines a deployment-owned virtual network.
func resourceNetwork() (resource *schema.Resource) {
	resource = localResource("network", map[string]*schema.Schema{
		"logical_group_id": requiredStringSchema("The owning logical group identifier."),
		"mode":             requiredStringSchema("The requested network mode."),
		"name":             requiredStringSchema("The virtual network name."),
		"summary":          summarySchema(),
	}, func(data *schema.ResourceData) (description string, err error) {
		description = fmt.Sprintf("create %q virtual network %q in logical group %q", data.Get("mode").(string), data.Get("name").(string), data.Get("logical_group_id").(string))

		return
	})

	return
}

// resourceVirtualMachine defines a catalog-template virtual machine.
func resourceVirtualMachine() (resource *schema.Resource) {
	resource = localResource("virtual-machine", map[string]*schema.Schema{
		"cpu_cores":        requiredIntSchema("Requested CPU core count."),
		"boot_disk_gib":    requiredIntSchema("Requested boot disk size in GiB."),
		"logical_group_id": requiredStringSchema("The owning logical group identifier."),
		"memory_mib":       requiredIntSchema("Requested memory in MiB."),
		"name":             requiredStringSchema("The virtual machine name."),
		"template":         requiredStringSchema("The approved template catalog identifier."),
		"summary":          summarySchema(),
	}, func(data *schema.ResourceData) (description string, err error) {
		description = fmt.Sprintf("create VM %q in logical group %q from template %q with %d CPU cores, %d MiB memory, and a %d GiB boot disk", data.Get("name").(string), data.Get("logical_group_id").(string), data.Get("template").(string), data.Get("cpu_cores").(int), data.Get("memory_mib").(int), data.Get("boot_disk_gib").(int))

		return
	})

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
		"name":               requiredStringSchema("The attachment name."),
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

// resourceAddressReservation defines one environment-network address lease.
func resourceAddressReservation() (resource *schema.Resource) {
	resource = localResource("address-reservation", map[string]*schema.Schema{
		"address_family":        requiredStringSchema("The requested address family."),
		"environment_network":   requiredStringSchema("The authorized environment network name."),
		"network_attachment_id": requiredStringSchema("The network attachment receiving the lease."),
		"summary":               summarySchema(),
	}, func(data *schema.ResourceData) (description string, err error) {
		description = fmt.Sprintf("reserve one %s address from environment network %q for attachment %q", data.Get("address_family").(string), data.Get("environment_network").(string), data.Get("network_attachment_id").(string))

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
