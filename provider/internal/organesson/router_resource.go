package organesson

import (
	"context"
	"embed"
	"encoding/binary"
	"fmt"
	"net/http"
	"net/netip"
	"os"
	"path/filepath"
	"strings"

	"github.com/hashicorp/terraform-plugin-sdk/v2/diag"
	"github.com/hashicorp/terraform-plugin-sdk/v2/helper/schema"
)

//go:embed assets/router-linux.sh
var routerAssets embed.FS

// resourceRouter configures a Debian Linux guest as a small DHCP/DNS router.
func resourceRouter() (resource *schema.Resource) {
	resource = &schema.Resource{
		CreateContext: routerCreate,
		ReadContext:   guestSetupRead,
		DeleteContext: guestSetupDelete,
		Importer:      &schema.ResourceImporter{StateContext: schema.ImportStatePassthroughContext},
		Schema: map[string]*schema.Schema{
			"dhcp_end":             {Type: schema.TypeString, Required: true, ForceNew: true, Description: "Last IPv4 host offered by DHCP."},
			"dhcp_start":           {Type: schema.TypeString, Required: true, ForceNew: true, Description: "First IPv4 host offered by DHCP."},
			"dns_servers":          {Type: schema.TypeSet, Required: true, ForceNew: true, MinItems: 1, Elem: &schema.Schema{Type: schema.TypeString}, Description: "DNS servers advertised to clients."},
			"egress_attachment_id": {Type: schema.TypeString, Optional: true, ForceNew: true, Description: "Optional WAN attachment; when present, the router enables filtered forwarding and NAT."},
			"execution_status":     {Type: schema.TypeString, Computed: true, Description: "Guest execution result."},
			"exit_code":            {Type: schema.TypeInt, Computed: true, Description: "Guest script exit code."},
			"lan_attachment_id":    {Type: schema.TypeString, Required: true, ForceNew: true, Description: "LAN attachment whose MAC is configured as the router interface."},
			"lan_ipv4_address":     {Type: schema.TypeString, Required: true, ForceNew: true, Description: "Router LAN address and prefix, for example 192.168.1.1/24."},
			"sha256":               {Type: schema.TypeString, Computed: true, Description: "Digest of the generic router setup package."},
			"subnet":               {Type: schema.TypeString, Required: true, ForceNew: true, Description: "Router LAN subnet in CIDR notation."},
			"virtual_machine_id":   {Type: schema.TypeString, Required: true, ForceNew: true, Description: "Organesson VM resource identifier for a prepared Debian router guest."},
		},
	}
	return
}

// routerCreate validates the network settings and runs the embedded router setup through Organesson.
func routerCreate(ctx context.Context, data *schema.ResourceData, meta interface{}) (diagnostics diag.Diagnostics) {
	var (
		client     *apiClient = meta.(*apiClient)
		lanAddress netip.Prefix
		subnet     netip.Prefix
		dhcpStart  netip.Addr
		dhcpEnd    netip.Addr
		err        error
	)
	if lanAddress, err = netip.ParsePrefix(data.Get("lan_ipv4_address").(string)); err != nil {
		diagnostics = diag.Errorf("router LAN address must be an IPv4 CIDR")
		return
	}
	if subnet, err = netip.ParsePrefix(data.Get("subnet").(string)); err != nil || !subnet.Addr().Is4() || !lanAddress.Addr().Is4() || subnet != subnet.Masked() || lanAddress.Bits() != subnet.Bits() || !subnet.Contains(lanAddress.Addr()) {
		diagnostics = diag.Errorf("router subnet must be IPv4 and contain its LAN address")
		return
	}
	if dhcpStart, err = netip.ParseAddr(data.Get("dhcp_start").(string)); err != nil {
		diagnostics = diag.Errorf("DHCP start must be a valid IPv4 address")
		return
	}
	if dhcpEnd, err = netip.ParseAddr(data.Get("dhcp_end").(string)); err != nil || !dhcpStart.Is4() || !dhcpEnd.Is4() || !subnet.Contains(dhcpStart) || !subnet.Contains(dhcpEnd) || dhcpStart.Compare(dhcpEnd) > 0 || dhcpStart.Compare(lanAddress.Addr()) <= 0 && dhcpEnd.Compare(lanAddress.Addr()) >= 0 {
		diagnostics = diag.Errorf("DHCP range must be ordered IPv4 hosts within the router subnet and exclude the router address")
		return
	}
	if dhcpStart == subnet.Masked().Addr() || dhcpEnd == routerBroadcastAddress(subnet) {
		diagnostics = diag.Errorf("DHCP range cannot include the network or broadcast address")
		return
	}
	var vmID string
	if vmID, err = remoteID(data.Get("virtual_machine_id").(string)); err != nil {
		diagnostics = diag.FromErr(err)
		return
	}
	var lanMAC string
	if lanMAC, err = routerAttachmentMAC(ctx, client, data.Get("lan_attachment_id").(string)); err != nil {
		diagnostics = diag.FromErr(err)
		return
	}
	var wanMAC string
	if data.Get("egress_attachment_id").(string) != "" {
		if wanMAC, err = routerAttachmentMAC(ctx, client, data.Get("egress_attachment_id").(string)); err != nil {
			diagnostics = diag.FromErr(err)
			return
		}
	}
	var dnsServers []string
	for _, raw := range data.Get("dns_servers").(*schema.Set).List() {
		var address netip.Addr
		if address, err = netip.ParseAddr(raw.(string)); err != nil || !address.Is4() {
			diagnostics = diag.Errorf("router DNS servers must be valid IPv4 addresses")
			return
		}
		dnsServers = append(dnsServers, address.String())
	}
	var script []byte
	if script, err = routerAssets.ReadFile("assets/router-linux.sh"); err != nil {
		diagnostics = diag.FromErr(err)
		return
	}
	var directory string
	if directory, err = os.MkdirTemp("", "organesson-router-"); err != nil {
		diagnostics = diag.FromErr(err)
		return
	}
	defer os.RemoveAll(directory)
	if err = os.WriteFile(filepath.Join(directory, "router.sh"), script, 0755); err != nil {
		diagnostics = diag.FromErr(err)
		return
	}
	var configuration string = strings.Join([]string{
		"LAN_MAC=" + shellQuoted(lanMAC),
		"WAN_MAC=" + shellQuoted(wanMAC),
		"LAN_ADDRESS=" + shellQuoted(lanAddress.Addr().String()),
		"LAN_SUBNET=" + shellQuoted(subnet.String()),
		"DHCP_START=" + shellQuoted(dhcpStart.String()),
		"DHCP_END=" + shellQuoted(dhcpEnd.String()),
		"DNS_SERVERS=(" + strings.Join(dnsServers, " ") + ")",
	}, "\n") + "\n"
	if err = os.WriteFile(filepath.Join(directory, "organesson-router.conf"), []byte(configuration), 0600); err != nil {
		diagnostics = diag.FromErr(err)
		return
	}
	var artifact artifactPackage
	if artifact, err = packageArtifact(directory, "router.sh"); err != nil {
		diagnostics = diag.FromErr(err)
		return
	}
	var result struct {
		Execution guestSetupResult `json:"execution"`
	}
	if err = client.requestBytes(ctx, http.MethodPost, "/api/v1/virtual-machines/"+vmID+"/guest-setup", "application/vnd.organesson.artifact+gzip", map[string]string{
		"X-Organesson-Artifact-SHA256":     artifact.SHA256,
		"X-Organesson-Artifact-Entrypoint": "router.sh",
	}, artifact.Archive, &result); err != nil {
		diagnostics = diag.FromErr(err)
		return
	}
	if result.Execution.SHA256 != artifact.SHA256 || result.Execution.Status != "succeeded" || result.Execution.ExitCode != 0 {
		diagnostics = diag.Errorf("Organesson did not confirm successful router configuration")
		return
	}
	if err = data.Set("execution_status", result.Execution.Status); err != nil {
		diagnostics = diag.FromErr(err)
		return
	}
	if err = data.Set("exit_code", result.Execution.ExitCode); err != nil {
		diagnostics = diag.FromErr(err)
		return
	}
	if err = data.Set("sha256", artifact.SHA256); err != nil {
		diagnostics = diag.FromErr(err)
		return
	}
	data.SetId(localResourceID("router", fmt.Sprintf("%s:%s:%s", vmID, lanMAC, artifact.SHA256)))
	return
}

// routerAttachmentMAC returns the deterministic guest-visible NIC address for an Organesson attachment.
func routerAttachmentMAC(ctx context.Context, client *apiClient, attachmentID string) (mac string, err error) {
	var id string
	if id, err = remoteID(attachmentID); err != nil {
		return
	}
	var result networkAttachmentResult
	if err = client.request(ctx, http.MethodGet, "/api/v1/network-attachments/"+id, nil, &result); err != nil {
		return
	}
	if result.Configuration.Placement.MAC == "" {
		err = fmt.Errorf("network attachment %q has no Proxmox MAC address", attachmentID)
		return
	}
	mac = result.Configuration.Placement.MAC
	return
}

func shellQuoted(value string) (quoted string) {
	quoted = "'" + strings.ReplaceAll(value, "'", "'\\''") + "'"
	return
}

func routerBroadcastAddress(prefix netip.Prefix) (address netip.Addr) {
	var network [4]byte = prefix.Masked().Addr().As4()
	var hostBits uint = uint(32 - prefix.Bits())
	var hostMask uint32
	if hostBits == 32 {
		hostMask = ^uint32(0)
	} else {
		hostMask = (uint32(1) << hostBits) - 1
	}
	var broadcast [4]byte
	binary.BigEndian.PutUint32(broadcast[:], binary.BigEndian.Uint32(network[:])|hostMask)
	address = netip.AddrFrom4(broadcast)
	return
}
