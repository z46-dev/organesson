package organesson

import (
	"fmt"

	"github.com/hashicorp/terraform-plugin-sdk/v2/helper/schema"
)

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
		"disk_gib":         requiredIntSchema("Requested disk size in GiB."),
		"logical_group_id": requiredStringSchema("The owning logical group identifier."),
		"memory_mib":       requiredIntSchema("Requested memory in MiB."),
		"name":             requiredStringSchema("The virtual machine name."),
		"template":         requiredStringSchema("The approved template catalog identifier."),
		"summary":          summarySchema(),
	}, func(data *schema.ResourceData) (description string, err error) {
		description = fmt.Sprintf("create VM %q in logical group %q from template %q with %d CPU cores, %d MiB memory, and %d GiB disk", data.Get("name").(string), data.Get("logical_group_id").(string), data.Get("template").(string), data.Get("cpu_cores").(int), data.Get("memory_mib").(int), data.Get("disk_gib").(int))

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
		"static_ipv4":        optionalStringSchema("An optional static IPv4 address and prefix."),
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
		if value := data.Get("static_ipv4").(string); value != "" {
			description += fmt.Sprintf(" with static IPv4 %q", value)
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

// resourceArtifact defines immutable deployment artifact metadata.
func resourceArtifact() (resource *schema.Resource) {
	resource = localResource("artifact", map[string]*schema.Schema{
		"deployment_id": requiredStringSchema("The owning deployment identifier."),
		"path":          requiredStringSchema("The local artifact path."),
		"sha256":        requiredStringSchema("The artifact SHA-256 digest."),
		"summary":       summarySchema(),
	}, func(data *schema.ResourceData) (description string, err error) {
		description = fmt.Sprintf("register artifact %q with SHA-256 %q for deployment %q", data.Get("path").(string), data.Get("sha256").(string), data.Get("deployment_id").(string))

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
