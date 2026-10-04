package organesson

import (
	"context"

	"github.com/hashicorp/terraform-plugin-sdk/v2/diag"
	"github.com/hashicorp/terraform-plugin-sdk/v2/helper/schema"
)

// Provider returns the initial local-only Organesson provider.
func Provider() (provider *schema.Provider) {
	provider = &schema.Provider{
		Schema: map[string]*schema.Schema{
			"endpoint": {
				Type:        schema.TypeString,
				Optional:    true,
				DefaultFunc: schema.EnvDefaultFunc("ORGANESSON_ENDPOINT", "http://127.0.0.1:6800"),
				Description: "Organesson API origin. Set ORGANESSON_ENDPOINT to override.",
			},
			"ca_cert_file": {
				Type:        schema.TypeString,
				Optional:    true,
				DefaultFunc: schema.EnvDefaultFunc("ORGANESSON_CA_CERT", ""),
				Description: "Optional PEM CA certificate file for a private Organesson API certificate. Set ORGANESSON_CA_CERT to override.",
			},
			"token": {
				Type:        schema.TypeString,
				Optional:    true,
				Sensitive:   true,
				DefaultFunc: schema.EnvDefaultFunc("ORGANESSON_TOKEN", nil),
				Description: "Personal API token. Set ORGANESSON_TOKEN; do not store it in OpenTofu configuration.",
			},
		},
		ConfigureContextFunc: configureProvider,
		ResourcesMap: map[string]*schema.Resource{
			"organesson_address_pool_request":        resourceAddressPoolRequest(),
			"organesson_artifact":                    resourceArtifact(),
			"organesson_deployment":                  resourceDeployment(),
			"organesson_guest_network_configuration": resourceGuestNetworkConfiguration(),
			"organesson_guest_setup":                 resourceGuestSetup(),
			"organesson_logical_group":               resourceLogicalGroup(),
			"organesson_network":                     resourceNetwork(),
			"organesson_network_attachment":          resourceNetworkAttachment(),
			"organesson_router":                      resourceRouter(),
			"organesson_permission_grant":            resourcePermissionGrant(),
			"organesson_user_group":                  resourceUserGroup(),
			"organesson_virtual_disk":                resourceVirtualDisk(),
			"organesson_virtual_machine":             resourceVirtualMachine(),
		},
	}

	return
}

// configureProvider validates provider settings and builds its API client.
func configureProvider(ctx context.Context, data *schema.ResourceData) (meta interface{}, diagnostics diag.Diagnostics) {
	var client *apiClient
	var err error
	if client, err = configuredClient(data.Get("endpoint").(string), data.Get("token").(string), data.Get("ca_cert_file").(string)); err != nil {
		diagnostics = diag.FromErr(err)
		return
	}
	meta = client
	return
}
