package organesson

import "github.com/hashicorp/terraform-plugin-sdk/v2/helper/schema"

// Provider returns the initial local-only Organesson provider.
func Provider() (provider *schema.Provider) {
	provider = &schema.Provider{
		ResourcesMap: map[string]*schema.Resource{
			"organesson_address_reservation": resourceAddressReservation(),
			"organesson_artifact":            resourceArtifact(),
			"organesson_deployment":          resourceDeployment(),
			"organesson_guest_setup":         resourceGuestSetup(),
			"organesson_logical_group":       resourceLogicalGroup(),
			"organesson_network":             resourceNetwork(),
			"organesson_network_attachment":  resourceNetworkAttachment(),
			"organesson_virtual_machine":     resourceVirtualMachine(),
		},
	}

	return
}
