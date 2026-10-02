package proxmox

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	pve "github.com/luthermonson/go-proxmox"
	"github.com/z46-dev/organesson/backend/config"
)

type liveAgentExecStatus struct {
	Exited   int    `json:"exited"`
	ExitCode int    `json:"exitcode"`
	ErrData  string `json:"err-data"`
}

// TestLiveProxmoxGuestIPForSSH starts the snapshotted lab source and reports guest-agent addresses for an operator SSH session.
func TestLiveProxmoxGuestIPForSSH(t *testing.T) {
	if os.Getenv("ORGANESSON_LIVE_PVE_GUEST_IP") != "1" {
		t.Skip("set ORGANESSON_LIVE_PVE_GUEST_IP=1 to start the lab source and read guest IP addresses")
	}
	var configPath string = os.Getenv("ORGANESSON_CONFIG")
	if configPath == "" {
		configPath = "../config.toml"
	}
	if err := config.Init(configPath); err != nil {
		t.Fatalf("load Organesson configuration: %v", err)
	}
	var sourceID string = os.Getenv("ORGANESSON_LIVE_PVE_SOURCE_ID")
	var vmid int
	var err error
	if vmid, err = strconv.Atoi(sourceID); err != nil || vmid < 1 {
		t.Fatalf("invalid Proxmox source VMID %q", sourceID)
	}
	var client *pve.Client
	if client, err = newAPIClient(config.Cfg.Proxmox); err != nil {
		t.Fatalf("configure Proxmox API client: %v", err)
	}
	var cluster *pve.Cluster
	if cluster, err = client.Cluster(t.Context()); err != nil {
		t.Fatalf("read Proxmox cluster: %v", err)
	}
	var resources pve.ClusterResources
	if resources, err = cluster.Resources(t.Context(), "vm"); err != nil {
		t.Fatalf("read Proxmox VM inventory: %v", err)
	}
	var sourceNode string
	var sourcePool string
	for _, resource := range resources {
		if resource != nil && resource.Type == "qemu" && resource.VMID == uint64(vmid) {
			sourceNode = resource.Node
			sourcePool = resource.Pool
			break
		}
	}
	if sourcePool != "organesson" {
		t.Fatalf("refusing to start VM outside the organesson pool (pool=%q)", sourcePool)
	}
	var node *pve.Node
	if node, err = client.Node(t.Context(), sourceNode); err != nil {
		t.Fatalf("read source node: %v", err)
	}
	var source *pve.VirtualMachine
	if source, err = node.VirtualMachine(t.Context(), vmid); err != nil {
		t.Fatalf("read source VM status: %v", err)
	}
	if err = source.Ping(t.Context()); err != nil {
		t.Fatalf("read source VM configuration: %v", err)
	}
	t.Logf("Proxmox network device net0: %q", source.VirtualMachineConfig.Nets["net0"])
	var snapshots []*pve.VirtualMachineSnapshot
	if snapshots, err = source.Snapshots(t.Context()); err != nil {
		t.Fatalf("read source VM snapshots: %v", err)
	}
	var snapshotFound bool
	for _, snapshot := range snapshots {
		if snapshot != nil && snapshot.Name == "before" {
			snapshotFound = true
			break
		}
	}
	if !snapshotFound {
		t.Fatal("refusing to start VM: expected recovery snapshot 'before' was not found")
	}
	var startedForCheck bool = source.IsStopped()
	if startedForCheck {
		if os.Getenv("ORGANESSON_LIVE_PVE_POWER_ON") != "1" {
			t.Fatal("source is stopped; set ORGANESSON_LIVE_PVE_POWER_ON=1 to start it temporarily")
		}
		t.Cleanup(func() {
			if err := stopLiveSource(t, client, sourceNode, vmid); err != nil {
				t.Errorf("restore source VM to stopped state: %v", err)
			}
		})
		var startTask *pve.Task
		if startTask, err = source.Start(t.Context()); err != nil {
			t.Fatalf("start source VM: %v", err)
		}
		if err = waitTask(t.Context(), client, startTask); err != nil {
			t.Fatalf("wait for source VM to start: %v", err)
		}
		if err = source.WaitForAgent(t.Context(), 120); err != nil {
			t.Fatalf("wait for source QEMU Guest Agent: %v", err)
		}
	}
	var guestAddresses []string
	var interfaceSummary []string
	var addressDeadline time.Time = time.Now().Add(60 * time.Second)
	for time.Now().Before(addressDeadline) && len(guestAddresses) == 0 {
		var interfaces []*pve.AgentNetworkIface
		if interfaces, err = source.AgentGetNetworkIFaces(t.Context()); err != nil {
			t.Fatalf("read guest network interfaces through QEMU Guest Agent: %v", err)
		}
		interfaceSummary = interfaceSummary[:0]
		for _, iface := range interfaces {
			if iface == nil {
				continue
			}
			var interfaceAddresses []string
			for _, address := range iface.IPAddresses {
				if address == nil {
					continue
				}
				interfaceAddresses = append(interfaceAddresses, address.IPAddress)
				var parsedIP net.IP = net.ParseIP(address.IPAddress)
				if parsedIP == nil || parsedIP.IsLoopback() || parsedIP.IsLinkLocalUnicast() || parsedIP.IsUnspecified() {
					continue
				}
				guestAddresses = append(guestAddresses, fmt.Sprintf("%s=%s/%d", iface.Name, address.IPAddress, address.Prefix))
			}
			interfaceSummary = append(interfaceSummary, fmt.Sprintf("%s (%s): %v", iface.Name, iface.HardwareAddress, interfaceAddresses))
		}
		if len(guestAddresses) == 0 {
			time.Sleep(2 * time.Second)
		}
	}
	if len(guestAddresses) == 0 {
		t.Fatalf("QEMU Guest Agent returned no usable guest IP addresses; interfaces: %v", interfaceSummary)
	}
	t.Logf("guest addresses from QEMU Guest Agent: %s", strings.Join(guestAddresses, ", "))
	t.Log("holding the VM online for up to four minutes for the requested SSH setup")
	select {
	case <-t.Context().Done():
	case <-time.After(4 * time.Minute):
	}
}

// TestLiveQemuGuestAgentAccountExperiment tests account administration through QEMU Guest Agent on a snapshotted lab VM.
func TestLiveQemuGuestAgentAccountExperiment(t *testing.T) {
	if os.Getenv("ORGANESSON_LIVE_PVE_ACCOUNT_EXPERIMENT") != "1" {
		t.Skip("set ORGANESSON_LIVE_PVE_ACCOUNT_EXPERIMENT=1 to run the explicit live account experiment")
	}
	var sourceID string = os.Getenv("ORGANESSON_LIVE_PVE_SOURCE_ID")
	var expectedSnapshot string = os.Getenv("ORGANESSON_LIVE_PVE_SNAPSHOT")
	if sourceID == "" || expectedSnapshot == "" {
		t.Fatal("ORGANESSON_LIVE_PVE_SOURCE_ID and ORGANESSON_LIVE_PVE_SNAPSHOT must identify the lab VM and its recovery snapshot")
	}
	var configPath string = os.Getenv("ORGANESSON_CONFIG")
	if configPath == "" {
		configPath = "../config.toml"
	}
	if err := config.Init(configPath); err != nil {
		t.Fatalf("load Organesson configuration: %v", err)
	}
	var vmid int
	var err error
	if vmid, err = strconv.Atoi(sourceID); err != nil || vmid < 1 {
		t.Fatalf("invalid Proxmox source VMID %q", sourceID)
	}
	var client *pve.Client
	if client, err = newAPIClient(config.Cfg.Proxmox); err != nil {
		t.Fatalf("configure Proxmox API client: %v", err)
	}
	var cluster *pve.Cluster
	if cluster, err = client.Cluster(t.Context()); err != nil {
		t.Fatalf("read Proxmox cluster: %v", err)
	}
	var resources pve.ClusterResources
	if resources, err = cluster.Resources(t.Context(), "vm"); err != nil {
		t.Fatalf("read Proxmox VM inventory: %v", err)
	}
	var sourceNode string
	var sourcePool string
	for _, resource := range resources {
		if resource != nil && resource.Type == "qemu" && resource.VMID == uint64(vmid) {
			sourceNode = resource.Node
			sourcePool = resource.Pool
			break
		}
	}
	if sourcePool != "organesson" {
		t.Fatalf("refusing live account experiment outside the organesson pool (pool=%q)", sourcePool)
	}
	var node *pve.Node
	if node, err = client.Node(t.Context(), sourceNode); err != nil {
		t.Fatalf("read source node: %v", err)
	}
	var source *pve.VirtualMachine
	if source, err = node.VirtualMachine(t.Context(), vmid); err != nil {
		t.Fatalf("read source VM status: %v", err)
	}
	var snapshots []*pve.VirtualMachineSnapshot
	if snapshots, err = source.Snapshots(t.Context()); err != nil {
		t.Fatalf("read recovery snapshots: %v", err)
	}
	var snapshotFound bool
	for _, snapshot := range snapshots {
		if snapshot != nil && snapshot.Name == expectedSnapshot && snapshot.Name != "current" {
			snapshotFound = true
			break
		}
	}
	if !snapshotFound {
		var snapshotNames []string
		for _, snapshot := range snapshots {
			if snapshot != nil {
				snapshotNames = append(snapshotNames, snapshot.Name)
			}
		}
		t.Fatalf("required recovery snapshot %q was not found; available snapshots: %v", expectedSnapshot, snapshotNames)
	}
	t.Logf("verified recovery snapshot %q; VM pool=%q", expectedSnapshot, sourcePool)
	var startedForCheck bool = source.IsStopped()
	if startedForCheck {
		if os.Getenv("ORGANESSON_LIVE_PVE_POWER_ON") != "1" {
			t.Fatal("source is stopped; set ORGANESSON_LIVE_PVE_POWER_ON=1 to temporarily start it")
		}
		t.Cleanup(func() {
			if err := stopLiveSource(t, client, sourceNode, vmid); err != nil {
				t.Errorf("restore source VM to stopped state: %v", err)
			}
		})
		var startTask *pve.Task
		if startTask, err = source.Start(t.Context()); err != nil {
			t.Fatalf("start source VM temporarily: %v", err)
		}
		if err = waitTask(t.Context(), client, startTask); err != nil {
			t.Fatalf("wait for source VM to start: %v", err)
		}
		if err = source.WaitForAgent(t.Context(), 120); err != nil {
			t.Fatalf("wait for source QEMU Guest Agent: %v", err)
		}
	}
	var passwdFile *pve.AgentFileRead
	if passwdFile, err = source.AgentFileRead(t.Context(), "/etc/passwd"); err != nil {
		t.Fatalf("read Linux account metadata through QEMU Guest Agent: %v", err)
	}
	var initialAccounts []string = interactiveLinuxAccounts(passwdFile.Content)
	if passwdFile.Truncated || len(initialAccounts) != 1 || initialAccounts[0] != "administrator" {
		t.Fatalf("refusing account experiment: expected only administrator as an interactive account, got %v", initialAccounts)
	}
	var loggedInUsers []*pve.AgentUser
	if loggedInUsers, err = source.AgentGetUsers(t.Context()); err != nil {
		t.Fatalf("check active guest logins: %v", err)
	}
	for _, loggedInUser := range loggedInUsers {
		if loggedInUser != nil && loggedInUser.User == "administrator" {
			t.Fatalf("refusing administrator removal while the account is logged in")
		}
	}
	const probeUsername string = "ogqga_probe"
	const accountHelper string = "/usr/libexec/qemu-ga/fsfreeze-hook.d/organesson-account-helper"
	var commandStatus int
	var commandError string
	var probeCreated bool
	if passwdFile, err = source.AgentFileRead(t.Context(), "/etc/passwd"); err != nil || strings.Contains(passwdFile.Content, probeUsername+":") {
		t.Fatalf("refusing to overwrite an existing probe account: err=%v", err)
	}
	t.Cleanup(func() {
		if !probeCreated {
			return
		}
		if commandStatus, commandError, err = runLiveAgentCommand(context.Background(), client, source, sourceNode, vmid, []string{accountHelper, "remove-probe"}); err != nil || commandStatus != 0 {
			t.Errorf("clean up QEMU Guest Agent probe account (exit=%d error=%q err=%v)", commandStatus, commandError, err)
		}
	})
	probeCreated = true
	if commandStatus, commandError, err = runLiveAgentCommand(t.Context(), client, source, sourceNode, vmid, []string{accountHelper, "create-probe"}); err != nil {
		t.Fatalf("run QEMU Guest Agent useradd probe: %v", err)
	}
	if commandStatus != 0 {
		if err = inspectGuestUserDeletion(t, client, source, sourceNode, vmid); err != nil {
			t.Logf("additional read-only guest diagnostics failed: %v", err)
		}
		t.Fatalf("QEMU Guest Agent could not create probe user (exit=%d error=%q)", commandStatus, commandError)
	}
	if passwdFile, err = source.AgentFileRead(t.Context(), "/etc/passwd"); err != nil || !strings.Contains(passwdFile.Content, probeUsername+":") {
		t.Fatalf("verify QEMU Guest Agent created the probe account: file=%v err=%v", passwdFile, err)
	}
	if commandStatus, commandError, err = runLiveAgentCommand(t.Context(), client, source, sourceNode, vmid, []string{accountHelper, "remove-probe"}); err != nil || commandStatus != 0 {
		t.Fatalf("remove probe account through QEMU Guest Agent (exit=%d error=%q err=%v)", commandStatus, commandError, err)
	}
	probeCreated = false
	if commandStatus, commandError, err = runLiveAgentCommand(t.Context(), client, source, sourceNode, vmid, []string{"/bin/sh", "-c", "test ! -e /home/" + probeUsername}); err != nil || commandStatus != 0 {
		t.Fatal("probe account home directory remains after userdel --remove")
	}
	t.Log("QEMU Guest Agent created and removed the probe account and its home directory")
	if commandStatus, commandError, err = runLiveAgentCommand(t.Context(), client, source, sourceNode, vmid, []string{accountHelper, "remove-administrator"}); err != nil || commandStatus != 0 {
		t.Fatalf("remove administrator through QEMU Guest Agent (exit=%d error=%q err=%v)", commandStatus, commandError, err)
	}
	if passwdFile, err = source.AgentFileRead(t.Context(), "/etc/passwd"); err != nil || len(interactiveLinuxAccounts(passwdFile.Content)) != 0 {
		t.Fatalf("verify administrator removal: remaining accounts=%v err=%v", interactiveLinuxAccounts(passwdFile.Content), err)
	}
	t.Log("removed administrator and its home directory through QEMU Guest Agent")
}

// runLiveAgentCommand executes one guest command and returns its exit status and stderr.
func runLiveAgentCommand(ctx context.Context, client *pve.Client, source *pve.VirtualMachine, nodeName string, vmid int, command []string) (exitCode int, errData string, err error) {
	var response map[string]interface{}
	if err = client.Post(ctx, fmt.Sprintf("/nodes/%s/qemu/%d/agent/exec", nodeName, vmid), map[string]interface{}{
		"command":    command,
		"input-data": "",
	}, &response); err != nil {
		return
	}
	var pidValue interface{}
	var pidNumber float64
	var found bool
	if pidValue, found = response["pid"]; !found {
		err = fmt.Errorf("guest agent exec returned no process ID (response=%v)", response)
		return
	}
	if pidNumber, found = pidValue.(float64); !found {
		err = fmt.Errorf("guest agent exec returned an invalid process ID (%T)", pidValue)
		return
	}
	var pid int = int(pidNumber)
	exitCode, errData, err = waitLiveAgentExecExit(ctx, client, nodeName, vmid, pid)
	return
}

// TestLiveProxmoxSourcePreflight checks the explicitly selected lab source against the configured cluster.
func TestLiveProxmoxSourcePreflight(t *testing.T) {
	if os.Getenv("ORGANESSON_LIVE_PVE_PREFLIGHT") != "1" {
		t.Skip("set ORGANESSON_LIVE_PVE_PREFLIGHT=1 to inspect a live source VM")
	}
	var sourceID string = os.Getenv("ORGANESSON_LIVE_PVE_SOURCE_ID")
	if sourceID == "" {
		t.Fatal("ORGANESSON_LIVE_PVE_SOURCE_ID must identify the source VM to inspect")
	}
	var expectedOS string = os.Getenv("ORGANESSON_LIVE_PVE_GUEST_OS")
	if expectedOS == "" {
		t.Fatal("ORGANESSON_LIVE_PVE_GUEST_OS must match the source catalog OS ID")
	}
	var configPath string = os.Getenv("ORGANESSON_CONFIG")
	if configPath == "" {
		configPath = "../config.toml"
	}
	if err := config.Init(configPath); err != nil {
		t.Fatalf("load Organesson configuration: %v", err)
	}
	var service *Service = New(config.Cfg.Proxmox)
	var inventory ResourceInventory
	var err error
	if inventory, err = service.ResourceInventory(t.Context()); err != nil {
		t.Fatalf("read live Proxmox resource inventory: %v", err)
	}
	t.Logf("PVE policy targets available: pool classes=%t storage local-lvm=%t", contains(inventory.Pools, "classes"), contains(inventory.Storages, "local-lvm"))
	var vmid int
	if vmid, err = strconv.Atoi(sourceID); err != nil || vmid < 1 {
		t.Fatalf("invalid Proxmox source VMID %q", sourceID)
	}
	var client *pve.Client
	if client, err = newAPIClient(config.Cfg.Proxmox); err != nil {
		t.Fatalf("configure Proxmox API client: %v", err)
	}
	var sourceNode string
	var sourcePool string
	var cluster *pve.Cluster
	if cluster, err = client.Cluster(t.Context()); err != nil {
		t.Fatalf("read Proxmox cluster: %v", err)
	}
	var resources pve.ClusterResources
	if resources, err = cluster.Resources(t.Context(), "vm"); err != nil {
		t.Fatalf("read Proxmox VM inventory: %v", err)
	}
	for _, resource := range resources {
		if resource != nil && resource.Type == "qemu" && resource.VMID == uint64(vmid) {
			sourceNode = resource.Node
			sourcePool = resource.Pool
			break
		}
	}
	if sourceNode == "" {
		t.Fatalf("source VMID %s is not an ordinary QEMU VM in the cluster inventory", sourceID)
	}
	t.Logf("source VM is in Proxmox resource pool %q", sourcePool)
	var node *pve.Node
	if node, err = client.Node(t.Context(), sourceNode); err != nil {
		t.Fatalf("read source node: %v", err)
	}
	var source *pve.VirtualMachine
	if source, err = node.VirtualMachine(t.Context(), vmid); err != nil {
		t.Fatalf("read source VM status: %v", err)
	}
	var startedForCheck bool = source.IsStopped()
	if startedForCheck {
		if os.Getenv("ORGANESSON_LIVE_PVE_POWER_ON") != "1" {
			t.Fatal("source is stopped; set ORGANESSON_LIVE_PVE_POWER_ON=1 to temporarily start it and restore its stopped state")
		}
		t.Cleanup(func() {
			if err := stopLiveSource(t, client, sourceNode, vmid); err != nil {
				t.Errorf("restore source VM to stopped state: %v", err)
			}
		})
		var startTask *pve.Task
		if startTask, err = source.Start(t.Context()); err != nil {
			t.Fatalf("start source VM temporarily for guest-agent preflight: %v", err)
		}
		if err = waitTask(t.Context(), client, startTask); err != nil {
			t.Fatalf("wait for source VM to start: %v", err)
		}
		if err = source.WaitForAgent(t.Context(), 120); err != nil {
			t.Fatalf("wait for source QEMU Guest Agent: %v", err)
		}
		t.Log("started stopped source VM temporarily; cleanup will shut it down after preflight")
	}
	var result PreflightResult
	if result, err = service.InspectTemplate(t.Context(), sourceID, expectedOS); err != nil {
		t.Fatalf("inspect live Proxmox source: %v", err)
	}
	t.Logf("source=%s state=%s agent_reachable=%t guest_os=%s root_verified=%t passed=%t", result.SourceID, result.PowerState, result.AgentReachable, result.GuestOSID, result.GuestAgentRootVerified, result.Passed)
	if !result.Passed || result.PowerState != "running" || !result.AgentReachable {
		t.Fatalf("live source is not ready for provisioning: %#v", result.Checks)
	}
	if !strings.Contains(strings.ToLower(result.GuestOSID), "windows") && !result.GuestAgentRootVerified {
		t.Fatal("live Linux source did not prove QEMU Guest Agent root execution")
	}
	if !strings.Contains(strings.ToLower(result.GuestOSID), "windows") {
		var passwdFile *pve.AgentFileRead
		if passwdFile, err = source.AgentFileRead(t.Context(), "/etc/passwd"); err != nil {
			t.Fatalf("read Linux account metadata through QEMU Guest Agent: %v", err)
		}
		if bool(passwdFile.Truncated) {
			t.Fatal("QEMU Guest Agent returned truncated Linux account metadata")
		}
		var interactiveAccounts []string = interactiveLinuxAccounts(passwdFile.Content)
		var accountToRemove string = os.Getenv("ORGANESSON_LIVE_PVE_REMOVE_ACCOUNT")
		if accountToRemove != "" {
			if sourcePool != "organesson" {
				t.Fatalf("refusing guest account removal: source VM is in pool %q, not organesson", sourcePool)
			}
			if !validLinuxUsername(accountToRemove) || (len(interactiveAccounts) != 0 && (len(interactiveAccounts) != 1 || interactiveAccounts[0] != accountToRemove)) {
				t.Fatalf("refusing guest account removal: the requested account is not the only interactive non-system account (accounts=%v)", interactiveAccounts)
			}
			if os.Getenv("ORGANESSON_LIVE_PVE_DIAGNOSTICS") == "1" {
				if err = inspectGuestUserDeletion(t, client, source, sourceNode, vmid); err != nil {
					t.Fatalf("inspect guest userdel execution restrictions: %v", err)
				}
				t.Fatal("read-only guest diagnostics completed; no guest account was modified")
			}
			if len(interactiveAccounts) == 1 {
				var loggedInUsers []*pve.AgentUser
				if loggedInUsers, err = source.AgentGetUsers(t.Context()); err != nil {
					t.Fatalf("check active guest logins before account removal: %v", err)
				}
				for _, loggedInUser := range loggedInUsers {
					if loggedInUser != nil && loggedInUser.User == accountToRemove {
						t.Fatalf("refusing guest account removal while %q is logged in", accountToRemove)
					}
				}
				var execPID int
				if execPID, err = source.AgentExec(t.Context(), guestAgentCommand("/usr/sbin/userdel", "--remove", accountToRemove), ""); err != nil {
					t.Fatalf("remove the explicitly selected temporary guest account: %v", err)
				}
				var exitCode int
				var execErrData string
				if exitCode, execErrData, err = waitLiveAgentExecExit(t.Context(), client, sourceNode, vmid, execPID); err != nil {
					t.Fatalf("wait for temporary guest account removal: %v", err)
				}
				if exitCode != 0 {
					t.Fatalf("temporary guest account removal failed: exit=%d error=%s", exitCode, execErrData)
				}
				t.Logf("removed temporary guest account %q and its home directory", accountToRemove)
			} else {
				t.Logf("temporary guest account %q was already absent", accountToRemove)
			}
			if passwdFile, err = source.AgentFileRead(t.Context(), "/etc/passwd"); err != nil {
				t.Fatalf("verify the guest account removal: %v", err)
			}
			if bool(passwdFile.Truncated) || len(interactiveLinuxAccounts(passwdFile.Content)) != 0 {
				t.Fatalf("interactive guest account remains after removing %q", accountToRemove)
			}
			t.Logf("removed temporary guest account %q and its home directory", accountToRemove)
		} else if len(interactiveAccounts) > 0 {
			t.Fatalf("source VM still has interactive non-system guest accounts %v; remove them before marking the catalog source provisioning-ready", interactiveAccounts)
		}
		if templateID := os.Getenv("ORGANESSON_LIVE_TEMPLATE_ID"); templateID != "" {
			if err = recordLiveTemplateReadiness(t, templateID, result.GuestAgentRootVerified); err != nil {
				t.Fatalf("record source preflight/readiness in Organesson: %v", err)
			}
		}
	}
}

// waitLiveAgentExecExit polls guest-agent command status while ignoring irrelevant PVE bool/number fields.
func waitLiveAgentExecExit(ctx context.Context, client *pve.Client, nodeName string, vmid int, pid int) (exitCode int, errData string, err error) {
	var deadline time.Time = time.Now().Add(2 * time.Minute)
	for time.Now().Before(deadline) {
		var status liveAgentExecStatus
		if err = client.Get(ctx, fmt.Sprintf("/nodes/%s/qemu/%d/agent/exec-status?pid=%d", nodeName, vmid, pid), &status); err != nil {
			return
		}
		if status.Exited != 0 {
			exitCode = status.ExitCode
			errData = status.ErrData
			return
		}
		select {
		case <-ctx.Done():
			err = ctx.Err()
			return
		case <-time.After(time.Second):
		}
	}
	err = fmt.Errorf("guest-agent command did not exit before its timeout")
	return
}

// inspectGuestUserDeletion records binary permissions, mount flags, and SELinux mode without altering the guest.
func inspectGuestUserDeletion(t *testing.T, client *pve.Client, source *pve.VirtualMachine, nodeName string, vmid int) (err error) {
	t.Helper()
	const diagnosticsPath string = "/tmp/organesson-userdel-diagnostics.txt"
	var diagnosticCommand string = "{ /usr/bin/id; /usr/bin/id -Z; /usr/bin/stat -c '%A %a %U:%G %n' /usr/sbin/useradd /usr/sbin/userdel; /usr/bin/ls -lZ /usr/sbin/useradd /usr/sbin/userdel; /usr/sbin/getenforce; /usr/sbin/ausearch -m AVC -ts recent; } > " + diagnosticsPath + " 2>&1"
	var pid int
	if pid, err = source.AgentExec(t.Context(), []string{"/bin/sh", "-c", diagnosticCommand}, ""); err != nil {
		return
	}
	var exitCode int
	var errData string
	if exitCode, errData, err = waitLiveAgentExecExit(t.Context(), client, nodeName, vmid, pid); err != nil {
		return
	}
	var output *pve.AgentFileRead
	if output, err = source.AgentFileRead(t.Context(), diagnosticsPath); err != nil {
		return
	}
	t.Logf("guest userdel diagnostics (exit=%d error=%q): %s", exitCode, errData, strings.TrimSpace(output.Content))
	var cleanupPID int
	if cleanupPID, err = source.AgentExec(t.Context(), []string{"/bin/rm", "-f", diagnosticsPath}, ""); err != nil {
		return
	}
	_, _, err = waitLiveAgentExecExit(t.Context(), client, nodeName, vmid, cleanupPID)
	return
}

// interactiveLinuxAccounts returns named non-system accounts with login shells.
func interactiveLinuxAccounts(contents string) (accounts []string) {
	for _, line := range strings.Split(contents, "\n") {
		var fields []string = strings.SplitN(line, ":", 7)
		if len(fields) != 7 {
			continue
		}
		var uid int
		if uid, _ = strconv.Atoi(fields[2]); uid < 1000 {
			continue
		}
		if strings.Contains(fields[6], "nologin") || fields[6] == "/bin/false" {
			continue
		}
		accounts = append(accounts, fields[0])
	}
	return
}

// validLinuxUsername prevents the live account-removal helper from accepting command syntax.
func validLinuxUsername(username string) (valid bool) {
	if username == "" || len(username) > 32 || username[0] < 'a' || username[0] > 'z' {
		return
	}
	for _, character := range username[1:] {
		if (character < 'a' || character > 'z') && (character < '0' || character > '9') && character != '_' && character != '-' {
			return
		}
	}
	valid = true
	return
}

// recordLiveTemplateReadiness persists a passing PVE preflight and operator readiness through the admin API.
func recordLiveTemplateReadiness(t *testing.T, templateID string, guestAgentRootVerified bool) (err error) {
	t.Helper()
	var sessionCookie string = os.Getenv("ORGANESSON_LIVE_ADMIN_SESSION")
	var csrfToken string = os.Getenv("ORGANESSON_LIVE_ADMIN_CSRF")
	if sessionCookie == "" || csrfToken == "" {
		err = fmt.Errorf("ORGANESSON_LIVE_ADMIN_SESSION and ORGANESSON_LIVE_ADMIN_CSRF are required")
		return
	}
	var endpoint string = strings.TrimRight(os.Getenv("ORGANESSON_LIVE_BACKEND_URL"), "/")
	if endpoint == "" {
		endpoint = "http://127.0.0.1:6800"
	}
	var client *http.Client = &http.Client{Timeout: 30 * time.Second}
	var requests []struct {
		method string
		path   string
		body   string
	} = []struct {
		method string
		path   string
		body   string
	}{
		{method: http.MethodPost, path: "/api/v1/admin/vm-templates/" + url.PathEscape(templateID) + "/preflight", body: "{}"},
		{method: http.MethodPut, path: "/api/v1/admin/vm-templates/" + url.PathEscape(templateID) + "/readiness", body: fmt.Sprintf(`{"guest_agent_root_verified":%t,"provisioning_account_removed":true}`, guestAgentRootVerified)},
	}
	for _, requestData := range requests {
		var request *http.Request
		if request, err = http.NewRequest(requestData.method, endpoint+requestData.path, strings.NewReader(requestData.body)); err != nil {
			return
		}
		request.Header.Set("Content-Type", "application/json")
		request.Header.Set("Cookie", "session_id="+sessionCookie+"; csrf_="+csrfToken)
		request.Header.Set("X-Csrf-Token", csrfToken)
		var response *http.Response
		if response, err = client.Do(request); err != nil {
			return
		}
		_ = response.Body.Close()
		if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
			err = fmt.Errorf("admin API %s %s returned HTTP %d", requestData.method, requestData.path, response.StatusCode)
			return
		}
	}
	t.Logf("catalog preflight recorded and source marked ready through the admin API")
	return
}

// stopLiveSource gracefully restores a temporarily started source to its initial stopped state.
func stopLiveSource(t *testing.T, client *pve.Client, nodeName string, vmid int) (err error) {
	t.Helper()
	var ctx context.Context
	var cancel context.CancelFunc
	ctx, cancel = context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	var node *pve.Node
	if node, err = client.Node(ctx, nodeName); err != nil {
		return
	}
	var source *pve.VirtualMachine
	if source, err = node.VirtualMachine(ctx, vmid); err != nil {
		return
	}
	if source.IsStopped() {
		return
	}
	var shutdownTask *pve.Task
	if shutdownTask, err = source.Shutdown(ctx); err != nil {
		return
	}
	if err = waitTask(ctx, client, shutdownTask); err != nil {
		return
	}
	var deadline time.Time = time.Now().Add(2 * time.Minute)
	for time.Now().Before(deadline) {
		if source, err = node.VirtualMachine(ctx, vmid); err != nil {
			return
		}
		if source.IsStopped() {
			return
		}
		select {
		case <-ctx.Done():
			err = ctx.Err()
			return
		case <-time.After(time.Second):
		}
	}
	err = fmt.Errorf("source VM %d did not report stopped after graceful shutdown", vmid)
	return
}
