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
