package proxmox

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	pve "github.com/luthermonson/go-proxmox"
	"github.com/z46-dev/organesson/backend/config"
)

const templateReadyTag string = "organesson-template-valid"

type (
	// TemplatePreparationRequest contains the source VM, trusted prep script, and explicit accounts to remove.
	TemplatePreparationRequest struct {
		SourceID   string            `json:"source_id"`
		ExpectedOS string            `json:"expected_os"`
		Scripts    map[string]string `json:"scripts"`
		Usernames  []string          `json:"usernames"`
	}

	apiTemplatePreparationDriver struct {
		settings config.ProxmoxConfiguration
	}
)

var templateUsernamePattern = regexp.MustCompile(`^[a-z_][a-z0-9_-]{0,31}\$?$`)
var windowsTemplateUsernamePattern = regexp.MustCompile(`^[A-Za-z0-9_.-]{1,20}$`)

func validLinuxDeletionName(username string) (valid bool) {
	valid = templateUsernamePattern.MatchString(username) && username != "root"
	return
}

func validWindowsDeletionName(username string) (valid bool) {
	valid = windowsTemplateUsernamePattern.MatchString(username) && !strings.HasPrefix(username, "-")
	return
}

// TemplatePreparationOSFamily selects the supported guest preparation adapter for a catalog OS value.
func TemplatePreparationOSFamily(value string) (family string) {
	value = strings.ToLower(strings.TrimSpace(value))
	switch {
	case IsLinuxTemplateOS(value):
		family = "linux"
	case value == "windows" || strings.HasPrefix(value, "windows-") || value == "windows11" || value == "windowsserver2025":
		family = "windows"
	case value == "bsd" || value == "freebsd":
		family = "freebsd"
	}
	return
}

// IsTemplatePreparationSupported reports whether the catalog OS has a tested preparation adapter.
func IsTemplatePreparationSupported(value string) (supported bool) {
	supported = TemplatePreparationOSFamily(value) != ""
	return
}

// ValidateTemplatePreparationScripts accepts only the script bundle names required by the selected OS adapter.
func ValidateTemplatePreparationScripts(expectedOS string, scripts map[string]string) (err error) {
	var required []string
	switch TemplatePreparationOSFamily(expectedOS) {
	case "linux":
		required = []string{"linux"}
	case "windows":
		required = []string{"windows-common", "windows-entry"}
	case "freebsd":
		required = []string{"freebsd"}
	default:
		err = errors.New("unsupported source operating system")
		return
	}
	if len(scripts) != len(required) {
		err = errors.New("preparation script bundle does not match the selected operating system")
		return
	}
	var totalBytes int
	for _, name := range required {
		if strings.TrimSpace(scripts[name]) == "" {
			err = fmt.Errorf("preparation bundle is missing %s", name)
			return
		}
		totalBytes += len(scripts[name])
	}
	if totalBytes > 512*1024 {
		err = errors.New("preparation script bundle exceeds 512 KiB")
	}
	return
}

// Prepare powers on a registered ordinary VM, prepares it over QGA, removes requested Linux accounts, then shuts it down and tags it.
func (driver *apiTemplatePreparationDriver) Prepare(ctx context.Context, request TemplatePreparationRequest) (result PreflightResult, err error) {
	var family string = TemplatePreparationOSFamily(request.ExpectedOS)
	if family == "" {
		err = errors.New("unsupported source operating system")
		return
	}
	if err = ValidateTemplatePreparationScripts(request.ExpectedOS, request.Scripts); err != nil {
		return
	}
	var vmid int
	if vmid, err = strconv.Atoi(request.SourceID); err != nil || vmid < 1 {
		err = errors.New("source identifier must be a positive Proxmox VMID")
		return
	}
	if len(request.Usernames) > 32 {
		err = errors.New("at most 32 guest accounts may be removed in one preparation run")
		return
	}
	for _, username := range request.Usernames {
		var valid bool
		switch family {
		case "linux", "freebsd":
			valid = validLinuxDeletionName(username) && (family != "freebsd" || username != "root")
		case "windows":
			valid = validWindowsDeletionName(username)
		}
		if !valid {
			err = fmt.Errorf("invalid or protected %s account name %q", family, username)
			return
		}
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
			result = PreflightResult{SourceID: request.SourceID, Node: resource.Node, Name: resource.Name, PowerState: resource.Status, IsQEMU: true, IsProxmoxTemplate: resource.Template != 0, CheckedAt: time.Now().UTC()}
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
	var guestFilesDelivered bool
	defer func() {
		if err == nil {
			return
		}
		var operationErr error = err
		var cleanupContext context.Context
		var cancel context.CancelFunc
		cleanupContext, cancel = context.WithTimeout(context.Background(), 3*time.Minute)
		defer cancel()
		var cleanupErr error
		var stateErr error = vm.Ping(cleanupContext)
		if stateErr == nil && vm.IsStopped() {
			err = operationErr
			return
		}
		if guestFilesDelivered && stateErr == nil && vm.IsRunning() {
			cleanupErr = cleanupTemplatePrepFiles(cleanupContext, driver.settings, vm, vmid, family)
		}
		var shutdownTask *pve.Task
		var shutdownErr error
		if shutdownTask, shutdownErr = vm.Shutdown(cleanupContext); shutdownErr == nil {
			shutdownErr = waitTask(cleanupContext, client, shutdownTask)
		}
		if shutdownErr == nil {
			shutdownErr = waitVMStopped(cleanupContext, client, vmid, nodeName)
		}
		err = operationErr
		if cleanupErr != nil || shutdownErr != nil {
			err = fmt.Errorf("%w (cleanup error: file cleanup=%v, shutdown=%v)", operationErr, cleanupErr, shutdownErr)
		}
	}()
	if tagTask, tagErr := vm.RemoveTag(ctx, templateReadyTag); tagErr == nil && tagTask != nil {
		if err = waitTask(ctx, client, tagTask); err != nil {
			return
		}
	} else if tagErr != nil && !errors.Is(tagErr, pve.ErrNoop) {
		err = fmt.Errorf("remove stale Organesson template-valid tag: %w", tagErr)
		return
	}
	if vm.VirtualMachineConfig == nil || !guestAgentEnabled(vm.VirtualMachineConfig.Agent) {
		err = errors.New("QEMU Guest Agent must be enabled in the Proxmox VM configuration before preparation")
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
		err = errors.New("source VM must be in a normal running state before preparation")
		return
	}
	if err = waitForGuestAgent(ctx, vm); err != nil {
		return
	}
	var osInfo *pve.AgentOsInfo
	if osInfo, err = vm.AgentOsInfo(ctx); err != nil {
		return
	}
	result.GuestOSID = strings.ToLower(osInfo.ID)
	result.GuestOSName = osInfo.PrettyName
	if !guestOSMatchesTemplate(request.ExpectedOS, result.GuestOSID) {
		err = fmt.Errorf("registered OS %q does not match detected guest OS %q", request.ExpectedOS, result.GuestOSID)
		return
	}
	guestFilesDelivered = true
	if err = prepareTemplateGuest(ctx, driver.settings, vm, vmid, family, request.Scripts); err != nil {
		return
	}
	for _, username := range request.Usernames {
		if err = removeTemplateGuestAccount(ctx, driver.settings, vm, vmid, family, username); err != nil {
			return
		}
	}
	if result.GuestAgentRootVerified, err = verifyTemplateGuestExecution(ctx, driver.settings, vm, vmid, family); err != nil || !result.GuestAgentRootVerified {
		if err == nil {
			err = fmt.Errorf("QEMU Guest Agent did not verify unrestricted %s execution after preparation", familyExecutionName(family))
		}
		return
	}
	if err = cleanupTemplatePrepFiles(ctx, driver.settings, vm, vmid, family); err != nil {
		return
	}
	guestFilesDelivered = false
	if family == "windows" {
		if err = finalizeWindowsTemplate(ctx, driver.settings, vm, vmid); err != nil {
			return
		}
	}
	result.AgentConfigured = true
	result.AgentReachable = true
	result.PowerState = "running"
	result.Passed = true
	result.Checks = []Check{
		{Name: "source_exists", Passed: true, Required: true, Details: "Ordinary QEMU source VM was found."},
		{Name: "qemu_guest_agent_enabled", Passed: true, Required: true, Details: "QEMU Guest Agent is enabled and reachable."},
		{Name: "guest_os_matches", Passed: true, Required: true, Details: "Guest OS was detected as " + result.GuestOSName + "."},
		{Name: "guest_agent_root_execution", Passed: true, Required: true, Details: "QEMU Guest Agent executed a command as unrestricted " + familyExecutionName(family) + "."},
		{Name: "preparation_script", Passed: true, Required: true, Details: "The " + strings.ToUpper(family) + " preparation script completed successfully."},
		{Name: "requested_accounts_removed", Passed: true, Required: true, Details: fmt.Sprintf("Verified %d explicitly requested account(s) are absent.", len(request.Usernames))},
	}
	var shutdownTask *pve.Task
	if shutdownTask, err = vm.Shutdown(ctx); err != nil {
		result.Passed = false
		return
	}
	if err = waitTask(ctx, client, shutdownTask); err != nil {
		result.Passed = false
		return
	}
	if err = waitVMStopped(ctx, client, vmid, nodeName); err != nil {
		result.Passed = false
		return
	}
	var tagTask *pve.Task
	if tagTask, err = vm.AddTag(ctx, templateReadyTag); err != nil && !errors.Is(err, pve.ErrNoop) {
		result.Passed = false
		return
	} else if tagTask != nil {
		if err = waitTask(ctx, client, tagTask); err != nil {
			result.Passed = false
			return
		}
	}
	result.PowerState = "stopped"
	result.CheckedAt = time.Now().UTC()
	return
}

// IsLinuxTemplateOS reports whether a catalog OS choice can use the shared Linux preparation workflow.
func IsLinuxTemplateOS(value string) (supported bool) {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "almalinux", "alpine", "amzn", "arch", "centos", "debian", "endeavouros", "fedora", "linux", "linuxmint", "manjaro", "ol", "opensuse", "opensuse-leap", "opensuse-tumbleweed", "oracle", "pop", "rhel", "rocky", "sled", "sles", "ubuntu":
		supported = true
	}
	return
}

func guestAgentEnabled(setting string) (enabled bool) {
	var mode string = strings.Split(strings.TrimSpace(setting), ",")[0]
	enabled = mode == "1" || mode == "enabled=1"
	return
}

func guestOSMatchesTemplate(expected string, detected string) (matches bool) {
	expected = strings.ToLower(strings.TrimSpace(expected))
	detected = strings.ToLower(strings.TrimSpace(detected))
	var family string = TemplatePreparationOSFamily(expected)
	switch family {
	case "linux":
		matches = expected == "linux" || expected == detected
	case "windows":
		matches = strings.Contains(detected, "windows") || strings.Contains(detected, "win")
	case "freebsd":
		matches = strings.Contains(detected, "freebsd") || detected == "bsd"
	}
	if !matches && (expected == "rhel" || expected == "rocky" || expected == "almalinux" || expected == "centos" || expected == "ol" || expected == "oracle" || expected == "amzn") {
		matches = detected == "rhel" || detected == "rocky" || detected == "almalinux" || detected == "centos" || detected == "ol" || detected == "oracle" || detected == "amzn"
	}
	if !matches && (expected == "opensuse" || expected == "sles" || expected == "sled") {
		matches = strings.HasPrefix(detected, "opensuse") || detected == "sles" || detected == "sled"
	}
	return
}

const windowsPowerShellPath string = `C:\Windows\System32\WindowsPowerShell\v1.0\powershell.exe`

func prepareTemplateGuest(ctx context.Context, settings config.ProxmoxConfiguration, vm *pve.VirtualMachine, vmid int, family string, scripts map[string]string) (err error) {
	switch family {
	case "linux":
		const scriptPath string = "/run/organesson-template-prep.sh"
		if err = writeGuestFile(ctx, vm, scriptPath, []byte(scripts["linux"])); err != nil {
			return
		}
		_, err = runGuestCommand(ctx, settings, vm, vmid, guestAgentCommand("/bin/bash", scriptPath), 900)
	case "windows":
		const directory string = `C:\Windows\Temp\OrganessonPrep`
		const commonPath string = directory + `\og-prep-windows-common.ps1`
		const entryPath string = directory + `\og-prep-entry.ps1`
		if _, err = runWindowsPowerShell(ctx, settings, vm, vmid, `New-Item -ItemType Directory -Force -Path 'C:\Windows\Temp\OrganessonPrep' | Out-Null`, 30); err != nil {
			return
		}
		if err = vm.AgentFileWrite(ctx, commonPath, []byte(scripts["windows-common"])); err != nil {
			return
		}
		if err = vm.AgentFileWrite(ctx, entryPath, []byte(scripts["windows-entry"])); err != nil {
			return
		}
		_, err = runWindowsPowerShell(ctx, settings, vm, vmid, `& 'C:\Windows\Temp\OrganessonPrep\og-prep-entry.ps1' -PrepareOnly`, 3600)
	case "freebsd":
		const scriptPath string = "/tmp/organesson-template-prep.sh"
		if err = writeGuestFile(ctx, vm, scriptPath, []byte(scripts["freebsd"])); err != nil {
			return
		}
		_, err = runGuestCommand(ctx, settings, vm, vmid, []string{"/bin/sh", scriptPath}, 1800)
	}
	return
}

func removeTemplateGuestAccount(ctx context.Context, settings config.ProxmoxConfiguration, vm *pve.VirtualMachine, vmid int, family string, username string) (err error) {
	switch family {
	case "linux":
		err = removeLinuxGuestAccount(ctx, settings, vm, vmid, username)
	case "freebsd":
		err = removeFreeBSDGuestAccount(ctx, settings, vm, vmid, username)
	case "windows":
		var command string = windowsAccountRemovalCommand(username)
		_, err = runWindowsPowerShell(ctx, settings, vm, vmid, command, 120)
	}
	return
}

func verifyTemplateGuestExecution(ctx context.Context, settings config.ProxmoxConfiguration, vm *pve.VirtualMachine, vmid int, family string) (verified bool, err error) {
	switch family {
	case "linux":
		verified, err = verifyLinuxGuestAgentRoot(ctx, settings, vm, vmid)
	case "freebsd":
		var status guestAgentExecStatus
		status, err = runGuestCommand(ctx, settings, vm, vmid, []string{"/usr/bin/id", "-u"}, 15)
		verified = err == nil && strings.TrimSpace(status.OutData) == "0"
	case "windows":
		var status guestAgentExecStatus
		status, err = runWindowsPowerShell(ctx, settings, vm, vmid, "whoami.exe", 15)
		verified = err == nil && strings.EqualFold(strings.TrimSpace(status.OutData), `nt authority\system`)
	}
	return
}

func cleanupTemplatePrepFiles(ctx context.Context, settings config.ProxmoxConfiguration, vm *pve.VirtualMachine, vmid int, family string) (err error) {
	switch family {
	case "linux":
		_, err = runGuestCommand(ctx, settings, vm, vmid, guestAgentCommand("/bin/rm", "-f", "--", "/run/organesson-template-prep.sh"), 15)
	case "freebsd":
		_, err = runGuestCommand(ctx, settings, vm, vmid, []string{"/bin/rm", "-f", "--", "/tmp/organesson-template-prep.sh"}, 15)
	case "windows":
		_, err = runWindowsPowerShell(ctx, settings, vm, vmid, `Remove-Item -LiteralPath 'C:\Windows\Temp\OrganessonPrep' -Recurse -Force -ErrorAction SilentlyContinue`, 30)
	}
	return
}

func finalizeWindowsTemplate(ctx context.Context, settings config.ProxmoxConfiguration, vm *pve.VirtualMachine, vmid int) (err error) {
	const sysprepCommand string = `Start-Process -FilePath "$env:SystemRoot\System32\Sysprep\Sysprep.exe" -ArgumentList @('/generalize','/oobe','/quit','/quiet') -Wait -PassThru | ForEach-Object { if ($_.ExitCode -ne 0) { exit $_.ExitCode } }`
	_, err = runWindowsPowerShell(ctx, settings, vm, vmid, sysprepCommand, 900)
	return
}

func runWindowsPowerShell(ctx context.Context, settings config.ProxmoxConfiguration, vm *pve.VirtualMachine, vmid int, script string, timeoutSeconds int) (status guestAgentExecStatus, err error) {
	status, err = runGuestCommand(ctx, settings, vm, vmid, []string{windowsPowerShellPath, "-NoProfile", "-NonInteractive", "-ExecutionPolicy", "Bypass", "-Command", script}, timeoutSeconds)
	return
}

func windowsAccountRemovalCommand(username string) (command string) {
	command = fmt.Sprintf(`$Name = '%s'; $User = Get-LocalUser -Name $Name -ErrorAction SilentlyContinue; if ($null -eq $User) { exit 0 }; if ($User.SID.Value -match '-50[0-4]$') { throw 'Refusing to remove a built-in Windows account.' }; $Processes = Get-Process -IncludeUserName -ErrorAction Stop | Where-Object { $_.UserName -and $_.UserName.ToLower().EndsWith(('\' + $Name.ToLower())) }; if ($Processes) { throw "Account $Name has running processes." }; $Profiles = @(Get-CimInstance Win32_UserProfile | Where-Object { $_.SID -eq $User.SID.Value }); if ($Profiles | Where-Object { $_.Loaded }) { throw "Account $Name has a loaded profile." }; Remove-LocalUser -Name $Name; $Profiles | Remove-CimInstance -ErrorAction Stop; if (Get-LocalUser -Name $Name -ErrorAction SilentlyContinue) { throw "Account $Name still exists after removal." }`, username)
	return
}

func removeFreeBSDGuestAccount(ctx context.Context, settings config.ProxmoxConfiguration, vm *pve.VirtualMachine, vmid int, username string) (err error) {
	const removalScript string = `set -eu
name=$1
command -v pw >/dev/null 2>&1 || { echo "FreeBSD pw command is unavailable" >&2; exit 1; }
entry=$(pw usershow "$name" 2>/dev/null) || exit 0
uid=$(printf '%s\n' "$entry" | awk -F: '{print $3}')
[ "$uid" -ge 1000 ] || { echo "refusing to remove system account $name (uid $uid)" >&2; exit 1; }
if command -v pgrep >/dev/null 2>&1; then
    if pgrep -u "$name" >/dev/null 2>&1; then
        echo "account $name has running processes" >&2
        exit 1
    fi
elif command -v ps >/dev/null 2>&1; then
    processes=$(ps -U "$name" -o pid= 2>/dev/null) || { echo "could not verify running processes for $name" >&2; exit 1; }
    if [ -n "$processes" ]; then
        echo "account $name has running processes" >&2
        exit 1
    fi
else
    echo "cannot verify whether account $name has running processes" >&2
    exit 1
fi
pw userdel -r "$name"
if pw usershow "$name" >/dev/null 2>&1; then
    echo "account $name still exists after deletion" >&2
    exit 1
fi`
	var command []string = []string{"/bin/sh", "-c", removalScript, "organesson-account-remove", username}
	_, err = runGuestCommand(ctx, settings, vm, vmid, command, 60)
	return
}

func familyExecutionName(family string) (name string) {
	switch family {
	case "linux":
		name = "root"
	case "freebsd":
		name = "root"
	case "windows":
		name = "SYSTEM"
	}
	return
}

func writeGuestFile(ctx context.Context, vm *pve.VirtualMachine, path string, content []byte) (err error) {
	const chunkSize int = 40 * 1024
	for offset := 0; offset < len(content); offset += chunkSize {
		var end int = offset + chunkSize
		if end > len(content) {
			end = len(content)
		}
		if err = vm.AgentFileWrite(ctx, path, content[offset:end]); err != nil {
			return
		}
	}
	return
}

func runGuestCommand(ctx context.Context, settings config.ProxmoxConfiguration, vm *pve.VirtualMachine, vmid int, command []string, timeoutSeconds int) (status guestAgentExecStatus, err error) {
	var pid int
	if pid, err = vm.AgentExec(ctx, command, ""); err != nil {
		return
	}
	if status, err = waitForGuestExecExit(ctx, settings, vm.Node, vmid, pid, timeoutSeconds); err != nil {
		return
	}
	if status.ExitCode != 0 {
		err = fmt.Errorf("guest command failed (exit %d): %s", status.ExitCode, strings.TrimSpace(status.ErrData+" "+status.OutData))
	}
	return
}

func removeLinuxGuestAccount(ctx context.Context, settings config.ProxmoxConfiguration, vm *pve.VirtualMachine, vmid int, username string) (err error) {
	const removalScript string = `set -eu
name=$1
command -v id >/dev/null 2>&1 || { echo "id command is unavailable" >&2; exit 1; }
if uid=$(id -u "$name" 2>/dev/null); then
    :
elif command -v getent >/dev/null 2>&1 && getent passwd "$name" >/dev/null 2>&1; then
    echo "could not resolve account $name" >&2
    exit 1
else
    exit 0
fi
[ "$uid" -ge 1000 ] || { echo "refusing to remove system account $name (uid $uid)" >&2; exit 1; }
if command -v pgrep >/dev/null 2>&1; then
    if pgrep -u "$name" >/dev/null 2>&1; then
        echo "account $name has running processes" >&2
        exit 1
    fi
elif command -v ps >/dev/null 2>&1; then
    processes=$(ps -u "$name" -o pid= 2>/dev/null) || { echo "could not verify running processes for $name" >&2; exit 1; }
    if [ -n "$processes" ]; then
        echo "account $name has running processes" >&2
        exit 1
    fi
else
    echo "cannot verify whether account $name has running processes" >&2
    exit 1
fi
if command -v userdel >/dev/null 2>&1; then
    userdel --remove "$name"
elif command -v deluser >/dev/null 2>&1; then
    deluser --remove-home "$name"
else
    echo "no supported account deletion command found" >&2
    exit 1
fi
if id -u "$name" >/dev/null 2>&1; then
    echo "account $name still exists after deletion" >&2
    exit 1
fi`
	var command []string = guestAgentCommand("/bin/sh", "-c", removalScript, "organesson-account-remove", username)
	_, err = runGuestCommand(ctx, settings, vm, vmid, command, 60)
	return
}

func waitVMStopped(ctx context.Context, client *pve.Client, vmid int, nodeName string) (err error) {
	var deadline <-chan time.Time = time.After(180 * time.Second)
	var ticker *time.Ticker = time.NewTicker(2 * time.Second)
	defer ticker.Stop()
	for {
		var node *pve.Node
		if node, err = client.Node(ctx, nodeName); err != nil {
			return
		}
		var vm *pve.VirtualMachine
		if vm, err = node.VirtualMachine(ctx, vmid); err != nil {
			return
		}
		if vm.Ping(ctx) == nil && vm.IsStopped() {
			return
		}
		select {
		case <-ctx.Done():
			err = ctx.Err()
			return
		case <-deadline:
			err = errors.New("source VM did not shut down within 180 seconds")
			return
		case <-ticker.C:
		}
	}
}
