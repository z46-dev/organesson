package proxmox

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/z46-dev/organesson/backend/config"
)

func TestValidateSDNNetworkRequestRestrictsToIsolatedNetworks(t *testing.T) {
	var valid SDNNetworkRequest = SDNNetworkRequest{
		Name: "shared-student-lan", Mode: "managed", Subnet: "192.168.100.0/24", Gateway: "192.168.100.1",
		DHCPEnabled: true, EgressPolicy: "isolated", OperationKey: "og-test-network",
	}
	if err := validateSDNNetworkRequest(valid); err != nil {
		t.Fatalf("valid isolated managed network rejected: %v", err)
	}
	valid.EgressPolicy = "environment-network"
	if err := validateSDNNetworkRequest(valid); err == nil {
		t.Fatal("network with an uplink policy was accepted by isolated SDN driver")
	}
	valid = SDNNetworkRequest{Name: "link", Mode: "unmanaged-layer-2", EgressPolicy: "isolated", OperationKey: "og-private"}
	if err := validateSDNNetworkRequest(valid); err != nil {
		t.Fatalf("valid bare layer-2 network rejected: %v", err)
	}
	valid.DHCPEnabled = true
	if err := validateSDNNetworkRequest(valid); err == nil {
		t.Fatal("unmanaged layer-2 network accepted DHCP")
	}
}

func TestValidateSDNNetworkRequestRejectsNonCanonicalOrInvalidSubnet(t *testing.T) {
	for _, subnet := range []string{"192.168.100.1/24", "2001:db8::/64", "not-a-prefix"} {
		var request SDNNetworkRequest = SDNNetworkRequest{
			Name: "lab", Mode: "managed", Subnet: subnet, Gateway: "192.168.100.1",
			EgressPolicy: "isolated", OperationKey: "og-lab",
		}
		if err := validateSDNNetworkRequest(request); err == nil {
			t.Errorf("accepted invalid subnet %q", subnet)
		}
	}
}

func TestSDNNetworkIdentifiersAreStableAndShort(t *testing.T) {
	var first SDNNetworkPlacement = namesForSDNNetwork("deployment:network")
	var second SDNNetworkPlacement = namesForSDNNetwork("deployment:network")
	if first != second || len(first.Zone) > 8 || len(first.VNet) > 8 || first.Zone == first.VNet {
		t.Fatalf("unexpected SDN names: %#v %#v", first, second)
	}
	if first == namesForSDNNetwork("another:network") {
		t.Fatal("different operation keys collided in SDN naming")
	}
}

type testSDNNetworkDriver struct {
	placement SDNNetworkPlacement
	deleted   bool
}

func (driver *testSDNNetworkDriver) Create(_ context.Context, _ SDNNetworkRequest) (placement SDNNetworkPlacement, err error) {
	placement = driver.placement
	return
}

func (driver *testSDNNetworkDriver) Read(_ context.Context, _ SDNNetworkRequest, _ SDNNetworkPlacement) (err error) {
	return
}

func (driver *testSDNNetworkDriver) Delete(_ context.Context, _ string, _ string) (err error) {
	driver.deleted = true
	return
}

func TestServiceSDNNetworkLifecycleUsesDriver(t *testing.T) {
	var driver *testSDNNetworkDriver = &testSDNNetworkDriver{placement: SDNNetworkPlacement{Zone: "oz123456", VNet: "on123456"}}
	var service *Service = &Service{sdnNetworkDriver: driver, alwaysConfigured: true}
	var placement SDNNetworkPlacement
	var err error
	if placement, err = service.CreateSDNNetwork(context.Background(), SDNNetworkRequest{OperationKey: "test"}); err != nil {
		t.Fatalf("create SDN network: %v", err)
	}
	if placement.VNet != driver.placement.VNet {
		t.Fatalf("unexpected placement: %#v", placement)
	}
	if err = service.DeleteSDNNetwork(context.Background(), placement.VNet, "test"); err != nil || !driver.deleted {
		t.Fatalf("delete SDN network: deleted=%t err=%v", driver.deleted, err)
	}
}

func TestAPISDNNetworkDriverCreatesOnlyIsolatedSimpleZoneAndDeletesIt(t *testing.T) {
	var zones map[string]map[string]any = make(map[string]map[string]any)
	var vnets map[string]map[string]any = make(map[string]map[string]any)
	var subnets map[string]map[string]map[string]any = make(map[string]map[string]map[string]any)
	var server *httptest.Server = httptest.NewTLSServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		var body map[string]any
		if request.Body != nil {
			_ = json.NewDecoder(request.Body).Decode(&body)
		}
		var path string = strings.TrimPrefix(request.URL.Path, "/api2/json")
		var data any
		switch {
		case request.Method == http.MethodGet && path == "/cluster/status":
			data = []map[string]any{}
		case request.Method == http.MethodGet && path == "/cluster/sdn/zones":
			var result []map[string]any
			for _, zone := range zones {
				result = append(result, zone)
			}
			data = result
		case request.Method == http.MethodPost && path == "/cluster/sdn/zones":
			if body["type"] != "simple" || body["bridge"] != nil {
				t.Errorf("zone was not an isolated Simple zone: %#v", body)
			}
			zones[body["zone"].(string)] = body
		case request.Method == http.MethodGet && path == "/cluster/sdn/vnets":
			var result []map[string]any
			for _, vnet := range vnets {
				result = append(result, vnet)
			}
			data = result
		case request.Method == http.MethodGet && path == "/cluster/sdn/ipams/pve/status":
			var vnetName string
			var zoneName string
			for name, vnet := range vnets {
				vnetName = name
				zoneName = vnet["zone"].(string)
			}
			data = []map[string]any{
				{"vnet": vnetName, "zone": zoneName, "ip": "192.168.100.12", "mac": "02:00:00:00:00:12", "hostname": "student-vm", "subnet": "192.168.100.0-24", "vmid": "301"},
				{"vnet": "unrelated", "zone": zoneName, "ip": "192.168.100.13", "vmid": "302"},
			}
		case request.Method == http.MethodPost && path == "/cluster/sdn/vnets":
			vnets[body["vnet"].(string)] = body
		case request.Method == http.MethodGet && strings.HasPrefix(path, "/cluster/sdn/vnets/") && !strings.HasSuffix(path, "/subnets"):
			data = vnets[strings.TrimPrefix(path, "/cluster/sdn/vnets/")]
		case request.Method == http.MethodGet && strings.HasSuffix(path, "/subnets"):
			var vnetName string = strings.TrimSuffix(strings.TrimPrefix(path, "/cluster/sdn/vnets/"), "/subnets")
			data = []map[string]any{}
			for _, subnet := range subnets[vnetName] {
				data = append(data.([]map[string]any), subnet)
			}
		case request.Method == http.MethodPost && strings.HasPrefix(path, "/cluster/sdn/vnets/") && strings.HasSuffix(path, "/subnets"):
			var vnetName string = strings.TrimSuffix(strings.TrimPrefix(path, "/cluster/sdn/vnets/"), "/subnets")
			if subnets[vnetName] == nil {
				subnets[vnetName] = make(map[string]map[string]any)
			}
			body["cidr"] = body["subnet"]
			body["id"] = fmt.Sprintf("%s-%s-%s", vnets[vnetName]["zone"], vnetName, strings.ReplaceAll(body["subnet"].(string), "/", "-"))
			subnets[vnetName][body["id"].(string)] = body
		case request.Method == http.MethodPut && path == "/cluster/sdn/":
			data = "UPID:osmium:00000001:00000001:1234:sdnapply::root@pam:"
		case request.Method == http.MethodGet && strings.Contains(path, "/tasks/"):
			data = map[string]string{"status": "stopped", "exitstatus": "OK"}
		case request.Method == http.MethodDelete && strings.Contains(path, "/subnets/"):
			var parts []string = strings.Split(path, "/")
			delete(subnets[parts[4]], parts[6])
		case request.Method == http.MethodDelete && strings.HasPrefix(path, "/cluster/sdn/vnets/"):
			delete(vnets, strings.TrimPrefix(path, "/cluster/sdn/vnets/"))
		case request.Method == http.MethodDelete && strings.HasPrefix(path, "/cluster/sdn/zones/"):
			delete(zones, strings.TrimPrefix(path, "/cluster/sdn/zones/"))
		default:
			t.Errorf("unexpected Proxmox request %s %s body=%#v", request.Method, path, body)
			writer.WriteHeader(http.StatusNotFound)
			_, _ = writer.Write([]byte(`{"errors":"unexpected request"}`))
			return
		}
		if request.Method == http.MethodPut && path == "/cluster/sdn/" {
			_, _ = fmt.Fprintf(writer, `{"data":%q}`, data)
			return
		}
		if request.Method == http.MethodGet && strings.Contains(path, "/tasks/") {
			_, _ = fmt.Fprintf(writer, `{"data":{"status":"stopped","exitstatus":"OK"}}`)
			return
		}
		if data == nil {
			_, _ = writer.Write([]byte(`{"data":null}`))
			return
		}
		var response []byte
		if response, _ = json.Marshal(map[string]any{"data": data}); len(response) > 0 {
			_, _ = writer.Write(response)
		}
	}))
	defer server.Close()
	var driver *apiSDNNetworkDriver = &apiSDNNetworkDriver{settings: config.ProxmoxConfiguration{
		APIURL: server.URL + "/api2/json", APITokenID: "organesson@test!test", APITokenSecret: "unused", InsecureSkipVerify: true,
	}}
	var request SDNNetworkRequest = SDNNetworkRequest{
		Name: "shared-student-lan", Mode: "managed", Subnet: "192.168.100.0/24", Gateway: "192.168.100.1",
		DHCPEnabled: true, EgressPolicy: "isolated", OperationKey: "og-test-network",
	}
	var placement SDNNetworkPlacement
	var err error
	if placement, err = driver.Create(context.Background(), request); err != nil {
		t.Fatalf("create isolated SDN network: %v", err)
	}
	if zones[placement.Zone]["dhcp"] != "dnsmasq" || vnets[placement.VNet]["zone"] != placement.Zone || len(subnets[placement.VNet]) != 1 {
		t.Fatalf("unexpected isolated SDN state: zones=%#v vnets=%#v subnets=%#v", zones, vnets, subnets)
	}
	if err = driver.Read(context.Background(), request, placement); err != nil {
		t.Fatalf("refresh created isolated SDN network: %v", err)
	}
	var ipamEntries []SDNIPAMEntry
	var ipamState string
	if ipamEntries, ipamState, err = driver.ReadIPAM(context.Background(), request, placement); err != nil {
		t.Fatalf("read network IPAM: %v", err)
	}
	if ipamState != "available" || len(ipamEntries) != 1 || ipamEntries[0].IP != "192.168.100.12" || ipamEntries[0].VMID != "301" {
		t.Fatalf("unexpected filtered IPAM entries: state=%q entries=%#v", ipamState, ipamEntries)
	}
	zones[placement.Zone]["dhcp"] = "unexpected"
	if err = driver.Read(context.Background(), request, placement); err == nil {
		t.Fatal("SDN zone DHCP drift was not detected")
	}
	zones[placement.Zone]["dhcp"] = "dnsmasq"
	if err = driver.Delete(context.Background(), placement.VNet, request.OperationKey); err != nil {
		t.Fatalf("delete isolated SDN network: %v", err)
	}
	if len(zones) != 0 || len(vnets) != 0 || len(subnets[placement.VNet]) != 0 {
		t.Fatalf("SDN destroy left resources behind: zones=%#v vnets=%#v subnets=%#v", zones, vnets, subnets)
	}
}
