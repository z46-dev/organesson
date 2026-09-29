package organesson

import "github.com/hashicorp/terraform-plugin-sdk/v2/helper/schema"

// Provider returns the initial local-only Organesson provider.
func Provider() (provider *schema.Provider) {
	provider = &schema.Provider{
		ResourcesMap: map[string]*schema.Resource{
			"organesson_team_lan": resourceTeamLAN(),
		},
	}

	return
}
