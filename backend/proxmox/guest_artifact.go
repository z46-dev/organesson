package proxmox

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"path"
	"strings"
	"time"

	pve "github.com/luthermonson/go-proxmox"
	"github.com/z46-dev/organesson/backend/config"
)

const (
	MaxGuestArtifactArchiveBytes int   = 17 * 1024 * 1024
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
		Archive        []byte `json:"-"`
	}

	// GuestArtifactFile is a verified regular file from the compressed artifact.
	GuestArtifactFile struct {
		Path       string
		Contents   []byte
		Executable bool
		SHA256     string
	}

	// GuestArtifactResult reports successful execution without returning arbitrary guest output.
	GuestArtifactResult struct {
		SHA256   string `json:"sha256"`
		Status   string `json:"status"`
		ExitCode int    `json:"exit_code"`
		Reused   bool   `json:"reused,omitempty"`
	}

	apiGuestArtifactDriver struct {
		settings config.ProxmoxConfiguration
	}
)

// GuestArtifactExitError reports the guest entrypoint's non-zero exit status.
type GuestArtifactExitError struct {
	ExitCode int
}

// Error describes a guest entrypoint that completed unsuccessfully.
func (executionError *GuestArtifactExitError) Error() (message string) {
	message = fmt.Sprintf("guest artifact entrypoint exited with status %d", executionError.ExitCode)
	return
}

// ExecuteGuestArtifact validates, delivers, and runs an artifact on a managed Linux VM.
func (service *Service) ExecuteGuestArtifact(ctx context.Context, request GuestArtifactRequest) (result GuestArtifactResult, err error) {
	if service == nil || service.guestArtifactDriver == nil || !service.Configured() {
		err = ErrNotConfigured
		return
	}
	var files []GuestArtifactFile
	if files, err = validateGuestArtifact(request); err != nil {
		return
	}
	service.lifecycleLock.Lock()
	defer service.lifecycleLock.Unlock()
	result, err = service.guestArtifactDriver.Execute(ctx, request, files)
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
	var operationNonce [16]byte
	if _, err = rand.Read(operationNonce[:]); err != nil {
		return
	}
	var workDirectory string = "/run/organesson-artifact-" + hex.EncodeToString(operationNonce[:])
	var cleanup func() error = func() (cleanupErr error) {
		var cleanupContext context.Context
		var cancel context.CancelFunc
		cleanupContext, cancel = context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		var cleanupPID int
		if cleanupPID, cleanupErr = vm.AgentExec(cleanupContext, guestAgentCommand("/usr/bin/rm", "-rf", "--", workDirectory), ""); cleanupErr == nil {
			var cleanupStatus *pve.AgentExecStatus
			if cleanupStatus, cleanupErr = vm.WaitForAgentExecExit(cleanupContext, cleanupPID, 10); cleanupErr == nil && cleanupStatus.ExitCode != 0 {
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
	if mkdirPID, err = vm.AgentExec(ctx, guestAgentCommand("/usr/bin/mkdir", "-m", "0700", "--", workDirectory), ""); err != nil {
		return
	}
	var mkdirStatus *pve.AgentExecStatus
	if mkdirStatus, err = vm.WaitForAgentExecExit(ctx, mkdirPID, 10); err != nil || mkdirStatus.ExitCode != 0 {
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
			if err = vm.AgentFileWrite(ctx, chunkPath, file.Contents[offset:end]); err != nil {
				return
			}
			offset = end
		}
	}
	var manifestPath string = workDirectory + "/manifest"
	if err = vm.AgentFileWrite(ctx, manifestPath, []byte(manifest.String())); err != nil {
		return
	}
	var scriptPath string = workDirectory + "/run.sh"
	if err = vm.AgentFileWrite(ctx, scriptPath, []byte(guestArtifactScript())); err != nil {
		return
	}
	var pid int
	if pid, err = vm.AgentExec(ctx, guestAgentCommand("/usr/bin/bash", scriptPath, workDirectory, request.Entrypoint), ""); err != nil {
		return
	}
	var status *pve.AgentExecStatus
	if status, err = vm.WaitForAgentExecExit(ctx, pid, 300); err != nil {
		return
	}
	if status.ExitCode != 0 {
		result = GuestArtifactResult{SHA256: request.SHA256, Status: "failed", ExitCode: status.ExitCode}
		err = &GuestArtifactExitError{ExitCode: status.ExitCode}
		return
	}
	result = GuestArtifactResult{SHA256: request.SHA256, Status: "succeeded", ExitCode: status.ExitCode}
	return
}

func guestArtifactScript() (script string) {
	script = "#!/usr/bin/bash\nset -eu\nwork=$1\nentrypoint=$2\nroot=\"$work/root\"\ncleanup() { /usr/bin/rm -rf -- \"$work\"; }\ntrap cleanup EXIT\ntrap 'cleanup; exit 129' HUP\ntrap 'cleanup; exit 130' INT\ntrap 'cleanup; exit 143' TERM\n/usr/bin/install -d -m 0700 -- \"$root\"\ntab=$(printf '\\t')\nwhile IFS=\"$tab\" read -r index mode expected_digest relative; do\n    [ -n \"$index\" ] || continue\n    target=\"$root/$relative\"\n    case \"$target\" in \"$root\"/*) ;; *) echo 'unsafe artifact path' >&2; exit 1 ;; esac\n    /usr/bin/install -d -m 0755 -- \"$(/usr/bin/dirname -- \"$target\")\"\n    : > \"$target\"\n    for part in \"$work\"/part-\"$index\"-*; do\n        [ -f \"$part\" ] || continue\n        /usr/bin/cat -- \"$part\" >> \"$target\"\n    done\n    actual_digest=$(/usr/bin/sha256sum -- \"$target\")\n    actual_digest=${actual_digest%% *}\n    [ \"$actual_digest\" = \"$expected_digest\" ] || { echo 'artifact file integrity check failed' >&2; exit 1; }\n    /usr/bin/chmod \"$mode\" -- \"$target\"\ndone < \"$work/manifest\"\ncd \"$root\"\n/usr/bin/bash \"$root/$entrypoint\"\n"
	return
}

func validateGuestArtifactContentType(value string) (valid bool) {
	valid = strings.EqualFold(strings.TrimSpace(value), "application/vnd.organesson.artifact+gzip")
	return
}
