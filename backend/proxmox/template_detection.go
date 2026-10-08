package proxmox

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	pve "github.com/luthermonson/go-proxmox"
	"github.com/z46-dev/organesson/backend/config"
)

// Detect powers up a source VM when needed, detects its guest metadata, verifies privileged QGA, then stops it.
func (driver *apiTemplateDetectionDriver) Detect(ctx context.Context, sourceID string, guestType string) (result PreflightResult, err error) {
	var vmid int
	if vmid, err = strconv.Atoi(sourceID); err != nil || vmid < 1 {
		err = errors.New("source identifier must be a positive Proxmox VMID")
		return
	}
	var client *pve.Client
	if client, err = newAPIClient(driver.settings); err != nil {
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
	var nodeName string
	for _, resource := range resources {
		if resource != nil && resource.Type == "qemu" && resource.VMID == uint64(vmid) {
			nodeName = resource.Node
			result = PreflightResult{
				SourceID: sourceID, Node: resource.Node, Name: resource.Name, PowerState: resource.Status,
				IsQEMU: true, IsProxmoxTemplate: resource.Template != 0, CheckedAt: time.Now().UTC(),
			}
			break
		}
	}
	if nodeName == "" {
		err = fmt.Errorf("Proxmox QEMU VM %d was not found", vmid)
		return
	}
	if result.IsProxmoxTemplate {
		err = errors.New("source must be an ordinary Proxmox VM, not a native PVE template")
		return
	}
	var node *pve.Node
	if node, err = client.Node(ctx, nodeName); err != nil {
		return
	}
	var vm *pve.VirtualMachine
	if vm, err = node.VirtualMachine(ctx, vmid); err != nil {
		return
	}
	defer func() {
		if err == nil {
			return
		}
		var operationErr error = err
		var cleanupContext context.Context
		var cancel context.CancelFunc
		cleanupContext, cancel = context.WithTimeout(context.Background(), 3*time.Minute)
		defer cancel()
		var cleanupErr error = stopTemplateDetectionVM(cleanupContext, client, vm, vmid, nodeName)
		err = operationErr
		if cleanupErr != nil {
			err = fmt.Errorf("%w (could not restore source VM to stopped state: %v)", operationErr, cleanupErr)
		}
	}()
	if vm.VirtualMachineConfig == nil || !guestAgentEnabled(vm.VirtualMachineConfig.Agent) {
		err = errors.New("QEMU Guest Agent must be enabled in the Proxmox VM configuration before registration")
		return
	}
	if vm.IsStopped() {
		var task *pve.Task
		if task, err = vm.Start(ctx); err != nil {
			return
		}
		if err = waitTask(ctx, client, task); err != nil {
			return
		}
		if err = vm.Ping(ctx); err != nil {
			return
		}
	}
	if !vm.IsRunning() {
		err = errors.New("source VM must be in a normal running state for guest detection")
		return
	}
	if err = waitForGuestAgent(ctx, vm); err != nil {
		return
	}
	var osInfo *pve.AgentOsInfo
	if osInfo, err = vm.AgentOsInfo(ctx); err != nil {
		return
	}
	if osInfo == nil {
		err = errors.New("QEMU Guest Agent returned no operating-system information")
		return
	}
	result.GuestOSID = strings.ToLower(strings.TrimSpace(osInfo.ID))
	result.GuestOSName = strings.TrimSpace(osInfo.PrettyName)
	if result.GuestOSName == "" {
		result.GuestOSName = strings.TrimSpace(osInfo.Name)
	}
	result.GuestOSVersion = strings.TrimSpace(osInfo.VersionID)
	if result.GuestOSVersion == "" {
		result.GuestOSVersion = strings.TrimSpace(osInfo.Version)
	}
	result.GuestArchitecture = normalizeGuestArchitecture(osInfo.Machine)
	if !guestOSMatchesTemplate(guestType, result.GuestOSID) {
		err = fmt.Errorf("selected guest type %q does not match detected guest OS %q", guestType, result.GuestOSID)
		return
	}
	switch guestType {
	case "linux":
		if !IsLinuxTemplateOS(result.GuestOSID) {
			err = fmt.Errorf("Linux distribution %q does not have a supported preparation adapter", result.GuestOSID)
			return
		}
	case "windows":
		if !strings.Contains(result.GuestOSID, "windows") && result.GuestOSID != "mswindows" {
			err = fmt.Errorf("Windows guest identifier %q is not supported", result.GuestOSID)
			return
		}
	case "bsd":
		if result.GuestOSID != "freebsd" {
			err = fmt.Errorf("BSD guest %q is not supported; currently only FreeBSD has a preparation adapter", result.GuestOSID)
			return
		}
	}
	if result.GuestOSName == "" || result.GuestOSVersion == "" || result.GuestArchitecture == "" {
		err = errors.New("QEMU Guest Agent did not provide a guest name, version, and supported architecture")
		return
	}
	if result.GuestAgentRootVerified, err = verifyTemplateGuestExecution(ctx, driver.settings, vm, vmid, TemplatePreparationOSFamily(guestType)); err != nil {
		return
	}
	if !result.GuestAgentRootVerified {
		err = fmt.Errorf("QEMU Guest Agent did not verify unrestricted %s execution", familyExecutionName(TemplatePreparationOSFamily(guestType)))
		return
	}
	result.AgentConfigured = true
	result.AgentReachable = true
	result.PowerState = "running"
	result.Passed = true
	result.Checks = []Check{
		{Name: "source_exists", Passed: true, Required: true, Details: "Ordinary QEMU source VM was found."},
		{Name: "qemu_guest_agent_enabled", Passed: true, Required: true, Details: "QEMU Guest Agent is enabled and reachable."},
		{Name: "guest_os_matches", Passed: true, Required: true, Details: fmt.Sprintf("Detected %s %s (%s).", result.GuestOSName, result.GuestOSVersion, result.GuestArchitecture)},
		{Name: "guest_agent_root_execution", Passed: true, Required: true, Details: "QEMU Guest Agent executed a command as unrestricted " + familyExecutionName(TemplatePreparationOSFamily(guestType)) + "."},
	}
	if err = stopTemplateDetectionVM(ctx, client, vm, vmid, nodeName); err != nil {
		result.Passed = false
		return
	}
	result.PowerState = "stopped"
	result.CheckedAt = time.Now().UTC()
	return
}

// stopTemplateDetectionVM gracefully stops a detected source and confirms PVE reports it stopped.
func stopTemplateDetectionVM(ctx context.Context, client *pve.Client, vm *pve.VirtualMachine, vmid int, nodeName string) (err error) {
	if err = vm.Ping(ctx); err != nil {
		return
	}
	if vm.IsStopped() {
		return
	}
	var task *pve.Task
	if task, err = vm.Shutdown(ctx); err != nil {
		return
	}
	if err = waitTask(ctx, client, task); err != nil {
		return
	}
	err = waitVMStopped(ctx, client, vmid, nodeName)
	return
}

// normalizeGuestArchitecture maps QEMU Guest Agent machine names to supported Organesson values.
func normalizeGuestArchitecture(machine string) (architecture string) {
	switch strings.ToLower(strings.TrimSpace(machine)) {
	case "x86_64", "amd64":
		architecture = "x86_64"
	case "aarch64", "arm64":
		architecture = "aarch64"
	}
	return
}

type apiTemplateDetectionDriver struct {
	settings config.ProxmoxConfiguration
}
