package proxmox

import "testing"

func TestNetworkAttachmentMACIsStableAndLocallyAdministered(t *testing.T) {
	var first string = networkAttachmentMAC("deployment:vm:default")
	var second string = networkAttachmentMAC("deployment:vm:default")
	if first != second || first[:3] != "02:" || first == networkAttachmentMAC("deployment:vm:other") {
		t.Fatalf("unexpected deterministic network MAC: %q, %q", first, second)
	}
}

func TestNetworkOptionMapReadsProxmoxDeviceMACAndBridge(t *testing.T) {
	var options map[string]string = networkOptionMap("virtio=02:11:22:33:44:55,bridge=on123456,firewall=0")
	if options["macaddr"] != "02:11:22:33:44:55" || options["bridge"] != "on123456" {
		t.Fatalf("failed to parse Proxmox NIC options: %#v", options)
	}
}

func TestNextNetworkDeviceChoosesFirstFreeSlot(t *testing.T) {
	var device string
	var err error
	if device, err = nextNetworkDevice(map[string]string{"net0": "", "net2": ""}); err != nil {
		t.Fatalf("select network device: %v", err)
	}
	if device != "net1" {
		t.Fatalf("expected net1, got %q", device)
	}
}

func TestValidateNetworkAttachmentRequestRequiresMarkedVNet(t *testing.T) {
	var request NetworkAttachmentRequest = NetworkAttachmentRequest{
		Node: "pve1", VMID: "100", VMOperationKey: "vm-operation", Bridge: "on123456", AttachmentOperationKey: "attachment-operation",
	}
	if err := validateNetworkAttachmentRequest(request); err == nil {
		t.Fatal("accepted a managed VNet without its ownership operation key")
	}
	request.NetworkOperationKey = "network-operation"
	if err := validateNetworkAttachmentRequest(request); err != nil {
		t.Fatalf("rejected marked VNet attachment: %v", err)
	}
	request.NetworkOperationKey = ""
	request.Bridge = "vmbr0,tag=4"
	if err := validateNetworkAttachmentRequest(request); err == nil {
		t.Fatal("accepted a bridge value containing Proxmox configuration delimiters")
	}
}
