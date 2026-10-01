package proxmox

import (
	"context"
	"crypto/x509"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	pve "github.com/luthermonson/go-proxmox"
	"github.com/z46-dev/organesson/backend/config"
)

type (
	// Check is one read-only source-VM preflight result.
	Check struct {
		Name     string `json:"name"`
		Passed   bool   `json:"passed"`
		Required bool   `json:"required"`
		Details  string `json:"details"`
	}

	// PreflightResult reports the source facts read from Proxmox without changing it.
	PreflightResult struct {
		SourceID          string    `json:"source_id"`
		Node              string    `json:"node,omitempty"`
		Name              string    `json:"name,omitempty"`
		PowerState        string    `json:"power_state,omitempty"`
		GuestOSID         string    `json:"guest_os_id,omitempty"`
		GuestOSName       string    `json:"guest_os_name,omitempty"`
		AgentConfigured   bool      `json:"agent_configured"`
		AgentReachable    bool      `json:"agent_reachable"`
		IsQEMU            bool      `json:"is_qemu"`
		IsProxmoxTemplate bool      `json:"is_proxmox_template"`
		Passed            bool      `json:"passed"`
		Checks            []Check   `json:"checks"`
		CheckedAt         time.Time `json:"checked_at"`
	}

	// Inspector provides a test seam for the read-only Proxmox source lookup.
	Inspector interface {
		Inspect(context.Context, string, string) (PreflightResult, error)
	}

	// Service configures the Proxmox API driver and performs read-only checks.
	Service struct {
		settings         config.ProxmoxConfiguration
		inspector        Inspector
		alwaysConfigured bool
	}
)

// New creates a Proxmox preflight service using the supported Go API client.
func New(settings config.ProxmoxConfiguration) (service *Service) {
	service = &Service{settings: settings}
	service.inspector = &apiInspector{settings: settings}
	return
}

// NewWithInspector creates a service backed by a fake inspector for tests.
func NewWithInspector(inspector Inspector) (service *Service) {
	service = &Service{inspector: inspector, alwaysConfigured: true}
	return
}

// Configured reports whether connection details and a token secret are available.
func (service *Service) Configured() (configured bool) {
	if service == nil {
		return
	}
	if service.alwaysConfigured {
		configured = true
		return
	}
	if service.settings.APIURL == "" || service.settings.APITokenID == "" {
		return
	}
	configured = strings.TrimSpace(service.settings.APITokenSecret) != ""
	return
}

// InsecureTLS reports whether certificate verification was explicitly disabled.
func (service *Service) InsecureTLS() (insecure bool) {
	if service != nil {
		insecure = service.settings.InsecureSkipVerify
	}
	return
}

// InspectTemplate checks a QEMU source by VMID using only Proxmox GET requests.
func (service *Service) InspectTemplate(ctx context.Context, sourceID string, expectedOS string) (result PreflightResult, err error) {
	if service == nil || service.inspector == nil || !service.Configured() {
		err = errors.New("Proxmox connection is not configured")
		return
	}
	result, err = service.inspector.Inspect(ctx, sourceID, strings.ToLower(strings.TrimSpace(expectedOS)))
	return
}

type apiInspector struct {
	settings config.ProxmoxConfiguration
}

// Inspect locates the source by VMID and verifies its Proxmox guest-agent settings.
func (inspector *apiInspector) Inspect(ctx context.Context, sourceID string, expectedOS string) (result PreflightResult, err error) {
	result = PreflightResult{SourceID: sourceID, CheckedAt: time.Now().UTC()}
	var vmID int
	if vmID, err = strconv.Atoi(sourceID); err != nil || vmID < 1 {
		err = errors.New("source identifier must be a positive Proxmox VMID")
		return
	}
	var apiURL *url.URL
	if apiURL, err = url.ParseRequestURI(inspector.settings.APIURL); err != nil || apiURL.Scheme != "https" || apiURL.Host == "" || apiURL.User != nil || apiURL.RawQuery != "" || apiURL.Fragment != "" || !strings.HasSuffix(strings.TrimSuffix(apiURL.Path, "/"), "/api2/json") {
		err = errors.New("Proxmox API URL must be an HTTPS /api2/json URL without user info, query, or fragment")
		return
	}
	if inspector.settings.InsecureSkipVerify && inspector.settings.RootCABundlePath != "" {
		err = errors.New("configure either a trusted Proxmox CA bundle or insecure TLS, not both")
		return
	}

	var options []pve.Option = []pve.Option{
		pve.WithAPIToken(inspector.settings.APITokenID, inspector.settings.APITokenSecret),
		pve.WithHTTPClient(&http.Client{
			Timeout: 15 * time.Second,
			CheckRedirect: func(_ *http.Request, _ []*http.Request) (err error) {
				return http.ErrUseLastResponse
			},
		}),
	}
	if inspector.settings.InsecureSkipVerify {
		options = append(options, pve.WithInsecureSkipVerify())
	}
	if inspector.settings.RootCABundlePath != "" {
		var rootCA []byte
		if rootCA, err = os.ReadFile(inspector.settings.RootCABundlePath); err != nil {
			return
		}
		var roots *x509.CertPool = x509.NewCertPool()
		if !roots.AppendCertsFromPEM(rootCA) {
			err = errors.New("Proxmox CA bundle contains no valid certificates")
			return
		}
		options = append(options, pve.WithRootCAs(roots))
	}

	var client *pve.Client = pve.NewClient(strings.TrimSuffix(inspector.settings.APIURL, "/"), options...)
	var cluster *pve.Cluster
	if cluster, err = client.Cluster(ctx); err != nil {
		return
	}
	var resources pve.ClusterResources
	if resources, err = cluster.Resources(ctx, "vm"); err != nil {
		return
	}

	for _, resource := range resources {
		if resource == nil || resource.Type != "qemu" || resource.VMID != uint64(vmID) {
			continue
		}
		result.IsQEMU = true
		result.Node = resource.Node
		result.Name = resource.Name
		result.PowerState = resource.Status
		result.IsProxmoxTemplate = resource.Template != 0
		break
	}
	result.Checks = append(result.Checks,
		Check{Name: "source_exists", Passed: result.IsQEMU, Required: true, Details: sourceExistsDetail(result)},
		Check{Name: "ordinary_vm", Passed: result.IsQEMU && !result.IsProxmoxTemplate, Required: true, Details: ordinaryVMDetail(result)},
	)
	if !result.IsQEMU {
		result.Passed = false
		return
	}

	var node *pve.Node
	if node, err = client.Node(ctx, result.Node); err != nil {
		return
	}
	var vm *pve.VirtualMachine
	if vm, err = node.VirtualMachine(ctx, vmID); err != nil {
		return
	}
	var agentSetting string = ""
	if vm.VirtualMachineConfig != nil {
		agentSetting = strings.TrimSpace(vm.VirtualMachineConfig.Agent)
	}
	var agentMode string = strings.Split(agentSetting, ",")[0]
	result.AgentConfigured = agentMode == "1" || agentMode == "enabled=1"
	result.Checks = append(result.Checks, Check{
		Name:     "qemu_guest_agent_enabled",
		Passed:   result.AgentConfigured,
		Required: true,
		Details:  agentConfiguredDetail(result.AgentConfigured),
	})

	if result.PowerState == "running" {
		var osInfo *pve.AgentOsInfo
		if osInfo, err = vm.AgentOsInfo(ctx); err == nil && osInfo != nil {
			result.AgentReachable = true
			result.GuestOSID = strings.ToLower(osInfo.ID)
			result.GuestOSName = osInfo.PrettyName
			var osMatches bool = expectedOS == "" || expectedOS == result.GuestOSID
			result.Checks = append(result.Checks, Check{Name: "guest_agent_reachable", Passed: true, Required: true, Details: "QEMU Guest Agent returned operating-system information."})
			result.Checks = append(result.Checks, Check{
				Name:     "guest_os_matches",
				Passed:   osMatches,
				Required: true,
				Details:  guestOSDetail(expectedOS, result.GuestOSID, osMatches),
			})
		} else {
			err = nil
			result.Checks = append(result.Checks, Check{Name: "guest_agent_reachable", Passed: false, Required: true, Details: "The guest agent did not return operating-system information."})
			result.Checks = append(result.Checks, Check{Name: "guest_os_matches", Passed: false, Required: true, Details: "The guest operating system could not be verified."})
		}
	} else {
		result.Checks = append(result.Checks, Check{Name: "guest_agent_reachable", Passed: false, Required: false, Details: "The VM is stopped; guest-agent reachability can only be checked while it is running."})
		result.Checks = append(result.Checks, Check{Name: "guest_os_matches", Passed: false, Required: false, Details: "The VM is stopped; verify the guest OS during the privileged execution check."})
	}

	result.Passed = true
	for _, check := range result.Checks {
		if check.Required && !check.Passed {
			result.Passed = false
			break
		}
	}
	return
}

func sourceExistsDetail(result PreflightResult) (details string) {
	if result.IsQEMU {
		details = fmt.Sprintf("Found QEMU VM %s on node %s.", result.SourceID, result.Node)
	} else {
		details = fmt.Sprintf("VMID %s was not found as a QEMU VM.", result.SourceID)
	}
	return
}

func ordinaryVMDetail(result PreflightResult) (details string) {
	details = "The source is an ordinary VM, not a Proxmox template."
	if result.IsProxmoxTemplate {
		details = "The source is converted to a Proxmox template; Organesson expects an ordinary VM."
	}
	return
}

func agentConfiguredDetail(configured bool) (details string) {
	details = "QEMU Guest Agent is disabled in the Proxmox VM configuration."
	if configured {
		details = "QEMU Guest Agent is enabled in the Proxmox VM configuration."
	}
	return
}

func guestOSDetail(expectedOS string, guestOS string, matches bool) (details string) {
	details = fmt.Sprintf("Guest reports OS ID %q; expected %q.", guestOS, expectedOS)
	if matches {
		details = fmt.Sprintf("Guest reports expected OS ID %q.", guestOS)
	}
	return
}
