package proxmox

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/netip"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	pve "github.com/luthermonson/go-proxmox"
	"github.com/z46-dev/golog"
	"github.com/z46-dev/organesson/backend/config"
	"github.com/z46-dev/organesson/backend/db"
)

// TestLiveProxmoxFirewallReadiness inspects firewall prerequisites and only VMs in Organesson's PVE pool.
func TestLiveProxmoxFirewallReadiness(t *testing.T) {
	if os.Getenv("ORGANESSON_LIVE_PVE_FIREWALL") != "1" {
		t.Skip("set ORGANESSON_LIVE_PVE_FIREWALL=1 to inspect live Proxmox firewall readiness")
	}
	var configPath string = os.Getenv("ORGANESSON_CONFIG")
	if configPath == "" {
		configPath = filepath.Join("..", "config.toml")
	}
	if err := config.Init(configPath); err != nil {
		t.Fatalf("load Organesson configuration: %v", err)
	}
	var client *pve.Client
	var err error
	if client, err = newAPIClient(config.Cfg.Proxmox); err != nil {
		t.Fatalf("configure Proxmox API client: %v", err)
	}
	var cluster *pve.Cluster
	if cluster, err = client.Cluster(context.Background()); err != nil {
		t.Fatalf("read Proxmox cluster: %v", err)
	}
	if os.Getenv("ORGANESSON_ALLOW_VXLAN_PEERS") == "1" {
		if err = ensureLiveVXLANPeerFirewallRules(context.Background(), client, cluster); err != nil {
			t.Fatalf("ensure narrowly scoped VXLAN peer firewall rules: %v", err)
		}
		t.Log("verified only peer-specific UDP/4789 rules on quorate VXLAN nodes; no cluster policy or existing unrelated rules changed")
	}
	var clusterOptions *pve.FirewallClusterOption
	if clusterOptions, err = cluster.FirewallOptions(context.Background()); err != nil {
		t.Fatalf("read cluster firewall options: %v", err)
	}
	if os.Getenv("ORGANESSON_LIVE_SDN_DIAGNOSTICS") == "1" {
		t.Logf("cluster firewall policy: %+v", clusterOptions)
	}
	if os.Getenv("ORGANESSON_ENABLE_CLUSTER_FIREWALL") == "1" && clusterOptions.Enable != 1 {
		if err = cluster.FirewallOptionsUpdate(context.Background(), &pve.FirewallClusterOptionUpdateOption{Enable: 1}); err != nil {
			t.Fatalf("enable cluster firewall without changing existing policy: %v", err)
		}
		if clusterOptions, err = cluster.FirewallOptions(context.Background()); err != nil {
			t.Fatalf("verify cluster firewall options: %v", err)
		}
	}
	t.Logf("cluster firewall enabled: %t", clusterOptions != nil && clusterOptions.Enable == 1)
	var clusterRules []*pve.FirewallRule
	if clusterRules, err = cluster.FirewallRules(context.Background()); err != nil {
		t.Fatalf("read cluster firewall rules: %v", err)
	}
	for _, rule := range clusterRules {
		if rule != nil {
			t.Logf("cluster firewall rule pos=%d enabled=%t type=%s action=%s source=%s destination=%s interface=%s comment=%q", rule.Pos, rule.Enable == 1, rule.Type, rule.Action, rule.Source, rule.Dest, rule.Iface, rule.Comment)
		}
	}
	var resources pve.ClusterResources
	if resources, err = cluster.Resources(context.Background(), "vm"); err != nil {
		t.Fatalf("read Proxmox VM inventory: %v", err)
	}
	if os.Getenv("ORGANESSON_LIVE_SDN_DIAGNOSTICS") == "1" {
		var zone *pve.SDNZone
		if zone, err = cluster.SDNZone(context.Background(), "ogvxlan"); err != nil {
			t.Fatalf("read Organesson VXLAN zone config: %v", err)
		}
		t.Logf("ogvxlan zone config: %+v", zone)
		for _, nodeName := range []string{"osmium", "tungsten"} {
			var node *pve.Node
			if node, err = client.Node(context.Background(), nodeName); err != nil {
				t.Fatalf("read node %q: %v", nodeName, err)
			}
			var zoneStatuses []*pve.SDNZoneStatus
			if zoneStatuses, err = node.SDNZones(context.Background()); err != nil {
				t.Fatalf("read SDN zone status on %q: %v", nodeName, err)
			}
			var content []*pve.SDNZoneContent
			if content, err = node.SDNZoneContent(context.Background(), "ogvxlan"); err != nil {
				t.Fatalf("read ogvxlan VNet status on %q: %v", nodeName, err)
			}
			for _, status := range zoneStatuses {
				if status != nil {
					t.Logf("node %s zone %s runtime status: %s", nodeName, status.Zone, status.Status)
				}
			}
			for _, vnet := range content {
				if vnet != nil {
					t.Logf("node %s VNet %s runtime status: %s (%s)", nodeName, vnet.VNet, vnet.Status, vnet.StatusMsg)
				}
			}
		}
	}
	var inspected int
	for _, resource := range resources {
		if resource == nil || resource.Type != "qemu" || resource.Pool != "organesson" {
			continue
		}
		var vmid int
		if vmid, err = strconv.Atoi(strings.TrimPrefix(resource.ID, "qemu/")); err != nil {
			t.Fatalf("parse Organesson VM ID %q: %v", resource.ID, err)
		}
		var node *pve.Node
		if node, err = client.Node(context.Background(), resource.Node); err != nil {
			t.Fatalf("read node %q: %v", resource.Node, err)
		}
		var nodeOptions *pve.FirewallNodeOption
		if nodeOptions, err = node.FirewallOptionGet(context.Background()); err != nil {
			t.Fatalf("read node %q firewall options: %v", resource.Node, err)
		}
		if os.Getenv("ORGANESSON_LIVE_SDN_DIAGNOSTICS") == "1" {
			t.Logf("node %s firewall policy: %+v", resource.Node, nodeOptions)
		}
		var nodeRules []*pve.FirewallRule
		if nodeRules, err = node.FirewallRules(context.Background()); err != nil {
			t.Fatalf("read node %q firewall rules: %v", resource.Node, err)
		}
		for _, rule := range nodeRules {
			if rule != nil {
				t.Logf("node %s firewall rule pos=%d enabled=%t type=%s action=%s source=%s destination=%s interface=%s comment=%q", resource.Node, rule.Pos, rule.Enable == 1, rule.Type, rule.Action, rule.Source, rule.Dest, rule.Iface, rule.Comment)
			}
		}
		var vm *pve.VirtualMachine
		if vm, err = node.VirtualMachine(context.Background(), vmid); err != nil {
			t.Fatalf("read Organesson VM %d: %v", vmid, err)
		}
		if err = vm.Ping(context.Background()); err != nil {
			t.Fatalf("read Organesson VM %d status and config: %v", vmid, err)
		}
		if os.Getenv("ORGANESSON_LIVE_SDN_DIAGNOSTICS") == "1" && (vmid == 129 || vmid == 168) {
			var vmRules []*pve.FirewallRule
			if vmRules, err = vm.FirewallRules(context.Background()); err != nil {
				t.Fatalf("read diagnostic VM %d firewall rules: %v", vmid, err)
			}
			t.Logf("diagnostic VM %d firewall rules: %+v", vmid, vmRules)
		}
		var guestAgent string = "not queried (VM stopped)"
		if vm.Status == "running" {
			guestAgent = "unavailable"
			if err = vm.AgentPing(context.Background()); err == nil {
				guestAgent = "available"
			}
		}
		if os.Getenv("ORGANESSON_ROUTER_DIAGNOSTICS") == "1" && strings.Contains(strings.ToLower(resource.Name), "router") && vm.Status == "running" {
			var rules []*pve.FirewallRule
			if rules, err = vm.FirewallRules(context.Background()); err != nil {
				t.Fatalf("read router VM %d firewall rules: %v", vmid, err)
			}
			for _, rule := range rules {
				if rule != nil {
					t.Logf("router VM %d firewall rule: type=%s action=%s enabled=%t interface=%s protocol=%s source-port=%s destination-port=%s source=%s comment=%q", vmid, rule.Type, rule.Action, rule.Enable == 1, rule.Iface, rule.Proto, rule.Sport, rule.Dport, rule.Source, rule.Comment)
				}
			}
			var pid int
			if pid, err = vm.AgentExec(context.Background(), guestAgentCommand("/bin/sh", "-c", "echo '--- network ---'; ip -br address; echo '--- listeners ---'; ss -lnutp | grep -E '(:53|:67|:547)' || true; echo '--- input firewall ---'; nft list chain inet organesson input; echo '--- dnsmasq config ---'; cat /etc/dnsmasq.d/organesson-router.conf; echo '--- reservations ---'; cat /etc/organesson/dnsmasq-reservations; echo '--- leases ---'; cat /var/lib/misc/dnsmasq.leases; echo '--- service ---'; systemctl --no-pager --full status dnsmasq.service; echo '--- recent dnsmasq logs ---'; journalctl -u dnsmasq.service --no-pager -n 100"), ""); err != nil {
				t.Fatalf("run router VM %d diagnostics: %v", vmid, err)
			}
			var diagnostic guestAgentExecStatus
			if diagnostic, err = waitForGuestExecExit(context.Background(), config.Cfg.Proxmox, vm.Node, vmid, pid, 70); err != nil {
				t.Fatalf("read router VM %d diagnostics: %v", vmid, err)
			}
			t.Logf("Router VM %d (%s) diagnostics (exit %d):\n%s%s", vmid, resource.Name, diagnostic.ExitCode, diagnostic.OutData, diagnostic.ErrData)
		}
		var vmOptions *pve.FirewallVirtualMachineOption
		if vmOptions, err = getVMFirewallOptions(context.Background(), client, vm); err != nil {
			t.Fatalf("read Organesson VM %d firewall options: %v", vmid, err)
		}
		if os.Getenv("ORGANESSON_RESTORE_FIREWALL_LOGGING") == "1" && vmid == 129 && (vmOptions.LogLevelIn == "debug" || vmOptions.LogLevelOut == "debug") {
			vmOptions.LogLevelIn = "nolog"
			vmOptions.LogLevelOut = "nolog"
			if err = vm.FirewallOptionSet(context.Background(), vmOptions); err != nil {
				t.Fatalf("restore normal firewall log levels on diagnostic VM %d: %v", vmid, err)
			}
			if vmOptions, err = getVMFirewallOptions(context.Background(), client, vm); err != nil {
				t.Fatalf("verify normal firewall log levels on diagnostic VM %d: %v", vmid, err)
			}
		}
		t.Logf("Organesson VM %d (%s) on %s: status=%s, guest-agent=%s, node firewall enabled=%v, VM firewall=%+v", vmid, resource.Name, resource.Node, vm.Status, guestAgent, nodeOptions == nil || nodeOptions.Enable == nil || bool(*nodeOptions.Enable), vmOptions)
		inspected++
	}
	t.Logf("inspected %d VMs in the organesson pool", inspected)
	var store *db.Store
	var databasePath string = config.Cfg.Database.File
	if !filepath.IsAbs(databasePath) {
		databasePath = filepath.Join(filepath.Dir(configPath), databasePath)
	}
	if store, err = db.Open(databasePath, golog.New(), false); err != nil {
		t.Fatalf("open Organesson database for filter verification: %v", err)
	}
	defer store.Close()
	var managedResources []*db.ManagedResource
	if managedResources, err = store.ManagedResources.SelectAll(); err != nil {
		t.Fatalf("read Organesson managed resources for filter verification: %v", err)
	}
	type (
		liveManagedNetworkConfiguration struct {
			Request SDNNetworkRequest `json:"request"`
		}
		liveAttachmentConfiguration struct {
			Request           NetworkAttachmentRequest   `json:"request"`
			Placement         NetworkAttachmentPlacement `json:"placement"`
			Guest             *GuestNetworkRequest       `json:"guest_network"`
			AddressPrefix     string                     `json:"address_prefix,omitempty"`
			IPv6AddressPrefix string                     `json:"ipv6_address_prefix,omitempty"`
		}
		liveRouterAttachmentConfiguration struct {
			Request   NetworkAttachmentRequest   `json:"request"`
			Placement NetworkAttachmentPlacement `json:"placement"`
			Guest     *GuestNetworkRequest       `json:"guest_network"`
		}
	)
	var verifiedAttachments int
	var routerLANSubnets map[string][]string = make(map[string][]string)
	for _, resource := range managedResources {
		if resource == nil || resource.Kind != "virtual_network" {
			continue
		}
		var network liveManagedNetworkConfiguration
		if err = json.Unmarshal([]byte(resource.ConfigurationJSON), &network); err != nil {
			t.Fatalf("decode managed network %d: %v", resource.ID, err)
		}
		if network.Request.RouterVMID < 1 {
			continue
		}
		var subnets []string
		if network.Request.Subnet != "" {
			subnets = append(subnets, network.Request.Subnet)
		}
		if network.Request.IPv6Subnet != "" {
			subnets = append(subnets, network.Request.IPv6Subnet)
		}
		routerLANSubnets[strconv.Itoa(network.Request.RouterVMID)+"/"+resource.ExternalID] = subnets
	}
	if os.Getenv("ORGANESSON_RECONCILE_ROUTER_DNS_RULES") == "1" {
		for _, resource := range managedResources {
			if resource == nil || resource.Kind != "network_attachment" {
				continue
			}
			var attachment liveAttachmentConfiguration
			if err = json.Unmarshal([]byte(resource.ConfigurationJSON), &attachment); err != nil {
				t.Fatalf("decode network attachment %d for router DNS reconciliation: %v", resource.ID, err)
			}
			subnets := routerLANSubnets[attachment.Request.VMID+"/"+attachment.Request.Bridge]
			if len(subnets) == 0 {
				continue
			}
			var routerVMID int
			if routerVMID, err = strconv.Atoi(attachment.Request.VMID); err != nil {
				t.Fatalf("parse router VM ID for attachment %d: %v", resource.ID, err)
			}
			var router *pve.VirtualMachine
			if _, router, _, err = locateManagedVM(context.Background(), client, routerVMID); err != nil {
				t.Fatalf("locate router VM %d for DNS firewall rules: %v", routerVMID, err)
			}
			attachment.Request.AllowedClientSubnets = subnets
			if err = ensureVMInterfaceRouterDNSRules(context.Background(), router, attachment.Placement.Device, attachment.Request); err != nil {
				t.Fatalf("ensure router DNS ingress rules for VM %d attachment %d: %v", routerVMID, resource.ID, err)
			}
			if err = verifyVMInterfaceRouterDNSRules(context.Background(), router, attachment.Placement.Device, attachment.Request); err != nil {
				t.Fatalf("verify router DNS ingress rules for VM %d attachment %d: %v", routerVMID, resource.ID, err)
			}
			t.Logf("reconciled DNS-only router ingress on VM %d NIC %s from %v", routerVMID, attachment.Placement.Device, subnets)
		}
	}
	for _, resource := range managedResources {
		if resource == nil || resource.Kind != "network_attachment" {
			continue
		}
		var attachment liveAttachmentConfiguration
		if err = json.Unmarshal([]byte(resource.ConfigurationJSON), &attachment); err != nil {
			t.Fatalf("decode network attachment %d: %v", resource.ID, err)
		}
		if !attachment.Request.EnforceAddressFilter {
			continue
		}
		var vmid int
		if vmid, err = parseVMID(attachment.Request.VMID); err != nil {
			t.Fatalf("parse VMID for filtered attachment %d: %v", resource.ID, err)
		}
		var vm *pve.VirtualMachine
		if _, vm, _, err = locateManagedVM(context.Background(), client, vmid); err != nil {
			t.Fatalf("locate VM %d for filtered attachment %d: %v", vmid, resource.ID, err)
		}
		if err = verifyManagedVM(vm, attachment.Request.VMOperationKey); err != nil {
			t.Fatalf("verify VM %d for filtered attachment %d: %v", vmid, resource.ID, err)
		}
		if attachment.Request.AllowDHCPv6Server && os.Getenv("ORGANESSON_RECONCILE_DHCPV6_RULES") == "1" {
			if err = ensureVMInterfaceDHCPv6ServerRule(context.Background(), vm, attachment.Placement.Device, attachment.Request); err != nil {
				t.Fatalf("reconcile DHCPv6 firewall rule for attachment %d on VM %d: %v", resource.ID, vmid, err)
			}
			if err = verifyVMInterfaceDHCPv6ServerRule(context.Background(), vm, attachment.Placement.Device, attachment.Request); err != nil {
				t.Fatalf("verify DHCPv6 firewall rule for attachment %d on VM %d: %v", resource.ID, vmid, err)
			}
		}
		if err = verifyVMInterfaceIPFilter(context.Background(), client, vm, attachment.Placement.Device, attachment.Request); err != nil {
			t.Fatalf("verify Proxmox source-address filter for attachment %d on VM %d: %v", resource.ID, vmid, err)
		}
		if os.Getenv("ORGANESSON_APPLY_VM_FIREWALL_POLICY") == "1" {
			var driver apiNetworkAttachmentDriver
			if err = driver.enableVMIPFiltering(context.Background(), client, vm); err != nil {
				t.Fatalf("enable allocated-address firewall policy for Organesson VM %d: %v", vmid, err)
			}
		}
		var diagnosticAttachmentID string = os.Getenv("ORGANESSON_LIVE_GUEST_NETWORK_ID")
		if os.Getenv("ORGANESSON_GUEST_NETWORK_DIAGNOSTICS") == "1" && vm.Status == "running" && (diagnosticAttachmentID == "" || diagnosticAttachmentID == strconv.Itoa(resource.ID)) {
			t.Logf("diagnosing attachment %d on VM %d with allowed addresses %v", resource.ID, vmid, attachment.Request.AllowedAddresses)
			var reactivation string
			if os.Getenv("ORGANESSON_GUEST_REACTIVATE") == "1" {
				reactivation = "nmcli connection up \"$connection\"; sleep 50; "
			}
			var command string = "mac=" + shellQuote(strings.ToLower(attachment.Placement.MAC)) + "; connection='organesson-'$(printf '%s' \"$mac\" | tr -d ':'); interface=; for file in /sys/class/net/*/address; do [ -r \"$file\" ] || continue; if [ \"$(tr '[:upper:]' '[:lower:]' < \"$file\")\" = \"$mac\" ]; then interface=${file%/address}; interface=${interface##*/}; break; fi; done; echo \"interface=$interface connection=$connection\"; " + reactivation + "nmcli -g ipv4.method,ipv4.addresses,ipv4.gateway,ipv6.method,ipv6.addresses,ipv6.gateway connection show \"$connection\"; echo '--- live addresses ---'; nmcli -g IP4.ADDRESS,IP6.ADDRESS device show \"$interface\" | tr -d '\\\\'; ip -4 -o address show dev \"$interface\"; ip -6 -o address show dev \"$interface\" scope global; echo '--- route probes ---'; ip route get 192.168.2.1 from 192.168.2.30; ip -6 route get fd42:2::1 from fd42:2::3; echo '--- capture tools ---'; command -v tcpdump || true; command -v tshark || true; command -v python3 || true; echo '--- NetworkManager logs ---'; journalctl -u NetworkManager --no-pager -n 25"
			var pid int
			if pid, err = vm.AgentExec(context.Background(), guestAgentCommand("/bin/sh", "-c", command), ""); err != nil {
				t.Fatalf("run guest-network diagnostics for attachment %d: %v", resource.ID, err)
			}
			var diagnostic guestAgentExecStatus
			if diagnostic, err = waitForGuestExecExit(context.Background(), config.Cfg.Proxmox, vm.Node, vmid, pid, 70); err != nil {
				t.Fatalf("read guest-network diagnostics for attachment %d: %v", resource.ID, err)
			}
			t.Logf("guest-network diagnostics attachment %d (exit %d):\n%s%s", resource.ID, diagnostic.ExitCode, diagnostic.OutData, diagnostic.ErrData)
		}
		if os.Getenv("ORGANESSON_DHCP_FIREWALL_DIAGNOSTIC") == "1" && diagnosticAttachmentID == strconv.Itoa(resource.ID) {
			var output string
			if output, err = diagnoseDHCPWithoutNICFirewall(context.Background(), client, vm, attachment, config.Cfg.Proxmox); err != nil {
				t.Fatalf("diagnose DHCP on attachment %d with its NIC firewall temporarily disabled: %v", resource.ID, err)
			}
			t.Logf("temporary DHCP firewall diagnostic attachment %d:\n%s", resource.ID, output)
		}
		if os.Getenv("ORGANESSON_ROUTER_REACHABILITY_DIAGNOSTIC") == "1" && diagnosticAttachmentID == strconv.Itoa(resource.ID) {
			var output string
			if output, err = diagnoseRouterTCPConnectivity(context.Background(), vm, attachment, config.Cfg.Proxmox, os.Getenv("ORGANESSON_LIVE_ROUTER_ADDRESS")); err != nil {
				t.Fatalf("diagnose router reachability from attachment %d: %v", resource.ID, err)
			}
			t.Logf("router reachability diagnostic attachment %d:\n%s", resource.ID, output)
		}
		if os.Getenv("ORGANESSON_SOURCE_FILTER_DIAGNOSTIC") == "1" && diagnosticAttachmentID == strconv.Itoa(resource.ID) {
			var routerVMID int
			if routerVMID, err = strconv.Atoi(os.Getenv("ORGANESSON_LIVE_ROUTER_VMID")); err != nil {
				t.Fatalf("parse diagnostic router VMID: %v", err)
			}
			var output string
			if output, err = diagnoseAllocatedAndSpoofedSources(context.Background(), client, vm, routerVMID, attachment, config.Cfg.Proxmox); err != nil {
				t.Fatalf("diagnose allocated and spoofed sources for attachment %d: %v", resource.ID, err)
			}
			t.Logf("allocated and spoofed source diagnostic attachment %d:\n%s", resource.ID, output)
		}
		if os.Getenv("ORGANESSON_ENVIRONMENT_SOURCE_FILTER_DIAGNOSTIC") == "1" && diagnosticAttachmentID == strconv.Itoa(resource.ID) {
			var output string
			if output, err = diagnoseEnvironmentAllocatedAndSpoofedSource(context.Background(), vm, attachment, config.Cfg.Proxmox); err != nil {
				t.Fatalf("diagnose environment-network allocated and spoofed source for attachment %d: %v", resource.ID, err)
			}
			t.Logf("environment-network source diagnostic attachment %d:\n%s", resource.ID, output)
		}
		if os.Getenv("ORGANESSON_DHCP_DUALSTACK_CROSSNODE_DIAGNOSTIC") == "1" && diagnosticAttachmentID == strconv.Itoa(resource.ID) {
			var routerVMID int
			if routerVMID, err = strconv.Atoi(os.Getenv("ORGANESSON_LIVE_ROUTER_VMID")); err != nil {
				t.Fatalf("parse diagnostic router VMID: %v", err)
			}
			var output string
			if output, err = diagnoseDualStackDHCPCrossNode(context.Background(), client, vm, routerVMID, attachment, config.Cfg.Proxmox); err != nil {
				t.Fatalf("diagnose cross-node dual-stack DHCP for attachment %d: %v", resource.ID, err)
			}
			t.Logf("cross-node dual-stack DHCP diagnostic attachment %d:\n%s", resource.ID, output)
		}
		if (os.Getenv("ORGANESSON_DHCP_COLOCATE_ROUTER_DIAGNOSTIC") == "1" || os.Getenv("ORGANESSON_DHCP_CROSSNODE_ROUTER_DIAGNOSTIC") == "1") && diagnosticAttachmentID == strconv.Itoa(resource.ID) {
			var routerVMID int
			if routerVMID, err = strconv.Atoi(os.Getenv("ORGANESSON_LIVE_ROUTER_VMID")); err != nil {
				t.Fatalf("parse diagnostic router VMID: %v", err)
			}
			var colocateRouter bool = os.Getenv("ORGANESSON_DHCP_COLOCATE_ROUTER_DIAGNOSTIC") == "1"
			var output string
			if output, err = diagnoseDHCPWithRouterPlacement(context.Background(), client, vm, routerVMID, attachment, config.Cfg.Proxmox, colocateRouter); err != nil {
				t.Fatalf("diagnose DHCP for attachment %d: %v", resource.ID, err)
			}
			t.Logf("DHCP diagnostic attachment %d (co-located=%t):\n%s", resource.ID, colocateRouter, output)
		}
		if os.Getenv("ORGANESSON_DHCPV6_COLOCATE_ROUTER_DIAGNOSTIC") == "1" && diagnosticAttachmentID == strconv.Itoa(resource.ID) {
			var routerVMID int
			if routerVMID, err = strconv.Atoi(os.Getenv("ORGANESSON_LIVE_ROUTER_VMID")); err != nil {
				t.Fatalf("parse diagnostic router VMID: %v", err)
			}
			var routerGuest *GuestNetworkRequest
			for _, routerAttachmentResource := range managedResources {
				if routerAttachmentResource == nil || routerAttachmentResource.Kind != "network_attachment" {
					continue
				}
				var routerAttachment liveRouterAttachmentConfiguration
				if err = json.Unmarshal([]byte(routerAttachmentResource.ConfigurationJSON), &routerAttachment); err != nil {
					t.Fatalf("decode router network attachment %d: %v", routerAttachmentResource.ID, err)
				}
				if routerAttachment.Request.VMID == strconv.Itoa(routerVMID) && routerAttachment.Request.Bridge == attachment.Request.Bridge && routerAttachment.Guest != nil {
					routerGuest = routerAttachment.Guest
					break
				}
			}
			if routerGuest == nil {
				t.Fatalf("managed router VM %d has no saved guest network configuration on bridge %q", routerVMID, attachment.Request.Bridge)
			}
			var output string
			if output, err = diagnoseDHCPv6WithCoLocatedRouter(context.Background(), client, vm, routerVMID, routerGuest, attachment, config.Cfg.Proxmox); err != nil {
				t.Fatalf("diagnose DHCPv6 with router colocated for attachment %d: %v", resource.ID, err)
			}
			t.Logf("co-located-router DHCPv6 diagnostic attachment %d:\n%s", resource.ID, output)
		}
		var expected []string
		for _, value := range attachment.Request.AllowedAddresses {
			var address netip.Addr
			if address, err = netip.ParseAddr(value); err != nil {
				t.Fatalf("parse allocated source address %q: %v", value, err)
			}
			var prefixBits int = 128
			if address.Is4() {
				prefixBits = 32
			}
			expected = append(expected, netip.PrefixFrom(address, prefixBits).String())
		}
		t.Logf("verified VM %d %s source filter permits only %v", vmid, attachment.Placement.Device, expected)
		verifiedAttachments++
	}
	if verifiedAttachments == 0 {
		t.Fatal("no live filtered attachments were available for source-address verification")
	}
	t.Logf("verified exact source filters on %d managed attachments", verifiedAttachments)
}

// TestVXLANPeerFirewallRulesAreRestricted verifies the exact node-to-node UDP allowance.
func TestVXLANPeerFirewallRulesAreRestricted(t *testing.T) {
	var rules []*pve.FirewallRule
	var err error
	if rules, err = vxlanPeerFirewallRules("10.0.0.4", "10.0.0.5"); err != nil {
		t.Fatal(err)
	}
	if len(rules) != 2 {
		t.Fatalf("expected inbound and outbound peer rules, got %d", len(rules))
	}
	if rules[0].Type != "in" || rules[0].Source != "10.0.0.5" || rules[0].Dest != "10.0.0.4" {
		t.Fatalf("inbound VXLAN rule is not scoped to the peer pair: %+v", rules[0])
	}
	if rules[1].Type != "out" || rules[1].Source != "10.0.0.4" || rules[1].Dest != "10.0.0.5" {
		t.Fatalf("outbound VXLAN rule is not scoped to the peer pair: %+v", rules[1])
	}
	for _, rule := range rules {
		if rule.Action != "ACCEPT" || rule.Enable != 1 || rule.Proto != "udp" || rule.Dport != "4789" || rule.Sport != "" {
			t.Fatalf("VXLAN rule grants more than UDP destination port 4789: %+v", rule)
		}
	}
}

type (
	liveVXLANNodeRules struct {
		Node    *pve.Node
		Rules   []*pve.FirewallRule
		Desired []*pve.FirewallRule
	}
)

// vxlanPeerFirewallRules builds bidirectional, peer-specific host rules for the default VXLAN port.
func vxlanPeerFirewallRules(localAddress string, peerAddress string) (rules []*pve.FirewallRule, err error) {
	var local netip.Addr
	var peer netip.Addr
	if local, err = netip.ParseAddr(localAddress); err != nil {
		return
	}
	if peer, err = netip.ParseAddr(peerAddress); err != nil {
		return
	}
	if !local.Is4() || !peer.Is4() || local == peer {
		err = errors.New("VXLAN peer firewall rules require two distinct IPv4 peer addresses")
		return
	}
	var localText string = local.String()
	var peerText string = peer.String()
	rules = []*pve.FirewallRule{
		{Type: "in", Action: "ACCEPT", Enable: 1, Proto: "udp", Dport: "4789", Source: peerText, Dest: localText, Comment: "Organesson VXLAN UDP 4789 from " + peerText},
		{Type: "out", Action: "ACCEPT", Enable: 1, Proto: "udp", Dport: "4789", Source: localText, Dest: peerText, Comment: "Organesson VXLAN UDP 4789 to " + peerText},
	}
	return
}

// ensureLiveVXLANPeerFirewallRules adds only exact peer-pair UDP 4789 rules after a fail-closed preflight.
func ensureLiveVXLANPeerFirewallRules(ctx context.Context, client *pve.Client, cluster *pve.Cluster) (err error) {
	if cluster.Quorate != 1 {
		err = fmt.Errorf("cluster is not quorate; refusing firewall changes")
		return
	}
	var zone *pve.SDNZone
	if zone, err = cluster.SDNZone(ctx, "ogvxlan"); err != nil {
		return
	}
	if zone.Type != "vxlan" || len(zone.Nodes) != 2 || len(zone.Peers) != 2 {
		err = fmt.Errorf("ogvxlan must have exactly two configured nodes and peers; refusing firewall changes")
		return
	}
	var clusterRules []*pve.FirewallRule
	if clusterRules, err = cluster.FirewallRules(ctx); err != nil {
		return
	}
	if len(clusterRules) != 0 {
		err = fmt.Errorf("cluster firewall rules exist; refusing to guess their precedence")
		return
	}
	var zoneNodes map[string]bool = make(map[string]bool, len(zone.Nodes))
	for _, nodeName := range zone.Nodes {
		zoneNodes[nodeName] = true
	}
	var nodeNamesByAddress map[string]string = make(map[string]string, len(zone.Peers))
	for _, peerAddress := range zone.Peers {
		var address netip.Addr
		if address, err = netip.ParseAddr(peerAddress); err != nil {
			return
		}
		if !address.Is4() {
			err = fmt.Errorf("VXLAN peer %q is not IPv4; refusing firewall changes", peerAddress)
			return
		}
		for _, status := range cluster.Nodes {
			if status != nil && status.IP == address.String() && status.Online == 1 && zoneNodes[status.Name] {
				nodeNamesByAddress[address.String()] = status.Name
			}
		}
	}
	if len(nodeNamesByAddress) != 2 {
		err = fmt.Errorf("could not map both VXLAN peers to online cluster nodes; refusing firewall changes")
		return
	}
	var nodeRules []liveVXLANNodeRules
	for localAddress, nodeName := range nodeNamesByAddress {
		var peerAddress string
		for candidateAddress := range nodeNamesByAddress {
			if candidateAddress != localAddress {
				peerAddress = candidateAddress
				break
			}
		}
		var node *pve.Node
		if node, err = client.Node(ctx, nodeName); err != nil {
			return
		}
		var options *pve.FirewallNodeOption
		if options, err = node.FirewallOptionGet(ctx); err != nil {
			return
		}
		if options == nil || options.Enable != nil && !bool(*options.Enable) {
			err = fmt.Errorf("node %q firewall is disabled; refusing to change firewall policy", nodeName)
			return
		}
		var rules []*pve.FirewallRule
		if rules, err = node.FirewallRules(ctx); err != nil {
			return
		}
		var desired []*pve.FirewallRule
		if desired, err = vxlanPeerFirewallRules(localAddress, peerAddress); err != nil {
			return
		}
		if err = validateLiveVXLANNodeRules(rules, desired); err != nil {
			return
		}
		nodeRules = append(nodeRules, liveVXLANNodeRules{Node: node, Rules: rules, Desired: desired})
	}
	for _, nodeRuleSet := range nodeRules {
		for _, desired := range nodeRuleSet.Desired {
			var exists bool
			for _, existing := range nodeRuleSet.Rules {
				if existing != nil && existing.Comment == desired.Comment {
					exists = true
					break
				}
			}
			if exists {
				continue
			}
			if err = nodeRuleSet.Node.NewFirewallRule(ctx, desired); err != nil {
				return
			}
		}
	}
	for _, nodeRuleSet := range nodeRules {
		var rules []*pve.FirewallRule
		if rules, err = nodeRuleSet.Node.FirewallRules(ctx); err != nil {
			return
		}
		if err = validateLiveVXLANNodeRules(rules, nodeRuleSet.Desired); err != nil {
			return
		}
	}
	return
}

// validateLiveVXLANNodeRules refuses unrelated host rules and verifies each owned rule exactly.
func validateLiveVXLANNodeRules(existingRules []*pve.FirewallRule, desiredRules []*pve.FirewallRule) (err error) {
	var desiredByComment map[string]*pve.FirewallRule = make(map[string]*pve.FirewallRule, len(desiredRules))
	for _, desired := range desiredRules {
		desiredByComment[desired.Comment] = desired
	}
	for _, existing := range existingRules {
		if existing == nil {
			continue
		}
		desired, exists := desiredByComment[existing.Comment]
		if !exists {
			err = fmt.Errorf("node has unrelated host firewall rule %q; refusing to alter rule precedence", existing.Comment)
			return
		}
		if !vxlanFirewallRulesEqual(existing, desired) {
			err = fmt.Errorf("Organesson VXLAN rule %q differs from its exact desired configuration", existing.Comment)
			return
		}
	}
	return
}

// vxlanFirewallRulesEqual compares only stable, configured VXLAN rule fields.
func vxlanFirewallRulesEqual(actual *pve.FirewallRule, expected *pve.FirewallRule) (equal bool) {
	equal = actual.Type == expected.Type && actual.Action == expected.Action && actual.Enable == expected.Enable &&
		actual.Proto == expected.Proto && actual.Dport == expected.Dport && actual.Sport == expected.Sport &&
		actual.Source == expected.Source && actual.Dest == expected.Dest && actual.Comment == expected.Comment
	return
}

// diagnoseAllocatedAndSpoofedSources checks IPv4/IPv6 connectivity and source filtering at the router's live node.
func diagnoseAllocatedAndSpoofedSources(ctx context.Context, client *pve.Client, guestVM *pve.VirtualMachine, routerVMID int, attachment struct {
	Request           NetworkAttachmentRequest   `json:"request"`
	Placement         NetworkAttachmentPlacement `json:"placement"`
	Guest             *GuestNetworkRequest       `json:"guest_network"`
	AddressPrefix     string                     `json:"address_prefix,omitempty"`
	IPv6AddressPrefix string                     `json:"ipv6_address_prefix,omitempty"`
}, settings config.ProxmoxConfiguration) (output string, err error) {
	var allocated4, allocated6 string
	for _, rawAddress := range attachment.Request.AllowedAddresses {
		var address netip.Addr
		if address, err = netip.ParseAddr(rawAddress); err != nil {
			return
		}
		if address.Is4() {
			allocated4 = address.String()
		} else {
			allocated6 = address.String()
		}
	}
	if allocated4 == "" || allocated6 == "" {
		err = errors.New("diagnostic requires allocated IPv4 and IPv6 addresses")
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
	var routerNodeName string
	for _, resource := range resources {
		if resource != nil && resource.Type == "qemu" && resource.Pool == "organesson" && resource.ID == "qemu/"+strconv.Itoa(routerVMID) {
			routerNodeName = resource.Node
			break
		}
	}
	if routerNodeName == "" {
		err = errors.New("diagnostic router is not in the organesson Proxmox pool")
		return
	}
	var routerNode *pve.Node
	var routerVM *pve.VirtualMachine
	if routerNode, err = client.Node(ctx, routerNodeName); err != nil {
		return
	}
	if routerVM, err = routerNode.VirtualMachine(ctx, routerVMID); err != nil {
		return
	}
	if routerVM.Node == guestVM.Node {
		err = errors.New("diagnostic guest and router must be on different Proxmox nodes")
		return
	}
	var interfaceName string = interfaceNameForMAC(attachment.Placement.MAC)
	var prefix4 netip.Prefix
	var prefix6 netip.Prefix
	if prefix4, err = netip.ParsePrefix(attachment.AddressPrefix); err != nil || !prefix4.Addr().Is4() {
		err = errors.New("source-filter diagnostic requires an IPv4 allocation prefix")
		return
	}
	if prefix6, err = netip.ParsePrefix(attachment.IPv6AddressPrefix); err != nil || !prefix6.Addr().Is6() {
		err = errors.New("source-filter diagnostic requires an IPv6 allocation prefix")
		return
	}
	var address4 netip.Addr
	var address6 netip.Addr
	if address4, err = netip.ParseAddr(allocated4); err != nil || !prefix4.Contains(address4) {
		err = errors.New("allocated IPv4 address is outside its saved prefix")
		return
	}
	if address6, err = netip.ParseAddr(allocated6); err != nil || !prefix6.Contains(address6) {
		err = errors.New("allocated IPv6 address is outside its saved prefix")
		return
	}
	var routerAddressPID int
	if routerAddressPID, err = routerVM.AgentExec(ctx, guestAgentCommand("/bin/sh", "-c", "ip -6 -o address show dev ens18 scope link | awk '{print $4}' | cut -d/ -f1"), ""); err != nil {
		return
	}
	var routerAddressStatus guestAgentExecStatus
	if routerAddressStatus, err = waitForGuestExecExit(ctx, settings, routerVM.Node, routerVMID, routerAddressPID, 10); err != nil {
		return
	}
	var routerAddress6 string = strings.TrimSpace(routerAddressStatus.OutData)
	if routerAddress6 == "" || !strings.HasPrefix(routerAddress6, "fe80:") {
		err = fmt.Errorf("router VM %d has no LAN link-local IPv6 address", routerVMID)
		return
	}
	var source string = "import socket,time,sys\niface=sys.argv[1]; router6=sys.argv[2]\nfor family, source, destination in [(socket.AF_INET, '" + allocated4 + "', '192.168.2.1'), (socket.AF_INET6, '" + allocated6 + "', router6)]:\n for attempt in range(4):\n  try:\n   s=socket.socket(family, socket.SOCK_STREAM); s.setsockopt(socket.SOL_SOCKET, socket.SO_BINDTODEVICE, iface.encode()); s.settimeout(3); s.bind((source, 0)); target=(destination,53) if family==socket.AF_INET else (destination,53,0,socket.if_nametoindex(iface)); s.connect(target); s.close(); print(f'allocated {source} via {iface} reached {destination}:53'); break\n  except OSError:\n   if attempt == 3: raise\n   time.sleep(1)"
	var spoofSource string = "import socket,sys\nf=sys.argv[1]; src=sys.argv[2]; dst=sys.argv[3]; iface=sys.argv[4]; family=socket.AF_INET6 if f=='6' else socket.AF_INET\ns=socket.socket(family,socket.SOCK_STREAM); s.setsockopt(socket.SOL_SOCKET, socket.SO_BINDTODEVICE, iface.encode()); s.settimeout(3); target=(dst,53) if family==socket.AF_INET else (dst,53,0,socket.if_nametoindex(iface))\ntry:\n s.bind((src,0)); s.connect(target); print('UNEXPECTED: spoof source connected'); sys.exit(1)\nexcept OSError as e:\n print(f'spoof source blocked on {iface}: {e}')"
	var command string = "set -eu; iface=" + interfaceName + "; allocated4=" + shellQuote(allocated4) + "; allocated6=" + shellQuote(allocated6) + "; added4=0; added6=0; cleanup(){ ip -4 address del 192.168.2.99/24 dev \"$iface\" 2>/dev/null || true; ip -6 address del fd42:2::dead/64 dev \"$iface\" 2>/dev/null || true; if [ \"$added4\" = 1 ]; then ip -4 address del \"$allocated4/" + strconv.Itoa(prefix4.Bits()) + "\" dev \"$iface\" 2>/dev/null || true; fi; if [ \"$added6\" = 1 ]; then ip -6 address del \"$allocated6/128\" dev \"$iface\" 2>/dev/null || true; fi; }; trap cleanup EXIT; if ! ip -4 -o address show dev \"$iface\" | awk '{print $4}' | cut -d/ -f1 | grep -Fxq \"$allocated4\"; then ip -4 address add \"$allocated4/" + strconv.Itoa(prefix4.Bits()) + "\" dev \"$iface\"; added4=1; fi; if ! ip -6 -o address show dev \"$iface\" scope global | awk '{print $4}' | cut -d/ -f1 | grep -Fxq \"$allocated6\"; then ip -6 address add \"$allocated6/128\" dev \"$iface\" nodad; added6=1; fi; python3 -c " + shellQuote(source) + " \"$iface\" " + shellQuote(routerAddress6) + "; ip -4 address add 192.168.2.99/24 dev \"$iface\"; if python3 -c " + shellQuote(spoofSource) + " 4 192.168.2.99 192.168.2.1 \"$iface\"; then :; else exit 1; fi; ip -6 address add fd42:2::dead/64 dev \"$iface\" nodad; if python3 -c " + shellQuote(spoofSource) + " 6 fd42:2::dead " + shellQuote(routerAddress6) + " \"$iface\"; then :; else exit 1; fi"
	var vmid int
	if vmid, err = strconv.Atoi(attachment.Request.VMID); err != nil {
		return
	}
	var routerCapturePID int
	if routerCapturePID, err = routerVM.AgentExec(ctx, guestAgentCommand("/bin/sh", "-c", "exec /usr/bin/timeout 25 /usr/bin/tcpdump -ni ens18 -vv -l 'tcp port 53'"), ""); err != nil {
		return
	}
	var guestCapturePID int
	if guestCapturePID, err = guestVM.AgentExec(ctx, guestAgentCommand("/bin/sh", "-c", "exec /usr/bin/timeout 25 /usr/bin/tcpdump -ni "+interfaceName+" -vv -l 'tcp port 53'"), ""); err != nil {
		return
	}
	var pid int
	if pid, err = guestVM.AgentExec(ctx, guestAgentCommand("/bin/sh", "-c", command), ""); err != nil {
		return
	}
	var status guestAgentExecStatus
	if status, err = waitForGuestExecExit(ctx, settings, guestVM.Node, vmid, pid, 40); err != nil {
		return
	}
	output = status.OutData + status.ErrData
	var routerCapture guestAgentExecStatus
	if routerCapture, err = waitForGuestExecExit(ctx, settings, routerVM.Node, routerVMID, routerCapturePID, 40); err != nil {
		return
	}
	var guestCapture guestAgentExecStatus
	if guestCapture, err = waitForGuestExecExit(ctx, settings, guestVM.Node, vmid, guestCapturePID, 40); err != nil {
		return
	}
	output += "\n--- guest TCP/53 packets ---\n" + guestCapture.OutData + guestCapture.ErrData
	output += "\n--- router TCP/53 packets ---\n" + routerCapture.OutData + routerCapture.ErrData
	if status.ExitCode != 0 {
		err = fmt.Errorf("allocated/spoof source diagnostic exited %d: %s", status.ExitCode, output)
	}
	return
}

// diagnoseDHCPWithoutNICFirewall briefly toggles one test NIC to isolate firewall impact and always restores it.
func diagnoseDHCPWithoutNICFirewall(ctx context.Context, client *pve.Client, vm *pve.VirtualMachine, attachment struct {
	Request           NetworkAttachmentRequest   `json:"request"`
	Placement         NetworkAttachmentPlacement `json:"placement"`
	Guest             *GuestNetworkRequest       `json:"guest_network"`
	AddressPrefix     string                     `json:"address_prefix,omitempty"`
	IPv6AddressPrefix string                     `json:"ipv6_address_prefix,omitempty"`
}, settings config.ProxmoxConfiguration) (output string, err error) {
	var original string = vm.VirtualMachineConfig.Nets[attachment.Placement.Device]
	var disabled string = strings.Replace(original, ",firewall=1", ",firewall=0", 1)
	if disabled == original {
		err = errors.New("diagnostic NIC did not have firewall=1")
		return
	}
	var task *pve.Task
	if task, err = vm.Config(ctx, pve.VirtualMachineOption{Name: attachment.Placement.Device, Value: disabled}); err != nil {
		return
	}
	if err = waitTask(ctx, client, task); err != nil {
		return
	}
	defer func() {
		var restoreTask *pve.Task
		var restoreErr error
		if restoreTask, restoreErr = vm.Config(ctx, pve.VirtualMachineOption{Name: attachment.Placement.Device, Value: original}); restoreErr == nil {
			restoreErr = waitTask(ctx, client, restoreTask)
		}
		if err == nil && restoreErr != nil {
			err = fmt.Errorf("restore NIC firewall: %w", restoreErr)
		}
	}()
	var connection string = "organesson-" + strings.ReplaceAll(strings.ToLower(attachment.Placement.MAC), ":", "")
	var command string = "nmcli connection up " + shellQuote(connection) + "; sleep 10; ip -4 -o address show"
	var vmid int
	if vmid, err = strconv.Atoi(attachment.Request.VMID); err != nil {
		return
	}
	var pid int
	if pid, err = vm.AgentExec(ctx, guestAgentCommand("/bin/sh", "-c", command), ""); err != nil {
		return
	}
	var status guestAgentExecStatus
	if status, err = waitForGuestExecExit(ctx, settings, vm.Node, vmid, pid, 40); err != nil {
		return
	}
	output = status.OutData + status.ErrData
	if status.ExitCode != 0 {
		err = fmt.Errorf("DHCP retry exited %d", status.ExitCode)
	}
	return
}

// diagnoseRouterTCPConnectivity checks cross-node IPv4 reachability using an allocated source address.
func diagnoseRouterTCPConnectivity(ctx context.Context, vm *pve.VirtualMachine, attachment struct {
	Request           NetworkAttachmentRequest   `json:"request"`
	Placement         NetworkAttachmentPlacement `json:"placement"`
	Guest             *GuestNetworkRequest       `json:"guest_network"`
	AddressPrefix     string                     `json:"address_prefix,omitempty"`
	IPv6AddressPrefix string                     `json:"ipv6_address_prefix,omitempty"`
}, settings config.ProxmoxConfiguration, routerAddress string) (output string, err error) {
	var sourceAddress string
	for _, address := range attachment.Request.AllowedAddresses {
		var parsed netip.Addr
		if parsed, err = netip.ParseAddr(address); err != nil {
			return
		}
		if parsed.Is4() {
			sourceAddress = parsed.String()
			break
		}
	}
	if sourceAddress == "" || routerAddress == "" {
		err = errors.New("diagnostic requires allocated IPv4 and a router IPv4 address")
		return
	}
	var source string = "import socket; s=socket.socket(); s.settimeout(4); s.bind((" + shellQuote(sourceAddress) + ", 0)); s.connect((" + shellQuote(routerAddress) + ", 53)); print('TCP/53 reachable from allocated source')"
	var command string = "python3 -c " + shellQuote(source)
	var vmid int
	if vmid, err = strconv.Atoi(attachment.Request.VMID); err != nil {
		return
	}
	var pid int
	if pid, err = vm.AgentExec(ctx, guestAgentCommand("/bin/sh", "-c", command), ""); err != nil {
		return
	}
	var status guestAgentExecStatus
	if status, err = waitForGuestExecExit(ctx, settings, vm.Node, vmid, pid, 15); err != nil {
		return
	}
	output = status.OutData + status.ErrData
	if status.ExitCode != 0 {
		err = fmt.Errorf("allocated-source connectivity probe exited %d: %s", status.ExitCode, output)
	}
	return
}

// diagnoseEnvironmentAllocatedAndSpoofedSource probes an allocated environment source and one non-allocated source.
func diagnoseEnvironmentAllocatedAndSpoofedSource(ctx context.Context, vm *pve.VirtualMachine, attachment struct {
	Request           NetworkAttachmentRequest   `json:"request"`
	Placement         NetworkAttachmentPlacement `json:"placement"`
	Guest             *GuestNetworkRequest       `json:"guest_network"`
	AddressPrefix     string                     `json:"address_prefix,omitempty"`
	IPv6AddressPrefix string                     `json:"ipv6_address_prefix,omitempty"`
}, settings config.ProxmoxConfiguration) (output string, err error) {
	if attachment.Guest == nil || len(attachment.Guest.DNS) == 0 {
		err = errors.New("environment source diagnostic requires a saved guest DNS target")
		return
	}
	var allocatedAddress string
	for _, value := range attachment.Request.AllowedAddresses {
		var address netip.Addr
		if address, err = netip.ParseAddr(value); err != nil {
			return
		}
		if address.Is4() {
			allocatedAddress = address.String()
			break
		}
	}
	var dnsAddress netip.Addr
	if dnsAddress, err = netip.ParseAddr(attachment.Guest.DNS[0]); err != nil || !dnsAddress.Is4() || allocatedAddress == "" {
		err = errors.New("environment source diagnostic requires allocated IPv4 and IPv4 DNS")
		return
	}
	var interfaceName string = interfaceNameForMAC(attachment.Placement.MAC)
	var source string = "import socket,sys\niface,allocated,dns,spoof=sys.argv[1:]\ndef connect(address,freebind=False):\n s=socket.socket(socket.AF_INET,socket.SOCK_STREAM); s.setsockopt(socket.SOL_SOCKET,socket.SO_BINDTODEVICE,iface.encode()); s.settimeout(4)\n if freebind: s.setsockopt(socket.SOL_IP,getattr(socket,'IP_FREEBIND',15),1)\n s.bind((address,0)); s.connect((dns,53)); s.close()\nconnect(allocated); print(f'allocated source {allocated} reached {dns}:53')\ntry:\n connect(spoof,True); print('UNEXPECTED: spoof source connected'); sys.exit(1)\nexcept OSError as error:\n print(f'spoof source {spoof} blocked: {error}')"
	var command string = "set -eu; iface=" + interfaceName + "; python3 -c " + shellQuote(source) + " \"$iface\" " + shellQuote(allocatedAddress) + " " + shellQuote(dnsAddress.String()) + " 10.192.255.254"
	var vmid int
	if vmid, err = strconv.Atoi(attachment.Request.VMID); err != nil {
		return
	}
	var pid int
	if pid, err = vm.AgentExec(ctx, guestAgentCommand("/bin/sh", "-c", command), ""); err != nil {
		return
	}
	var status guestAgentExecStatus
	if status, err = waitForGuestExecExit(ctx, settings, vm.Node, vmid, pid, 20); err != nil {
		return
	}
	output = status.OutData + status.ErrData
	if status.ExitCode != 0 {
		err = fmt.Errorf("environment source probe exited %d: %s", status.ExitCode, output)
	}
	return
}

// diagnoseDualStackDHCPCrossNode renews one allocated DHCPv4/DHCPv6 interface across nodes and captures both ends.
func diagnoseDualStackDHCPCrossNode(ctx context.Context, client *pve.Client, guestVM *pve.VirtualMachine, routerVMID int, attachment struct {
	Request           NetworkAttachmentRequest   `json:"request"`
	Placement         NetworkAttachmentPlacement `json:"placement"`
	Guest             *GuestNetworkRequest       `json:"guest_network"`
	AddressPrefix     string                     `json:"address_prefix,omitempty"`
	IPv6AddressPrefix string                     `json:"ipv6_address_prefix,omitempty"`
}, settings config.ProxmoxConfiguration) (output string, err error) {
	var routerVM *pve.VirtualMachine
	if _, routerVM, _, err = locateManagedVM(ctx, client, routerVMID); err != nil {
		return
	}
	if routerVM.Node == guestVM.Node {
		err = errors.New("cross-node DHCP diagnostic requires the router and client on different Proxmox nodes")
		return
	}
	if attachment.Guest == nil || attachment.Guest.Method != "dhcp" || attachment.Guest.IPv6Method != "dhcp" {
		err = errors.New("cross-node dual-stack DHCP diagnostic requires an applied DHCPv4/DHCPv6 guest profile")
		return
	}
	var ipv4Address string
	var ipv6Address string
	for _, value := range attachment.Request.AllowedAddresses {
		var address netip.Addr
		if address, err = netip.ParseAddr(value); err != nil {
			return
		}
		if address.Is4() {
			ipv4Address = address.String()
		} else {
			ipv6Address = address.String()
		}
	}
	if ipv4Address == "" || ipv6Address == "" {
		err = errors.New("cross-node dual-stack DHCP diagnostic requires allocated IPv4 and IPv6 addresses")
		return
	}
	var guestInterface string = interfaceNameForMAC(attachment.Placement.MAC)
	var connection string = "organesson-" + strings.ReplaceAll(strings.ToLower(attachment.Placement.MAC), ":", "")
	var routerCapturePID int
	if routerCapturePID, err = routerVM.AgentExec(ctx, guestAgentCommand("/bin/sh", "-c", "exec /usr/bin/timeout 25 /usr/bin/tcpdump -ni ens18 -vv -l 'udp port 67 or 68 or 546 or 547'"), ""); err != nil {
		return
	}
	var guestCapturePID int
	if guestCapturePID, err = guestVM.AgentExec(ctx, guestAgentCommand("/bin/sh", "-c", "iface="+guestInterface+"; exec /usr/bin/timeout 25 /usr/bin/tcpdump -ni \"$iface\" -vv -l 'udp port 67 or 68 or 546 or 547'"), ""); err != nil {
		return
	}
	var command string = "set -eu; iface=" + guestInterface + "; nmcli connection down " + shellQuote(connection) + " || true; sleep 2; nmcli connection up " + shellQuote(connection) + "; sleep 5; echo '--- applied client addresses ---'; ip -4 -o address show dev \"$iface\"; ip -6 -o address show dev \"$iface\" scope global"
	var guestVMID int
	if guestVMID, err = strconv.Atoi(attachment.Request.VMID); err != nil {
		return
	}
	var pid int
	if pid, err = guestVM.AgentExec(ctx, guestAgentCommand("/bin/sh", "-c", command), ""); err != nil {
		return
	}
	var guestStatus guestAgentExecStatus
	if guestStatus, err = waitForGuestExecExit(ctx, settings, guestVM.Node, guestVMID, pid, 35); err != nil {
		return
	}
	if guestStatus.ExitCode != 0 {
		err = fmt.Errorf("guest DHCP renewal exited %d: %s", guestStatus.ExitCode, strings.TrimSpace(guestStatus.ErrData+" "+guestStatus.OutData))
		return
	}
	var routerCapture guestAgentExecStatus
	if routerCapture, err = waitForGuestExecExit(ctx, settings, routerVM.Node, routerVMID, routerCapturePID, 35); err != nil {
		return
	}
	var guestCapture guestAgentExecStatus
	if guestCapture, err = waitForGuestExecExit(ctx, settings, guestVM.Node, guestVMID, guestCapturePID, 35); err != nil {
		return
	}
	output = fmt.Sprintf("guest VM %d on %s ↔ router VM %d on %s\n%s\n--- client DHCP packets ---\n%s%s\n--- router DHCP packets ---\n%s%s", guestVMID, guestVM.Node, routerVMID, routerVM.Node, guestStatus.OutData, guestCapture.OutData, guestCapture.ErrData, routerCapture.OutData, routerCapture.ErrData)
	if !strings.Contains(guestStatus.OutData, ipv4Address) || !strings.Contains(guestStatus.OutData, ipv6Address) {
		err = fmt.Errorf("renewed guest does not show both allocated DHCP addresses %s and %s: %s", ipv4Address, ipv6Address, guestStatus.OutData)
		return
	}
	if !strings.Contains(routerCapture.OutData, "BOOTP/DHCP, Reply") || !strings.Contains(routerCapture.OutData, ipv4Address) || !strings.Contains(routerCapture.OutData, "dhcp6 reply") || !strings.Contains(routerCapture.OutData, ipv6Address) {
		err = fmt.Errorf("router capture did not confirm both allocated DHCP reservations: %s", output)
	}
	return
}

// diagnoseDHCPWithRouterPlacement captures client and router packets, optionally colocating the router temporarily.
func diagnoseDHCPWithRouterPlacement(ctx context.Context, client *pve.Client, guestVM *pve.VirtualMachine, routerVMID int, attachment struct {
	Request           NetworkAttachmentRequest   `json:"request"`
	Placement         NetworkAttachmentPlacement `json:"placement"`
	Guest             *GuestNetworkRequest       `json:"guest_network"`
	AddressPrefix     string                     `json:"address_prefix,omitempty"`
	IPv6AddressPrefix string                     `json:"ipv6_address_prefix,omitempty"`
}, settings config.ProxmoxConfiguration, colocateRouter bool) (output string, err error) {
	var cluster *pve.Cluster
	if cluster, err = client.Cluster(ctx); err != nil {
		return
	}
	var resources pve.ClusterResources
	if resources, err = cluster.Resources(ctx, "vm"); err != nil {
		return
	}
	var originalNode string
	for _, resource := range resources {
		if resource != nil && resource.Type == "qemu" && resource.Pool == "organesson" && resource.ID == "qemu/"+strconv.Itoa(routerVMID) {
			originalNode = resource.Node
			break
		}
	}
	if originalNode == "" {
		err = errors.New("diagnostic router is not in the organesson Proxmox pool")
		return
	}
	var routerNode *pve.Node
	var routerVM *pve.VirtualMachine
	if routerNode, err = client.Node(ctx, originalNode); err != nil {
		return
	}
	if routerVM, err = routerNode.VirtualMachine(ctx, routerVMID); err != nil {
		return
	}
	var pathInfo string = fmt.Sprintf("guest VM %d on %s NICs=%v; router VM %d on %s NICs=%v\n", guestVM.VMID, guestVM.Node, guestVM.VirtualMachineConfig.Nets, routerVMID, routerVM.Node, routerVM.VirtualMachineConfig.Nets)
	if originalNode != guestVM.Node && colocateRouter {
		var task *pve.Task
		if task, err = routerVM.Migrate(ctx, &pve.VirtualMachineMigrateOptions{Target: guestVM.Node, Online: pve.IntOrBool(true)}); err != nil {
			return
		}
		if err = waitTask(ctx, client, task); err != nil {
			return
		}
		if routerNode, err = client.Node(ctx, guestVM.Node); err != nil {
			return
		}
		if routerVM, err = routerNode.VirtualMachine(ctx, routerVMID); err != nil {
			return
		}
		defer func() {
			var targetNode *pve.Node
			var currentRouter *pve.VirtualMachine
			var restoreTask *pve.Task
			var restoreErr error
			if targetNode, restoreErr = client.Node(ctx, guestVM.Node); restoreErr == nil {
				currentRouter, restoreErr = targetNode.VirtualMachine(ctx, routerVMID)
			}
			if restoreErr == nil {
				restoreTask, restoreErr = currentRouter.Migrate(ctx, &pve.VirtualMachineMigrateOptions{Target: originalNode, Online: pve.IntOrBool(true)})
			}
			if restoreErr == nil {
				restoreErr = waitTask(ctx, client, restoreTask)
			}
			if err == nil && restoreErr != nil {
				err = fmt.Errorf("restore router node %q: %w", originalNode, restoreErr)
			}
		}()
	}
	var guestInterface string = interfaceNameForMAC(attachment.Placement.MAC)
	var routerCapturePID int
	var guestCapturePID int
	if routerCapturePID, err = routerVM.AgentExec(ctx, guestAgentCommand("/bin/sh", "-c", "exec /usr/bin/timeout 25 /usr/bin/tcpdump -ni ens18 -vv -l 'udp port 67 or 68 or icmp or icmp6'"), ""); err != nil {
		return
	}
	if guestCapturePID, err = guestVM.AgentExec(ctx, guestAgentCommand("/bin/sh", "-c", "exec /usr/bin/timeout 25 /usr/bin/tcpdump -ni "+guestInterface+" -vv -l 'udp port 67 or 68 or icmp or icmp6'"), ""); err != nil {
		return
	}
	var connection string = "organesson-" + strings.ReplaceAll(strings.ToLower(attachment.Placement.MAC), ":", "")
	var command string = "set -u; iface=" + guestInterface + "; trap 'ip -4 address del 192.168.2.30/24 dev \"$iface\" 2>/dev/null || true' EXIT; timeout 20 nmcli connection up " + shellQuote(connection) + " || true; echo '--- guest IPv4 ---'; nmcli -g IP4.ADDRESS device show \"$iface\"; ip -4 address add 192.168.2.30/24 dev \"$iface\" 2>/dev/null || true; echo '--- same-VNet ICMP probe ---'; ping -4 -I \"$iface\" -c 2 -W 1 192.168.2.1 || true; echo '--- guest neighbor ---'; ip neigh show dev \"$iface\""
	var pid int
	var vmid int
	if vmid, err = strconv.Atoi(attachment.Request.VMID); err != nil {
		return
	}
	if pid, err = guestVM.AgentExec(ctx, guestAgentCommand("/bin/sh", "-c", command), ""); err != nil {
		return
	}
	var status guestAgentExecStatus
	if status, err = waitForGuestExecExit(ctx, settings, guestVM.Node, vmid, pid, 40); err != nil {
		return
	}
	output = pathInfo + status.OutData + status.ErrData
	if status.ExitCode != 0 {
		err = fmt.Errorf("co-located DHCP probe exited %d", status.ExitCode)
		return
	}
	var routerCapture guestAgentExecStatus
	if routerCapture, err = waitForGuestExecExit(ctx, settings, routerVM.Node, routerVMID, routerCapturePID, 40); err != nil {
		return
	}
	var guestCapture guestAgentExecStatus
	if guestCapture, err = waitForGuestExecExit(ctx, settings, guestVM.Node, vmid, guestCapturePID, 40); err != nil {
		return
	}
	output += "\n--- client DHCPv4 packets ---\n" + guestCapture.OutData + guestCapture.ErrData
	output += "\n--- router DHCPv4 packets ---\n" + routerCapture.OutData + routerCapture.ErrData
	return
}

// diagnoseDHCPv6WithCoLocatedRouter temporarily adds one exact DUID reservation and restores guest/router state.
func diagnoseDHCPv6WithCoLocatedRouter(ctx context.Context, client *pve.Client, guestVM *pve.VirtualMachine, routerVMID int, routerGuest *GuestNetworkRequest, attachment struct {
	Request           NetworkAttachmentRequest   `json:"request"`
	Placement         NetworkAttachmentPlacement `json:"placement"`
	Guest             *GuestNetworkRequest       `json:"guest_network"`
	AddressPrefix     string                     `json:"address_prefix,omitempty"`
	IPv6AddressPrefix string                     `json:"ipv6_address_prefix,omitempty"`
}, settings config.ProxmoxConfiguration) (output string, err error) {
	if len(attachment.Request.AllowedAddresses) < 2 {
		err = errors.New("diagnostic attachment lacks dual-stack allocations")
		return
	}
	var originalGuest GuestNetworkRequest
	if attachment.Guest != nil {
		originalGuest = *attachment.Guest
	} else if originalGuest, err = readLiveGuestNetworkConfiguration(ctx, guestVM, attachment, settings); err != nil {
		return
	}
	var ipv6Address string
	for _, value := range attachment.Request.AllowedAddresses {
		var address netip.Addr
		if address, err = netip.ParseAddr(value); err != nil {
			return
		}
		if address.Is6() {
			ipv6Address = address.String()
			break
		}
	}
	if ipv6Address == "" {
		err = errors.New("diagnostic attachment has no allocated IPv6 address")
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
	var originalNode string
	for _, resource := range resources {
		if resource != nil && resource.Type == "qemu" && resource.Pool == "organesson" && resource.ID == "qemu/"+strconv.Itoa(routerVMID) {
			originalNode = resource.Node
			break
		}
	}
	if originalNode == "" {
		err = errors.New("diagnostic router is not in the organesson Proxmox pool")
		return
	}
	if routerGuest == nil {
		err = errors.New("diagnostic managed router attachment has no saved guest configuration")
		return
	}
	var sourceNode *pve.Node
	var routerVM *pve.VirtualMachine
	if sourceNode, err = client.Node(ctx, originalNode); err != nil {
		return
	}
	if routerVM, err = sourceNode.VirtualMachine(ctx, routerVMID); err != nil {
		return
	}
	var originalConfig *pve.AgentFileRead
	if originalConfig, err = routerVM.AgentFileRead(ctx, "/etc/dnsmasq.d/organesson-router.conf"); err != nil || originalConfig == nil || bool(originalConfig.Truncated) {
		err = fmt.Errorf("read original router DHCP config: %w", err)
		return
	}
	var originalReservations *pve.AgentFileRead
	if originalReservations, err = routerVM.AgentFileRead(ctx, routerDHCPReservationsFile); err != nil {
		err = fmt.Errorf("read original router reservations: %w", err)
		return
	}
	var originalLeases *pve.AgentFileRead
	if originalLeases, err = routerVM.AgentFileRead(ctx, "/var/lib/misc/dnsmasq.leases"); err != nil {
		err = fmt.Errorf("read original router leases: %w", err)
		return
	}
	if originalConfig == nil || bool(originalConfig.Truncated) || originalReservations == nil || bool(originalReservations.Truncated) || originalLeases == nil || bool(originalLeases.Truncated) {
		err = errors.New("router DHCP configuration, reservation, or lease file is unavailable or truncated")
		return
	}
	var moved bool
	if originalNode != guestVM.Node {
		var task *pve.Task
		if task, err = routerVM.Migrate(ctx, &pve.VirtualMachineMigrateOptions{Target: guestVM.Node, Online: pve.IntOrBool(true)}); err != nil {
			return
		}
		if err = waitTask(ctx, client, task); err != nil {
			return
		}
		moved = true
	}
	defer func() {
		var service *Service = New(settings)
		var restoreErrors []error
		if guestErr := service.ConfigureGuestNetwork(ctx, originalGuest); guestErr != nil {
			restoreErrors = append(restoreErrors, fmt.Errorf("restore guest network configuration: %w", guestErr))
		}
		var activeRouter *pve.VirtualMachine
		var activeNode *pve.Node
		var restoreErr error
		if activeNode, restoreErr = client.Node(ctx, guestVM.Node); restoreErr == nil {
			activeRouter, restoreErr = activeNode.VirtualMachine(ctx, routerVMID)
		}
		if restoreErr == nil {
			_, restoreErr = runGuestAgentCommand(ctx, activeRouter, settings, routerVMID, "/usr/bin/systemctl", "stop", "dnsmasq.service")
		}
		if restoreErr == nil {
			restoreErr = activeRouter.AgentFileWrite(ctx, "/etc/dnsmasq.d/organesson-router.conf", []byte(originalConfig.Content))
		}
		if restoreErr == nil {
			restoreErr = activeRouter.AgentFileWrite(ctx, routerDHCPReservationsFile, []byte(originalReservations.Content))
		}
		if restoreErr == nil {
			restoreErr = activeRouter.AgentFileWrite(ctx, "/var/lib/misc/dnsmasq.leases", []byte(removeDHCPv6LeasesByDUID(originalLeases.Content, DHCPv6DUIDFromMAC(attachment.Placement.MAC))))
		}
		if restoreErr == nil {
			_, restoreErr = runGuestAgentCommand(ctx, activeRouter, settings, routerVMID, "/usr/bin/systemctl", "start", "dnsmasq.service")
		}
		if restoreErr != nil {
			restoreErrors = append(restoreErrors, fmt.Errorf("restore router configuration: %w", restoreErr))
		}
		if moved {
			var restoreNode *pve.Node
			var restoreTask *pve.Task
			var migrationErr error
			if restoreNode, migrationErr = client.Node(ctx, guestVM.Node); migrationErr == nil {
				activeRouter, migrationErr = restoreNode.VirtualMachine(ctx, routerVMID)
				if migrationErr == nil {
					restoreTask, migrationErr = activeRouter.Migrate(ctx, &pve.VirtualMachineMigrateOptions{Target: originalNode, Online: pve.IntOrBool(true)})
				}
				if migrationErr == nil {
					migrationErr = waitTask(ctx, client, restoreTask)
				}
			}
			if migrationErr != nil {
				restoreErrors = append(restoreErrors, fmt.Errorf("restore router node %q: %w", originalNode, migrationErr))
			}
		}
		if restoreErr = errors.Join(restoreErrors...); err == nil && restoreErr != nil {
			err = restoreErr
		} else if err != nil && restoreErr != nil {
			err = errors.Join(err, restoreErr)
		}
	}()
	if originalNode != guestVM.Node {
		var targetNode *pve.Node
		if targetNode, err = client.Node(ctx, guestVM.Node); err != nil {
			return
		}
		if routerVM, err = targetNode.VirtualMachine(ctx, routerVMID); err != nil {
			return
		}
	}
	var expectedRouterGuest GuestNetworkRequest = *routerGuest
	expectedRouterGuest.Node = routerVM.Node
	if err = New(settings).ConfigureGuestNetwork(ctx, expectedRouterGuest); err != nil {
		err = fmt.Errorf("apply filtered router LAN network configuration: %w", err)
		return
	}
	if os.Getenv("ORGANESSON_DHCPV6_ROUTER_RULE_DIAGNOSTIC") == "1" {
		var routerDevice string
		for device, network := range routerVM.VirtualMachineConfig.Nets {
			if networkOptionMap(network)["bridge"] == attachment.Request.Bridge {
				routerDevice = device
				break
			}
		}
		if routerDevice == "" {
			err = fmt.Errorf("diagnostic router VM %d has no NIC on bridge %q", routerVMID, attachment.Request.Bridge)
			return
		}
		var comment string = "Organesson temporary DHCPv6 ingress diagnostic"
		var rules []*pve.FirewallRule
		if rules, err = routerVM.FirewallRules(ctx); err != nil {
			return
		}
		for _, rule := range rules {
			if rule != nil && rule.Comment == comment {
				err = fmt.Errorf("temporary DHCPv6 ingress diagnostic rule already exists on VM %d", routerVMID)
				return
			}
		}
		var diagnosticRule *pve.FirewallRule = &pve.FirewallRule{
			Type: "in", Action: "ACCEPT", Enable: 1, Iface: routerDevice,
			Proto: "udp", Sport: "546", Dport: "547", Source: "fe80::/10", Comment: comment,
		}
		if err = routerVM.NewFirewallRule(ctx, diagnosticRule); err != nil {
			return
		}
		defer func() {
			var activeRules []*pve.FirewallRule
			var cleanupErr error
			if activeRules, cleanupErr = routerVM.FirewallRules(ctx); cleanupErr == nil {
				var found bool
				for _, rule := range activeRules {
					if rule != nil && rule.Comment == comment {
						cleanupErr = rule.Delete(ctx)
						found = true
						break
					}
				}
				if !found {
					cleanupErr = fmt.Errorf("temporary DHCPv6 ingress diagnostic rule was not found on VM %d", routerVMID)
				}
			}
			if err == nil && cleanupErr != nil {
				err = fmt.Errorf("remove temporary DHCPv6 ingress diagnostic rule: %w", cleanupErr)
			} else if err != nil && cleanupErr != nil {
				err = errors.Join(err, fmt.Errorf("remove temporary DHCPv6 ingress diagnostic rule: %w", cleanupErr))
			}
		}()
	}
	var disableAllDiagnosticNICFirewalls bool = os.Getenv("ORGANESSON_DHCPV6_DISABLE_NIC_FIREWALL") == "1"
	var disableRouterDiagnosticNICFirewall bool = disableAllDiagnosticNICFirewalls || os.Getenv("ORGANESSON_DHCPV6_DISABLE_ROUTER_FIREWALL") == "1"
	if disableRouterDiagnosticNICFirewall {
		type diagnosticNICConfiguration struct {
			vm     *pve.VirtualMachine
			device string
			config string
		}
		var diagnosticNICs []diagnosticNICConfiguration
		var guestNIC string = guestVM.VirtualMachineConfig.Nets[attachment.Placement.Device]
		var disabledGuestNIC string = strings.Replace(guestNIC, ",firewall=1", ",firewall=0", 1)
		if disabledGuestNIC == guestNIC {
			err = fmt.Errorf("diagnostic VM %d NIC %s did not have firewall=1", guestVM.VMID, attachment.Placement.Device)
			return
		}
		if disableAllDiagnosticNICFirewalls {
			diagnosticNICs = append(diagnosticNICs, diagnosticNICConfiguration{vm: guestVM, device: attachment.Placement.Device, config: guestNIC})
		}
		var routerDevice string
		var routerNIC string
		for device, network := range routerVM.VirtualMachineConfig.Nets {
			if networkOptionMap(network)["bridge"] == attachment.Request.Bridge {
				routerDevice = device
				routerNIC = network
				break
			}
		}
		if routerDevice == "" {
			err = fmt.Errorf("diagnostic router VM %d has no NIC on bridge %q", routerVMID, attachment.Request.Bridge)
			return
		}
		var disabledRouterNIC string = strings.Replace(routerNIC, ",firewall=1", ",firewall=0", 1)
		if disabledRouterNIC == routerNIC {
			err = fmt.Errorf("diagnostic router VM %d NIC %s did not have firewall=1", routerVMID, routerDevice)
			return
		}
		diagnosticNICs = append(diagnosticNICs, diagnosticNICConfiguration{vm: routerVM, device: routerDevice, config: routerNIC})
		defer func() {
			var restoreErrors []error
			for index := len(diagnosticNICs) - 1; index >= 0; index-- {
				var restoreTask *pve.Task
				var restoreErr error
				if restoreTask, restoreErr = diagnosticNICs[index].vm.Config(ctx, pve.VirtualMachineOption{Name: diagnosticNICs[index].device, Value: diagnosticNICs[index].config}); restoreErr == nil {
					restoreErr = waitTask(ctx, client, restoreTask)
				}
				if restoreErr != nil {
					restoreErrors = append(restoreErrors, fmt.Errorf("restore VM %d NIC %s firewall: %w", diagnosticNICs[index].vm.VMID, diagnosticNICs[index].device, restoreErr))
				}
			}
			var restoreErr error = errors.Join(restoreErrors...)
			if err == nil && restoreErr != nil {
				err = restoreErr
			} else if err != nil && restoreErr != nil {
				err = errors.Join(err, restoreErr)
			}
		}()
		for _, nic := range diagnosticNICs {
			var disabledNIC string = strings.Replace(nic.config, ",firewall=1", ",firewall=0", 1)
			var task *pve.Task
			if task, err = nic.vm.Config(ctx, pve.VirtualMachineOption{Name: nic.device, Value: disabledNIC}); err != nil {
				return
			}
			if err = waitTask(ctx, client, task); err != nil {
				return
			}
		}
	}
	var reservation string = "id:" + DHCPv6DUIDFromMAC(attachment.Placement.MAC) + ",[" + ipv6Address + "]"
	var configLines []string
	for _, line := range strings.Split(originalConfig.Content, "\n") {
		var trimmed string = strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "dhcp-range=") {
			var fields []string = strings.Split(strings.TrimPrefix(trimmed, "dhcp-range="), ",")
			var firstAddress netip.Addr
			if len(fields) > 0 {
				firstAddress, _ = netip.ParseAddr(strings.TrimSpace(fields[0]))
			}
			if firstAddress.Is6() || strings.Contains(trimmed, ",constructor:") {
				continue
			}
		}
		if trimmed != "" {
			configLines = append(configLines, trimmed)
		}
	}
	var configContent string = strings.Join(configLines, "\n") + "\nlog-dhcp\ndhcp-range=fd42:2::30,fd42:2::40,12h\n"
	var reservationContent string = strings.TrimSpace(originalReservations.Content) + "\n" + reservation + "\n"
	var leaseContent string = removeDHCPv6LeasesByDUID(originalLeases.Content, DHCPv6DUIDFromMAC(attachment.Placement.MAC))
	if _, err = runGuestAgentCommand(ctx, routerVM, settings, routerVMID, "/usr/bin/systemctl", "stop", "dnsmasq.service"); err != nil {
		return
	}
	if err = routerVM.AgentFileWrite(ctx, "/etc/dnsmasq.d/organesson-router.conf", []byte(configContent)); err != nil {
		return
	}
	if err = routerVM.AgentFileWrite(ctx, routerDHCPReservationsFile, []byte(reservationContent)); err != nil {
		return
	}
	if err = routerVM.AgentFileWrite(ctx, "/var/lib/misc/dnsmasq.leases", []byte(leaseContent)); err != nil {
		return
	}
	if _, err = runGuestAgentCommand(ctx, routerVM, settings, routerVMID, "/usr/bin/systemctl", "start", "dnsmasq.service"); err != nil {
		return
	}
	var request GuestNetworkRequest = originalGuest
	request.IPv6Method = "dhcp"
	request.IPv6Address = ""
	request.IPv6Gateway = ""
	request.IPv6DNS = nil
	var captureCommand string = "interface=" + interfaceNameForMAC(attachment.Placement.MAC) + "; exec /usr/bin/timeout 25 /usr/bin/tcpdump -ni \"$interface\" -vv -l 'icmp6 or udp port 546 or 547'"
	var capturePID int
	if capturePID, err = guestVM.AgentExec(ctx, guestAgentCommand("/bin/sh", "-c", captureCommand), ""); err != nil {
		return
	}
	var routerCapturePID int
	if routerCapturePID, err = routerVM.AgentExec(ctx, guestAgentCommand("/bin/sh", "-c", "exec /usr/bin/timeout 25 /usr/bin/tcpdump -ni ens18 -vv -l 'icmp6 or udp port 546 or 547'"), ""); err != nil {
		return
	}
	var activationError error = New(settings).ConfigureGuestNetwork(ctx, request)
	var captureStatus guestAgentExecStatus
	if captureStatus, err = waitForGuestExecExit(ctx, settings, guestVM.Node, int(guestVM.VMID), capturePID, 40); err != nil {
		err = fmt.Errorf("read DHCPv6 guest packet capture: %w", err)
		return
	}
	if captureStatus.ExitCode != 0 && captureStatus.ExitCode != 124 {
		err = fmt.Errorf("DHCPv6 guest packet capture exited %d: %s%s", captureStatus.ExitCode, captureStatus.OutData, captureStatus.ErrData)
		return
	}
	var routerCaptureStatus guestAgentExecStatus
	if routerCaptureStatus, err = waitForGuestExecExit(ctx, settings, routerVM.Node, routerVMID, routerCapturePID, 40); err != nil {
		err = fmt.Errorf("read DHCPv6 router packet capture: %w", err)
		return
	}
	if routerCaptureStatus.ExitCode != 0 && routerCaptureStatus.ExitCode != 124 {
		err = fmt.Errorf("DHCPv6 router packet capture exited %d: %s%s", routerCaptureStatus.ExitCode, routerCaptureStatus.OutData, routerCaptureStatus.ErrData)
		return
	}
	if activationError != nil {
		var guestDiagnostics string
		var routerDiagnostics string
		guestDiagnostics, err = runGuestAgentCommand(ctx, guestVM, settings, int(guestVM.VMID), "/bin/sh", "-c", "echo '--- guest addresses ---'; ip -6 -o address show dev "+interfaceNameForMAC(attachment.Placement.MAC)+"; echo '--- guest connection ---'; nmcli -f GENERAL.STATE,IP6.ADDRESS,IP6.GATEWAY device show "+interfaceNameForMAC(attachment.Placement.MAC)+"; echo '--- capture tools ---'; command -v tcpdump || true; command -v tshark || true; command -v python3 || true; echo '--- NetworkManager log ---'; journalctl -u NetworkManager --no-pager -n 35")
		if err != nil {
			return
		}
		routerDiagnostics, err = runGuestAgentCommand(ctx, routerVM, settings, routerVMID, "/bin/sh", "-c", "echo '--- dnsmasq config ---'; cat /etc/dnsmasq.d/organesson-router.conf; echo '--- reservations ---'; cat /etc/organesson/dnsmasq-reservations; echo '--- dnsmasq log ---'; journalctl -u dnsmasq.service --no-pager -n 60")
		if err != nil {
			return
		}
		var firewallRules []*pve.FirewallRule
		if firewallRules, err = guestVM.FirewallRules(ctx); err == nil {
			for _, rule := range firewallRules {
				if rule != nil {
					guestDiagnostics += fmt.Sprintf("\nfirewall rule: %+v", rule)
				}
			}
		}
		var firewallLogs []*pve.FirewallLogEntry
		if firewallLogs, err = guestVM.FirewallLog(ctx, 0, 100, 0, 0); err == nil {
			for _, entry := range firewallLogs {
				if entry != nil {
					guestDiagnostics += fmt.Sprintf("\nfirewall log %d: %s", entry.LineNum, entry.Text)
				}
			}
		}
		output = fmt.Sprintf("activation error: %v\n--- guest packet capture ---\n%s%s\n--- router packet capture ---\n%s%s\n--- guest ---\n%s\n--- router ---\n%s", activationError, captureStatus.OutData, captureStatus.ErrData, routerCaptureStatus.OutData, routerCaptureStatus.ErrData, guestDiagnostics, routerDiagnostics)
		err = fmt.Errorf("DHCPv6 did not obtain its allocated address: %w\n%s", activationError, output)
		return
	}
	if err = New(settings).ReadGuestNetwork(ctx, request); err != nil {
		return
	}
	var connection string = "organesson-" + strings.ReplaceAll(strings.ToLower(attachment.Placement.MAC), ":", "")
	var command string = "nmcli -g ipv6.method,ipv6.dhcp-duid,ipv6.dhcp-iaid connection show " + shellQuote(connection) + "; ip -6 -o address show dev " + interfaceNameForMAC(attachment.Placement.MAC)
	var networkOutput string
	if networkOutput, err = runGuestAgentCommand(ctx, guestVM, settings, int(guestVM.VMID), "/bin/sh", "-c", command); err == nil {
		output = fmt.Sprintf("--- guest packet capture ---\n%s%s\n--- router packet capture ---\n%s%s\n--- guest network ---\n%s", captureStatus.OutData, captureStatus.ErrData, routerCaptureStatus.OutData, routerCaptureStatus.ErrData, networkOutput)
	}
	return
}

// removeDHCPv6LeasesByDUID drops only DHCPv6 lease rows for a test NIC's DUID.
func removeDHCPv6LeasesByDUID(content string, duid string) (filtered string) {
	var lines []string
	for _, line := range strings.Split(content, "\n") {
		var fields []string = strings.Fields(line)
		if len(fields) >= 5 {
			var address netip.Addr
			if address, _ = netip.ParseAddr(fields[2]); address.Is6() && strings.EqualFold(fields[4], duid) {
				continue
			}
		}
		if strings.TrimSpace(line) != "" {
			lines = append(lines, line)
		}
	}
	if len(lines) > 0 {
		filtered = strings.Join(lines, "\n") + "\n"
	}
	return
}

// readLiveGuestNetworkConfiguration snapshots the current per-NIC NetworkManager settings for safe restoration.
func readLiveGuestNetworkConfiguration(ctx context.Context, vm *pve.VirtualMachine, attachment struct {
	Request           NetworkAttachmentRequest   `json:"request"`
	Placement         NetworkAttachmentPlacement `json:"placement"`
	Guest             *GuestNetworkRequest       `json:"guest_network"`
	AddressPrefix     string                     `json:"address_prefix,omitempty"`
	IPv6AddressPrefix string                     `json:"ipv6_address_prefix,omitempty"`
}, settings config.ProxmoxConfiguration) (request GuestNetworkRequest, err error) {
	var connection string = "organesson-" + strings.ReplaceAll(strings.ToLower(attachment.Placement.MAC), ":", "")
	var output string
	var command string = "nmcli -g ipv4.method,ipv4.addresses,ipv4.gateway,ipv4.dns,ipv4.never-default,ipv6.method,ipv6.addresses,ipv6.gateway,ipv6.dns,ipv6.never-default connection show " + shellQuote(connection) + " | tr -d '\\\\'"
	if output, err = runGuestAgentCommand(ctx, vm, settings, int(vm.VMID), "/bin/sh", "-c", command); err != nil {
		return
	}
	var values []string = strings.Split(strings.TrimSuffix(output, "\n"), "\n")
	if len(values) != 10 {
		err = fmt.Errorf("expected 10 saved NetworkManager values, got %d: %q", len(values), output)
		return
	}
	request = GuestNetworkRequest{
		Node: vm.Node, VMID: strconv.Itoa(int(vm.VMID)), VMOperationKey: attachment.Request.VMOperationKey,
		AttachmentKey: attachment.Request.AttachmentOperationKey, Bridge: attachment.Request.Bridge,
		NetworkOperationKey: attachment.Request.NetworkOperationKey, Placement: attachment.Placement,
		EnforceAddressFilter: attachment.Request.EnforceAddressFilter, AllowedAddresses: append([]string{}, attachment.Request.AllowedAddresses...),
		NeverDefault: values[4] == "yes", IPv6NeverDefault: values[9] == "yes",
	}
	switch values[0] {
	case "auto":
		request.Method = "dhcp"
	case "manual":
		request.Method = "static"
		request.Address = firstNetworkManagerValue(values[1])
		request.Gateway = values[2]
		request.DNS = splitNetworkManagerValues(values[3])
	case "disabled":
		request.Method = "disabled"
	default:
		err = fmt.Errorf("unsupported current IPv4 method %q", values[0])
		return
	}
	switch values[5] {
	case "auto":
		request.IPv6Method = "slaac"
	case "dhcp":
		request.IPv6Method = "dhcp"
	case "manual":
		request.IPv6Method = "static"
		request.IPv6Address = firstNetworkManagerValue(values[6])
		request.IPv6Gateway = values[7]
		request.IPv6DNS = splitNetworkManagerValues(values[8])
	case "disabled":
		request.IPv6Method = "disabled"
	default:
		err = fmt.Errorf("unsupported current IPv6 method %q", values[5])
	}
	return
}

// firstNetworkManagerValue selects the first address from an nmcli multi-value field.
func firstNetworkManagerValue(value string) (first string) {
	var values []string = splitNetworkManagerValues(value)
	if len(values) > 0 {
		first = values[0]
	}
	return
}

// splitNetworkManagerValues parses nmcli's comma/semicolon-separated multi-value output.
func splitNetworkManagerValues(value string) (values []string) {
	for _, entry := range strings.FieldsFunc(value, func(r rune) bool { return r == ',' || r == ';' }) {
		if strings.TrimSpace(entry) != "" {
			values = append(values, strings.TrimSpace(entry))
		}
	}
	return
}

// restartRouterDNSMasq reloads the managed DHCPv4/DHCPv6 service and reports its command result.
func restartRouterDNSMasq(ctx context.Context, vm *pve.VirtualMachine, settings config.ProxmoxConfiguration) (err error) {
	var pid int
	if pid, err = vm.AgentExec(ctx, guestAgentCommand("/usr/bin/systemctl", "restart", "dnsmasq.service"), ""); err != nil {
		return
	}
	var status guestAgentExecStatus
	if status, err = waitForGuestExecExit(ctx, settings, vm.Node, int(vm.VMID), pid, 30); err != nil {
		return
	}
	if status.ExitCode != 0 {
		err = fmt.Errorf("dnsmasq restart failed: %s", strings.TrimSpace(status.ErrData+" "+status.OutData))
	}
	return
}

// runGuestAgentCommand executes a diagnostic command inside one already identified VM.
func runGuestAgentCommand(ctx context.Context, vm *pve.VirtualMachine, settings config.ProxmoxConfiguration, vmid int, command ...string) (output string, err error) {
	if len(command) == 0 {
		err = errors.New("guest-agent diagnostic command is empty")
		return
	}
	var wrappedCommand []string = guestAgentCommand(command...)
	var pid int
	if pid, err = vm.AgentExec(ctx, wrappedCommand, ""); err != nil {
		return
	}
	var status guestAgentExecStatus
	if status, err = waitForGuestExecExit(ctx, settings, vm.Node, vmid, pid, 30); err != nil {
		return
	}
	output = status.OutData + status.ErrData
	if status.ExitCode != 0 {
		err = fmt.Errorf("guest-agent diagnostic exited %d: %s", status.ExitCode, output)
	}
	return
}

// interfaceNameForMAC finds the guest interface with a stable Organesson NIC MAC.
func interfaceNameForMAC(mac string) (script string) {
	script = "$(for file in /sys/class/net/*/address; do [ \"$(tr '[:upper:]' '[:lower:]' < \"$file\")\" = " + shellQuote(strings.ToLower(mac)) + " ] && { printf '%s' \"${file%/address}\" | sed 's#.*/##'; break; }; done)"
	return
}
