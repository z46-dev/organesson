package organesson

import (
	"net/netip"
	"os/exec"
	"strings"
	"testing"
)

func TestEmbeddedRouterSetupIsValidShell(t *testing.T) {
	var script []byte
	var err error
	if script, err = routerAssets.ReadFile("assets/router-linux.sh"); err != nil {
		t.Fatalf("read embedded router setup: %v", err)
	}
	var command *exec.Cmd = exec.Command("bash", "-n")
	command.Stdin = strings.NewReader(string(script))
	if output, commandErr := command.CombinedOutput(); commandErr != nil {
		t.Fatalf("embedded router setup is not valid shell: %v: %s", commandErr, output)
	}
}

func TestRouterBroadcastAddress(t *testing.T) {
	var prefix = netip.MustParsePrefix("192.168.2.0/24")
	if address := routerBroadcastAddress(prefix); address.String() != "192.168.2.255" {
		t.Fatalf("unexpected subnet broadcast address: %s", address)
	}
}

func TestRouterWANAllowedNetworksUsesOnlyValidatedScopes(t *testing.T) {
	var attachment networkAttachmentResult
	attachment.Configuration.Request.AllowedAddresses = []string{"172.29.0.0/16", "192.0.2.10", "fd42::/64"}
	attachment.Configuration.AddressPrefix = "172.29.0.0/16"
	attachment.Configuration.IPv6AddressPrefix = "fd42::/64"
	var networks []string
	var err error
	if networks, err = routerWANAllowedNetworks(attachment); err != nil {
		t.Fatalf("valid router egress scopes were rejected: %v", err)
	}
	if strings.Join(networks, ",") != "172.29.0.0/16,192.0.2.10/32,fd42::/64" {
		t.Fatalf("unexpected normalized router egress scopes: %#v", networks)
	}
	attachment.Configuration.Request.AllowedAddresses = []string{"not-a-network"}
	if _, err = routerWANAllowedNetworks(attachment); err == nil {
		t.Fatal("invalid router egress scope was accepted")
	}
}
