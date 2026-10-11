package proxmox

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
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
			var resources []map[string]any = []map[string]any{
				{"type": "node", "node": "pve1", "status": "online", "maxcpu": 16, "maxmem": 17179869184, "mem": 8589934592, "cpu": 0.2},
				{"type": "node", "node": "pve2", "status": "online", "maxcpu": 16, "maxmem": 17179869184, "mem": 2147483648, "cpu": 0.05},
				{"type": "qemu", "vmid": 156, "node": "pve1", "name": "fedora-source", "status": "stopped", "maxcpu": 4, "maxmem": 8589934592},
			}
			if cloneCreated {
				resources = append(resources, map[string]any{"type": "qemu", "vmid": 900, "node": "pve2", "name": "alice-fedora", "status": "stopped", "maxcpu": 2, "maxmem": 4294967296})
			}
			writePVEData(response, resources)
		case request.Method == http.MethodGet && request.URL.Path == "/api2/json/storage":
			writePVEData(response, []map[string]any{{"storage": "laas", "type": "nfs", "shared": 1, "content": "images,backup,rootdir"}})
		case request.Method == http.MethodGet && request.URL.Path == "/api2/json/nodes/pve1/status":
			writePVEData(response, map[string]any{"node": "pve1", "status": "online"})
		case request.Method == http.MethodGet && request.URL.Path == "/api2/json/nodes/pve2/status":
			writePVEData(response, map[string]any{"node": "pve2", "status": "online"})
		case request.Method == http.MethodGet && request.URL.Path == "/api2/json/nodes/pve1/qemu/156/status/current":
			writePVEData(response, map[string]any{"vmid": "156", "name": "fedora-source", "status": "stopped"})
		case request.Method == http.MethodGet && request.URL.Path == "/api2/json/nodes/pve1/qemu/156/config":
			writePVEData(response, map[string]any{"name": "fedora-source", "scsi0": "laas:vm-156-disk-0,size=32G", "net0": "virtio=AA:BB:CC:DD:EE:FF,bridge=vmbr0"})
		case request.Method == http.MethodGet && request.URL.Path == "/api2/json/cluster/nextid":
			writePVEData(response, "900")
		case request.Method == http.MethodPost && request.URL.Path == "/api2/json/nodes/pve1/qemu/156/clone":
			cloneRequest = body
			cloneCount++
			cloneCreated = true
			writePVEData(response, "UPID:pve1:1:1:1:qmclone:900:root@pam:")
		case request.Method == http.MethodGet && strings.HasSuffix(request.URL.Path, "/tasks/UPID:pve1:1:1:1:qmclone:900:root@pam:/status"):
			writePVEData(response, map[string]any{"status": "stopped", "exitstatus": "OK"})
		case request.Method == http.MethodGet && request.URL.Path == "/api2/json/nodes/pve2/qemu/900/status/current":
			writePVEData(response, map[string]any{"vmid": "900", "name": "alice-fedora", "status": "stopped"})
		case request.Method == http.MethodGet && request.URL.Path == "/api2/json/nodes/pve2/qemu/900/config":
			writePVEData(response, map[string]any{"name": "alice-fedora", "description": "Organesson managed resource og-test", "boot": "order=scsi0;ide2;net0", "scsi0": "laas:vm-900-disk-0,size=" + bootDiskSize, "ide2": "isos:iso/fedora.iso,media=cdrom", "net0": "virtio=AA:BB:CC:DD:EE:FF,bridge=vmbr0"})
		case request.Method == http.MethodPost && request.URL.Path == "/api2/json/nodes/pve2/qemu/900/config":
			configRequest = body
			writePVEData(response, "UPID:pve1:2:2:2:qmconfig:900:root@pam:")
		case request.Method == http.MethodGet && strings.HasSuffix(request.URL.Path, "/tasks/UPID:pve1:2:2:2:qmconfig:900:root@pam:/status"):
			writePVEData(response, map[string]any{"status": "stopped", "exitstatus": "OK"})
		case request.Method == http.MethodPut && request.URL.Path == "/api2/json/nodes/pve2/qemu/900/resize":
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
		SourceVMID: "156", Name: "alice-fedora", Pool: "class-labs", Storage: "laas", Cores: 2,
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
	if placement.VMID != "900" || placement.Node != "pve2" || placement.PowerState != "stopped" || cloneCount != 1 || resizeCount != 2 {
		t.Fatalf("unexpected Proxmox placement: %#v", placement)
	}
	if cloneRequest["full"] != float64(1) || cloneRequest["name"] != "alice-fedora" || cloneRequest["pool"] != "class-labs" || cloneRequest["storage"] != "laas" || cloneRequest["target"] != "pve2" || cloneRequest["description"] != "Organesson managed resource og-test" {
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
		case request.Method == http.MethodGet && request.URL.Path == "/api2/json/cluster/resources":
			writePVEData(response, []map[string]any{{"type": "qemu", "vmid": 901, "node": "pve1", "name": "managed-vm", "status": vmState}})
		case request.Method == http.MethodGet && request.URL.Path == "/api2/json/cluster/status":
			writePVEData(response, []any{})
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
	}, inventory: &vmInventoryCache{}}
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

func TestVMReadUsesCurrentClusterNodeAfterMigration(t *testing.T) {
	var fakePVE *httptest.Server = httptest.NewTLSServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		switch {
		case request.Method == http.MethodGet && request.URL.Path == "/api2/json/cluster/status":
			writePVEData(response, []any{})
		case request.Method == http.MethodGet && request.URL.Path == "/api2/json/cluster/resources":
			writePVEData(response, []map[string]any{{"type": "qemu", "vmid": 902, "node": "tungsten", "name": "moved-vm", "status": "running"}})
		case request.Method == http.MethodGet && request.URL.Path == "/api2/json/nodes/tungsten/status":
			writePVEData(response, map[string]any{"node": "tungsten", "status": "online"})
		case request.Method == http.MethodGet && request.URL.Path == "/api2/json/nodes/tungsten/qemu/902/status/current":
			writePVEData(response, map[string]any{"vmid": 902, "name": "moved-vm", "status": "running"})
		case request.Method == http.MethodGet && request.URL.Path == "/api2/json/nodes/tungsten/qemu/902/config":
			writePVEData(response, map[string]any{"name": "moved-vm", "description": "Organesson managed resource og-migrated"})
		default:
			t.Errorf("unexpected migrated-VM lookup: %s %s", request.Method, request.URL.String())
			writePVEData(response, nil)
		}
	}))
	defer fakePVE.Close()
	var driver *apiVMDriver = &apiVMDriver{settings: config.ProxmoxConfiguration{
		APIURL: fakePVE.URL + "/api2/json", APITokenID: "test!organesson", APITokenSecret: "secret", InsecureSkipVerify: true,
	}, inventory: &vmInventoryCache{}}
	var placement VMPlacement
	var err error
	if placement, err = driver.Read(context.Background(), "osmium", "902", "og-migrated"); err != nil {
		t.Fatalf("read migrated VM using stale node hint: %v", err)
	}
	if placement.Node != "tungsten" || placement.PowerState != "running" {
		t.Fatalf("cluster placement was not refreshed after migration: %#v", placement)
	}
}

// TestReadManyUsesOneClusterInventoryAndVerifiesEveryVM proves batch refresh avoids repeated cluster scans.
func TestReadManyUsesOneClusterInventoryAndVerifiesEveryVM(t *testing.T) {
	var inventoryReads atomic.Int32
	var fakePVE *httptest.Server = httptest.NewTLSServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		switch {
		case request.Method == http.MethodGet && request.URL.Path == "/api2/json/cluster/status":
			writePVEData(response, []any{})
		case request.Method == http.MethodGet && request.URL.Path == "/api2/json/cluster/resources":
			inventoryReads.Add(1)
			writePVEData(response, []map[string]any{
				{"type": "qemu", "vmid": 901, "node": "osmium", "name": "first", "status": "running"},
				{"type": "qemu", "vmid": 902, "node": "tungsten", "name": "second", "status": "stopped"},
			})
		case request.Method == http.MethodGet && strings.HasPrefix(request.URL.Path, "/api2/json/nodes/") && strings.HasSuffix(request.URL.Path, "/status"):
			writePVEData(response, map[string]any{"status": "online"})
		case request.Method == http.MethodGet && strings.HasSuffix(request.URL.Path, "/status/current"):
			if strings.Contains(request.URL.Path, "/901/") {
				writePVEData(response, map[string]any{"vmid": 901, "name": "first", "status": "running"})
			} else {
				writePVEData(response, map[string]any{"vmid": 902, "name": "second", "status": "stopped"})
			}
		case request.Method == http.MethodGet && strings.HasSuffix(request.URL.Path, "/config"):
			if strings.Contains(request.URL.Path, "/901/") {
				writePVEData(response, map[string]any{"description": "Organesson managed resource og-first"})
			} else {
				writePVEData(response, map[string]any{"description": "Organesson managed resource og-second"})
			}
		default:
			t.Errorf("unexpected fake PVE request: %s %s", request.Method, request.URL.String())
			writePVEData(response, nil)
		}
	}))
	defer fakePVE.Close()

	var driver *apiVMDriver = &apiVMDriver{settings: config.ProxmoxConfiguration{
		APIURL: fakePVE.URL + "/api2/json", APITokenID: "test!organesson", APITokenSecret: "secret", InsecureSkipVerify: true,
	}, inventory: &vmInventoryCache{}}
	var requests []VMReadRequest = []VMReadRequest{
		{VMID: "901", OperationKey: "og-first"},
		{VMID: "902", OperationKey: "og-second"},
	}
	var results []VMReadResult
	var err error
	if results, err = driver.ReadMany(context.Background(), requests); err != nil {
		t.Fatalf("read VM batch: %v", err)
	}
	if len(results) != len(requests) || results[0].Err != nil || results[1].Err != nil {
		t.Fatalf("VM batch did not return both placements: %#v", results)
	}
	if results[0].Placement.Node != "osmium" || results[0].Placement.PowerState != "running" || results[1].Placement.Node != "tungsten" || results[1].Placement.PowerState != "stopped" {
		t.Fatalf("VM batch placements were incorrect: %#v", results)
	}
	if inventoryReads.Load() != 1 {
		t.Fatalf("batch refreshed cluster inventory %d times, want one", inventoryReads.Load())
	}
	if _, err = driver.Read(context.Background(), "old-node-hint", "901", "og-first"); err != nil {
		t.Fatalf("read VM using shared inventory cache: %v", err)
	}
	if inventoryReads.Load() != 1 {
		t.Fatalf("single VM read ignored the shared inventory cache: %d inventory reads", inventoryReads.Load())
	}
	driver.invalidateVMInventory()
	if _, err = driver.Read(context.Background(), "old-node-hint", "901", "og-first"); err != nil {
		t.Fatalf("read VM after invalidating inventory cache: %v", err)
	}
	if inventoryReads.Load() != 2 {
		t.Fatalf("invalidated cache was not refreshed, got %d inventory reads", inventoryReads.Load())
	}
}

func writePVEData(response http.ResponseWriter, data any) {
	response.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(response).Encode(map[string]any{"data": data})
}
