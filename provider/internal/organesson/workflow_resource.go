package organesson

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/BurntSushi/toml"
	"github.com/hashicorp/terraform-plugin-sdk/v2/diag"
	"github.com/hashicorp/terraform-plugin-sdk/v2/helper/schema"
)

type (
	guestWorkflowDefinition struct {
		Name  string              `toml:"name"`
		Steps []guestWorkflowStep `toml:"steps"`
	}
	guestWorkflowStep struct {
		Name                    string   `toml:"name"`
		Target                  string   `toml:"target"`
		SourceDirectory         string   `toml:"source_directory"`
		Entrypoint              string   `toml:"entrypoint"`
		DependsOn               []string `toml:"depends_on"`
		RebootAfter             bool     `toml:"reboot_after"`
		RebootTimeoutSeconds    int      `toml:"reboot_timeout_seconds"`
		WaitForWindowsFirstBoot bool     `toml:"wait_for_windows_first_boot"`
		artifact                artifactPackage
	}
)

// guestWorkflowResource executes a declarative, dependency-ordered guest workflow during apply.
func guestWorkflowResource() (resource *schema.Resource) {
	resource = &schema.Resource{
		CreateContext: guestWorkflowCreate,
		ReadContext:   guestWorkflowRead,
		UpdateContext: guestWorkflowUpdate,
		DeleteContext: guestWorkflowDelete,
		Timeouts:      &schema.ResourceTimeout{Create: schema.DefaultTimeout(60 * time.Minute)},
		CustomizeDiff: guestWorkflowCustomizeDiff,
		Schema: map[string]*schema.Schema{
			"definition_file":         {Type: schema.TypeString, Required: true, ForceNew: true, Description: "TOML workflow definition containing named guest steps and dependencies."},
			"dependency_resource_ids": resourceDependencySchema(),
			"execution_summary":       {Type: schema.TypeString, Computed: true, Description: "Summary of the steps completed by the workflow."},
			"sha256":                  {Type: schema.TypeString, Computed: true, Description: "Fingerprint of the workflow definition and referenced artifact packages."},
			"source_root":             {Type: schema.TypeString, Optional: true, Default: ".", ForceNew: true, Description: "Base directory for artifact paths in the workflow definition."},
			"virtual_machines":        {Type: schema.TypeMap, Required: true, ForceNew: true, Elem: &schema.Schema{Type: schema.TypeString}, Description: "Map of workflow target names to managed VM resource IDs."},
		},
	}
	return
}

// guestWorkflowUpdate records ordering-only changes without replaying completed workflow steps.
func guestWorkflowUpdate(_ context.Context, _ *schema.ResourceData, _ interface{}) (diagnostics diag.Diagnostics) {
	return
}

// guestWorkflowCustomizeDiff fingerprints all inputs so artifact edits rerun the workflow.
func guestWorkflowCustomizeDiff(_ context.Context, data *schema.ResourceDiff, _ interface{}) (err error) {
	var digest string
	if digest, _, err = loadGuestWorkflow(data.Get("definition_file").(string), data.Get("source_root").(string)); err != nil {
		return
	}
	var previousDigest any
	previousDigest, _ = data.GetChange("sha256")
	if err = data.SetNew("sha256", digest); err != nil {
		return
	}
	if oldDigest, ok := previousDigest.(string); ok && oldDigest != "" && oldDigest != digest {
		err = data.ForceNew("sha256")
	}
	return
}

// loadGuestWorkflow parses the TOML graph, packages its artifacts, and rejects invalid dependency graphs.
func loadGuestWorkflow(definitionFile string, sourceRoot string) (digest string, steps []guestWorkflowStep, err error) {
	var definition guestWorkflowDefinition
	if _, err = toml.DecodeFile(definitionFile, &definition); err != nil {
		err = fmt.Errorf("read guest workflow definition: %w", err)
		return
	}
	if strings.TrimSpace(definition.Name) == "" || len(definition.Steps) == 0 || len(definition.Steps) > 64 {
		err = errors.New("guest workflow requires a name and between 1 and 64 steps")
		return
	}
	var definitionBytes []byte
	if definitionBytes, err = os.ReadFile(definitionFile); err != nil {
		return
	}
	var hasher = sha256.New()
	_, _ = hasher.Write(definitionBytes)
	var root string
	if root, err = filepath.Abs(sourceRoot); err != nil {
		return
	}
	var names map[string]int = make(map[string]int, len(definition.Steps))
	for index, step := range definition.Steps {
		if !validWorkflowName(step.Name) || !validWorkflowName(step.Target) || names[step.Name] != 0 {
			err = fmt.Errorf("workflow step %d has an invalid or duplicate name or target", index+1)
			return
		}
		names[step.Name] = index + 1
		if step.RebootTimeoutSeconds == 0 {
			step.RebootTimeoutSeconds = 900
		}
		if step.RebootAfter && (step.RebootTimeoutSeconds < 60 || step.RebootTimeoutSeconds > 3600) {
			err = fmt.Errorf("workflow step %q reboot timeout must be between 60 and 3600 seconds", step.Name)
			return
		}
		if !step.RebootAfter && step.RebootTimeoutSeconds != 900 {
			err = fmt.Errorf("workflow step %q sets a reboot timeout without reboot_after", step.Name)
			return
		}
		if step.SourceDirectory == "" || filepath.IsAbs(step.SourceDirectory) || filepath.Clean(step.SourceDirectory) != step.SourceDirectory || step.SourceDirectory == ".." || strings.HasPrefix(step.SourceDirectory, ".."+string(filepath.Separator)) {
			err = fmt.Errorf("workflow step %q has an unsafe source_directory", step.Name)
			return
		}
		step.SourceDirectory = filepath.Join(root, step.SourceDirectory)
		if step.artifact, err = packageArtifact(step.SourceDirectory, step.Entrypoint); err != nil {
			err = fmt.Errorf("workflow step %q artifact: %w", step.Name, err)
			return
		}
		_, _ = hasher.Write([]byte(step.artifact.SHA256))
		definition.Steps[index] = step
	}
	if steps, err = orderGuestWorkflowSteps(definition.Steps, names); err != nil {
		return
	}
	digest = hex.EncodeToString(hasher.Sum(nil))
	return
}

// orderGuestWorkflowSteps validates references and returns a stable topological ordering.
func orderGuestWorkflowSteps(steps []guestWorkflowStep, names map[string]int) (ordered []guestWorkflowStep, err error) {
	var completed map[string]bool = make(map[string]bool, len(steps))
	ordered = make([]guestWorkflowStep, 0, len(steps))
	for len(ordered) < len(steps) {
		var progressed bool
		for _, step := range steps {
			if completed[step.Name] {
				continue
			}
			var ready bool = true
			for _, dependency := range step.DependsOn {
				if _, exists := names[dependency]; !exists || dependency == step.Name {
					err = fmt.Errorf("workflow step %q depends on unknown or itself: %q", step.Name, dependency)
					return
				}
				if !completed[dependency] {
					ready = false
				}
			}
			if ready {
				ordered = append(ordered, step)
				completed[step.Name] = true
				progressed = true
			}
		}
		if !progressed {
			err = errors.New("guest workflow dependencies contain a cycle")
			return
		}
	}
	return
}

// validWorkflowName accepts concise identifiers used by workflow targets and steps.
func validWorkflowName(value string) (valid bool) {
	if value == "" || len(value) > 64 {
		return
	}
	for index, character := range value {
		if (character < 'a' || character > 'z') && (character < 'A' || character > 'Z') && (character < '0' || character > '9') && character != '-' && character != '_' {
			return
		}
		if index == 0 && ((character < 'a' || character > 'z') && (character < 'A' || character > 'Z')) {
			return
		}
	}
	valid = true
	return
}

// guestWorkflowCreate executes each step once, recording only its digest and final summary in state.
func guestWorkflowCreate(ctx context.Context, data *schema.ResourceData, meta interface{}) (diagnostics diag.Diagnostics) {
	var client *apiClient = meta.(*apiClient)
	var digest string
	var steps []guestWorkflowStep
	var err error
	if digest, steps, err = loadGuestWorkflow(data.Get("definition_file").(string), data.Get("source_root").(string)); err != nil {
		diagnostics = diag.FromErr(err)
		return
	}
	if digest != data.Get("sha256").(string) {
		diagnostics = diag.Errorf("workflow definition or artifact changed after planning; run OpenTofu plan again")
		return
	}
	var targets map[string]string
	if targets, err = stringMap(data.Get("virtual_machines")); err != nil {
		diagnostics = diag.FromErr(err)
		return
	}
	var completed []string
	for _, step := range steps {
		rawVMID, exists := targets[step.Target]
		if !exists || strings.TrimSpace(rawVMID) == "" {
			diagnostics = diag.Errorf("workflow step %q refers to missing VM target %q", step.Name, step.Target)
			return
		}
		var vmID string
		if vmID, err = remoteID(rawVMID); err != nil {
			diagnostics = diag.Errorf("workflow step %q target: %v", step.Name, err)
			return
		}
		if step.WaitForWindowsFirstBoot {
			if err = client.requestWithTimeout(ctx, "POST", "/api/v1/virtual-machines/"+vmID+"/guest-windows-first-boot-wait", nil, nil, 21*time.Minute); err != nil {
				diagnostics = diag.Errorf("workflow step %q on %s could not confirm unattended Windows first boot: %v", step.Name, step.Target, err)
				return
			}
		}
		var result struct {
			Execution guestSetupResult `json:"execution"`
		}
		if err = executeWorkflowGuestStep(ctx, client, vmID, step, &result); err != nil {
			diagnostics = diag.Errorf("workflow step %q on %s failed: %v", step.Name, step.Target, err)
			return
		}
		if result.Execution.SHA256 != step.artifact.SHA256 || result.Execution.Status != "succeeded" || result.Execution.ExitCode != 0 {
			diagnostics = diag.Errorf("workflow step %q on %s did not succeed", step.Name, step.Target)
			return
		}
		completed = append(completed, step.Name)
		if step.RebootAfter && !result.Execution.Reused {
			if err = client.requestWithTimeout(ctx, "POST", "/api/v1/virtual-machines/"+vmID+"/guest-reboot-wait", map[string]int{"timeout_seconds": step.RebootTimeoutSeconds}, nil, time.Duration(step.RebootTimeoutSeconds+30)*time.Second); err != nil {
				diagnostics = diag.Errorf("workflow step %q completed, but its reboot did not: %v", step.Name, err)
				return
			}
		}
	}
	if err = data.Set("execution_summary", strings.Join(completed, ", ")); err != nil {
		diagnostics = diag.FromErr(err)
		return
	}
	data.SetId(localResourceID("guest-workflow", digest))
	return
}

// executeWorkflowGuestStep retries transport failures only; guest-reported script failures remain visible immediately.
func executeWorkflowGuestStep(ctx context.Context, client *apiClient, vmID string, step guestWorkflowStep, result interface{}) (err error) {
	const maximumAttempts int = 3
	var attempt int
	for attempt = 1; attempt <= maximumAttempts; attempt++ {
		err = client.requestBytes(ctx, "POST", "/api/v1/virtual-machines/"+vmID+"/guest-setup", "application/vnd.organesson.artifact+gzip", map[string]string{
			"X-Organesson-Artifact-SHA256":     step.artifact.SHA256,
			"X-Organesson-Artifact-Entrypoint": step.Entrypoint,
		}, step.artifact.Archive, result)
		if err == nil || attempt == maximumAttempts || !retryableWorkflowGuestSetupError(err) {
			return
		}
		var timer *time.Timer = time.NewTimer(time.Duration(attempt*5) * time.Second)
		select {
		case <-ctx.Done():
			timer.Stop()
			err = ctx.Err()
			return
		case <-timer.C:
		}
	}
	return
}

// retryableWorkflowGuestSetupError recognizes lost QGA transport, not an entrypoint's non-zero exit status.
func retryableWorkflowGuestSetupError(err error) (retry bool) {
	if err == nil {
		return
	}
	var message string = strings.ToLower(err.Error())
	retry = strings.Contains(message, "guest-setup returned 502 bad gateway") && strings.Contains(message, `"exit_code":-1`)
	return
}

// stringMap converts Terraform's map representation to a name-to-resource-ID lookup.
func stringMap(value interface{}) (result map[string]string, err error) {
	var values map[string]interface{}
	var ok bool
	if values, ok = value.(map[string]interface{}); !ok {
		err = errors.New("workflow virtual_machines must be a map of target names to resource IDs")
		return
	}
	result = make(map[string]string, len(values))
	for name, rawValue := range values {
		var stringValue string
		if stringValue, ok = rawValue.(string); !ok {
			err = fmt.Errorf("workflow virtual_machines target %q must be a resource ID string", name)
			return
		}
		result[name] = stringValue
	}
	return
}

// guestWorkflowRead preserves the one-shot workflow result without repeating guest side effects.
func guestWorkflowRead(_ context.Context, _ *schema.ResourceData, _ interface{}) (diagnostics diag.Diagnostics) {
	return
}

// guestWorkflowDelete removes only the OpenTofu record; guest changes are intentionally not rolled back.
func guestWorkflowDelete(_ context.Context, data *schema.ResourceData, _ interface{}) (diagnostics diag.Diagnostics) {
	data.SetId("")
	return
}
