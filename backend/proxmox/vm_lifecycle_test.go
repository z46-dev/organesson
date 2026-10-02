package proxmox

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/z46-dev/organesson/backend/config"
)

// TestCloneVMUsesPVECloneTasksAndAppliesTheRequestedSafeBaseline exercises the real driver against a fake PVE API.
func TestCloneVMUsesPVECloneTasksAndAppliesTheRequestedSafeBaseline(t *testing.T) {
	var cloneRequest map[string]any
	var configRequest map[string]any
	var resizeRequest map[string]any
	var cloneCreated bool
	var cloneCount int
	var resizeCount int
	var bootDiskSize string = "32G"
	var fakePVE *httptest.Server
	fakePVE = httptest.NewTLSServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if request.Header.Get("Authorization") != "PVEAPIToken=test!organesson=secret" {
			t.Errorf("missing Proxmox token header")
		}
		var body map[string]any
		if request.Body != nil {
			_ = json.NewDecoder(request.Body).Decode(&body)
		}
		switch {
		case request.Method == http.MethodGet && request.URL.Path == "/api2/json/cluster/status":
			writePVEData(response, []any{})
		case request.Method == http.MethodGet && request.URL.Path == "/api2/json/cluster/resources":
			var resources []map[string]any = []map[string]any{{"type": "qemu", "vmid": 156, "node": "pve1", "name": "fedora-source", "status": "stopped"}}
			if cloneCreated {
				resources = append(resources, map[string]any{"type": "qemu", "vmid": 900, "node": "pve1", "name": "alice-fedora", "status": "stopped"})
			}
			writePVEData(response, resources)
		case request.Method == http.MethodGet && request.URL.Path == "/api2/json/nodes/pve1/status":
			writePVEData(response, map[string]any{"node": "pve1", "status": "online"})
		case request.Method == http.MethodGet && request.URL.Path == "/api2/json/nodes/pve1/qemu/156/status/current":
			writePVEData(response, map[string]any{"vmid": "156", "name": "fedora-source", "status": "stopped"})
		case request.Method == http.MethodGet && request.URL.Path == "/api2/json/nodes/pve1/qemu/156/config":
			writePVEData(response, map[string]any{"name": "fedora-source", "scsi0": "local-lvm:vm-156-disk-0,size=32G", "net0": "virtio=AA:BB:CC:DD:EE:FF,bridge=vmbr0"})
		case request.Method == http.MethodGet && request.URL.Path == "/api2/json/cluster/nextid":
			writePVEData(response, "900")
		case request.Method == http.MethodPost && request.URL.Path == "/api2/json/nodes/pve1/qemu/156/clone":
			cloneRequest = body
			cloneCount++
			cloneCreated = true
			writePVEData(response, "UPID:pve1:1:1:1:qmclone:900:root@pam:")
		case request.Method == http.MethodGet && strings.HasSuffix(request.URL.Path, "/tasks/UPID:pve1:1:1:1:qmclone:900:root@pam:/status"):
			writePVEData(response, map[string]any{"status": "stopped", "exitstatus": "OK"})
		case request.Method == http.MethodGet && request.URL.Path == "/api2/json/nodes/pve1/qemu/900/status/current":
			writePVEData(response, map[string]any{"vmid": "900", "name": "alice-fedora", "status": "stopped"})
		case request.Method == http.MethodGet && request.URL.Path == "/api2/json/nodes/pve1/qemu/900/config":
			writePVEData(response, map[string]any{"name": "alice-fedora", "description": "Organesson managed resource og-test", "boot": "order=scsi0;ide2;net0", "scsi0": "local-lvm:vm-900-disk-0,size=" + bootDiskSize, "ide2": "isos:iso/fedora.iso,media=cdrom", "net0": "virtio=AA:BB:CC:DD:EE:FF,bridge=vmbr0"})
		case request.Method == http.MethodPost && request.URL.Path == "/api2/json/nodes/pve1/qemu/900/config":
			configRequest = body
			writePVEData(response, "UPID:pve1:2:2:2:qmconfig:900:root@pam:")
		case request.Method == http.MethodGet && strings.HasSuffix(request.URL.Path, "/tasks/UPID:pve1:2:2:2:qmconfig:900:root@pam:/status"):
			writePVEData(response, map[string]any{"status": "stopped", "exitstatus": "OK"})
		case request.Method == http.MethodPut && request.URL.Path == "/api2/json/nodes/pve1/qemu/900/resize":
			resizeCount++
			if resizeCount == 1 {
				response.WriteHeader(http.StatusInternalServerError)
				return
			}
			resizeRequest = body
			bootDiskSize = "64G"
			writePVEData(response, "UPID:pve1:3:3:3:qmresize:900:root@pam:")
		case request.Method == http.MethodGet && strings.HasSuffix(request.URL.Path, "/tasks/UPID:pve1:3:3:3:qmresize:900:root@pam:/status"):
			writePVEData(response, map[string]any{"status": "stopped", "exitstatus": "OK"})
		default:
			t.Errorf("unexpected fake PVE request: %s %s", request.Method, request.URL.String())
			writePVEData(response, nil)
		}
	}))
	defer fakePVE.Close()

	var settings config.ProxmoxConfiguration = config.ProxmoxConfiguration{
		APIURL:             fakePVE.URL + "/api2/json",
		APITokenID:         "test!organesson",
		APITokenSecret:     "secret",
		InsecureSkipVerify: true,
	}
	var driver *apiVMDriver = &apiVMDriver{settings: settings}
	var placement VMPlacement
	var err error
	var cloneRequestInput VMCloneRequest = VMCloneRequest{
		SourceVMID: "156", Name: "alice-fedora", Pool: "class-labs", Storage: "local-lvm", Cores: 2,
		MemoryMiB: 4096, BootDiskGiB: 64, OperationKey: "og-test",
	}
	if _, err = driver.Clone(context.Background(), cloneRequestInput); err == nil {
		t.Fatal("first request should expose the simulated interrupted resize")
	}
	if cloneCount != 1 || resizeCount != 1 {
		t.Fatalf("failed request should leave one recoverable VM: clones=%d resizes=%d", cloneCount, resizeCount)
	}
	if placement, err = driver.Clone(context.Background(), cloneRequestInput); err != nil {
		t.Fatalf("recover interrupted clone: %v", err)
	}
	if placement.VMID != "900" || placement.Node != "pve1" || placement.PowerState != "stopped" || cloneCount != 1 || resizeCount != 2 {
		t.Fatalf("unexpected Proxmox placement: %#v", placement)
	}
	if cloneRequest["full"] != float64(1) || cloneRequest["name"] != "alice-fedora" || cloneRequest["pool"] != "class-labs" || cloneRequest["storage"] != "local-lvm" || cloneRequest["description"] != "Organesson managed resource og-test" {
		t.Fatalf("clone request did not apply target metadata: %#v", cloneRequest)
	}
	if configRequest["cores"] != float64(2) || configRequest["memory"] != float64(4096) || configRequest["delete"] != "ide2,net0" || configRequest["boot"] != "order=scsi0" {
		t.Fatalf("clone baseline did not apply compute size, remove installer media/NICs, and boot from disk: %#v", configRequest)
	}
	if resizeRequest["disk"] != "scsi0" || resizeRequest["size"] != "+32G" {
		t.Fatalf("unexpected boot-disk resize request: %#v", resizeRequest)
	}
}

// TestVMDriverRequiresOwnershipMarkerBeforePowerOrDelete proves stale IDs cannot touch unrelated guests.
func TestVMDriverRequiresOwnershipMarkerBeforePowerOrDelete(t *testing.T) {
	var vmState string = "stopped"
	var vmDescription string = "Organesson managed resource og-owned"
	var powerActions int
	var deletes int
	var fakePVE *httptest.Server
	fakePVE = httptest.NewTLSServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		switch {
		case request.Method == http.MethodGet && request.URL.Path == "/api2/json/nodes/pve1/status":
			writePVEData(response, map[string]any{"node": "pve1", "status": "online"})
		case request.Method == http.MethodGet && request.URL.Path == "/api2/json/nodes/pve1/qemu/901/status/current":
			writePVEData(response, map[string]any{"vmid": 901, "name": "managed-vm", "status": vmState})
		case request.Method == http.MethodGet && request.URL.Path == "/api2/json/nodes/pve1/qemu/901/config":
			writePVEData(response, map[string]any{"name": "managed-vm", "description": vmDescription})
		case request.Method == http.MethodPost && strings.HasPrefix(request.URL.Path, "/api2/json/nodes/pve1/qemu/901/status/"):
			powerActions++
			if strings.HasSuffix(request.URL.Path, "/start") {
				vmState = "running"
			} else if strings.HasSuffix(request.URL.Path, "/shutdown") {
				vmState = "stopped"
			} else {
				t.Errorf("unexpected PVE power action path: %s", request.URL.Path)
			}
			writePVEData(response, "UPID:pve1:1:1:1:qmpower:901:root@pam:")
		case request.Method == http.MethodDelete && request.URL.Path == "/api2/json/nodes/pve1/qemu/901":
			deletes++
			writePVEData(response, "UPID:pve1:2:2:2:qmdestroy:901:root@pam:")
		case request.Method == http.MethodGet && strings.Contains(request.URL.Path, "/tasks/UPID:") && strings.HasSuffix(request.URL.Path, "/status"):
			writePVEData(response, map[string]any{"status": "stopped", "exitstatus": "OK"})
		default:
			t.Errorf("unexpected fake PVE request: %s %s", request.Method, request.URL.String())
			writePVEData(response, nil)
		}
	}))
	defer fakePVE.Close()
	var driver *apiVMDriver = &apiVMDriver{settings: config.ProxmoxConfiguration{
		APIURL: fakePVE.URL + "/api2/json", APITokenID: "test!organesson", APITokenSecret: "secret", InsecureSkipVerify: true,
	}}
	var ctx context.Context = context.Background()
	if _, err := driver.Power(ctx, "pve1", "901", "og-stale", "start"); err == nil {
		t.Fatal("power action must reject a mismatched ownership marker")
	}
	if err := driver.Delete(ctx, "pve1", "901", "og-stale"); err == nil {
		t.Fatal("delete must reject a mismatched ownership marker")
	}
	if powerActions != 0 || deletes != 0 {
		t.Fatalf("mismatched ownership marker reached a destructive endpoint: power=%d delete=%d", powerActions, deletes)
	}
	var placement VMPlacement
	var err error
	if placement, err = driver.Power(ctx, "pve1", "901", "og-owned", "start"); err != nil {
		t.Fatalf("authorized power action: %v", err)
	}
	if placement.PowerState != "running" || powerActions != 1 {
		t.Fatalf("expected live running state after one start: placement=%#v power=%d", placement, powerActions)
	}
	if placement, err = driver.Power(ctx, "pve1", "901", "og-owned", "start"); err != nil {
		t.Fatalf("repeat authorized start action: %v", err)
	}
	if placement.PowerState != "running" || powerActions != 1 {
		t.Fatalf("starting an already-running VM must be a no-op: placement=%#v power=%d", placement, powerActions)
	}
	if placement, err = driver.Power(ctx, "pve1", "901", "og-owned", "stop"); err != nil {
		t.Fatalf("authorized stop action: %v", err)
	}
	if placement.PowerState != "stopped" || powerActions != 2 {
		t.Fatalf("expected live stopped state after one stop: placement=%#v power=%d", placement, powerActions)
	}
	if placement, err = driver.Power(ctx, "pve1", "901", "og-owned", "stop"); err != nil {
		t.Fatalf("repeat authorized stop action: %v", err)
	}
	if placement.PowerState != "stopped" || powerActions != 2 {
		t.Fatalf("stopping an already-stopped VM must be a no-op: placement=%#v power=%d", placement, powerActions)
	}
	vmState = "running"
	if err = driver.Delete(ctx, "pve1", "901", "og-owned"); err != nil {
		t.Fatalf("authorized VM delete: %v", err)
	}
	if deletes != 1 || powerActions != 3 || vmState != "stopped" {
		t.Fatalf("delete must stop only the owned running VM before purge: deletes=%d power actions=%d state=%s", deletes, powerActions, vmState)
	}
	vmDescription = "Unrelated VM"
	if _, err = driver.Read(ctx, "pve1", "901", "og-owned"); err == nil {
		t.Fatal("refresh must reject a VM whose ownership marker changed")
	}
}

func writePVEData(response http.ResponseWriter, data any) {
	response.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(response).Encode(map[string]any{"data": data})
}
