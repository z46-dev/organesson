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
	"sync"
	"time"

	pve "github.com/luthermonson/go-proxmox"
	"github.com/z46-dev/organesson/backend/config"
)

var ErrNotConfigured = errors.New("Proxmox connection is not configured")
var ErrSDNNetworkNotFound = errors.New("Organesson Proxmox SDN network is missing")

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
		SourceID               string    `json:"source_id"`
		Node                   string    `json:"node,omitempty"`
		Name                   string    `json:"name,omitempty"`
		PowerState             string    `json:"power_state,omitempty"`
		GuestOSID              string    `json:"guest_os_id,omitempty"`
		GuestOSName            string    `json:"guest_os_name,omitempty"`
		AgentConfigured        bool      `json:"agent_configured"`
		AgentReachable         bool      `json:"agent_reachable"`
		GuestAgentRootVerified bool      `json:"guest_agent_root_verified"`
		IsQEMU                 bool      `json:"is_qemu"`
		IsProxmoxTemplate      bool      `json:"is_proxmox_template"`
		Passed                 bool      `json:"passed"`
		Checks                 []Check   `json:"checks"`
		CheckedAt              time.Time `json:"checked_at"`
	}

	// Inspector provides a test seam for the read-only Proxmox source lookup.
	Inspector interface {
		Inspect(context.Context, string, string) (PreflightResult, error)
	}

	// ResourceInventoryReader supplies the platform's current authorized PVE resources.
	ResourceInventoryReader interface {
		ReadResourceInventory(context.Context) (ResourceInventory, error)
	}

	// SDNNetworkDriver owns isolated Proxmox Simple-zone networks.
	SDNNetworkDriver interface {
		Create(context.Context, SDNNetworkRequest) (SDNNetworkPlacement, error)
		Read(context.Context, SDNNetworkRequest, SDNNetworkPlacement) error
		Delete(context.Context, string, string) error
	}

	// SDNNetworkIPAMReader reads address records associated with one SDN VNet.
	SDNNetworkIPAMReader interface {
		ReadIPAM(context.Context, SDNNetworkRequest, SDNNetworkPlacement) ([]SDNIPAMEntry, string, error)
	}

	// NetworkAttachmentDriver attaches owned VNet or approved bridge interfaces to managed VMs.
	NetworkAttachmentDriver interface {
		Attach(context.Context, NetworkAttachmentRequest) (NetworkAttachmentPlacement, error)
		Read(context.Context, NetworkAttachmentRequest, NetworkAttachmentPlacement) error
		Detach(context.Context, NetworkAttachmentRequest, NetworkAttachmentPlacement) error
	}

	// GuestNetworkDriver configures guest interfaces through the QEMU Guest Agent.
	GuestNetworkDriver interface {
		Configure(context.Context, GuestNetworkRequest) error
		Read(context.Context, GuestNetworkRequest) error
		Remove(context.Context, GuestNetworkRequest) error
	}

	// GuestArtifactDriver transfers and runs a validated Linux artifact through QEMU Guest Agent.
	GuestArtifactDriver interface {
		Execute(context.Context, GuestArtifactRequest, []GuestArtifactFile) (GuestArtifactResult, error)
	}

	// VMSnapshotDriver restricts snapshot operations to QEMU guests verified as Organesson-managed.
	VMSnapshotDriver interface {
		Create(context.Context, string, string, string, string, string) error
		Restore(context.Context, string, string, string, string, string) error
		Delete(context.Context, string, string, string, string, string) error
	}

	// Service configures the Proxmox API driver and performs read-only checks.
	Service struct {
		settings                config.ProxmoxConfiguration
		inspector               Inspector
		vmDriver                VMDriver
		snapshotDriver          VMSnapshotDriver
		inventoryReader         ResourceInventoryReader
		sdnNetworkDriver        SDNNetworkDriver
		networkAttachmentDriver NetworkAttachmentDriver
		guestNetworkDriver      GuestNetworkDriver
		guestArtifactDriver     GuestArtifactDriver
		lifecycleLock           sync.Mutex
		alwaysConfigured        bool
	}
)

// New creates a Proxmox preflight service using the supported Go API client.
func New(settings config.ProxmoxConfiguration) (service *Service) {
	service = &Service{settings: settings}
	service.inspector = &apiInspector{settings: settings}
	service.vmDriver = &apiVMDriver{settings: settings}
	service.snapshotDriver = &apiVMSnapshotDriver{settings: settings}
	service.inventoryReader = &apiResourceInventory{settings: settings}
	service.sdnNetworkDriver = &apiSDNNetworkDriver{settings: settings}
	service.networkAttachmentDriver = &apiNetworkAttachmentDriver{settings: settings}
	service.guestNetworkDriver = &apiGuestNetworkDriver{settings: settings}
	service.guestArtifactDriver = &apiGuestArtifactDriver{settings: settings}
	return
}

// NewWithVMSnapshotDriver injects a snapshot driver for domain and API tests.
func NewWithVMSnapshotDriver(driver VMSnapshotDriver) (service *Service) {
	service = &Service{snapshotDriver: driver, alwaysConfigured: true}
	return
}

// NewWithVMDriver creates a service with a controllable VM driver for lifecycle tests.
func NewWithVMDriver(driver VMDriver) (service *Service) {
	service = &Service{vmDriver: driver, alwaysConfigured: true}
	return
}

// NewWithSDNNetworkDriver creates a configured service with an injected SDN lifecycle driver.
func NewWithSDNNetworkDriver(driver SDNNetworkDriver) (service *Service) {
	service = &Service{sdnNetworkDriver: driver, alwaysConfigured: true}
	return
}

// NewWithProvisioningDrivers injects deterministic inventory, VM, SDN, NIC, and guest drivers for API integration tests.
func NewWithProvisioningDrivers(vmDriver VMDriver, networkDriver SDNNetworkDriver, attachmentDriver NetworkAttachmentDriver, inventoryReader ResourceInventoryReader, guestNetworkDriver GuestNetworkDriver) (service *Service) {
	service = &Service{
		vmDriver: vmDriver, sdnNetworkDriver: networkDriver,
		networkAttachmentDriver: attachmentDriver, inventoryReader: inventoryReader,
		guestNetworkDriver: guestNetworkDriver, alwaysConfigured: true,
	}
	return
}

// NewWithArtifactExecutionDrivers injects source inspection, inventory, VM, and artifact seams for API tests.
func NewWithArtifactExecutionDrivers(vmDriver VMDriver, inventory ResourceInventoryReader, inspector Inspector, artifactDriver GuestArtifactDriver) (service *Service) {
	service = &Service{
		vmDriver: vmDriver, inventoryReader: inventory, inspector: inspector,
		guestArtifactDriver: artifactDriver, alwaysConfigured: true,
	}
	return
}

// NewWithDrivers configures deterministic Proxmox seams for application lifecycle tests.
func NewWithDrivers(driver VMDriver, inventory ResourceInventoryReader, inspector Inspector) (service *Service) {
	service = &Service{
		inspector:        inspector,
		vmDriver:         driver,
		inventoryReader:  inventory,
		alwaysConfigured: true,
	}
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

// InspectTemplate checks source configuration and executes a harmless guest identity query.
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
	var client *pve.Client
	if client, err = newAPIClient(inspector.settings); err != nil {
		return
	}
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
			if osMatches && !strings.Contains(result.GuestOSID, "windows") {
				result.GuestAgentRootVerified, err = verifyLinuxGuestAgentRoot(ctx, vm)
				if err != nil {
					var details string = fmt.Sprintf("QEMU Guest Agent root-level execution check failed: %v", err)
					err = nil
					result.Checks = append(result.Checks, Check{Name: "guest_agent_root_execution", Passed: false, Required: true, Details: details})
				} else {
					var details string = "QEMU Guest Agent did not pass the full root/system execution check; UID or SELinux confinement may be the cause."
					if result.GuestAgentRootVerified {
						details = "QEMU Guest Agent executed a command as root (UID 0)."
					}
					result.Checks = append(result.Checks, Check{Name: "guest_agent_root_execution", Passed: result.GuestAgentRootVerified, Required: true, Details: details})
				}
			}
		} else {
			err = nil
			result.Checks = append(result.Checks, Check{Name: "guest_agent_reachable", Passed: false, Required: true, Details: "The guest agent did not return operating-system information."})
			result.Checks = append(result.Checks, Check{Name: "guest_os_matches", Passed: false, Required: true, Details: "The guest operating system could not be verified."})
		}
	} else {
		result.Checks = append(result.Checks, Check{Name: "guest_agent_reachable", Passed: false, Required: true, Details: "The VM is stopped; guest-agent reachability can only be checked while it is running."})
		result.Checks = append(result.Checks, Check{Name: "guest_os_matches", Passed: false, Required: true, Details: "The VM is stopped; verify the guest OS while the VM is running."})
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

// verifyLinuxGuestAgentRoot executes a harmless identity query through the template's SELinux QGA wrapper when present.
func verifyLinuxGuestAgentRoot(ctx context.Context, vm *pve.VirtualMachine) (verified bool, err error) {
	var pid int
	var command string = `check='test "$(/usr/bin/id -u)" = "0" || exit 1; context=$(/usr/bin/id -Z 2>/dev/null || true); case "$context" in *:virt_qemu_ga_t:*) exit 1 ;; esac'; if [ -x /usr/libexec/qemu-ga/fsfreeze-hook.d/organesson-qga-exec ]; then exec /usr/libexec/qemu-ga/fsfreeze-hook.d/organesson-qga-exec /bin/sh -c "$check"; fi; exec /bin/sh -c "$check"`
	if pid, err = vm.AgentExec(ctx, []string{"/bin/sh", "-c", command}, ""); err != nil {
		return
	}
	var status *pve.AgentExecStatus
	if status, err = vm.WaitForAgentExecExit(ctx, pid, 10); err != nil {
		return
	}
	verified = status.ExitCode == 0
	return
}

// newAPIClient creates a PVE API client using the configured certificate policy and token.
func newAPIClient(settings config.ProxmoxConfiguration) (client *pve.Client, err error) {
	var apiURL *url.URL
	if apiURL, err = url.ParseRequestURI(settings.APIURL); err != nil || apiURL.Scheme != "https" || apiURL.Host == "" || apiURL.User != nil || apiURL.RawQuery != "" || apiURL.Fragment != "" || !strings.HasSuffix(strings.TrimSuffix(apiURL.Path, "/"), "/api2/json") {
		err = errors.New("Proxmox API URL must be an HTTPS /api2/json URL without user info, query, or fragment")
		return
	}
	if settings.InsecureSkipVerify && settings.RootCABundlePath != "" {
		err = errors.New("configure either a trusted Proxmox CA bundle or insecure TLS, not both")
		return
	}
	var options []pve.Option = []pve.Option{
		pve.WithAPIToken(settings.APITokenID, settings.APITokenSecret),
		pve.WithHTTPClient(&http.Client{Timeout: 15 * time.Second, CheckRedirect: func(_ *http.Request, _ []*http.Request) (err error) { return http.ErrUseLastResponse }}),
	}
	if settings.InsecureSkipVerify {
		options = append(options, pve.WithInsecureSkipVerify())
	}
	if settings.RootCABundlePath != "" {
		var rootCA []byte
		if rootCA, err = os.ReadFile(settings.RootCABundlePath); err != nil {
			return
		}
		var roots *x509.CertPool = x509.NewCertPool()
		if !roots.AppendCertsFromPEM(rootCA) {
			err = errors.New("Proxmox CA bundle contains no valid certificates")
			return
		}
		options = append(options, pve.WithRootCAs(roots))
	}
	client = pve.NewClient(strings.TrimSuffix(settings.APIURL, "/"), options...)
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
