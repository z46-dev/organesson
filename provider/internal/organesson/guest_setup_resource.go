package organesson

import (
	"context"
	"fmt"

	"github.com/hashicorp/terraform-plugin-sdk/v2/diag"
	"github.com/hashicorp/terraform-plugin-sdk/v2/helper/schema"
)

type guestSetupResult struct {
	SHA256   string `json:"sha256"`
	Status   string `json:"status"`
	ExitCode int    `json:"exit_code"`
}

// guestSetupResource performs one audited guest operation while retaining only the result metadata in state.
func guestSetupResource() (resource *schema.Resource) {
	resource = &schema.Resource{
		CreateContext: guestSetupCreate,
		ReadContext:   guestSetupRead,
		DeleteContext: guestSetupDelete,
		Importer:      &schema.ResourceImporter{StateContext: schema.ImportStatePassthroughContext},
		Schema: map[string]*schema.Schema{
			"artifact_id":        {Type: schema.TypeString, Required: true, ForceNew: true, Description: "The local artifact manifest resource identifier."},
			"entrypoint":         {Type: schema.TypeString, Required: true, ForceNew: true, Description: "The entrypoint path within the artifact package."},
			"execution_status":   {Type: schema.TypeString, Computed: true, Description: "Guest execution result."},
			"exit_code":          {Type: schema.TypeInt, Computed: true, Description: "Guest entrypoint exit code."},
			"inline_files":       {Type: schema.TypeMap, Optional: true, ForceNew: true, Sensitive: true, Elem: &schema.Schema{Type: schema.TypeString}, Description: "Generated non-secret regular files packaged with the artifact."},
			"sha256":             {Type: schema.TypeString, Required: true, ForceNew: true, Description: "Expected SHA-256 digest from the artifact resource."},
			"source_directory":   {Type: schema.TypeString, Required: true, ForceNew: true, Description: "Local package source directory; artifact bytes are sent only during apply."},
			"virtual_machine_id": {Type: schema.TypeString, Required: true, ForceNew: true, Description: "Target Proxmox-backed VM resource identifier."},
		},
	}
	return
}

// guestSetupCreate verifies the package digest and asks Organesson to execute it on the managed guest.
func guestSetupCreate(ctx context.Context, data *schema.ResourceData, meta interface{}) (diagnostics diag.Diagnostics) {
	var client *apiClient = meta.(*apiClient)
	var artifact artifactPackage
	var err error
	if artifact, err = packageArtifactWithFiles(data.Get("source_directory").(string), data.Get("entrypoint").(string), artifactInlineFiles(data.Get("inline_files"))); err != nil {
		diagnostics = diag.FromErr(err)
		return
	}
	if artifact.SHA256 != data.Get("sha256").(string) {
		diagnostics = diag.Errorf("artifact source changed after its manifest was planned; run OpenTofu plan again")
		return
	}
	var vmID string
	if vmID, err = remoteID(data.Get("virtual_machine_id").(string)); err != nil {
		diagnostics = diag.FromErr(err)
		return
	}
	var result struct {
		Execution guestSetupResult `json:"execution"`
	}
	if err = client.requestBytes(ctx, "POST", "/api/v1/virtual-machines/"+vmID+"/guest-setup", "application/vnd.organesson.artifact+gzip", map[string]string{
		"X-Organesson-Artifact-SHA256":     artifact.SHA256,
		"X-Organesson-Artifact-Entrypoint": data.Get("entrypoint").(string),
	}, artifact.Archive, &result); err != nil {
		diagnostics = diag.FromErr(err)
		return
	}
	if result.Execution.SHA256 != artifact.SHA256 || result.Execution.Status != "succeeded" || result.Execution.ExitCode != 0 {
		diagnostics = diag.Errorf("Organesson did not confirm successful guest artifact execution")
		return
	}
	if err = data.Set("execution_status", result.Execution.Status); err != nil {
		diagnostics = diag.FromErr(err)
		return
	}
	if err = data.Set("exit_code", result.Execution.ExitCode); err != nil {
		diagnostics = diag.FromErr(err)
		return
	}
	data.SetId(localResourceID("guest-setup", fmt.Sprintf("%s:%s:%s", vmID, data.Get("artifact_id").(string), artifact.SHA256)))
	return
}

// artifactInlineFiles converts schema map values into generated artifact contents.
func artifactInlineFiles(value interface{}) (files map[string]string) {
	files = make(map[string]string)
	if values, ok := value.(map[string]interface{}); ok {
		for filePath, rawContents := range values {
			if contents, ok := rawContents.(string); ok {
				files[filePath] = contents
			}
		}
	}
	return
}

// guestSetupRead keeps this one-shot setup operation in state without rerunning guest code.
func guestSetupRead(_ context.Context, _ *schema.ResourceData, _ interface{}) (diagnostics diag.Diagnostics) {
	return
}

// guestSetupDelete removes only the OpenTofu record; setup effects are intentionally not rolled back.
func guestSetupDelete(_ context.Context, data *schema.ResourceData, _ interface{}) (diagnostics diag.Diagnostics) {
	data.SetId("")
	return
}
