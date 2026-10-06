package proxmox

import (
	"context"
	"errors"
	"fmt"
	"net/netip"
	"sort"
	"strings"

	pve "github.com/luthermonson/go-proxmox"
)

const routerDHCPReservationsFile = "/etc/organesson/dnsmasq-reservations"
const routerDHCPLeaseFile = "/var/lib/misc/dnsmasq.leases"

// SetRouterDHCPReservations atomically replaces the managed router's IPv4 and IPv6 lease reservations.
func (service *Service) SetRouterDHCPReservations(ctx context.Context, vmid int, operationKey string, reservations []SDNRouterDHCPReservation) (err error) {
	if service == nil || !service.Configured() {
		err = ErrNotConfigured
		return
	}
	var content []byte
	if content, err = renderRouterDHCPReservations(reservations); err != nil {
		return
	}
	var client *pve.Client
	if client, err = newAPIClient(service.settings); err != nil {
		return
	}
	var vm *pve.VirtualMachine
	if _, vm, _, err = locateManagedVM(ctx, client, vmid); err != nil {
		return
	}
	if err = verifyManagedVM(vm, operationKey); err != nil {
		return
	}
	if err = waitForGuestAgent(ctx, vm); err != nil {
		return
	}
	var pid int
	if pid, err = vm.AgentExec(ctx, guestAgentCommand("/usr/bin/install", "-d", "-o", "root", "-g", "root", "-m", "0755", "/etc/organesson"), ""); err != nil {
		return
	}
	var status guestAgentExecStatus
	if status, err = waitForGuestExecExit(ctx, service.settings, vm.Node, vmid, pid, 30); err != nil {
		return
	}
	if status.ExitCode != 0 {
		err = fmt.Errorf("managed router could not prepare the Organesson reservation directory: %s", strings.TrimSpace(status.ErrData+" "+status.OutData))
		return
	}
	if err = vm.AgentFileWrite(ctx, routerDHCPReservationsFile, content); err != nil {
		return
	}
	if pid, err = vm.AgentExec(ctx, guestAgentCommand("/bin/sh", "-c", routerDHCPLeaseCleanupCommand(routerDHCPLeaseFile, routerDHCPReservationsFile)), ""); err != nil {
		return
	}
	if status, err = waitForGuestExecExit(ctx, service.settings, vm.Node, vmid, pid, 30); err != nil {
		return
	}
	if status.ExitCode != 0 {
		err = fmt.Errorf("managed router could not clear conflicting DHCP leases: %s", strings.TrimSpace(status.ErrData+" "+status.OutData))
		return
	}
	if pid, err = vm.AgentExec(ctx, guestAgentCommand("/usr/bin/systemctl", "restart", "dnsmasq.service"), ""); err != nil {
		return
	}
	if status, err = waitForGuestExecExit(ctx, service.settings, vm.Node, vmid, pid, 30); err != nil {
		return
	}
	if status.ExitCode != 0 {
		err = fmt.Errorf("router dnsmasq did not accept the Organesson DHCP reservation list: %s", strings.TrimSpace(status.ErrData+" "+status.OutData))
	}
	return
}

// routerDHCPLeaseCleanupCommand removes only conflicting stale leases for reserved addresses.
func routerDHCPLeaseCleanupCommand(leaseFile string, reservationFile string) (script string) {
	script = "set -eu\n" +
		"lease_file=" + shellQuote(leaseFile) + "\n" +
		"reservation_file=" + shellQuote(reservationFile) + "\n" +
		"if [ -f \"$lease_file\" ] && [ -f \"$reservation_file\" ]; then\n" +
		"    lease_temp=\"${lease_file}.organesson.$$\"\n" +
		"    trap 'rm -f \"$lease_temp\"' EXIT\n" +
		"    awk 'FILENAME == ARGV[1] {\n" +
		"        count = split($0, fields, \",\")\n" +
		"        address = fields[count]\n" +
		"        gsub(/^\\[/, \"\", address)\n" +
		"        gsub(/\\]$/, \"\", address)\n" +
		"        identity = tolower(fields[1])\n" +
		"        if (substr(identity, 1, 3) == \"id:\") identity = substr(identity, 4)\n" +
		"        reserved[address] = identity\n" +
		"        next\n" +
		"    } {\n" +
		"        address = $3\n" +
		"        expected = reserved[address]\n" +
		"        if (expected != \"\") {\n" +
		"            actual = index(address, \":\") ? tolower($NF) : tolower($2)\n" +
		"            if (actual != expected) next\n" +
		"        }\n" +
		"        print\n" +
		"    }' \"$reservation_file\" \"$lease_file\" >\"$lease_temp\"\n" +
		"    chmod --reference=\"$lease_file\" \"$lease_temp\"\n" +
		"    mv -f \"$lease_temp\" \"$lease_file\"\n" +
		"    trap - EXIT\n" +
		"fi\n"
	return
}

func renderRouterDHCPReservations(reservations []SDNRouterDHCPReservation) (content []byte, err error) {
	var contentLines []string
	var unique map[string]bool = make(map[string]bool, len(reservations))
	for _, reservation := range reservations {
		var address netip.Addr
		if !validNetworkMAC(reservation.MAC) {
			err = errors.New("router DHCP reservation contains an invalid MAC address")
			return
		}
		if address, err = netip.ParseAddr(reservation.Address); err != nil || address.IsUnspecified() || address.IsMulticast() || address.Zone() != "" {
			err = errors.New("router DHCP reservation must contain a usable IP address")
			return
		}
		var identity string
		if address.Is4() {
			if reservation.DUID != "" {
				err = errors.New("IPv4 DHCP reservation cannot contain a DUID")
				return
			}
			identity = strings.ToLower(reservation.MAC)
		} else {
			var expectedDUID string = DHCPv6DUIDFromMAC(reservation.MAC)
			if reservation.DUID != expectedDUID {
				err = errors.New("IPv6 DHCP reservation must use the attachment's deterministic DUID-LL")
				return
			}
			identity = "id:" + reservation.DUID
		}
		var line string = identity + "," + address.String()
		if address.Is6() {
			line = identity + ",[" + address.String() + "]"
		}
		if unique[line] {
			continue
		}
		unique[line] = true
		contentLines = append(contentLines, line)
	}
	sort.Strings(contentLines)
	var rendered string = "# Generated by Organesson; edits are replaced.\n"
	if len(contentLines) > 0 {
		rendered += strings.Join(contentLines, "\n") + "\n"
	}
	content = []byte(rendered)
	return
}

// DHCPv6DUIDFromMAC returns the Ethernet DUID-LL used by managed guest profiles.
func DHCPv6DUIDFromMAC(mac string) (duid string) {
	duid = "00:03:00:01:" + strings.ToLower(mac)
	return
}
