package main

import (
	"github.com/hashicorp/terraform-plugin-sdk/v2/plugin"
	"github.com/z46-dev/organesson/provider/internal/organesson"
)

func main() {
	plugin.Serve(&plugin.ServeOpts{
		ProviderFunc: organesson.Provider,
	})
}
