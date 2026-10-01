package proxmox

import (
	"encoding/json"
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/z46-dev/organesson/backend/config"
)

// TestInspectTemplateUsesReadOnlyProxmoxRequests validates the driver integration without a live cluster.
func TestInspectTemplateUsesReadOnlyProxmoxRequests(t *testing.T) {
	var requests []string
	var server *httptest.Server
	server = httptest.NewTLSServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.Method != http.MethodGet {
			t.Errorf("preflight issued mutating HTTP method %s", request.Method)
		}
		if request.Header.Get("Authorization") != "PVEAPIToken=organesson@pve!catalog=test-secret" {
			t.Errorf("unexpected Proxmox authorization header %q", request.Header.Get("Authorization"))
		}
		requests = append(requests, request.URL.Path)
		writer.Header().Set("Content-Type", "application/json")
		switch request.URL.Path {
		case "/api2/json/cluster/status":
			_, _ = writer.Write([]byte(`{"data":[]}`))
		case "/api2/json/cluster/resources":
			_, _ = writer.Write([]byte(`{"data":[{"type":"qemu","vmid":156,"node":"pve1","name":"fedora-workstation","status":"stopped","template":0}]}`))
		case "/api2/json/nodes/pve1/status":
			_, _ = writer.Write([]byte(`{"data":{"node":"pve1","status":"online"}}`))
		case "/api2/json/nodes/pve1/qemu/156/status/current":
			_, _ = writer.Write([]byte(`{"data":{"vmid":156,"name":"fedora-workstation","status":"stopped"}}`))
		case "/api2/json/nodes/pve1/qemu/156/config":
			_, _ = writer.Write([]byte(`{"data":{"agent":"1","template":0}}`))
		default:
			http.NotFound(writer, request)
		}
	}))
	defer server.Close()
	var caPath string = filepath.Join(t.TempDir(), "pve-ca.pem")
	var certPEM []byte = pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: server.Certificate().Raw})
	if err := os.WriteFile(caPath, certPEM, 0600); err != nil {
		t.Fatalf("write test CA: %v", err)
	}
	var service *Service = New(config.ProxmoxConfiguration{
		APIURL:           server.URL + "/api2/json",
		APITokenID:       "organesson@pve!catalog",
		APITokenSecret:   "test-secret",
		RootCABundlePath: caPath,
	})
	var result PreflightResult
	var err error
	if result, err = service.InspectTemplate(t.Context(), "156", "fedora"); err != nil {
		t.Fatalf("inspect source: %v", err)
	}
	if !result.Passed || !result.IsQEMU || result.IsProxmoxTemplate || !result.AgentConfigured {
		t.Fatalf("unexpected passing source preflight: %#v", result)
	}
	if result.PowerState != "stopped" || result.Node != "pve1" {
		t.Fatalf("preflight did not capture the source location/state: %#v", result)
	}
	if len(requests) != 5 {
		t.Fatalf("expected five GET requests, got %d: %#v", len(requests), requests)
	}
	for _, path := range requests {
		if strings.Contains(path, "/agent/") {
			t.Fatalf("stopped source should not receive guest-agent commands: %s", path)
		}
	}
}

// TestInspectTemplateChecksGuestOSWhenRunning confirms mismatched running sources fail readiness.
func TestInspectTemplateChecksGuestOSWhenRunning(t *testing.T) {
	var server *httptest.Server
	server = httptest.NewTLSServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		var response string
		switch request.URL.Path {
		case "/api2/json/cluster/status":
			response = `{"data":[]}`
		case "/api2/json/cluster/resources":
			response = `{"data":[{"type":"qemu","vmid":157,"node":"pve1","name":"fedora-server","status":"running","template":0}]}`
		case "/api2/json/nodes/pve1/status":
			response = `{"data":{"node":"pve1","status":"online"}}`
		case "/api2/json/nodes/pve1/qemu/157/status/current":
			response = `{"data":{"vmid":157,"name":"fedora-server","status":"running"}}`
		case "/api2/json/nodes/pve1/qemu/157/config":
			response = `{"data":{"agent":"1"}}`
		case "/api2/json/nodes/pve1/qemu/157/agent/get-osinfo":
			response = `{"data":{"result":{"id":"ubuntu","pretty-name":"Ubuntu 26.04"}}}`
		default:
			http.NotFound(writer, request)
			return
		}
		_ = json.NewEncoder(writer).Encode(json.RawMessage(response))
	}))
	defer server.Close()
	var caPath string = filepath.Join(t.TempDir(), "pve-ca.pem")
	if err := os.WriteFile(caPath, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: server.Certificate().Raw}), 0600); err != nil {
		t.Fatalf("write test CA: %v", err)
	}
	var service *Service = New(config.ProxmoxConfiguration{
		APIURL:           server.URL + "/api2/json",
		APITokenID:       "organesson@pve!catalog",
		APITokenSecret:   "test-secret",
		RootCABundlePath: caPath,
	})
	var result PreflightResult
	var err error
	if result, err = service.InspectTemplate(t.Context(), "157", "fedora"); err != nil {
		t.Fatalf("inspect running source: %v", err)
	}
	if result.Passed || result.GuestOSID != "ubuntu" || !result.AgentReachable {
		t.Fatalf("expected OS mismatch to fail source preflight: %#v", result)
	}
}

// TestInspectTemplateCanUseExplicitInsecureTLS permits self-signed lab endpoints only when opted in.
func TestInspectTemplateCanUseExplicitInsecureTLS(t *testing.T) {
	var requests int
	var server *httptest.Server = httptest.NewTLSServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		requests++
		if request.Method != http.MethodGet {
			t.Errorf("preflight issued mutating HTTP method %s", request.Method)
		}
		writer.Header().Set("Content-Type", "application/json")
		switch request.URL.Path {
		case "/api2/json/cluster/status":
			_, _ = writer.Write([]byte(`{"data":[]}`))
		case "/api2/json/cluster/resources":
			_, _ = writer.Write([]byte(`{"data":[{"type":"qemu","vmid":156,"node":"pve1","name":"fedora-workstation","status":"stopped","template":0}]}`))
		case "/api2/json/nodes/pve1/status":
			_, _ = writer.Write([]byte(`{"data":{"node":"pve1","status":"online"}}`))
		case "/api2/json/nodes/pve1/qemu/156/status/current":
			_, _ = writer.Write([]byte(`{"data":{"vmid":156,"name":"fedora-workstation","status":"stopped"}}`))
		case "/api2/json/nodes/pve1/qemu/156/config":
			_, _ = writer.Write([]byte(`{"data":{"agent":"1","template":0}}`))
		default:
			http.NotFound(writer, request)
		}
	}))
	defer server.Close()
	var settings config.ProxmoxConfiguration = config.ProxmoxConfiguration{
		APIURL:         server.URL + "/api2/json",
		APITokenID:     "organesson@pve!catalog",
		APITokenSecret: "test-secret",
	}
	var secureService *Service = New(settings)
	if _, err := secureService.InspectTemplate(t.Context(), "156", "fedora"); err == nil {
		t.Fatal("self-signed certificate was accepted with verification enabled and no trusted CA")
	}
	if requests != 0 {
		t.Fatalf("certificate failure should happen before sending an API request; got %d requests", requests)
	}

	settings.InsecureSkipVerify = true
	var insecureService *Service = New(settings)
	var result PreflightResult
	var err error
	if result, err = insecureService.InspectTemplate(t.Context(), "156", "fedora"); err != nil {
		t.Fatalf("inspect source with explicit insecure TLS: %v", err)
	}
	if !result.Passed || requests != 5 || !insecureService.InsecureTLS() {
		t.Fatalf("explicit insecure TLS did not complete read-only preflight: result=%#v requests=%d", result, requests)
	}
}

// TestInspectTemplateRejectsConflictingTLSSettings prevents ambiguous trust configuration.
func TestInspectTemplateRejectsConflictingTLSSettings(t *testing.T) {
	var service *Service = New(config.ProxmoxConfiguration{
		APIURL:             "https://pve.example:8006/api2/json",
		APITokenID:         "organesson@pve!catalog",
		APITokenSecret:     "test-secret",
		RootCABundlePath:   "/tmp/pve-ca.pem",
		InsecureSkipVerify: true,
	})
	if _, err := service.InspectTemplate(t.Context(), "156", "fedora"); err == nil || !strings.Contains(err.Error(), "either a trusted Proxmox CA bundle or insecure TLS") {
		t.Fatalf("expected conflicting TLS settings to be rejected, got %v", err)
	}
}
