package proxmox

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	pve "github.com/luthermonson/go-proxmox"
	"github.com/z46-dev/organesson/backend/config"
)

const (
	MaxGuestArtifactArchiveBytes int   = 17 * 1024 * 1024
	maxGuestArtifactOutputBytes  int   = 32 * 1024
	maxGuestArtifactFiles        int   = 128
	maxGuestArtifactBytes        int64 = 16 * 1024 * 1024
	guestArtifactChunkBytes      int   = 40 * 1024
)

type (
	// GuestArtifactRequest identifies one managed VM and one immutable setup package.
	GuestArtifactRequest struct {
		Node           string `json:"node"`
		VMID           string `json:"vmid"`
		VMOperationKey string `json:"vm_operation_key"`
		Entrypoint     string `json:"entrypoint"`
		SHA256         string `json:"sha256"`
		GuestOSFamily  string `json:"guest_os_family"`
		Archive        []byte `json:"-"`
	}

	// GuestArtifactFile is a verified regular file from the compressed artifact.
	GuestArtifactFile struct {
		Path       string
		Contents   []byte
		Executable bool
		SHA256     string
	}

	// GuestArtifactResult reports execution status and bounded guest output.
	GuestArtifactResult struct {
		SHA256          string `json:"sha256"`
		Status          string `json:"status"`
		ExitCode        int    `json:"exit_code"`
		Stdout          string `json:"stdout"`
		Stderr          string `json:"stderr"`
		OutputTruncated bool   `json:"output_truncated,omitempty"`
	}

	apiGuestArtifactDriver struct {
		settings config.ProxmoxConfiguration
	}
)

type guestAgentExecStatus struct {
	ExitCode int    `json:"exitcode"`
	OutData  string `json:"out-data"`
	ErrData  string `json:"err-data"`
}

// waitForGuestExecExit polls QEMU Guest Agent status without relying on provider response fields that vary by PVE version.
func waitForGuestExecExit(ctx context.Context, settings config.ProxmoxConfiguration, node string, vmid int, pid int, timeoutSeconds int) (status guestAgentExecStatus, err error) {
	status, err = waitForGuestExecExitWithProgress(ctx, settings, node, vmid, pid, timeoutSeconds, nil)
	return
}

func waitForGuestExecExitWithProgress(ctx context.Context, settings config.ProxmoxConfiguration, node string, vmid int, pid int, timeoutSeconds int, progress func(string)) (status guestAgentExecStatus, err error) {
	var service *Service = &Service{settings: settings}
	var transport *http.Transport
	if transport, err = service.consoleTransport(); err != nil {
		return
	}
	var client *http.Client = &http.Client{
		Timeout: 15 * time.Second, Transport: transport,
		CheckRedirect: func(_ *http.Request, _ []*http.Request) (err error) { return http.ErrUseLastResponse },
	}
	var apiURL *url.URL
	if apiURL, err = url.Parse(settings.APIURL); err != nil {
		return
	}
	apiURL.Path = strings.TrimSuffix(apiURL.Path, "/") + "/nodes/" + url.PathEscape(node) + "/qemu/" + strconv.Itoa(vmid) + "/agent/exec-status"
	var query url.Values = apiURL.Query()
	query.Set("pid", strconv.Itoa(pid))
	apiURL.RawQuery = query.Encode()
	var timeout <-chan time.Time = time.After(time.Duration(timeoutSeconds) * time.Second)
	var ticker *time.Ticker = time.NewTicker(2 * time.Second)
	var started time.Time = time.Now()
	var nextProgress time.Time = started.Add(15 * time.Second)
	var consecutiveTransportFailures int
	defer ticker.Stop()
	for {
		if progress != nil && time.Now().After(nextProgress) {
			progress(fmt.Sprintf("Guest command is still running (%s elapsed).", time.Since(started).Round(5*time.Second)))
			nextProgress = time.Now().Add(15 * time.Second)
		}
		var request *http.Request
		if request, err = http.NewRequestWithContext(ctx, http.MethodGet, apiURL.String(), nil); err != nil {
			return
		}
		request.Header.Set("Authorization", "PVEAPIToken="+settings.APITokenID+"="+settings.APITokenSecret)
		var response *http.Response
		if response, err = client.Do(request); err != nil {
			consecutiveTransportFailures++
			if consecutiveTransportFailures >= 15 {
				return
			}
			select {
			case <-ctx.Done():
				err = ctx.Err()
				return
			case <-timeout:
				err = fmt.Errorf("guest command did not exit within %d seconds: %w", timeoutSeconds, err)
				return
			case <-ticker.C:
			}
			continue
		}
		consecutiveTransportFailures = 0
		var payload struct {
			Data *struct {
				Exited   int    `json:"exited"`
				ExitCode int    `json:"exitcode"`
				OutData  string `json:"out-data"`
				ErrData  string `json:"err-data"`
			} `json:"data"`
			Message string `json:"message"`
		}
		if response.StatusCode != http.StatusOK {
			var responseBody []byte
			responseBody, _ = io.ReadAll(io.LimitReader(response.Body, 4096))
			response.Body.Close()
			err = fmt.Errorf("Proxmox guest-agent exec status returned HTTP %d: %s", response.StatusCode, strings.TrimSpace(string(responseBody)))
			if response.StatusCode < http.StatusInternalServerError || response.StatusCode >= 600 || strings.Contains(strings.ToLower(string(responseBody)), "pid does not exist") {
				return
			}
			consecutiveTransportFailures++
			if consecutiveTransportFailures >= 15 {
				return
			}
			select {
			case <-ctx.Done():
				err = ctx.Err()
				return
			case <-timeout:
				err = fmt.Errorf("guest command did not exit within %d seconds: %w", timeoutSeconds, err)
				return
			case <-ticker.C:
			}
			continue
		}
		if err = json.NewDecoder(response.Body).Decode(&payload); err != nil {
			response.Body.Close()
			return
		}
		response.Body.Close()
		if payload.Data == nil {
			err = fmt.Errorf("Proxmox no longer has guest command status for pid %d: %s", pid, strings.TrimSpace(payload.Message))
			return
		}
		if payload.Data.Exited != 0 {
			status.ExitCode = payload.Data.ExitCode
			status.OutData = payload.Data.OutData
			status.ErrData = payload.Data.ErrData
			return
		}
		select {
		case <-ctx.Done():
			err = ctx.Err()
			return
		case <-timeout:
			err = fmt.Errorf("guest command did not exit within %d seconds", timeoutSeconds)
			return
		case <-ticker.C:
		}
	}
}

// GuestArtifactExitError reports the guest entrypoint's non-zero exit status.
type GuestArtifactExitError struct {
	ExitCode int
}

// Error describes a guest entrypoint that completed unsuccessfully.
func (executionError *GuestArtifactExitError) Error() (message string) {
	message = fmt.Sprintf("guest artifact entrypoint exited with status %d", executionError.ExitCode)
	return
}

// boundedGuestArtifactOutput keeps each returned output stream within the API response limit.
func boundedGuestArtifactOutput(output string) (bounded string, truncated bool) {
	if len(output) <= maxGuestArtifactOutputBytes {
		bounded = output
		return
	}
	truncated = true
	bounded = output[:maxGuestArtifactOutputBytes]
	for !utf8.ValidString(bounded) {
		bounded = bounded[:len(bounded)-1]
	}
	return
}

func guestArtifactResult(request GuestArtifactRequest, status guestAgentExecStatus) (result GuestArtifactResult) {
	var stdout, stderr string
	var stdoutTruncated, stderrTruncated bool
	stdout, stdoutTruncated = boundedGuestArtifactOutput(status.OutData)
	stderr, stderrTruncated = boundedGuestArtifactOutput(status.ErrData)
	result = GuestArtifactResult{
		SHA256: request.SHA256, ExitCode: status.ExitCode, Stdout: stdout, Stderr: stderr,
		OutputTruncated: stdoutTruncated || stderrTruncated,
	}
	return
}

// ExecuteGuestArtifact validates, delivers, and runs an artifact on a managed Linux, Windows, or BSD VM.
func (service *Service) ExecuteGuestArtifact(ctx context.Context, request GuestArtifactRequest) (result GuestArtifactResult, err error) {
	if service == nil || service.guestArtifactDriver == nil || !service.Configured() {
		err = ErrNotConfigured
		return
	}
	var files []GuestArtifactFile
	if files, err = validateGuestArtifact(request); err != nil {
		return
	}
	var unlock func() = service.lockGuestOperation(request.Node, request.VMID)
	defer unlock()
	result, err = service.guestArtifactDriver.Execute(ctx, request, files)
	return
}

// RestartGuestAfterSetup asks Proxmox to reboot a managed guest and waits for QEMU Guest Agent readiness.
func (service *Service) RestartGuestAfterSetup(ctx context.Context, request GuestArtifactRequest, timeout time.Duration) (err error) {
	if service == nil || service.guestArtifactDriver == nil || !service.Configured() {
		err = ErrNotConfigured
		return
	}
	var unlock func() = service.lockGuestOperation(request.Node, request.VMID)
	defer unlock()
	err = service.guestArtifactDriver.RestartAndWait(ctx, request, timeout)
	return
}

// WaitForWindowsFirstBoot waits outside the guest process for unattended Windows setup to finish.
func (service *Service) WaitForWindowsFirstBoot(ctx context.Context, request GuestArtifactRequest) (err error) {
	if service == nil || service.guestArtifactDriver == nil || !service.Configured() {
		err = ErrNotConfigured
		return
	}
	if request.GuestOSFamily != "windows" {
		err = errors.New("first-boot readiness is supported for Windows guests only")
		return
	}
	var unlock func() = service.lockGuestOperation(request.Node, request.VMID)
	defer unlock()
	err = service.guestArtifactDriver.WaitWindowsFirstBoot(ctx, request)
	return
}

// ValidateGuestArtifactArchive applies the same safety checks used immediately before guest delivery.
func ValidateGuestArtifactArchive(request GuestArtifactRequest) (err error) {
	_, err = validateGuestArtifact(request)
	return
}

// validateGuestArtifact verifies the digest, archive limits, entrypoint, and every extracted path.
func validateGuestArtifact(request GuestArtifactRequest) (files []GuestArtifactFile, err error) {
	if request.Node == "" || request.VMID == "" || request.VMOperationKey == "" {
		err = errors.New("artifact execution requires a managed Proxmox VM identity")
		return
	}
	if request.GuestOSFamily != "linux" && request.GuestOSFamily != "windows" && request.GuestOSFamily != "bsd" {
		err = errors.New("artifact execution requires a supported Linux, Windows, or BSD guest family")
		return
	}
	if len(request.Archive) == 0 || len(request.Archive) > MaxGuestArtifactArchiveBytes {
		err = fmt.Errorf("artifact compressed size must be between 1 and %d bytes", MaxGuestArtifactArchiveBytes)
		return
	}
	var digest [sha256.Size]byte = sha256.Sum256(request.Archive)
	if !strings.EqualFold(hex.EncodeToString(digest[:]), request.SHA256) {
		err = errors.New("artifact SHA-256 does not match the uploaded package")
		return
	}
	if !validArtifactRelativePath(request.Entrypoint) {
		err = errors.New("artifact entrypoint must be a clean relative path within the package")
		return
	}
	var compressed *gzip.Reader
	if compressed, err = gzip.NewReader(bytes.NewReader(request.Archive)); err != nil {
		err = fmt.Errorf("artifact is not a valid gzip package: %w", err)
		return
	}
	defer compressed.Close()
	var archive *tar.Reader = tar.NewReader(compressed)
	var totalBytes int64
	var entrypointFound bool
	var names map[string]struct{} = make(map[string]struct{})
	files = make([]GuestArtifactFile, 0, 8)
	for {
		var header *tar.Header
		if header, err = archive.Next(); err == io.EOF {
			break
		} else if err != nil {
			err = fmt.Errorf("read artifact archive: %w", err)
			return
		}
		if header.Typeflag != tar.TypeReg && header.Typeflag != tar.TypeRegA {
			err = fmt.Errorf("artifact contains unsupported non-regular entry %q", header.Name)
			return
		}
		if !validArtifactRelativePath(header.Name) || len(header.Name) > 512 {
			err = fmt.Errorf("artifact contains unsafe path %q", header.Name)
			return
		}
		if _, exists := names[header.Name]; exists {
			err = fmt.Errorf("artifact contains duplicate path %q", header.Name)
			return
		}
		for parent := path.Dir(header.Name); parent != "."; parent = path.Dir(parent) {
			if _, exists := names[parent]; exists {
				err = fmt.Errorf("artifact path %q conflicts with a file parent", header.Name)
				return
			}
		}
		for name := range names {
			if strings.HasPrefix(name, header.Name+"/") {
				err = fmt.Errorf("artifact file %q conflicts with a child path", header.Name)
				return
			}
		}
		if len(files) >= maxGuestArtifactFiles || header.Size < 0 || header.Size > maxGuestArtifactBytes-totalBytes {
			err = errors.New("artifact exceeds the supported file count or uncompressed size")
			return
		}
		var contents []byte
		if contents, err = io.ReadAll(io.LimitReader(archive, header.Size+1)); err != nil {
			err = fmt.Errorf("read artifact file %q: %w", header.Name, err)
			return
		}
		if int64(len(contents)) != header.Size {
			err = fmt.Errorf("artifact file %q has an invalid size", header.Name)
			return
		}
		totalBytes += int64(len(contents))
		names[header.Name] = struct{}{}
		if header.Name == request.Entrypoint {
			entrypointFound = true
		}
		var fileDigest [sha256.Size]byte = sha256.Sum256(contents)
		files = append(files, GuestArtifactFile{Path: header.Name, Contents: contents, Executable: header.Mode&0111 != 0, SHA256: hex.EncodeToString(fileDigest[:])})
	}
	if !entrypointFound || len(files) == 0 {
		err = errors.New("artifact entrypoint is missing from the package")
		return
	}
	if _, err = io.Copy(io.Discard, compressed); err != nil {
		err = fmt.Errorf("verify artifact gzip checksum: %w", err)
	}
	return
}

func validArtifactRelativePath(value string) (valid bool) {
	if value == "" || value == "." || value == ".." || strings.Contains(value, "\\") || strings.ContainsAny(value, "\x00\r\n\t") || path.IsAbs(value) || path.Clean(value) != value || strings.HasPrefix(value, "../") {
		return
	}
	valid = true
	return
}

// Execute transfers validated files in small QGA writes and removes the transient guest workspace.
func (driver *apiGuestArtifactDriver) Execute(ctx context.Context, request GuestArtifactRequest, files []GuestArtifactFile) (result GuestArtifactResult, err error) {
	var client *pve.Client
	if client, err = newAPIClient(driver.settings); err != nil {
		return
	}
	var node *pve.Node
	if node, err = client.Node(ctx, request.Node); err != nil {
		return
	}
	var vmid int
	if vmid, err = parseVMID(request.VMID); err != nil {
		return
	}
	var vm *pve.VirtualMachine
	if vm, err = node.VirtualMachine(ctx, vmid); err != nil {
		return
	}
	if err = verifyManagedVM(vm, request.VMOperationKey); err != nil {
		return
	}
	if err = waitForGuestAgent(ctx, vm); err != nil {
		return
	}
	if request.GuestOSFamily == "windows" {
		result, err = driver.executeWindows(ctx, vm, request, files)
		return
	}
	result, err = driver.executeUnix(ctx, vm, request, files)
	return
}

// executeUnix runs a verified artifact using the guest family's native shell and base utilities.
func (driver *apiGuestArtifactDriver) executeUnix(ctx context.Context, vm *pve.VirtualMachine, request GuestArtifactRequest, files []GuestArtifactFile) (result GuestArtifactResult, err error) {
	var operationNonce [16]byte
	if _, err = rand.Read(operationNonce[:]); err != nil {
		return
	}
	var vmid int
	if vmid, err = parseVMID(request.VMID); err != nil {
		return
	}
	var workDirectory string = "/run/organesson-artifact-" + hex.EncodeToString(operationNonce[:])
	var shellCommand string = "/usr/bin/bash"
	var runnerScript string = guestArtifactScript()
	var mkdirArguments []string = []string{"/usr/bin/mkdir", "-m", "0700", "--"}
	var removeArguments []string = []string{"/usr/bin/rm", "-rf", "--"}
	if request.GuestOSFamily == "bsd" {
		workDirectory = "/tmp/organesson-artifact-" + hex.EncodeToString(operationNonce[:])
		shellCommand = "/bin/sh"
		runnerScript = guestBSDArtifactScript()
		mkdirArguments = []string{"/bin/mkdir", "-m", "0700"}
		removeArguments = []string{"/bin/rm", "-rf"}
	}
	var cleanup func() error = func() (cleanupErr error) {
		var cleanupContext context.Context
		var cancel context.CancelFunc
		cleanupContext, cancel = context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		var cleanupPID int
		if cleanupPID, cleanupErr = vm.AgentExec(cleanupContext, guestAgentCommand(append(removeArguments, workDirectory)...), ""); cleanupErr == nil {
			var cleanupStatus guestAgentExecStatus
			if cleanupStatus, cleanupErr = waitForGuestExecExit(cleanupContext, driver.settings, request.Node, vmid, cleanupPID, 10); cleanupErr == nil && cleanupStatus.ExitCode != 0 {
				cleanupErr = errors.New("guest did not remove its temporary artifact workspace")
			}
		}
		return
	}
	defer func() {
		if cleanupErr := cleanup(); err == nil {
			err = cleanupErr
		}
	}()
	var mkdirPID int
	if mkdirPID, err = guestAgentExecWithRetry(ctx, vm, guestAgentCommand(append(mkdirArguments, workDirectory)...), ""); err != nil {
		return
	}
	var mkdirStatus guestAgentExecStatus
	if mkdirStatus, err = waitForGuestExecExit(ctx, driver.settings, request.Node, vmid, mkdirPID, 10); err != nil || mkdirStatus.ExitCode != 0 {
		if err == nil {
			err = errors.New("guest could not create its temporary artifact workspace")
		}
		return
	}
	var manifest strings.Builder
	for fileIndex, file := range files {
		var mode string = "0644"
		if file.Executable {
			mode = "0755"
		}
		fmt.Fprintf(&manifest, "%03d\t%s\t%s\t%s\n", fileIndex, mode, file.SHA256, file.Path)
		for offset, chunkIndex := 0, 0; offset < len(file.Contents); chunkIndex++ {
			var end int = offset + guestArtifactChunkBytes
			if end > len(file.Contents) {
				end = len(file.Contents)
			}
			var chunkPath string = fmt.Sprintf("%s/part-%03d-%05d", workDirectory, fileIndex, chunkIndex)
			if err = writeGuestArtifactFile(ctx, driver.settings, vm, vmid, request.GuestOSFamily, chunkPath, file.Contents[offset:end]); err != nil {
				return
			}
			offset = end
		}
	}
	var manifestPath string = workDirectory + "/manifest"
	if err = writeGuestArtifactFile(ctx, driver.settings, vm, vmid, request.GuestOSFamily, manifestPath, []byte(manifest.String())); err != nil {
		return
	}
	var scriptPath string = workDirectory + "/run.sh"
	if err = writeGuestArtifactFile(ctx, driver.settings, vm, vmid, request.GuestOSFamily, scriptPath, []byte(runnerScript)); err != nil {
		return
	}
	var pid int
	if pid, err = guestAgentExecWithRetry(ctx, vm, guestAgentCommand(shellCommand, scriptPath, workDirectory, request.Entrypoint), ""); err != nil {
		return
	}
	var status guestAgentExecStatus
	if status, err = waitForGuestExecExit(ctx, driver.settings, request.Node, vmid, pid, 300); err != nil {
		return
	}
	if status.ExitCode != 0 {
		result = guestArtifactResult(request, status)
		result.Status = "failed"
		err = &GuestArtifactExitError{ExitCode: status.ExitCode}
		return
	}
	result = guestArtifactResult(request, status)
	result.Status = "succeeded"
	return
}

// writeGuestArtifactFile uses guest-exec stdin on FreeBSD, where QGA file-write can time out.
func writeGuestArtifactFile(ctx context.Context, settings config.ProxmoxConfiguration, vm *pve.VirtualMachine, vmid int, guestOSFamily string, path string, content []byte) (err error) {
	if guestOSFamily == "bsd" {
		err = writeFreeBSDGuestFile(ctx, settings, vm, vmid, path, content)
		return
	}
	err = guestAgentFileWriteWithRetry(ctx, vm, path, content)
	return
}

// guestAgentExecWithRetry waits for a QGA recovery only when Proxmox confirms no command was started.
func guestAgentExecWithRetry(ctx context.Context, vm *pve.VirtualMachine, command []string, stdin string) (pid int, err error) {
	const maximumAttempts int = 3
	var attempt int
	for attempt = 1; attempt <= maximumAttempts; attempt++ {
		if pid, err = vm.AgentExec(ctx, command, stdin); err == nil || attempt == maximumAttempts || !guestAgentUnavailableError(err) {
			return
		}
		if err = waitForGuestAgent(ctx, vm); err != nil {
			return
		}
	}
	return
}

// guestAgentFileWriteWithRetry repeats an idempotent full-file write after an agent disconnect.
func guestAgentFileWriteWithRetry(ctx context.Context, vm *pve.VirtualMachine, path string, content []byte) (err error) {
	const maximumAttempts int = 3
	var attempt int
	for attempt = 1; attempt <= maximumAttempts; attempt++ {
		err = vm.AgentFileWrite(ctx, path, content)
		if err == nil || attempt == maximumAttempts || !guestAgentUnavailableError(err) {
			return
		}
		if err = waitForGuestAgent(ctx, vm); err != nil {
			return
		}
	}
	return
}

// guestAgentUnavailableError classifies the QGA startup/reboot race that is safe to retry.
func guestAgentUnavailableError(err error) (unavailable bool) {
	if err == nil {
		return
	}
	var message string = strings.ToLower(err.Error())
	unavailable = strings.Contains(message, "guest agent is not running") || strings.Contains(message, "guest-agent is not running")
	return
}

// RestartAndWait asks PVE to restart a managed guest and observes QEMU Guest Agent return.
func (driver *apiGuestArtifactDriver) RestartAndWait(ctx context.Context, request GuestArtifactRequest, timeout time.Duration) (err error) {
	var client *pve.Client
	if client, err = newAPIClient(driver.settings); err != nil {
		return
	}
	var node *pve.Node
	if node, err = client.Node(ctx, request.Node); err != nil {
		return
	}
	var vmid int
	if vmid, err = parseVMID(request.VMID); err != nil {
		return
	}
	var vm *pve.VirtualMachine
	if vm, err = node.VirtualMachine(ctx, vmid); err != nil {
		return
	}
	if err = verifyManagedVM(vm, request.VMOperationKey); err != nil {
		return
	}
	var command []string
	switch request.GuestOSFamily {
	case "windows":
		command = []string{`C:\Windows\System32\shutdown.exe`, "/r", "/t", "5", "/f"}
	case "linux":
		command = []string{"/bin/sh", "-c", "(sleep 2; /sbin/shutdown -r now) >/dev/null 2>&1 </dev/null &"}
	case "bsd":
		command = []string{"/bin/sh", "-c", "(sleep 2; /sbin/shutdown -r now) >/dev/null 2>&1 </dev/null &"}
	default:
		err = fmt.Errorf("unsupported guest family %q for a managed reboot", request.GuestOSFamily)
		return
	}
	if _, err = vm.AgentExec(ctx, command, ""); err != nil {
		return
	}
	err = driver.waitForReboot(ctx, request, timeout)
	return
}

// WaitWindowsFirstBoot retries QEMU Guest Agent checks across guest-initiated setup reboots.
func (driver *apiGuestArtifactDriver) WaitWindowsFirstBoot(ctx context.Context, request GuestArtifactRequest) (err error) {
	var vm *pve.VirtualMachine
	if vm, err = (&Service{settings: driver.settings}).consoleVM(ctx, request.Node, request.VMID, request.VMOperationKey); err != nil {
		return
	}
	if vm.IsStopped() {
		var client *pve.Client
		if client, err = newAPIClient(driver.settings); err != nil {
			return
		}
		var task *pve.Task
		if task, err = vm.Start(ctx); err != nil {
			return
		}
		if err = waitTask(ctx, client, task); err != nil {
			return
		}
	}
	var vmid int
	if vmid, err = parseVMID(request.VMID); err != nil {
		return
	}
	err = waitForWindowsSpecialize(ctx, driver.settings, vm, vmid, nil)
	return
}

// waitForReboot confirms that a managed guest agent disappeared and returned after its restart.
func (driver *apiGuestArtifactDriver) waitForReboot(ctx context.Context, request GuestArtifactRequest, timeout time.Duration) (err error) {
	var client *pve.Client
	if client, err = newAPIClient(driver.settings); err != nil {
		return
	}
	var node *pve.Node
	if node, err = client.Node(ctx, request.Node); err != nil {
		return
	}
	var vmid int
	if vmid, err = parseVMID(request.VMID); err != nil {
		return
	}
	var vm *pve.VirtualMachine
	if vm, err = node.VirtualMachine(ctx, vmid); err != nil {
		return
	}
	if err = verifyManagedVM(vm, request.VMOperationKey); err != nil {
		return
	}
	if timeout < time.Minute {
		err = errors.New("guest reboot wait timeout must be at least one minute")
		return
	}
	var deadline time.Time = time.Now().Add(timeout)
	var ticker *time.Ticker = time.NewTicker(250 * time.Millisecond)
	var agentDownAt time.Time
	var agentReturnedAt time.Time
	defer ticker.Stop()
	for {
		if time.Now().After(deadline) {
			err = fmt.Errorf("guest did not complete its expected reboot within %s", timeout.Round(time.Second))
			return
		}
		if pingErr := vm.AgentPing(ctx); pingErr != nil {
			if agentDownAt.IsZero() {
				agentDownAt = time.Now()
			}
			agentReturnedAt = time.Time{}
		} else if !agentDownAt.IsZero() {
			if time.Since(agentDownAt) >= 4*time.Second {
				if agentReturnedAt.IsZero() {
					agentReturnedAt = time.Now()
				} else if time.Since(agentReturnedAt) >= 20*time.Second {
					return
				}
			} else {
				agentDownAt = time.Time{}
			}
		}
		select {
		case <-ctx.Done():
			err = ctx.Err()
			return
		case <-ticker.C:
		}
	}
}

// executeWindows delivers validated files to a private temporary directory and runs its PowerShell entrypoint as LocalSystem.
func (driver *apiGuestArtifactDriver) executeWindows(ctx context.Context, vm *pve.VirtualMachine, request GuestArtifactRequest, files []GuestArtifactFile) (result GuestArtifactResult, err error) {
	var operationNonce [16]byte
	if _, err = rand.Read(operationNonce[:]); err != nil {
		return
	}
	var workDirectory string = `C:\Windows\Temp\OrganessonArtifact-` + hex.EncodeToString(operationNonce[:])
	var cleanup func() error = func() (cleanupErr error) {
		var cleanupContext context.Context
		var cancel context.CancelFunc
		cleanupContext, cancel = context.WithTimeout(context.Background(), 35*time.Second)
		defer cancel()
		var attempt int
		var command string = "if (Test-Path -LiteralPath " + powershellSingleQuoted(workDirectory) + ") { Remove-Item -LiteralPath " + powershellSingleQuoted(workDirectory) + " -Recurse -Force -ErrorAction Stop }; if (Test-Path -LiteralPath " + powershellSingleQuoted(workDirectory) + ") { throw 'temporary artifact directory still exists after cleanup' }"
		for attempt = 0; attempt < 3; attempt++ {
			if _, cleanupErr = runWindowsArtifactCommand(cleanupContext, driver.settings, vm, request.Node, request.VMID, command, 10); cleanupErr == nil {
				return
			}
			if attempt < 2 {
				var timer *time.Timer = time.NewTimer(time.Second)
				select {
				case <-cleanupContext.Done():
					timer.Stop()
					cleanupErr = cleanupContext.Err()
					return
				case <-timer.C:
				}
			}
		}
		return
	}
	defer func() {
		if cleanupErr := cleanup(); err == nil {
			err = cleanupErr
		}
	}()
	if _, err = runWindowsArtifactCommand(ctx, driver.settings, vm, request.Node, request.VMID, "New-Item -ItemType Directory -Force -Path "+powershellSingleQuoted(workDirectory)+" | Out-Null", 30); err != nil {
		return
	}
	var directories map[string]struct{} = make(map[string]struct{})
	for _, file := range files {
		for directory := path.Dir(file.Path); directory != "."; directory = path.Dir(directory) {
			directories[directory] = struct{}{}
		}
	}
	var orderedDirectories []string = make([]string, 0, len(directories))
	for directory := range directories {
		orderedDirectories = append(orderedDirectories, directory)
	}
	sort.Slice(orderedDirectories, func(left int, right int) bool {
		if strings.Count(orderedDirectories[left], "/") == strings.Count(orderedDirectories[right], "/") {
			return orderedDirectories[left] < orderedDirectories[right]
		}
		return strings.Count(orderedDirectories[left], "/") < strings.Count(orderedDirectories[right], "/")
	})
	for _, directory := range orderedDirectories {
		var windowsDirectory string = workDirectory + `\` + strings.ReplaceAll(directory, "/", `\`)
		if _, err = runWindowsArtifactCommand(ctx, driver.settings, vm, request.Node, request.VMID, "New-Item -ItemType Directory -Force -Path "+powershellSingleQuoted(windowsDirectory)+" | Out-Null", 30); err != nil {
			return
		}
	}
	for _, file := range files {
		var target string = workDirectory + `\` + strings.ReplaceAll(file.Path, "/", `\`)
		if err = guestAgentFileWriteWithRetry(ctx, vm, target, file.Contents); err != nil {
			return
		}
	}
	var entrypoint string = workDirectory + `\` + strings.ReplaceAll(request.Entrypoint, "/", `\`)
	var command string = "Set-Location -LiteralPath " + powershellSingleQuoted(workDirectory) + "; & " + powershellSingleQuoted(entrypoint) + "; if (-not $?) { exit 1 }"
	var status guestAgentExecStatus
	if status, err = runWindowsArtifactCommand(ctx, driver.settings, vm, request.Node, request.VMID, command, 1800); err != nil {
		return
	}
	if status.ExitCode != 0 {
		result = guestArtifactResult(request, status)
		result.Status = "failed"
		err = &GuestArtifactExitError{ExitCode: status.ExitCode}
		return
	}
	result = guestArtifactResult(request, status)
	result.Status = "succeeded"
	return
}

// runWindowsArtifactCommand executes one PowerShell command through QEMU Guest Agent as LocalSystem.
func runWindowsArtifactCommand(ctx context.Context, settings config.ProxmoxConfiguration, vm *pve.VirtualMachine, node string, vmidText string, command string, timeoutSeconds int) (status guestAgentExecStatus, err error) {
	var vmid int
	if vmid, err = strconv.Atoi(vmidText); err != nil {
		return
	}
	var pid int
	if pid, err = guestAgentExecWithRetry(ctx, vm, []string{windowsPowerShellPath, "-NoProfile", "-NonInteractive", "-ExecutionPolicy", "Bypass", "-Command", command}, ""); err != nil {
		return
	}
	if status, err = waitForGuestExecExit(ctx, settings, node, vmid, pid, timeoutSeconds); err != nil {
		return
	}
	if status.ExitCode != 0 {
		err = fmt.Errorf("Windows guest command failed (exit %d): %s", status.ExitCode, strings.TrimSpace(status.ErrData+" "+status.OutData))
	}
	return
}

// powershellSingleQuoted returns a PowerShell single-quoted string literal.
func powershellSingleQuoted(value string) (quoted string) {
	quoted = "'" + strings.ReplaceAll(value, "'", "''") + "'"
	return
}

func guestArtifactScript() (script string) {
	script = "#!/usr/bin/bash\nset -eu\nwork=$1\nentrypoint=$2\nroot=\"$work/root\"\ncleanup() { cd /; /usr/bin/rm -rf -- \"$work\"; }\ntrap cleanup EXIT\ntrap 'cleanup; exit 129' HUP\ntrap 'cleanup; exit 130' INT\ntrap 'cleanup; exit 143' TERM\n/usr/bin/install -d -m 0700 -- \"$root\"\ntab=$(printf '\\t')\nwhile IFS=\"$tab\" read -r index mode expected_digest relative; do\n    [ -n \"$index\" ] || continue\n    target=\"$root/$relative\"\n    case \"$target\" in \"$root\"/*) ;; *) echo 'unsafe artifact path' >&2; exit 1 ;; esac\n    /usr/bin/install -d -m 0755 -- \"$(/usr/bin/dirname -- \"$target\")\"\n    : > \"$target\"\n    for part in \"$work\"/part-\"$index\"-*; do\n        [ -f \"$part\" ] || continue\n        /usr/bin/cat -- \"$part\" >> \"$target\"\n    done\n    actual_digest=$(/usr/bin/sha256sum -- \"$target\")\n    actual_digest=${actual_digest%% *}\n    [ \"$actual_digest\" = \"$expected_digest\" ] || { echo 'artifact file integrity check failed' >&2; exit 1; }\n    /usr/bin/chmod \"$mode\" -- \"$target\"\ndone < \"$work/manifest\"\ncd \"$root\"\n/usr/bin/bash \"$root/$entrypoint\"\n"
	return
}

// guestBSDArtifactScript installs verified package files with FreeBSD base-system tools.
func guestBSDArtifactScript() (script string) {
	script = "#!/bin/sh\nset -eu\nwork=$1\nentrypoint=$2\nroot=\"$work/root\"\ncleanup() { cd /; /bin/rm -rf \"$work\"; }\ntrap cleanup EXIT\ntrap 'cleanup; exit 129' HUP\ntrap 'cleanup; exit 130' INT\ntrap 'cleanup; exit 143' TERM\n/usr/bin/install -d -m 0700 \"$root\"\ntab=$(printf '\\t')\nwhile IFS=\"$tab\" read -r index mode expected_digest relative; do\n    [ -n \"$index\" ] || continue\n    target=\"$root/$relative\"\n    case \"$target\" in \"$root\"/*) ;; *) echo 'unsafe artifact path' >&2; exit 1 ;; esac\n    /usr/bin/install -d -m 0755 \"$(/usr/bin/dirname \"$target\")\"\n    : > \"$target\"\n    for part in \"$work\"/part-\"$index\"-*; do\n        [ -f \"$part\" ] || continue\n        /bin/cat \"$part\" >> \"$target\"\n    done\n    actual_digest=$(/sbin/sha256 -q \"$target\")\n    [ \"$actual_digest\" = \"$expected_digest\" ] || { echo 'artifact file integrity check failed' >&2; exit 1; }\n    /bin/chmod \"$mode\" \"$target\"\ndone < \"$work/manifest\"\ncd \"$root\"\n/bin/sh \"$root/$entrypoint\"\n"
	return
}

func validateGuestArtifactContentType(value string) (valid bool) {
	valid = strings.EqualFold(strings.TrimSpace(value), "application/vnd.organesson.artifact+gzip")
	return
}
