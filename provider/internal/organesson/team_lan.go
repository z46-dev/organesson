package organesson

import (
	"context"
	"fmt"
	"log"
	"sort"
	"strings"

	"github.com/hashicorp/terraform-plugin-sdk/v2/diag"
	"github.com/hashicorp/terraform-plugin-sdk/v2/helper/schema"
)

type (
	teamLANConfig struct {
		Environment           string
		Name                  string
		Owner                 string
		PrimaryCPUCoreCount   int
		PrimaryDiskGiB        int
		PrimaryMemoryMiB      int
		SecondaryCPUCoreCount int
		SecondaryDiskGiB      int
		SecondaryMemoryMiB    int
		SetupArtifact         string
		Teams                 []string
		WorkstationTemplate   string
	}
)

// resourceTeamLAN defines the first local-only deployment expansion resource.
func resourceTeamLAN() (resource *schema.Resource) {
	resource = &schema.Resource{
		CreateContext: resourceTeamLANCreate,
		ReadContext:   resourceTeamLANRead,
		UpdateContext: resourceTeamLANUpdate,
		DeleteContext: resourceTeamLANDelete,
		Importer: &schema.ResourceImporter{
			StateContext: schema.ImportStatePassthroughContext,
		},
		Schema: map[string]*schema.Schema{
			"environment": requiredStringSchema("The logical Organesson environment name."),
			"name":        requiredStringSchema("The deployment name."),
			"owner":       requiredStringSchema("The deployment owner group."),
			"teams": {
				Type:        schema.TypeList,
				Required:    true,
				MinItems:    1,
				Description: "The team groups receiving identical logical resource groups.",
				Elem: &schema.Schema{
					Type: schema.TypeString,
				},
			},
			"workstation_template": requiredStringSchema("The approved workstation template catalog identifier."),
			"primary_cpu_cores":    requiredIntSchema("Requested primary VM CPU core count."),
			"primary_memory_mib":   requiredIntSchema("Requested primary VM memory in MiB."),
			"primary_disk_gib":     requiredIntSchema("Requested primary VM disk size in GiB."),
			"secondary_cpu_cores":  requiredIntSchema("Requested secondary VM CPU core count."),
			"secondary_memory_mib": requiredIntSchema("Requested secondary VM memory in MiB."),
			"secondary_disk_gib":   requiredIntSchema("Requested secondary VM disk size in GiB."),
			"setup_artifact":       requiredStringSchema("The deployment-supplied first-time setup artifact path."),
			"planned_actions": {
				Type:        schema.TypeList,
				Computed:    true,
				Description: "The PVE-independent actions parsed from the requested deployment.",
				Elem: &schema.Schema{
					Type: schema.TypeString,
				},
			},
		},
	}

	return
}

// requiredStringSchema returns a required string schema with a description.
func requiredStringSchema(description string) (field *schema.Schema) {
	field = &schema.Schema{
		Type:        schema.TypeString,
		Required:    true,
		Description: description,
	}

	return
}

// requiredIntSchema returns a positive required integer schema with a description.
func requiredIntSchema(description string) (field *schema.Schema) {
	field = &schema.Schema{
		Type:        schema.TypeInt,
		Required:    true,
		Description: description,
	}

	return
}

// resourceTeamLANCreate logs the parsed deployment actions without changing infrastructure.
func resourceTeamLANCreate(ctx context.Context, data *schema.ResourceData, meta interface{}) (diagnostics diag.Diagnostics) {
	var (
		config  teamLANConfig
		actions []string
		err     error
	)

	if config, err = readTeamLANConfig(data); err != nil {
		diagnostics = diag.FromErr(err)

		return
	}

	actions = buildTeamLANActions(config)

	log.Printf("[INFO] Organesson parsed deployment %q for environment %q", config.Name, config.Environment)
	for _, action := range actions {
		log.Printf("[INFO] Organesson planned action: %s", action)
	}

	if err = data.Set("planned_actions", actions); err != nil {
		diagnostics = diag.FromErr(err)

		return
	}

	data.SetId(teamLANID(config))

	return
}

// resourceTeamLANRead preserves the local-only parsed deployment state.
func resourceTeamLANRead(ctx context.Context, data *schema.ResourceData, meta interface{}) (diagnostics diag.Diagnostics) {
	return
}

// resourceTeamLANUpdate logs the newly parsed deployment actions without changing infrastructure.
func resourceTeamLANUpdate(ctx context.Context, data *schema.ResourceData, meta interface{}) (diagnostics diag.Diagnostics) {
	diagnostics = resourceTeamLANCreate(ctx, data, meta)

	return
}

// resourceTeamLANDelete logs the future cleanup boundary without changing infrastructure.
func resourceTeamLANDelete(ctx context.Context, data *schema.ResourceData, meta interface{}) (diagnostics diag.Diagnostics) {
	log.Printf("[INFO] Organesson would delete parsed deployment %q", data.Get("name").(string))
	data.SetId("")

	return
}

// readTeamLANConfig converts resource data into the local deployment model.
func readTeamLANConfig(data *schema.ResourceData) (config teamLANConfig, err error) {
	var teams []string

	if teams, err = readStringList(data.Get("teams")); err != nil {
		return
	}

	config = teamLANConfig{
		Environment:           data.Get("environment").(string),
		Name:                  data.Get("name").(string),
		Owner:                 data.Get("owner").(string),
		PrimaryCPUCoreCount:   data.Get("primary_cpu_cores").(int),
		PrimaryDiskGiB:        data.Get("primary_disk_gib").(int),
		PrimaryMemoryMiB:      data.Get("primary_memory_mib").(int),
		SecondaryCPUCoreCount: data.Get("secondary_cpu_cores").(int),
		SecondaryDiskGiB:      data.Get("secondary_disk_gib").(int),
		SecondaryMemoryMiB:    data.Get("secondary_memory_mib").(int),
		SetupArtifact:         data.Get("setup_artifact").(string),
		Teams:                 teams,
		WorkstationTemplate:   data.Get("workstation_template").(string),
	}

	return
}

// readStringList validates and converts an OpenTofu list value.
func readStringList(value interface{}) (values []string, err error) {
	var (
		items []interface{}
		ok    bool
	)

	if items, ok = value.([]interface{}); !ok {
		err = fmt.Errorf("expected a list of team names")

		return
	}

	values = make([]string, 0, len(items))
	for _, item := range items {
		var (
			teamName string
			isString bool
		)

		if teamName, isString = item.(string); !isString || strings.TrimSpace(teamName) == "" {
			err = fmt.Errorf("team names must be non-empty strings")

			return
		}

		values = append(values, teamName)
	}

	sort.Strings(values)

	return
}

// buildTeamLANActions expands the PVE-independent Team LAN deployment plan.
func buildTeamLANActions(config teamLANConfig) (actions []string) {
	actions = make([]string, 0, len(config.Teams)*7)

	for _, teamName := range config.Teams {
		actions = append(actions,
			fmt.Sprintf("create logical group %q under deployment %q", teamName, config.Name),
			fmt.Sprintf("create unmanaged Layer 2 network %q for team %q", "LAN", teamName),
			fmt.Sprintf("create primary VM for team %q from template %q with %d CPU cores, %d MiB memory, and %d GiB disk", teamName, config.WorkstationTemplate, config.PrimaryCPUCoreCount, config.PrimaryMemoryMiB, config.PrimaryDiskGiB),
			fmt.Sprintf("reserve one IPv4 address from environment network %q for primary VM of team %q", "cyber.lab", teamName),
			fmt.Sprintf("create secondary VM for team %q from template %q with %d CPU cores, %d MiB memory, and %d GiB disk", teamName, config.WorkstationTemplate, config.SecondaryCPUCoreCount, config.SecondaryMemoryMiB, config.SecondaryDiskGiB),
			fmt.Sprintf("build a temporary read-only setup ISO from artifact %q for team %q", config.SetupArtifact, teamName),
			fmt.Sprintf("run the setup artifact from temporary media on each VM of team %q, verify readiness, detach the media, and delete the ISO", teamName),
		)
	}

	return
}

// teamLANID produces a stable local identifier until Organesson assigns one.
func teamLANID(config teamLANConfig) (id string) {
	id = strings.Join([]string{"local", config.Environment, config.Name}, ":")

	return
}
