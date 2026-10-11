package proxmox

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	pve "github.com/luthermonson/go-proxmox"
	"github.com/z46-dev/organesson/backend/config"
)

const (
	templateReadyTag                  string = "organesson-template-valid"
	windowsValidationCloneDescription string = "Temporary Organesson Windows first-boot validation clone; safe to remove"
)

type (
	// TemplatePreparationRequest contains the source VM, trusted prep script, and explicit accounts to remove.
	TemplatePreparationRequest struct {
		SourceID   string            `json:"source_id"`
		ExpectedOS string            `json:"expected_os"`
		Scripts    map[string]string `json:"scripts"`
		Usernames  []string          `json:"usernames"`
		Progress   func(string)      `json:"-"`
	}

	apiTemplatePreparationDriver struct {
		settings config.ProxmoxConfiguration
	}
)

var templateUsernamePattern = regexp.MustCompile(`^[a-z_][a-z0-9_-]{0,31}\$?$`)
var windowsTemplateUsernamePattern = regexp.MustCompile(`^[A-Za-z0-9_.-]{1,20}$`)

// writePreparationProgress reports a concise lifecycle milestone when a caller requests progress updates.
func writePreparationProgress(request TemplatePreparationRequest, message string) {
	writeTemplatePreparationProgress(request.Progress, message)
}

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
		required = []string{"windows"}
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
	writePreparationProgress(request, "Checking source VM and preparation settings.")
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
	var sourcePool string
	for _, resource := range resources {
		if resource != nil && resource.Type == "qemu" && resource.VMID == uint64(vmid) {
			nodeName = resource.Node
			sourcePool = resource.Pool
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
		writePreparationProgress(request, "Starting the source VM.")
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
	writePreparationProgress(request, "QEMU Guest Agent is reachable; detecting guest OS.")
	var osInfo *pve.AgentOsInfo
	if osInfo, err = vm.AgentOsInfo(ctx); err != nil {
		return
	}
	result.GuestOSID = strings.ToLower(osInfo.ID)
	result.GuestOSName = osInfo.PrettyName
	if family == "freebsd" && result.GuestOSID == "" {
		if err = detectFreeBSDGuestMetadata(ctx, driver.settings, vm, vmid, &result); err != nil {
			return
		}
	}
	if !guestOSMatchesTemplate(request.ExpectedOS, result.GuestOSID) {
		err = fmt.Errorf("registered OS %q does not match detected guest OS %q", request.ExpectedOS, result.GuestOSID)
		return
	}
	writePreparationProgress(request, "Running the guest preparation script.")
	var filesMayExist bool
	if filesMayExist, err = prepareTemplateGuest(ctx, driver.settings, vm, vmid, family, request.Scripts, request.Progress); filesMayExist {
		guestFilesDelivered = true
	}
	if err != nil {
		return
	}
	for _, username := range request.Usernames {
		writePreparationProgress(request, fmt.Sprintf("Removing requested guest account %q.", username))
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
		writePreparationProgress(request, "Preparing unattended Windows first boot and sealing the source VM.")
		if err = finalizeWindowsTemplate(ctx, client, driver.settings, vm, vmid, request.Progress); err != nil {
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
	if family == "windows" {
		writePreparationProgress(request, "Booting a temporary network-disconnected Windows clone to verify OOBE completes unattended to the logon screen.")
		if err = validateWindowsFirstBoot(ctx, client, vm, nodeName, sourcePool, driver.settings, request.Progress); err != nil {
			result.Passed = false
			return
		}
		result.Checks = append(result.Checks, Check{Name: "windows_first_boot_specialize", Passed: true, Required: true, Details: "A disposable network-disconnected clone completed OOBE automatically and created the Organesson automation administrator."})
	}
	writePreparationProgress(request, "All checks passed; marking the source ready and leaving it stopped.")
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

const windowsBitLockerStatusCommand string = `$ErrorActionPreference = 'Stop'
$namespace = 'root\CIMV2\Security\MicrosoftVolumeEncryption'
try {
    $volume = Get-CimInstance -Namespace $namespace -ClassName Win32_EncryptableVolume -Filter ("DriveLetter='{0}'" -f $env:SystemDrive) -ErrorAction Stop
} catch {
    if ($_.Exception.Message -match '(?i)invalid namespace|0x8004100e') {
        $manageBde = Join-Path $env:SystemRoot 'System32\manage-bde.exe'
        $service = Get-Service -Name 'BDESVC' -ErrorAction SilentlyContinue
        if (-not (Test-Path -LiteralPath $manageBde -PathType Leaf) -and $null -eq $service) {
            Write-Output 'FullyDecrypted|0'
            exit 0
        }
        throw 'The BitLocker provider namespace is missing, but BitLocker tools or service are present; refusing to assume the OS volume is decrypted.'
    }
    throw
}
if ($null -eq $volume) { throw "Could not locate the BitLocker provider for OS volume $env:SystemDrive." }
$conversion = Invoke-CimMethod -InputObject $volume -MethodName GetConversionStatus -Arguments @{ PrecisionFactor = [uint32]2 } -ErrorAction Stop
if ($conversion.ReturnValue -ne 0) { throw ('BitLocker status query failed with 0x{0:X8}.' -f [uint32]$conversion.ReturnValue) }
$states = @('FullyDecrypted', 'FullyEncrypted', 'EncryptionInProgress', 'DecryptionInProgress', 'EncryptionPaused', 'DecryptionPaused')
$index = [int]$conversion.ConversionStatus
if ($index -lt 0 -or $index -ge $states.Count) { throw "Unknown BitLocker conversion state $index." }
'{0}|{1}' -f $states[$index], [int]$conversion.EncryptionPercentage`

const windowsBitLockerDisableCommand string = `$ErrorActionPreference = 'Stop'
$manageBde = Join-Path $env:SystemRoot 'System32\manage-bde.exe'
& $manageBde -off $env:SystemDrive
if ($LASTEXITCODE -ne 0) { throw "manage-bde could not start OS-volume decryption (exit $LASTEXITCODE)." }`

// prepareWindowsBitLockerForSysprep decrypts only the Windows OS volume and waits for the conversion to finish.
func prepareWindowsBitLockerForSysprep(ctx context.Context, settings config.ProxmoxConfiguration, vm *pve.VirtualMachine, vmid int, progress func(string)) (err error) {
	var state string
	var encryptionPercentage int
	if state, encryptionPercentage, err = windowsBitLockerStatus(ctx, settings, vm, vmid); err != nil {
		return
	}
	if state == "FullyDecrypted" {
		writeTemplatePreparationProgress(progress, "Windows OS volume is already fully decrypted.")
		return
	}
	writeTemplatePreparationProgress(progress, fmt.Sprintf("BitLocker is enabled on the Windows OS volume (%d%% encrypted); starting decryption.", encryptionPercentage))
	if _, err = runWindowsPowerShell(ctx, settings, vm, vmid, windowsBitLockerDisableCommand, 60); err != nil {
		return
	}
	var deadline time.Time = time.Now().Add(6 * time.Hour)
	var lastReportedPercentage int = -1
	var lastReportedAt time.Time = time.Now()
	var ticker *time.Ticker = time.NewTicker(15 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			err = ctx.Err()
			return
		case <-ticker.C:
		}
		if state, encryptionPercentage, err = windowsBitLockerStatus(ctx, settings, vm, vmid); err != nil {
			return
		}
		if state == "FullyDecrypted" {
			writeTemplatePreparationProgress(progress, "Windows OS volume is fully decrypted; continuing with Sysprep.")
			return
		}
		if state != "DecryptionInProgress" && state != "DecryptionPaused" {
			err = fmt.Errorf("BitLocker OS-volume decryption entered unexpected state %q", state)
			return
		}
		if encryptionPercentage != lastReportedPercentage || time.Since(lastReportedAt) >= time.Minute {
			writeTemplatePreparationProgress(progress, fmt.Sprintf("Decrypting Windows OS volume: %d%% remains encrypted.", encryptionPercentage))
			lastReportedPercentage = encryptionPercentage
			lastReportedAt = time.Now()
		}
		if time.Now().After(deadline) {
			err = errors.New("timed out waiting for BitLocker to fully decrypt the Windows OS volume")
			return
		}
	}
}

func windowsBitLockerStatus(ctx context.Context, settings config.ProxmoxConfiguration, vm *pve.VirtualMachine, vmid int) (state string, encryptionPercentage int, err error) {
	var status guestAgentExecStatus
	if status, err = runWindowsPowerShell(ctx, settings, vm, vmid, windowsBitLockerStatusCommand, 30); err != nil {
		return
	}
	state, encryptionPercentage, err = parseWindowsBitLockerStatus(status.OutData)
	return
}

func parseWindowsBitLockerStatus(value string) (state string, encryptionPercentage int, err error) {
	var fields []string = strings.SplitN(strings.TrimSpace(value), "|", 2)
	if len(fields) != 2 {
		err = fmt.Errorf("could not parse BitLocker OS-volume status %q", strings.TrimSpace(value))
		return
	}
	state = strings.TrimSpace(fields[0])
	if encryptionPercentage, err = strconv.Atoi(strings.TrimSpace(fields[1])); err != nil {
		err = fmt.Errorf("could not parse BitLocker encryption percentage: %w", err)
		return
	}
	// GetConversionStatus scales the percentage by 10^PrecisionFactor. The
	// query requests two decimal places, so normalize its 0..10000 result.
	if encryptionPercentage > 100 && encryptionPercentage <= 10000 {
		encryptionPercentage = (encryptionPercentage + 50) / 100
	}
	if encryptionPercentage < 0 || encryptionPercentage > 100 {
		err = fmt.Errorf("BitLocker reported invalid encryption percentage %d", encryptionPercentage)
	}
	return
}

func prepareTemplateGuest(ctx context.Context, settings config.ProxmoxConfiguration, vm *pve.VirtualMachine, vmid int, family string, scripts map[string]string, progress func(string)) (filesMayExist bool, err error) {
	switch family {
	case "linux":
		const scriptPath string = "/run/organesson-template-prep.sh"
		filesMayExist = true
		if err = writeGuestFile(ctx, vm, scriptPath, []byte(scripts["linux"])); err != nil {
			return
		}
		_, err = runGuestCommandWithProgress(ctx, settings, vm, vmid, guestAgentCommand("/bin/bash", scriptPath), 900, progress)
	case "windows":
		if err = prepareWindowsBitLockerForSysprep(ctx, settings, vm, vmid, progress); err != nil {
			return
		}
		const directory string = `C:\Windows\Temp\OrganessonPrep`
		const scriptPath string = directory + `\og-prep-windows.ps1`
		if _, err = runWindowsPowerShell(ctx, settings, vm, vmid, `New-Item -ItemType Directory -Force -Path 'C:\Windows\Temp\OrganessonPrep' | Out-Null`, 30); err != nil {
			return
		}
		filesMayExist = true
		if err = guestAgentFileWriteWithRetry(ctx, vm, scriptPath, []byte(scripts["windows"])); err != nil {
			return
		}
		_, err = runGuestCommandWithProgress(ctx, settings, vm, vmid, []string{windowsPowerShellPath, "-NoProfile", "-NonInteractive", "-ExecutionPolicy", "Bypass", "-Command", `& 'C:\Windows\Temp\OrganessonPrep\og-prep-windows.ps1' -ExpectedRelease Auto -PrepareOnly`}, 3600, progress)
	case "freebsd":
		const scriptPath string = "/tmp/organesson-template-prep.sh"
		filesMayExist = true
		if err = writeFreeBSDGuestFile(ctx, settings, vm, vmid, scriptPath, []byte(scripts["freebsd"])); err != nil {
			return
		}
		_, err = runGuestCommandWithProgress(ctx, settings, vm, vmid, []string{"/bin/sh", scriptPath}, 1800, progress)
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

func finalizeWindowsTemplate(ctx context.Context, client *pve.Client, settings config.ProxmoxConfiguration, vm *pve.VirtualMachine, vmid int, progress func(string)) (err error) {
	var sessionStatus guestAgentExecStatus
	if sessionStatus, err = runWindowsPowerShell(ctx, settings, vm, vmid, windowsSetupSessionStateCommand, 30); err != nil {
		return
	}
	var temporaryLoginConfigured bool
	var sysprepSucceeded bool
	defer func() {
		if err == nil || !temporaryLoginConfigured {
			return
		}
		var cleanupContext context.Context
		var cancel context.CancelFunc
		cleanupContext, cancel = context.WithTimeout(context.Background(), time.Minute)
		defer cancel()
		var cleanupErr error
		if _, cleanupErr = runWindowsPowerShell(cleanupContext, settings, vm, vmid, windowsTemporaryLoginCleanupCommand, 30); cleanupErr != nil {
			err = fmt.Errorf("%w (could not clear temporary Windows autologon: %v)", err, cleanupErr)
		}
		if !sysprepSucceeded {
			if _, cleanupErr = runWindowsPowerShell(cleanupContext, settings, vm, vmid, `Remove-Item -LiteralPath 'C:\Windows\Panther\unattend.xml' -Force -ErrorAction SilentlyContinue`, 30); cleanupErr != nil {
				err = fmt.Errorf("%w (could not remove temporary Windows answer file: %v)", err, cleanupErr)
			}
		}
	}()

	var currentState string = strings.TrimSpace(sessionStatus.OutData)
	if currentState != "INTERACTIVE_ADMIN_SESSION" && !windowsSourceAlreadySealed(currentState) {
		if currentState != "IMAGE_STATE_COMPLETE" {
			err = fmt.Errorf("Windows source is not in a reusable state for unattended preparation (detected image state %q)", currentState)
			return
		}
		writeTemplatePreparationProgress(progress, "Preparing a one-time local administrator session for Windows sealing.")
		temporaryLoginConfigured = true
		if err = prepareWindowsInteractiveAdminSession(ctx, client, settings, vm, vmid, progress); err != nil {
			return
		}
	}
	var answerFile []byte
	if answerFile, err = windowsUnattendFile(); err != nil {
		return
	}
	const answerFilePath string = `C:\Windows\Panther\unattend.xml`
	if _, err = runWindowsPowerShell(ctx, settings, vm, vmid, `New-Item -ItemType Directory -Force -Path 'C:\Windows\Panther' | Out-Null`, 30); err != nil {
		return
	}
	if err = guestAgentFileWriteWithRetry(ctx, vm, answerFilePath, answerFile); err != nil {
		return
	}
	const secureAnswerFileCommand string = `$path = 'C:\Windows\Panther\unattend.xml'
$acl = Get-Acl -LiteralPath $path
$acl.SetAccessRuleProtection($true, $false)
$acl.Access | ForEach-Object { [void]$acl.RemoveAccessRule($_) }
$system = New-Object System.Security.AccessControl.FileSystemAccessRule('SYSTEM', 'FullControl', 'Allow')
$administrators = New-Object System.Security.AccessControl.FileSystemAccessRule('BUILTIN\Administrators', 'FullControl', 'Allow')
$acl.SetAccessRule($system)
$acl.SetAccessRule($administrators)
Set-Acl -LiteralPath $path -AclObject $acl
[xml]$answer = Get-Content -LiteralPath $path
if (-not $answer.unattend.settings) { throw 'The Windows unattended setup answer file is invalid.' }`
	if _, err = runWindowsPowerShell(ctx, settings, vm, vmid, secureAnswerFileCommand, 30); err != nil {
		return
	}
	if windowsSourceAlreadySealed(currentState) {
		return
	}
	_, err = runWindowsPowerShell(ctx, settings, vm, vmid, windowsInteractiveSysprepCommand(), 900)
	if err != nil {
		return
	}
	sysprepSucceeded = true
	if temporaryLoginConfigured {
		if _, err = runWindowsPowerShell(ctx, settings, vm, vmid, windowsTemporaryLoginCleanupCommand, 30); err != nil {
			return
		}
		temporaryLoginConfigured = false
	}
	return
}

// prepareWindowsInteractiveAdminSession creates a short-lived autologon session so Sysprep can run outside QGA's SYSTEM session.
func prepareWindowsInteractiveAdminSession(ctx context.Context, client *pve.Client, settings config.ProxmoxConfiguration, vm *pve.VirtualMachine, vmid int, progress func(string)) (err error) {
	var password string
	if password, err = randomWindowsPassword(); err != nil {
		return
	}
	if _, err = runWindowsPowerShell(ctx, settings, vm, vmid, windowsTemporaryLoginSetupCommand(password), 60); err != nil {
		return
	}
	var rebootTask *pve.Task
	if rebootTask, err = vm.Reboot(ctx); err != nil {
		return
	}
	if err = waitTask(ctx, client, rebootTask); err != nil {
		return
	}
	if err = waitForWindowsGuestAgentRestart(ctx, vm, progress); err != nil {
		return
	}
	writeTemplatePreparationProgress(progress, "Waiting for the temporary Windows administrator desktop session.")
	var deadline time.Time = time.Now().Add(5 * time.Minute)
	var lastReport time.Time = time.Now()
	for time.Now().Before(deadline) {
		var status guestAgentExecStatus
		if status, err = runWindowsPowerShell(ctx, settings, vm, vmid, windowsTemporaryLoginStateCommand, 30); err == nil && strings.TrimSpace(status.OutData) == "ORGANESSON_PREP_SESSION_READY" {
			return
		}
		if time.Since(lastReport) >= time.Minute {
			writeTemplatePreparationProgress(progress, "Windows is still opening the temporary administrator session.")
			lastReport = time.Now()
		}
		var timer *time.Timer = time.NewTimer(5 * time.Second)
		select {
		case <-ctx.Done():
			timer.Stop()
			err = ctx.Err()
			return
		case <-timer.C:
		}
	}
	err = errors.New("Windows did not automatically sign in to its temporary preparation administrator")
	return
}

// waitForWindowsGuestAgentRestart requires a full guest-agent disconnect/reconnect cycle after enabling Windows autologon.
func waitForWindowsGuestAgentRestart(ctx context.Context, vm *pve.VirtualMachine, progress func(string)) (err error) {
	var deadline time.Time = time.Now().Add(90 * time.Second)
	var disconnected bool
	for time.Now().Before(deadline) {
		if err = vm.AgentPing(ctx); err != nil {
			disconnected = true
			break
		}
		var timer *time.Timer = time.NewTimer(2 * time.Second)
		select {
		case <-ctx.Done():
			timer.Stop()
			err = ctx.Err()
			return
		case <-timer.C:
		}
	}
	if !disconnected {
		err = errors.New("Windows did not restart after enabling its one-time administrator session")
		return
	}
	err = waitForWindowsGuestAgent(ctx, vm, progress)
	return
}

// windowsTemporaryLoginSetupCommand creates an isolated local admin and configures one automatic console sign-in.
func windowsTemporaryLoginSetupCommand(password string) (command string) {
	command = fmt.Sprintf(`$ErrorActionPreference = 'Stop'
$name = 'ogprep'
$password = ConvertTo-SecureString '%s' -AsPlainText -Force
$user = Get-LocalUser -Name $name -ErrorAction SilentlyContinue
if ($null -eq $user) { New-LocalUser -Name $name -FullName 'Organesson Temporary Preparation' -Password $password -PasswordNeverExpires | Out-Null } else { Set-LocalUser -Name $name -Password $password -PasswordNeverExpires $true; Enable-LocalUser -Name $name }
$group = Get-LocalGroup -SID 'S-1-5-32-544' -ErrorAction Stop
Add-LocalGroupMember -Group $group.Name -Member $name -ErrorAction SilentlyContinue
$winlogon = 'HKLM:\SOFTWARE\Microsoft\Windows NT\CurrentVersion\Winlogon'
Set-ItemProperty -LiteralPath $winlogon -Name AutoAdminLogon -Type String -Value '1'
Set-ItemProperty -LiteralPath $winlogon -Name AutoLogonCount -Type DWord -Value 1
Set-ItemProperty -LiteralPath $winlogon -Name DefaultUserName -Type String -Value $name
Set-ItemProperty -LiteralPath $winlogon -Name DefaultDomainName -Type String -Value '.'
Set-ItemProperty -LiteralPath $winlogon -Name DefaultPassword -Type String -Value '%s'`, password, password)
	return
}

const (
	windowsTemporaryLoginStateCommand string = `$user = [string](Get-CimInstance -ClassName Win32_ComputerSystem).UserName
if ($user.EndsWith('\ogprep', [System.StringComparison]::OrdinalIgnoreCase)) { 'ORGANESSON_PREP_SESSION_READY' } else { 'WAITING_FOR_PREP_SESSION' }`
	windowsTemporaryLoginCleanupCommand string = `$winlogon = 'HKLM:\SOFTWARE\Microsoft\Windows NT\CurrentVersion\Winlogon'
foreach ($name in @('AutoAdminLogon', 'AutoLogonCount', 'DefaultUserName', 'DefaultDomainName', 'DefaultPassword')) { Remove-ItemProperty -LiteralPath $winlogon -Name $name -ErrorAction SilentlyContinue }
$user = Get-LocalUser -Name 'ogprep' -ErrorAction SilentlyContinue
if ($null -ne $user -and $user.Enabled) { Disable-LocalUser -Name 'ogprep' }`
)

const windowsSetupSessionStateCommand string = `$computer = Get-CimInstance -ClassName Win32_ComputerSystem
$imageState = (Get-ItemProperty -LiteralPath 'HKLM:\SOFTWARE\Microsoft\Windows\CurrentVersion\Setup\State' -Name ImageState -ErrorAction Stop).ImageState
if ([string]::IsNullOrWhiteSpace([string]$computer.UserName)) { Write-Output $imageState } else { Write-Output 'INTERACTIVE_ADMIN_SESSION' }`

// windowsSourceAlreadySealed detects an image waiting for OOBE so preparation can add an answer file without rerunning Sysprep.
func windowsSourceAlreadySealed(imageState string) (sealed bool) {
	sealed = imageState == "IMAGE_STATE_GENERALIZE_RESEAL_TO_OOBE" || imageState == "IMAGE_STATE_SPECIALIZE_RESEAL_TO_OOBE"
	return
}

// windowsUnattendFile creates a first-boot answer file that skips interactive OOBE without a reusable template password.
func windowsUnattendFile() (contents []byte, err error) {
	var password string
	if password, err = randomWindowsPassword(); err != nil {
		return
	}
	contents = []byte(fmt.Sprintf(`<?xml version="1.0" encoding="utf-8"?>
<unattend xmlns="urn:schemas-microsoft-com:unattend" xmlns:wcm="http://schemas.microsoft.com/WMIConfig/2002/State">
  <settings pass="specialize">
    <component name="Microsoft-Windows-Deployment" processorArchitecture="amd64" publicKeyToken="31bf3856ad364e35" language="neutral" versionScope="nonSxS">
      <RunSynchronous>
        <RunSynchronousCommand wcm:action="add">
          <Order>1</Order>
          <Description>Allow unattended offline Windows first boot</Description>
          <Path>reg.exe add HKLM\SOFTWARE\Microsoft\Windows\CurrentVersion\OOBE /v BypassNRO /t REG_DWORD /d 1 /f</Path>
        </RunSynchronousCommand>
        <RunSynchronousCommand wcm:action="add">
          <Order>2</Order>
          <Description>Skip interactive Windows first-boot setup</Description>
          <Path>reg.exe add HKLM\SOFTWARE\Microsoft\Windows\CurrentVersion\OOBE /v SkipMachineOOBE /t REG_DWORD /d 1 /f</Path>
        </RunSynchronousCommand>
      </RunSynchronous>
    </component>
  </settings>
  <settings pass="oobeSystem">
    <component name="Microsoft-Windows-International-Core" processorArchitecture="amd64" publicKeyToken="31bf3856ad364e35" language="neutral" versionScope="nonSxS">
      <InputLocale>en-US</InputLocale>
      <SystemLocale>en-US</SystemLocale>
      <UILanguage>en-US</UILanguage>
      <UserLocale>en-US</UserLocale>
    </component>
    <component name="Microsoft-Windows-Shell-Setup" processorArchitecture="amd64" publicKeyToken="31bf3856ad364e35" language="neutral" versionScope="nonSxS">
      <OOBE>
        <HideEULAPage>true</HideEULAPage>
        <HideLocalAccountScreen>true</HideLocalAccountScreen>
        <HideOnlineAccountScreens>true</HideOnlineAccountScreens>
        <HideWirelessSetupInOOBE>true</HideWirelessSetupInOOBE>
        <ProtectYourPC>3</ProtectYourPC>
      </OOBE>
      <UserAccounts>
        <LocalAccounts>
          <LocalAccount wcm:action="add">
            <Password><Value>%s</Value><PlainText>true</PlainText></Password>
            <Description>Organesson automation account</Description>
            <DisplayName>Organesson Administrator</DisplayName>
            <Group>Administrators</Group>
            <Name>ogadmin</Name>
          </LocalAccount>
        </LocalAccounts>
      </UserAccounts>
    </component>
  </settings>
</unattend>`, password))
	return
}

// randomWindowsPassword generates an unpredictable, policy-compliant temporary Windows password.
func randomWindowsPassword() (password string, err error) {
	var randomPassword [24]byte
	if _, err = rand.Read(randomPassword[:]); err == nil {
		password = "Og-" + hex.EncodeToString(randomPassword[:]) + "!aA7"
	}
	return
}

// windowsInteractiveSysprepCommand starts Sysprep under the logged-in administrator's interactive token.
func windowsInteractiveSysprepCommand() (command string) {
	command = `$ErrorActionPreference = 'Stop'
$computer = Get-CimInstance -ClassName Win32_ComputerSystem
$interactiveUser = [string]$computer.UserName
if ([string]::IsNullOrWhiteSpace($interactiveUser)) { throw 'Sign in to Windows as a local administrator before running Prepare Source; Sysprep cannot run as the QEMU Guest Agent SYSTEM account.' }
$administratorsGroup = Get-LocalGroup -SID 'S-1-5-32-544' -ErrorAction Stop
$administrators = @(Get-LocalGroupMember -Group $administratorsGroup.Name -ErrorAction Stop | ForEach-Object { $_.Name.ToLowerInvariant() })
if ($interactiveUser.ToLowerInvariant() -notin $administrators) { throw "The interactive account '$interactiveUser' is not a local administrator; refusing to run Sysprep." }
$taskName = 'Organesson-Template-Sysprep-' + [guid]::NewGuid().ToString('N')
$sysprepPath = Join-Path $env:SystemRoot 'System32\Sysprep\Sysprep.exe'
$action = New-ScheduledTaskAction -Execute $sysprepPath -Argument '/generalize /oobe /quit /quiet /unattend:C:\Windows\Panther\unattend.xml'
$principal = New-ScheduledTaskPrincipal -UserId $interactiveUser -LogonType Interactive -RunLevel Highest
$settings = New-ScheduledTaskSettingsSet -ExecutionTimeLimit (New-TimeSpan -Minutes 15)
Register-ScheduledTask -TaskName $taskName -Action $action -Principal $principal -Settings $settings -Force | Out-Null
try {
    $startedAt = (Get-Date).AddSeconds(-2)
    Start-ScheduledTask -TaskName $taskName
    $deadline = (Get-Date).AddMinutes(15)
    do {
        Start-Sleep -Seconds 2
        $task = Get-ScheduledTask -TaskName $taskName
        $taskInfo = Get-ScheduledTaskInfo -TaskName $taskName
        if ($taskInfo.LastRunTime -ge $startedAt -and $task.State -ne 'Running') { break }
        if ((Get-Date) -ge $deadline) { throw 'Timed out waiting for interactive Sysprep to finish.' }
    } while ($true)
    if ($taskInfo.LastTaskResult -ne 0) { throw "Sysprep task failed with result $($taskInfo.LastTaskResult). Review C:\Windows\System32\Sysprep\Panther\setupact.log and setuperr.log." }
    $imageState = (Get-ItemProperty -LiteralPath 'HKLM:\SOFTWARE\Microsoft\Windows\CurrentVersion\Setup\State' -Name ImageState -ErrorAction Stop).ImageState
    if ($imageState -ne 'IMAGE_STATE_GENERALIZE_RESEAL_TO_OOBE') {
        $recentErrors = @(Get-Content -LiteralPath (Join-Path $env:SystemRoot 'System32\Sysprep\Panther\setuperr.log') -Tail 8 -ErrorAction SilentlyContinue)
        $errorSummary = ($recentErrors -join ' | ')
        if ($errorSummary.Length -gt 1800) { $errorSummary = $errorSummary.Substring($errorSummary.Length - 1800) }
        throw "Sysprep task completed with result $($taskInfo.LastTaskResult), but Windows image state is '$imageState', not IMAGE_STATE_GENERALIZE_RESEAL_TO_OOBE. Recent Sysprep errors: $errorSummary"
    }
    Write-Output "Sysprep succeeded as $interactiveUser; image state is $imageState."
} finally {
    Unregister-ScheduledTask -TaskName $taskName -Confirm:$false -ErrorAction SilentlyContinue
}`
	return
}

// validateWindowsFirstBoot verifies Windows specialize on a disposable clone with all NICs removed.
func validateWindowsFirstBoot(ctx context.Context, client *pve.Client, source *pve.VirtualMachine, nodeName string, sourcePool string, settings config.ProxmoxConfiguration, progress func(string)) (err error) {
	if source == nil || source.VirtualMachineConfig == nil {
		err = errors.New("cannot validate Windows first boot without the source VM configuration")
		return
	}
	var cluster *pve.Cluster
	writeTemplatePreparationProgress(progress, "Creating the temporary Windows validation clone.")
	if cluster, err = client.Cluster(ctx); err != nil {
		return
	}
	var cloneID int
	if cloneID, err = cluster.NextID(ctx); err != nil {
		return
	}
	var cloneTask *pve.Task
	if _, cloneTask, err = source.Clone(ctx, &pve.VirtualMachineCloneOptions{
		NewID: cloneID, Full: true, Name: fmt.Sprintf("og-win-validate-%d", cloneID), Pool: sourcePool,
		Description: windowsValidationCloneDescription,
	}); err != nil {
		return
	}
	if err = waitTask(ctx, client, cloneTask); err != nil {
		return
	}
	writeTemplatePreparationProgress(progress, "Temporary clone is ready; disabling its NIC links.")
	var clone *pve.VirtualMachine = &pve.VirtualMachine{}
	clone.New(client, nodeName, cloneID)
	defer func() {
		var cleanupContext context.Context
		var cancel context.CancelFunc
		cleanupContext, cancel = context.WithTimeout(context.Background(), 10*time.Minute)
		defer cancel()
		var cleanupErr error
		if cleanupErr = deleteTemporaryTemplateVM(cleanupContext, client, clone); cleanupErr != nil {
			if err == nil {
				err = fmt.Errorf("Windows first-boot validation passed but its temporary clone could not be removed: %w", cleanupErr)
			} else {
				err = fmt.Errorf("%w (temporary validation clone cleanup failed: %v)", err, cleanupErr)
			}
		}
	}()
	if err = clone.Ping(ctx); err != nil {
		return
	}
	if len(clone.VirtualMachineConfig.Nets) > 0 {
		var networkOptions []pve.VirtualMachineOption
		for device, current := range clone.VirtualMachineConfig.Nets {
			networkOptions = append(networkOptions, pve.VirtualMachineOption{Name: device, Value: isolatedValidationNIC(current)})
		}
		if err = clone.ConfigSync(ctx, networkOptions...); err != nil {
			err = fmt.Errorf("disconnect NICs on temporary Windows validation clone: %w", err)
			return
		}
	}
	var startTask *pve.Task
	if startTask, err = clone.Start(ctx); err != nil {
		return
	}
	if err = waitTask(ctx, client, startTask); err != nil {
		return
	}
	writeTemplatePreparationProgress(progress, "Waiting for Windows first boot and QEMU Guest Agent.")
	if err = waitForWindowsGuestAgent(ctx, clone, progress); err != nil {
		return
	}
	writeTemplatePreparationProgress(progress, "Guest Agent is reachable; checking Windows setup state.")
	err = waitForWindowsSpecialize(ctx, settings, clone, cloneID, progress)
	return
}

// isolatedValidationNIC preserves guest-visible NIC hardware while guaranteeing the temporary clone has no network link.
func isolatedValidationNIC(value string) (isolated string) {
	var options []string = strings.Split(value, ",")
	var hasLinkDown bool
	for index, option := range options {
		if strings.HasPrefix(strings.TrimSpace(option), "link_down=") {
			options[index] = "link_down=1"
			hasLinkDown = true
		}
	}
	if !hasLinkDown {
		options = append(options, "link_down=1")
	}
	isolated = strings.Join(options, ",")
	return
}

// windowsFirstBootStateValidated accepts only a complete Windows first-boot state.
func windowsFirstBootStateValidated(value string) (validated bool) {
	state := strings.TrimSpace(value)
	validated = state == "IMAGE_STATE_COMPLETE"
	return
}

// windowsFirstBootReadinessValidated requires the unattended OOBE settings to finish before setup continues.
func windowsFirstBootReadinessValidated(value string) (validated bool, imageState string) {
	var fields []string = strings.Split(strings.TrimSpace(value), "|")
	if len(fields) != 4 {
		return
	}
	imageState = strings.TrimSpace(fields[0])
	validated = windowsFirstBootStateValidated(imageState) && strings.TrimSpace(fields[1]) == "1" && strings.TrimSpace(fields[2]) == "1" && strings.TrimSpace(fields[3]) == "0"
	return
}

const windowsFirstBootReadinessCommand string = `$state = (Get-ItemProperty -LiteralPath 'HKLM:\SOFTWARE\Microsoft\Windows\CurrentVersion\Setup\State' -Name ImageState -ErrorAction Stop).ImageState
$oobe = Get-ItemProperty -LiteralPath 'HKLM:\SOFTWARE\Microsoft\Windows\CurrentVersion\OOBE' -Name SkipMachineOOBE -ErrorAction SilentlyContinue
$skipMachineOOBE = if ($null -ne $oobe -and [int]$oobe.SkipMachineOOBE -eq 1) { 1 } else { 0 }
$bootstrapAdmin = Get-LocalUser -Name 'ogadmin' -ErrorAction SilentlyContinue
$localAdministrator = Get-LocalUser | Where-Object { $_.SID.Value -match '-500$' -and $_.Enabled } | Select-Object -First 1
$adminPresent = if ($null -ne $bootstrapAdmin -or $null -ne $localAdministrator) { 1 } else { 0 }
$oobeActive = if (Get-Process -Name 'msoobe' -ErrorAction SilentlyContinue) { 1 } else { 0 }
'{0}|{1}|{2}|{3}' -f $state, $skipMachineOOBE, $adminPresent, $oobeActive`

// waitForWindowsSpecialize waits for the transient setup state to settle and captures Panther diagnostics on failure.
func waitForWindowsSpecialize(ctx context.Context, settings config.ProxmoxConfiguration, vm *pve.VirtualMachine, vmid int, progress func(string)) (err error) {
	var started time.Time = time.Now()
	var deadline time.Time = started.Add(20 * time.Minute)
	var lastState string
	var lastStateErr error
	var lastReport time.Time = started
	var readySince time.Time
	for time.Now().Before(deadline) {
		var status guestAgentExecStatus
		if status, lastStateErr = runWindowsPowerShell(ctx, settings, vm, vmid, windowsFirstBootReadinessCommand, 30); lastStateErr == nil {
			var validated bool
			if validated, lastState = windowsFirstBootReadinessValidated(status.OutData); validated {
				if readySince.IsZero() {
					readySince = time.Now()
				} else if time.Since(readySince) >= time.Minute {
					writeTemplatePreparationProgress(progress, "Windows completed unattended OOBE; SkipMachineOOBE is set, the Organesson local administrator exists, and msoobe has exited.")
					return
				}
			} else {
				readySince = time.Time{}
			}
		} else {
			readySince = time.Time{}
		}
		if time.Since(lastReport) >= time.Minute {
			writeTemplatePreparationProgress(progress, "Windows first boot is still running; current image state: "+lastState+"; waiting for unattended OOBE to finish.")
			lastReport = time.Now()
		}
		var timer *time.Timer = time.NewTimer(15 * time.Second)
		select {
		case <-ctx.Done():
			timer.Stop()
			err = ctx.Err()
			return
		case <-timer.C:
		}
	}
	var diagnostics string
	diagnostics, _ = readWindowsSetupDiagnostics(ctx, settings, vm, vmid)
	err = windowsFirstBootValidationError(lastState, lastStateErr, diagnostics)
	return
}

// readWindowsSetupDiagnostics returns a bounded excerpt of recent Windows Setup and Sysprep errors.
func readWindowsSetupDiagnostics(ctx context.Context, settings config.ProxmoxConfiguration, vm *pve.VirtualMachine, vmid int) (diagnostics string, err error) {
	const diagnosticCommand string = `$paths = @(
    'C:\Windows\Panther\setuperr.log',
    'C:\Windows\Panther\setupact.log',
    'C:\Windows\System32\Sysprep\Panther\setuperr.log',
    'C:\Windows\System32\Sysprep\Panther\setupact.log'
)
$diagnosticLines = @()
foreach ($path in $paths) {
    if (-not (Test-Path -LiteralPath $path -PathType Leaf)) { continue }
    $entries = @(Get-Content -LiteralPath $path -Tail 500 -ErrorAction SilentlyContinue | Select-String -Pattern '(?i)(error|failed|CryptoSysPrep|dwRet|0x[0-9a-f]{3,})' | Select-Object -Last 8)
    foreach ($entry in $entries) {
        $line = [string]$entry.Line
        if ($line.Length -gt 360) { $line = $line.Substring(0, 360) }
        $diagnosticLines += ('{0}: {1}' -f (Split-Path -Leaf $path), $line)
    }
}
if ($diagnosticLines.Count -eq 0) { 'No matching error lines were found in Windows Panther/Sysprep logs.' } else { $diagnosticLines | Select-Object -Last 16 }`
	var status guestAgentExecStatus
	status, err = runWindowsPowerShell(ctx, settings, vm, vmid, diagnosticCommand, 30)
	if err == nil {
		diagnostics = strings.TrimSpace(status.OutData)
	}
	return
}

// windowsFirstBootValidationError formats the image state with setup-log evidence when available.
func windowsFirstBootValidationError(imageState string, stateErr error, diagnostics string) (err error) {
	if imageState == "" && stateErr != nil {
		err = fmt.Errorf("could not read Windows first-boot image state: %w", stateErr)
	} else {
		err = fmt.Errorf("Windows first-boot validation reached image state %q; expected IMAGE_STATE_COMPLETE, SkipMachineOOBE=1, ogadmin present, and msoobe exited", imageState)
	}
	if diagnostics != "" {
		err = fmt.Errorf("%w; Windows Setup diagnostics: %s", err, diagnostics)
	}
	return
}

// waitForWindowsGuestAgent allows the first-boot specialize phase time to finish before QGA validation.
func waitForWindowsGuestAgent(ctx context.Context, vm *pve.VirtualMachine, progress func(string)) (err error) {
	var deadline <-chan time.Time = time.After(20 * time.Minute)
	var ticker *time.Ticker = time.NewTicker(5 * time.Second)
	var started time.Time = time.Now()
	var lastReport time.Time = started
	defer ticker.Stop()
	for {
		if err = vm.AgentPing(ctx); err == nil {
			return
		}
		if time.Since(lastReport) >= 30*time.Second {
			writeTemplatePreparationProgress(progress, "Windows is still booting; waiting for QEMU Guest Agent.")
			lastReport = time.Now()
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-deadline:
			err = fmt.Errorf("QEMU Guest Agent did not become reachable during Windows first boot: %w", err)
			return
		case <-ticker.C:
		}
	}
}

func writeTemplatePreparationProgress(progress func(string), message string) {
	if progress != nil {
		progress(message)
	}
}

// deleteTemporaryTemplateVM stops and removes a disposable validation clone, including its copied disks.
func deleteTemporaryTemplateVM(ctx context.Context, client *pve.Client, vm *pve.VirtualMachine) (err error) {
	if vm == nil {
		return
	}
	if err = vm.Ping(ctx); err != nil {
		return
	}
	if vm.VirtualMachineConfig == nil || vm.VirtualMachineConfig.Description != windowsValidationCloneDescription {
		err = errors.New("refusing to remove a VM that is not marked as Organesson's temporary Windows validation clone")
		return
	}
	if !vm.IsStopped() {
		var shutdownTask *pve.Task
		if shutdownTask, err = vm.Shutdown(ctx); err == nil {
			err = waitTask(ctx, client, shutdownTask)
		}
		if err != nil {
			var stopTask *pve.Task
			if stopTask, err = vm.Stop(ctx); err != nil {
				return
			}
			if err = waitTask(ctx, client, stopTask); err != nil {
				return
			}
		}
		if err = waitVMStopped(ctx, client, int(vm.VMID), vm.Node); err != nil {
			return
		}
	}
	var deleteTask *pve.Task
	if deleteTask, err = vm.Delete(ctx, &pve.VirtualMachineDeleteOptions{Purge: true, DestroyUnreferencedDisks: true}); err != nil {
		return
	}
	err = waitTask(ctx, client, deleteTask)
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
		if err = guestAgentFileWriteWithRetry(ctx, vm, path, content[offset:end]); err != nil {
			return
		}
	}
	return
}

// writeFreeBSDGuestFile transfers a setup script through guest-exec stdin to avoid FreeBSD QGA file-write timeouts.
func writeFreeBSDGuestFile(ctx context.Context, settings config.ProxmoxConfiguration, vm *pve.VirtualMachine, vmid int, path string, content []byte) (err error) {
	const chunkSize int = 512
	for offset := 0; offset < len(content); offset += chunkSize {
		var end int = offset + chunkSize
		if end > len(content) {
			end = len(content)
		}
		var command []string = []string{"/usr/bin/tee"}
		if offset > 0 {
			command = append(command, "-a")
		}
		command = append(command, path)
		var pid int
		if pid, err = guestAgentExecWithRetry(ctx, vm, command, string(content[offset:end])); err != nil {
			err = fmt.Errorf("start QGA script transfer with %q: %w", command[0], err)
			return
		}
		var status guestAgentExecStatus
		if status, err = waitForGuestExecExit(ctx, settings, vm.Node, vmid, pid, 15); err != nil {
			return
		}
		if status.ExitCode != 0 {
			err = fmt.Errorf("guest command failed writing preparation script (exit %d): %s", status.ExitCode, strings.TrimSpace(status.ErrData+" "+status.OutData))
			return
		}
	}
	return
}

func runGuestCommand(ctx context.Context, settings config.ProxmoxConfiguration, vm *pve.VirtualMachine, vmid int, command []string, timeoutSeconds int) (status guestAgentExecStatus, err error) {
	status, err = runGuestCommandWithProgress(ctx, settings, vm, vmid, command, timeoutSeconds, nil)
	return
}

func runGuestCommandWithProgress(ctx context.Context, settings config.ProxmoxConfiguration, vm *pve.VirtualMachine, vmid int, command []string, timeoutSeconds int, progress func(string)) (status guestAgentExecStatus, err error) {
	var pid int
	if pid, err = guestAgentExecWithRetry(ctx, vm, command, ""); err != nil {
		err = fmt.Errorf("start guest command %q: %w", command[0], err)
		return
	}
	if status, err = waitForGuestExecExitWithProgress(ctx, settings, vm.Node, vmid, pid, timeoutSeconds, progress); err != nil {
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
