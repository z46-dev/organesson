package proxmox

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/netip"
	"strings"
	"time"

	pve "github.com/luthermonson/go-proxmox"
	"github.com/z46-dev/organesson/backend/config"
)

type (
	// GuestNetworkRequest identifies one managed NIC and its desired IPv4 configuration.
	GuestNetworkRequest struct {
		Node                string                     `json:"node"`
		VMID                string                     `json:"vmid"`
		VMOperationKey      string                     `json:"vm_operation_key"`
		AttachmentKey       string                     `json:"attachment_operation_key"`
		Bridge              string                     `json:"bridge"`
		NetworkOperationKey string                     `json:"network_operation_key,omitempty"`
		Placement           NetworkAttachmentPlacement `json:"placement"`
		Method              string                     `json:"ipv4_method"`
		Address             string                     `json:"ipv4_address,omitempty"`
		Gateway             string                     `json:"ipv4_gateway,omitempty"`
		DNS                 []string                   `json:"ipv4_dns,omitempty"`
	}

	apiGuestNetworkDriver struct {
		settings config.ProxmoxConfiguration
	}
)

// ConfigureGuestNetwork applies the requested connection using the privileged QEMU Guest Agent.
func (service *Service) ConfigureGuestNetwork(ctx context.Context, request GuestNetworkRequest) (err error) {
	if service == nil || service.guestNetworkDriver == nil || !service.Configured() {
		err = ErrNotConfigured
		return
	}
	if err = validateGuestNetworkRequest(request); err != nil {
		return
	}
	err = service.guestNetworkDriver.Configure(ctx, request)
	return
}

// ReadGuestNetwork verifies that NetworkManager still matches the saved managed configuration.
func (service *Service) ReadGuestNetwork(ctx context.Context, request GuestNetworkRequest) (err error) {
	if service == nil || service.guestNetworkDriver == nil || !service.Configured() {
		err = ErrNotConfigured
		return
	}
	if err = validateGuestNetworkRequest(request); err != nil {
		return
	}
	err = service.guestNetworkDriver.Read(ctx, request)
	return
}

// RemoveGuestNetwork deletes only the NetworkManager profile created for this Organesson NIC.
func (service *Service) RemoveGuestNetwork(ctx context.Context, request GuestNetworkRequest) (err error) {
	if service == nil || service.guestNetworkDriver == nil || !service.Configured() {
		err = ErrNotConfigured
		return
	}
	if err = validateGuestNetworkRequest(request); err != nil {
		return
	}
	err = service.guestNetworkDriver.Remove(ctx, request)
	return
}

// Configure applies or deletes one identified NetworkManager connection inside a managed guest.
func (driver *apiGuestNetworkDriver) Configure(ctx context.Context, request GuestNetworkRequest) (err error) {
	err = driver.execute(ctx, request, guestNetworkScript(request, false))
	return
}

// Read asks NetworkManager to confirm the requested per-NIC IPv4 settings.
func (driver *apiGuestNetworkDriver) Read(ctx context.Context, request GuestNetworkRequest) (err error) {
	err = driver.execute(ctx, request, guestNetworkReadScript(request))
	return
}

// Remove removes the Organesson-owned connection without touching unrelated guest profiles.
func (driver *apiGuestNetworkDriver) Remove(ctx context.Context, request GuestNetworkRequest) (err error) {
	err = driver.execute(ctx, request, guestNetworkScript(request, true))
	return
}

// execute delivers a short-lived root script through QGA and removes it after execution.
func (driver *apiGuestNetworkDriver) execute(ctx context.Context, request GuestNetworkRequest, script string) (err error) {
	var client *pve.Client
	if client, err = newAPIClient(driver.settings); err != nil {
		return
	}
	var vmid int
	if vmid, err = parseVMID(request.VMID); err != nil {
		return
	}
	var node *pve.Node
	var vm *pve.VirtualMachine
	if node, vm, _, err = locateManagedVM(ctx, client, vmid); err != nil {
		return
	}
	if err = verifyManagedVM(vm, request.VMOperationKey); err != nil {
		return
	}
	if err = verifyGuestNetworkTarget(ctx, client, node, vm, request); err != nil {
		return
	}
	if err = waitForGuestAgent(ctx, vm); err != nil {
		return
	}
	var digest [sha256.Size]byte = sha256.Sum256([]byte(request.AttachmentKey))
	var path string = "/run/organesson-network-" + hex.EncodeToString(digest[:8]) + ".sh"
	if err = vm.AgentFileWrite(ctx, path, []byte(script)); err != nil {
		return
	}
	defer func() {
		var cleanupPID int
		var cleanupErr error
		if cleanupPID, cleanupErr = vm.AgentExec(ctx, guestAgentCommand("/usr/bin/rm", "-f", path), ""); cleanupErr == nil {
			_, cleanupErr = vm.WaitForAgentExecExit(ctx, cleanupPID, 10)
		}
		if err == nil && cleanupErr != nil {
			err = cleanupErr
		}
	}()
	var pid int
	if pid, err = vm.AgentExec(ctx, guestAgentCommand("/usr/bin/bash", path), ""); err != nil {
		return
	}
	var status *pve.AgentExecStatus
	if status, err = vm.WaitForAgentExecExit(ctx, pid, 60); err != nil {
		return
	}
	if status.ExitCode != 0 {
		err = fmt.Errorf("guest network command failed: %s", strings.TrimSpace(status.ErrData+" "+status.OutData))
	}
	return
}

// guestAgentCommand uses the SELinux-labeled Organesson wrapper when the guest prep script installed it.
func guestAgentCommand(command ...string) (result []string) {
	var wrapper string = "/usr/libexec/qemu-ga/fsfreeze-hook.d/organesson-qga-exec"
	var dispatch string = "if [ -x " + wrapper + " ]; then exec " + wrapper + " \"$@\"; fi; exec \"$@\""
	result = append([]string{"/bin/sh", "-c", dispatch, "organesson-qga"}, command...)
	return
}

func verifyGuestNetworkTarget(ctx context.Context, client *pve.Client, node *pve.Node, vm *pve.VirtualMachine, request GuestNetworkRequest) (err error) {
	var attachmentRequest NetworkAttachmentRequest = NetworkAttachmentRequest{
		Node: request.Node, VMID: request.VMID, VMOperationKey: request.VMOperationKey,
		Bridge: request.Bridge, NetworkOperationKey: request.NetworkOperationKey, AttachmentOperationKey: request.AttachmentKey,
	}
	if err = verifyAttachmentTarget(ctx, client, node, attachmentRequest); err != nil {
		return
	}
	if vm.VirtualMachineConfig == nil {
		err = errors.New("managed Proxmox VM has no virtual NIC configuration")
		return
	}
	var options map[string]string = networkOptionMap(vm.VirtualMachineConfig.Nets[request.Placement.Device])
	if options["bridge"] != request.Bridge || !strings.EqualFold(options["macaddr"], networkAttachmentMAC(request.AttachmentKey)) || !strings.EqualFold(request.Placement.MAC, networkAttachmentMAC(request.AttachmentKey)) {
		err = errors.New("guest network target no longer matches the marked Proxmox NIC")
	}
	return
}

func waitForGuestAgent(ctx context.Context, vm *pve.VirtualMachine) (err error) {
	var timeout <-chan time.Time = time.After(120 * time.Second)
	var ticker *time.Ticker = time.NewTicker(2 * time.Second)
	defer ticker.Stop()
	for {
		if err = vm.AgentPing(ctx); err == nil {
			return
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-timeout:
			return fmt.Errorf("QEMU Guest Agent did not become reachable within 120 seconds: %w", err)
		case <-ticker.C:
		}
	}
}

func validateGuestNetworkRequest(request GuestNetworkRequest) (err error) {
	if request.Node == "" || request.VMID == "" || request.VMOperationKey == "" || request.AttachmentKey == "" || request.Bridge == "" || !validNetworkMAC(request.Placement.MAC) || !strings.HasPrefix(request.Placement.Device, "net") {
		err = errors.New("guest network configuration requires a managed VM and NIC identity")
		return
	}
	switch request.Method {
	case "dhcp":
		if request.Address != "" || request.Gateway != "" || len(request.DNS) > 0 {
			err = errors.New("DHCP guest network configuration cannot include static IPv4 values")
		}
	case "static":
		var address netip.Prefix
		if address, err = netip.ParsePrefix(request.Address); err != nil || !address.Addr().Is4() {
			err = errors.New("static guest network configuration requires an IPv4 address and prefix")
			return
		}
		for _, candidate := range append([]string{request.Gateway}, request.DNS...) {
			if candidate == "" {
				continue
			}
			var parsed netip.Addr
			if parsed, err = netip.ParseAddr(candidate); err != nil || !parsed.Is4() {
				err = errors.New("guest gateway and DNS entries must be IPv4 addresses")
				return
			}
		}
	default:
		err = errors.New("guest IPv4 method must be dhcp or static")
	}
	return
}

func guestNetworkScript(request GuestNetworkRequest, remove bool) (script string) {
	var connection string = "organesson-" + strings.ReplaceAll(strings.ToLower(request.Placement.MAC), ":", "")
	var operation string
	if remove {
		operation = "nmcli connection delete \"$connection\" 2>/dev/null || true"
	} else {
		var method string = request.Method
		var nmcliMethod string = "auto"
		if method == "static" {
			nmcliMethod = "manual"
		}
		var address string
		var gateway string
		var dns string
		if method == "static" {
			address = request.Address
			gateway = request.Gateway
			dns = strings.Join(request.DNS, ",")
		}
		var options string = "ipv4.method " + shellQuote(nmcliMethod) + " ipv4.addresses " + shellQuote(address) + " ipv4.gateway " + shellQuote(gateway) + " ipv4.dns " + shellQuote(dns)
		operation = "if nmcli -t -f NAME connection show | /usr/bin/grep -Fxq \"$connection\"; then\n    nmcli connection modify \"$connection\" " + options + "\nelse\n    nmcli connection add type ethernet ifname \"$interface\" con-name \"$connection\" connection.autoconnect yes ethernet.mac-address " + shellQuote(request.Placement.MAC) + " " + options + "\nfi\nnmcli connection up \"$connection\""
	}
	script = "#!/usr/bin/bash\nset -eu\ntrap 'rm -f \"$0\"' EXIT\nmac=" + shellQuote(strings.ToLower(request.Placement.MAC)) + "\nconnection=" + shellQuote(connection) + "\ninterface=\nfor address_file in /sys/class/net/*/address; do\n    [ -r \"$address_file\" ] || continue\n    if [ \"$(tr '[:upper:]' '[:lower:]' < \"$address_file\")\" = \"$mac\" ]; then\n        interface=${address_file%/address}\n        interface=${interface##*/}\n        break\n    fi\ndone\n[ -n \"$interface\" ] || { echo 'managed NIC MAC was not found' >&2; exit 1; }\n" + operation + "\n"
	return
}

func guestNetworkReadScript(request GuestNetworkRequest) (script string) {
	var connection string = "organesson-" + strings.ReplaceAll(strings.ToLower(request.Placement.MAC), ":", "")
	var expectedMethod string = "auto"
	var checks string
	if request.Method == "static" {
		expectedMethod = "manual"
		checks = "actual_address=$(nmcli -g ipv4.addresses connection show \"$connection\")\ncase \",${actual_address},\" in *," + shellQuote(request.Address) + ",*) ;; *) echo 'guest IPv4 address drift detected' >&2; exit 1 ;; esac\nactual_gateway=$(nmcli -g ipv4.gateway connection show \"$connection\")\n[ \"$actual_gateway\" = " + shellQuote(request.Gateway) + " ] || { echo 'guest IPv4 gateway drift detected' >&2; exit 1; }\nactual_dns=$(nmcli -g ipv4.dns connection show \"$connection\")\n[ \"$actual_dns\" = " + shellQuote(strings.Join(request.DNS, ",")) + " ] || { echo 'guest IPv4 DNS drift detected' >&2; exit 1; }\n"
	} else {
		checks = "actual_address=$(nmcli -g ipv4.addresses connection show \"$connection\")\nactual_gateway=$(nmcli -g ipv4.gateway connection show \"$connection\")\nactual_dns=$(nmcli -g ipv4.dns connection show \"$connection\")\n[ -z \"$actual_address$actual_gateway$actual_dns\" ] || { echo 'guest DHCP profile contains static IPv4 values' >&2; exit 1; }\n"
	}
	script = "#!/usr/bin/bash\nset -eu\ntrap 'rm -f \"$0\"' EXIT\nconnection=" + shellQuote(connection) + "\nactual_method=$(nmcli -g ipv4.method connection show \"$connection\" 2>/dev/null)\n[ \"$actual_method\" = " + shellQuote(expectedMethod) + " ] || { echo 'guest IPv4 method drift detected' >&2; exit 1; }\n" + checks
	return
}

func shellQuote(value string) (quoted string) {
	quoted = "'" + strings.ReplaceAll(value, "'", "'\\''") + "'"
	return
}

func validNetworkMAC(value string) (valid bool) {
	var normalized string = strings.ToLower(value)
	valid = len(normalized) == 17 && normalized[2] == ':' && normalized[5] == ':' && normalized[8] == ':' && normalized[11] == ':' && normalized[14] == ':'
	if !valid {
		return
	}
	for index, character := range normalized {
		if index%3 == 2 {
			continue
		}
		if !(character >= '0' && character <= '9') && !(character >= 'a' && character <= 'f') {
			valid = false
			return
		}
	}
	return
}
