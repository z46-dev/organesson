package proxmox

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"strings"
	"time"

	pve "github.com/luthermonson/go-proxmox"
	"github.com/z46-dev/organesson/backend/config"
)

type (
	// GuestNetworkRequest identifies one managed NIC and its desired IPv4 and IPv6 configuration.
	GuestNetworkRequest struct {
		Node                 string                     `json:"node"`
		VMID                 string                     `json:"vmid"`
		VMOperationKey       string                     `json:"vm_operation_key"`
		AttachmentKey        string                     `json:"attachment_operation_key"`
		Bridge               string                     `json:"bridge"`
		NetworkOperationKey  string                     `json:"network_operation_key,omitempty"`
		Placement            NetworkAttachmentPlacement `json:"placement"`
		Method               string                     `json:"ipv4_method"`
		Address              string                     `json:"ipv4_address,omitempty"`
		Gateway              string                     `json:"ipv4_gateway,omitempty"`
		DNS                  []string                   `json:"ipv4_dns,omitempty"`
		NeverDefault         bool                       `json:"ipv4_never_default,omitempty"`
		IPv6Method           string                     `json:"ipv6_method,omitempty"`
		IPv6Address          string                     `json:"ipv6_address,omitempty"`
		IPv6Prefix           string                     `json:"ipv6_prefix,omitempty"`
		IPv6Gateway          string                     `json:"ipv6_gateway,omitempty"`
		IPv6DNS              []string                   `json:"ipv6_dns,omitempty"`
		IPv6NeverDefault     bool                       `json:"ipv6_never_default,omitempty"`
		EnforceAddressFilter bool                       `json:"enforce_address_filter,omitempty"`
		AllowedAddresses     []string                   `json:"allowed_addresses,omitempty"`
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
	request = normalizeGuestNetworkRequest(request)
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
	request = normalizeGuestNetworkRequest(request)
	if err = validateGuestNetworkRequest(request); err != nil {
		return
	}
	err = service.guestNetworkDriver.Read(ctx, request)
	return
}

// normalizeGuestNetworkRequest makes omitted IPv6 settings explicitly disabled instead of leaving OS defaults active.
func normalizeGuestNetworkRequest(request GuestNetworkRequest) (normalized GuestNetworkRequest) {
	normalized = request
	if normalized.IPv6Method == "" {
		normalized.IPv6Method = "disabled"
	}
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
			_, cleanupErr = waitForGuestExecExit(ctx, driver.settings, vm.Node, vmid, cleanupPID, 10)
		}
		if err == nil && cleanupErr != nil {
			err = cleanupErr
		}
	}()
	var pid int
	if pid, err = vm.AgentExec(ctx, guestAgentCommand("/usr/bin/bash", path), ""); err != nil {
		return
	}
	var status guestAgentExecStatus
	if status, err = waitForGuestExecExit(ctx, driver.settings, vm.Node, vmid, pid, 60); err != nil {
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
	case "disabled":
		if request.Address != "" || request.Gateway != "" || len(request.DNS) > 0 {
			err = errors.New("disabled IPv4 configuration cannot include static IPv4 values")
		}
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
		err = errors.New("guest IPv4 method must be disabled, dhcp, or static")
		return
	}
	if request.IPv6Method == "" {
		if request.IPv6Address != "" || request.IPv6Gateway != "" || len(request.IPv6DNS) > 0 {
			err = errors.New("IPv6 guest values require an IPv6 method")
			return
		}
		request.IPv6Method = "disabled"
	}
	if request.Method == "disabled" && request.IPv6Method == "disabled" {
		err = errors.New("at least one guest IP family must be enabled")
		return
	}
	if request.EnforceAddressFilter {
		var hasIPv4 bool
		var hasIPv6 bool
		for _, value := range request.AllowedAddresses {
			var allowed netip.Prefix
			if allowed, err = parseAllowedFilterEntry(value); err != nil || allowed.Addr().IsUnspecified() || allowed.Addr().IsMulticast() || allowed.Addr().Zone() != "" {
				err = errors.New("guest IP filter contains an invalid host or subnet entry")
				return
			}
			hasIPv4 = hasIPv4 || allowed.Addr().Is4()
			hasIPv6 = hasIPv6 || allowed.Addr().Is6()
		}
		if len(request.AllowedAddresses) == 0 || request.Method == "dhcp" && !hasIPv4 || (request.IPv6Method == "dhcp" || request.IPv6Method == "slaac") && !hasIPv6 {
			err = errors.New("dynamic guest networking requires an allowed address scope of the same family")
			return
		}
		if request.IPv6Method == "slaac" {
			if err = ValidateSLAACAllocation(request.IPv6Prefix, request.Placement.MAC, request.AllowedAddresses); err != nil {
				return
			}
		}
	}
	switch request.IPv6Method {
	case "disabled", "slaac", "dhcp":
		if request.IPv6Address != "" || request.IPv6Gateway != "" || len(request.IPv6DNS) > 0 {
			err = errors.New("non-static IPv6 configuration cannot include static IPv6 values")
		}
	case "static":
		var address netip.Prefix
		if address, err = netip.ParsePrefix(request.IPv6Address); err != nil || !address.Addr().Is6() || address.Addr().Is4In6() {
			err = errors.New("static guest IPv6 configuration requires an IPv6 address and prefix")
			return
		}
		for _, candidate := range append([]string{request.IPv6Gateway}, request.IPv6DNS...) {
			if candidate == "" {
				continue
			}
			var parsed netip.Addr
			if parsed, err = netip.ParseAddr(candidate); err != nil || !parsed.Is6() || parsed.Is4In6() {
				err = errors.New("guest IPv6 gateway and DNS entries must be valid IPv6 addresses")
				return
			}
		}
	default:
		err = errors.New("guest IPv6 method must be disabled, slaac, dhcp, or static")
	}
	return
}

// slaacEUI64Address derives the stable IPv6 host address generated from a /64 and NIC MAC.
func slaacEUI64Address(prefixValue string, macValue string) (address netip.Addr, err error) {
	var prefix netip.Prefix
	if prefix, err = netip.ParsePrefix(prefixValue); err != nil || !prefix.Addr().Is6() || prefix.Addr().Is4In6() || prefix.Bits() != 64 {
		err = errors.New("filtered SLAAC requires an allocated IPv6 /64 prefix")
		return
	}
	var mac net.HardwareAddr
	if mac, err = net.ParseMAC(macValue); err != nil || len(mac) != 6 {
		err = errors.New("filtered SLAAC requires a six-byte NIC MAC address")
		return
	}
	var host [8]byte
	host[0] = mac[0] ^ 0x02
	host[1] = mac[1]
	host[2] = mac[2]
	host[3] = 0xff
	host[4] = 0xfe
	host[5] = mac[3]
	host[6] = mac[4]
	host[7] = mac[5]
	var bytes [16]byte = prefix.Masked().Addr().As16()
	copy(bytes[8:], host[:])
	address = netip.AddrFrom16(bytes)
	return
}

// containsAllowedAddress reports whether a guest address is inside the allowed address or prefix entries.
func containsAllowedAddress(addresses []string, candidate netip.Addr) (found bool) {
	for _, value := range addresses {
		var allowed netip.Prefix
		if allowed, _ = parseAllowedFilterEntry(value); allowed.IsValid() && allowed.Contains(candidate) {
			found = true
			return
		}
	}
	return
}

// parseAllowedFilterEntry converts a host or canonical prefix into a comparable network prefix.
func parseAllowedFilterEntry(value string) (prefix netip.Prefix, err error) {
	if address, parseErr := netip.ParseAddr(value); parseErr == nil {
		var bits int = address.BitLen()
		prefix = netip.PrefixFrom(address, bits)
		return
	}
	if prefix, err = netip.ParsePrefix(value); err != nil || prefix != prefix.Masked() {
		err = errors.New("address filter entry must be a host address or canonical prefix")
	}
	return
}

// ValidateSLAACAllocation ensures the reserved IPv6 address is the EUI-64 host generated by this NIC.
func ValidateSLAACAllocation(prefix string, mac string, addresses []string) (err error) {
	var expected netip.Addr
	if expected, err = slaacEUI64Address(prefix, mac); err != nil {
		return
	}
	if !containsAllowedAddress(addresses, expected) {
		err = fmt.Errorf("SLAAC would assign %s, outside this interface's allowed address scope", expected)
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
		} else if method == "disabled" {
			nmcliMethod = "disabled"
		}
		var address string
		var gateway string
		var dns string
		if method == "static" {
			address = request.Address
			gateway = request.Gateway
			dns = strings.Join(request.DNS, ",")
		}
		var neverDefault string = "no"
		if request.NeverDefault {
			neverDefault = "yes"
		}
		var ipv6Method string = networkManagerIPv6Method(request.IPv6Method)
		var ipv6Address string
		var ipv6Gateway string
		var ipv6DNS string
		var ipv6NeverDefault string = "no"
		if request.IPv6Method == "static" {
			ipv6Address = request.IPv6Address
			ipv6Gateway = request.IPv6Gateway
			ipv6DNS = strings.Join(request.IPv6DNS, ",")
		}
		if request.IPv6NeverDefault {
			ipv6NeverDefault = "yes"
		}
		var options string = "ipv4.method " + shellQuote(nmcliMethod) + " ipv4.addresses " + shellQuote(address) + " ipv4.gateway " + shellQuote(gateway) + " ipv4.dns " + shellQuote(dns) + " ipv4.never-default " + neverDefault + " ipv6.method " + shellQuote(ipv6Method) + " ipv6.addresses " + shellQuote(ipv6Address) + " ipv6.gateway " + shellQuote(ipv6Gateway) + " ipv6.dns " + shellQuote(ipv6DNS) + " ipv6.never-default " + ipv6NeverDefault
		if request.IPv6Method == "slaac" || request.EnforceAddressFilter && request.IPv6Method != "disabled" {
			options += " ipv6.addr-gen-mode eui64 ipv6.ip6-privacy 0"
		}
		if request.IPv6Method == "dhcp" {
			options += " ipv6.dhcp-duid ll ipv6.dhcp-iaid mac"
		}
		operation = "if nmcli -t -f NAME connection show | /usr/bin/grep -Fxq \"$connection\"; then\n    nmcli connection modify \"$connection\" " + options + "\nelse\n    nmcli connection add type ethernet ifname \"$interface\" con-name \"$connection\" connection.autoconnect yes ethernet.mac-address " + shellQuote(request.Placement.MAC) + " " + options + "\nfi\nnmcli connection up \"$connection\""
		if request.EnforceAddressFilter && (request.Method == "dhcp" || request.IPv6Method == "dhcp" || request.IPv6Method == "slaac") {
			operation += "\n" + guestAllocatedAddressCheck(request, "interface")
		}
	}
	script = "#!/usr/bin/bash\nset -eu\ntrap 'rm -f \"$0\"' EXIT\nmac=" + shellQuote(strings.ToLower(request.Placement.MAC)) + "\nconnection=" + shellQuote(connection) + "\ninterface=\nfor address_file in /sys/class/net/*/address; do\n    [ -r \"$address_file\" ] || continue\n    if [ \"$(tr '[:upper:]' '[:lower:]' < \"$address_file\")\" = \"$mac\" ]; then\n        interface=${address_file%/address}\n        interface=${interface##*/}\n        break\n    fi\ndone\n[ -n \"$interface\" ] || { echo 'managed NIC MAC was not found' >&2; exit 1; }\n" + operation + "\n"
	return
}

// networkManagerIPv6Method maps API method names to NetworkManager IPv6 modes.
func networkManagerIPv6Method(method string) (mapped string) {
	switch method {
	case "disabled":
		mapped = "disabled"
	case "slaac":
		mapped = "auto"
	case "dhcp":
		mapped = "dhcp"
	case "static":
		mapped = "manual"
	default:
		mapped = "ignore"
	}
	return
}

func guestNetworkReadScript(request GuestNetworkRequest) (script string) {
	var connection string = "organesson-" + strings.ReplaceAll(strings.ToLower(request.Placement.MAC), ":", "")
	var expectedMethod string = "auto"
	var checks string
	if request.Method == "static" {
		expectedMethod = "manual"
		checks = "actual_address=$(nmcli -g ipv4.addresses connection show \"$connection\")\ncase \",${actual_address},\" in *," + shellQuote(request.Address) + ",*) ;; *) echo 'guest IPv4 address drift detected' >&2; exit 1 ;; esac\nactual_gateway=$(nmcli -g ipv4.gateway connection show \"$connection\")\n[ \"$actual_gateway\" = " + shellQuote(request.Gateway) + " ] || { echo 'guest IPv4 gateway drift detected' >&2; exit 1; }\nactual_dns=$(nmcli -g ipv4.dns connection show \"$connection\")\n[ \"$actual_dns\" = " + shellQuote(strings.Join(request.DNS, ",")) + " ] || { echo 'guest IPv4 DNS drift detected' >&2; exit 1; }\n"
	} else if request.Method == "dhcp" {
		checks = "actual_address=$(nmcli -g ipv4.addresses connection show \"$connection\")\nactual_gateway=$(nmcli -g ipv4.gateway connection show \"$connection\")\nactual_dns=$(nmcli -g ipv4.dns connection show \"$connection\")\n[ -z \"$actual_address$actual_gateway$actual_dns\" ] || { echo 'guest DHCP profile contains static IPv4 values' >&2; exit 1; }\n"
	} else {
		expectedMethod = "disabled"
	}
	var expectedNeverDefault string = "no"
	if request.NeverDefault {
		expectedNeverDefault = "yes"
	}
	checks += "actual_never_default=$(nmcli -g ipv4.never-default connection show \"$connection\")\n[ \"$actual_never_default\" = " + expectedNeverDefault + " ] || { echo 'guest IPv4 route preference drift detected' >&2; exit 1; }\n"
	var expectedIPv6Method string = networkManagerIPv6Method(request.IPv6Method)
	checks += "actual_ipv6_method=$(nmcli -g ipv6.method connection show \"$connection\")\n[ \"$actual_ipv6_method\" = " + shellQuote(expectedIPv6Method) + " ] || { echo 'guest IPv6 method drift detected' >&2; exit 1; }\n"
	if request.EnforceAddressFilter && request.IPv6Method != "disabled" {
		checks += "actual_ipv6_addr_gen_mode=$(nmcli -g ipv6.addr-gen-mode connection show \"$connection\")\n[ \"$actual_ipv6_addr_gen_mode\" = eui64 ] || { echo 'guest IPv6 link-local address generation drift detected' >&2; exit 1; }\nactual_ipv6_privacy=$(nmcli -g ipv6.ip6-privacy connection show \"$connection\")\n[ \"$actual_ipv6_privacy\" = 0 ] || { echo 'guest IPv6 privacy address drift detected' >&2; exit 1; }\n"
	}
	if request.IPv6Method == "static" {
		checks += "actual_ipv6_address=$(nmcli -g ipv6.addresses connection show \"$connection\" | tr -d '\\\\')\ncase \",${actual_ipv6_address},\" in *," + shellQuote(request.IPv6Address) + ",*) ;; *) echo 'guest IPv6 address drift detected' >&2; exit 1 ;; esac\nactual_ipv6_gateway=$(nmcli -g ipv6.gateway connection show \"$connection\" | tr -d '\\\\')\n[ \"$actual_ipv6_gateway\" = " + shellQuote(request.IPv6Gateway) + " ] || { echo 'guest IPv6 gateway drift detected' >&2; exit 1; }\nactual_ipv6_dns=$(nmcli -g ipv6.dns connection show \"$connection\" | tr -d '\\\\')\n[ \"$actual_ipv6_dns\" = " + shellQuote(strings.Join(request.IPv6DNS, ",")) + " ] || { echo 'guest IPv6 DNS drift detected' >&2; exit 1; }\n"
	} else if request.IPv6Method == "dhcp" {
		checks += "actual_ipv6_address=$(nmcli -g ipv6.addresses connection show \"$connection\")\nactual_ipv6_gateway=$(nmcli -g ipv6.gateway connection show \"$connection\")\nactual_ipv6_dns=$(nmcli -g ipv6.dns connection show \"$connection\")\n[ -z \"$actual_ipv6_address$actual_ipv6_gateway$actual_ipv6_dns\" ] || { echo 'guest DHCPv6 profile contains static IPv6 values' >&2; exit 1; }\nactual_ipv6_duid=$(nmcli -g ipv6.dhcp-duid connection show \"$connection\")\n[ \"$actual_ipv6_duid\" = ll ] || { echo 'guest DHCPv6 DUID drift detected' >&2; exit 1; }\nactual_ipv6_iaid=$(nmcli -g ipv6.dhcp-iaid connection show \"$connection\")\n[ \"$actual_ipv6_iaid\" = mac ] || { echo 'guest DHCPv6 IAID drift detected' >&2; exit 1; }\n"
	}
	var expectedIPv6NeverDefault string = "no"
	if request.IPv6NeverDefault {
		expectedIPv6NeverDefault = "yes"
	}
	checks += "actual_ipv6_never_default=$(nmcli -g ipv6.never-default connection show \"$connection\")\n[ \"$actual_ipv6_never_default\" = " + expectedIPv6NeverDefault + " ] || { echo 'guest IPv6 route preference drift detected' >&2; exit 1; }\n"
	if request.EnforceAddressFilter {
		checks += guestAllocatedAddressCheck(request, "interface")
	}
	script = "#!/usr/bin/bash\nset -eu\ntrap 'rm -f \"$0\"' EXIT\nconnection=" + shellQuote(connection) + "\nmac=" + shellQuote(strings.ToLower(request.Placement.MAC)) + "\ninterface=\nfor address_file in /sys/class/net/*/address; do\n    [ -r \"$address_file\" ] || continue\n    if [ \"$(tr '[:upper:]' '[:lower:]' < \"$address_file\")\" = \"$mac\" ]; then\n        interface=${address_file%/address}\n        interface=${interface##*/}\n        break\n    fi\ndone\n[ -n \"$interface\" ] || { echo 'managed NIC MAC was not found' >&2; exit 1; }\nactual_method=$(nmcli -g ipv4.method connection show \"$connection\" 2>/dev/null)\n[ \"$actual_method\" = " + shellQuote(expectedMethod) + " ] || { echo 'guest IPv4 method drift detected' >&2; exit 1; }\n" + checks
	return
}

// guestAllocatedAddressCheck rejects dynamically acquired addresses outside the interface's reservation.
func guestAllocatedAddressCheck(request GuestNetworkRequest, interfaceName string) (script string) {
	var checks []string
	if request.Method == "dhcp" {
		var allowedIPv4 []string = exactAllowedHostAddresses(request.AllowedAddresses, true)
		if len(allowedIPv4) > 0 {
			checks = append(checks, "verify_allocated_family IP4.ADDRESS "+shellQuote(strings.Join(allowedIPv4, " "))+" 0")
		}
	}
	if request.IPv6Method == "dhcp" || request.IPv6Method == "slaac" {
		var allowedIPv6 []string = exactAllowedHostAddresses(request.AllowedAddresses, false)
		if len(allowedIPv6) > 0 {
			checks = append(checks, "verify_allocated_family IP6.ADDRESS "+shellQuote(strings.Join(allowedIPv6, " "))+" 1")
		}
	}
	script = "verify_allocated_family() {\n    family=$1\n    allowed=$2\n    ignore_link_local=$3\n    attempt=0\n    last_actual=\n    while [ \"$attempt\" -lt 15 ]; do\n        actual=$(nmcli -g \"$family\" device show \"$" + interfaceName + "\" | cut -d/ -f1 | tr -d '\\\\')\n        last_actual=$actual\n        found=0\n        if [ -n \"$actual\" ]; then\n            while IFS= read -r address; do\n                [ -n \"$address\" ] || continue\n                if [ \"$ignore_link_local\" = 1 ]; then\n                    case \"$address\" in fe80:*|::1) continue ;; esac\n                fi\n                case \" $allowed \" in *\" $address \"*) found=1 ;; *) echo 'guest acquired an address outside its Organesson allocation' >&2; exit 1 ;; esac\n            done <<EOF\n$actual\nEOF\n        fi\n        [ \"$found\" = 1 ] && return 0\n        attempt=$((attempt + 1))\n        sleep 2\n    done\n    echo \"guest has no allocated address for filtered family $family (last observed: ${last_actual:-none})\" >&2\n    exit 1\n}\n"
	for _, check := range checks {
		script += check + "\n"
	}
	return
}

// exactAllowedHostAddresses returns only host entries for live exact-allocation checks.
func exactAllowedHostAddresses(addresses []string, ipv4 bool) (hosts []string) {
	for _, value := range addresses {
		var address netip.Addr
		if address, _ = netip.ParseAddr(value); address.IsValid() && address.Is4() == ipv4 {
			hosts = append(hosts, address.String())
		}
	}
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
