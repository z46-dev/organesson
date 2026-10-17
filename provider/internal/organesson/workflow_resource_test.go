package organesson

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/hashicorp/terraform-plugin-sdk/v2/helper/schema"
)

func TestGuestWorkflowRunsStepsInDependencyOrderAndWaitsForReboot(t *testing.T) {
	var root string = t.TempDir()
	for _, name := range []string{"rename", "join"} {
		var directory string = filepath.Join(root, name)
		if err := os.Mkdir(directory, 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(directory, "entry.ps1"), []byte("Write-Output 'ok'"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	var workflow string = "name = 'test workflow'\n" +
		"[[steps]]\nname = 'join-workstation'\ntarget = 'workstation'\nsource_directory = 'join'\nentrypoint = 'entry.ps1'\ndepends_on = ['rename-domain-controller']\n\n" +
		"[[steps]]\nname = 'rename-domain-controller'\ntarget = 'domain_controller'\nsource_directory = 'rename'\nentrypoint = 'entry.ps1'\nwait_for_windows_first_boot = true\nreboot_after = true\nreboot_timeout_seconds = 60\n"
	var definitionFile string = filepath.Join(root, "workflow.toml")
	if err := os.WriteFile(definitionFile, []byte(workflow), 0600); err != nil {
		t.Fatal(err)
	}
	var digest string
	var steps []guestWorkflowStep
	var err error
	if digest, steps, err = loadGuestWorkflow(definitionFile, root); err != nil {
		t.Fatalf("load workflow: %v", err)
	}
	if len(digest) != 64 || len(steps) != 2 || steps[0].Name != "rename-domain-controller" || steps[1].Name != "join-workstation" {
		t.Fatalf("workflow was not fingerprinted and topologically ordered: digest=%q steps=%#v", digest, steps)
	}
	var calls []string
	var server *httptest.Server = httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/api/v1/virtual-machines/47/guest-windows-first-boot-wait":
			calls = append(calls, "wait-first-boot")
			writer.Header().Set("Content-Type", "application/json")
			_, _ = writer.Write([]byte(`{"ready":true}`))
		case "/api/v1/virtual-machines/47/guest-setup", "/api/v1/virtual-machines/48/guest-setup":
			var entrypoint string = request.Header.Get("X-Organesson-Artifact-Entrypoint")
			calls = append(calls, "run:"+entrypoint)
			writer.Header().Set("Content-Type", "application/json")
			_, _ = fmt.Fprintf(writer, `{"execution":{"sha256":"%s","status":"succeeded","exit_code":0}}`, request.Header.Get("X-Organesson-Artifact-SHA256"))
		case "/api/v1/virtual-machines/47/guest-reboot-wait":
			var body struct {
				TimeoutSeconds int `json:"timeout_seconds"`
			}
			if err := json.NewDecoder(request.Body).Decode(&body); err != nil || body.TimeoutSeconds != 60 {
				t.Errorf("unexpected reboot wait request: %#v err=%v", body, err)
			}
			calls = append(calls, "wait-reboot")
			writer.Header().Set("Content-Type", "application/json")
			_, _ = writer.Write([]byte(`{"ready":true}`))
		default:
			t.Errorf("unexpected workflow API path %q", request.URL.Path)
			http.NotFound(writer, request)
		}
	}))
	defer server.Close()
	var client *apiClient
	if client, err = configuredClient(server.URL, "test-token", "", false); err != nil {
		t.Fatal(err)
	}
	var data *schema.ResourceData = schema.TestResourceDataRaw(t, guestWorkflowResource().Schema, map[string]interface{}{
		"definition_file":  definitionFile,
		"source_root":      root,
		"sha256":           digest,
		"virtual_machines": map[string]interface{}{"domain_controller": "47", "workstation": "48"},
	})
	if diagnostics := guestWorkflowCreate(context.Background(), data, client); diagnostics.HasError() {
		t.Fatalf("run workflow: %s", diagnostics[0].Summary+": "+diagnostics[0].Detail)
	}
	if expected := []string{"wait-first-boot", "run:entry.ps1", "wait-reboot", "run:entry.ps1"}; !reflect.DeepEqual(calls, expected) {
		t.Fatalf("workflow calls=%v, want %v", calls, expected)
	}
	if data.Get("execution_summary") != "rename-domain-controller, join-workstation" || data.Id() == "" {
		t.Fatalf("workflow state not recorded: id=%q summary=%q", data.Id(), data.Get("execution_summary"))
	}
}

func TestGuestWorkflowRejectsCyclesAndUnknownDependencies(t *testing.T) {
	var root string = t.TempDir()
	var artifactDirectory string = filepath.Join(root, "artifact")
	if err := os.Mkdir(artifactDirectory, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(artifactDirectory, "entry.sh"), []byte("#!/bin/sh\nexit 0\n"), 0600); err != nil {
		t.Fatal(err)
	}
	for _, testCase := range []struct {
		name string
		text string
	}{
		{name: "cycle", text: "name='cycle'\n[[steps]]\nname='one'\ntarget='vm'\nsource_directory='artifact'\nentrypoint='entry.sh'\ndepends_on=['two']\n[[steps]]\nname='two'\ntarget='vm'\nsource_directory='artifact'\nentrypoint='entry.sh'\ndepends_on=['one']\n"},
		{name: "unknown dependency", text: "name='unknown'\n[[steps]]\nname='one'\ntarget='vm'\nsource_directory='artifact'\nentrypoint='entry.sh'\ndepends_on=['missing']\n"},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			var definitionFile string = filepath.Join(root, testCase.name+".toml")
			if err := os.WriteFile(definitionFile, []byte(testCase.text), 0600); err != nil {
				t.Fatal(err)
			}
			if _, _, err := loadGuestWorkflow(definitionFile, root); err == nil {
				t.Fatal("invalid workflow was accepted")
			}
		})
	}
}
