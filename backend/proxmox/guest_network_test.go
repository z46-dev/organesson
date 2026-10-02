package proxmox

import (
	"os/exec"
	"strings"
	"testing"
)

func TestGuestNetworkScriptsAreValidAndNICScoped(t *testing.T) {
	var request GuestNetworkRequest = GuestNetworkRequest{
		Node: "pve1", VMID: "901", VMOperationKey: "og-vm",
		AttachmentKey: "og-nic", Bridge: "vmbr0", Placement: NetworkAttachmentPlacement{Device: "net1", MAC: "02:11:22:33:44:55"},
		Method: "static", Address: "10.0.0.100/8", Gateway: "10.0.0.1", DNS: []string{"10.0.0.2"},
	}
	for _, script := range []string{guestNetworkScript(request, false), guestNetworkScript(request, true), guestNetworkReadScript(request)} {
		var command *exec.Cmd = exec.Command("bash", "-n")
		command.Stdin = strings.NewReader(script)
		if output, err := command.CombinedOutput(); err != nil {
			t.Fatalf("guest script is not valid shell: %v: %s\n%s", err, output, script)
		}
		if !strings.Contains(script, "organesson-021122334455") || !strings.Contains(script, "02:11:22:33:44:55") && strings.Contains(script, "connection add") {
			t.Fatalf("guest script is not scoped to its managed NIC: %s", script)
		}
	}
	if !strings.Contains(guestNetworkScript(request, false), "trap 'rm -f \"$0\"' EXIT") {
		t.Fatal("ephemeral guest setup script must remove itself after execution")
	}
}

func TestValidateGuestNetworkRequest(t *testing.T) {
	var request GuestNetworkRequest = GuestNetworkRequest{
		Node: "pve1", VMID: "901", VMOperationKey: "og-vm", AttachmentKey: "og-nic", Bridge: "vmbr0",
		Placement: NetworkAttachmentPlacement{Device: "net1", MAC: "02:11:22:33:44:55"},
		Method:    "static", Address: "192.0.2.5/24", Gateway: "192.0.2.1",
	}
	if err := validateGuestNetworkRequest(request); err != nil {
		t.Fatalf("valid static guest configuration rejected: %v", err)
	}
	request.Address = "192.0.2.5"
	if err := validateGuestNetworkRequest(request); err == nil {
		t.Fatal("static configuration without a prefix was accepted")
	}
	request.Address = "192.0.2.5/24"
	request.Method = "dhcp"
	if err := validateGuestNetworkRequest(request); err == nil {
		t.Fatal("DHCP configuration with static values was accepted")
	}
}

func TestGuestAgentCommandUsesOptionalSELinuxWrapper(t *testing.T) {
	var command []string = guestAgentCommand("/usr/bin/bash", "/run/organesson-network.sh")
	if len(command) != 6 || command[0] != "/bin/sh" || command[1] != "-c" || !strings.Contains(command[2], "/usr/libexec/qemu-ga/fsfreeze-hook.d/organesson-qga-exec") || command[4] != "/usr/bin/bash" || command[5] != "/run/organesson-network.sh" {
		t.Fatalf("guest command did not preserve arguments through the optional SELinux wrapper: %#v", command)
	}
}
