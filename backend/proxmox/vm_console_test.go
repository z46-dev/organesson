package proxmox

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/fasthttp/websocket"
	serverWebsocket "github.com/gorilla/websocket"
	"github.com/z46-dev/organesson/backend/config"
)

// TestConsoleUsesServerSideTokenAndShortLivedTicket exercises the PVE VNC ticket and WebSocket handshake.
func TestConsoleUsesServerSideTokenAndShortLivedTicket(t *testing.T) {
	var ticketIssued bool
	var socketAuthorized bool
	var fakePVE *httptest.Server
	var upgrader serverWebsocket.Upgrader = serverWebsocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}
	fakePVE = httptest.NewTLSServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if request.Header.Get("Authorization") != "PVEAPIToken=test!organesson=secret" {
			t.Errorf("missing server-side Proxmox API token")
		}
		switch {
		case request.Method == http.MethodGet && request.URL.Path == "/api2/json/nodes/pve1/status":
			writePVEData(response, map[string]any{"node": "pve1", "status": "online"})
		case request.Method == http.MethodGet && strings.HasSuffix(request.URL.Path, "/qemu/900/status/current"):
			writePVEData(response, map[string]any{"vmid": 900, "name": "console-vm", "status": "running"})
		case request.Method == http.MethodGet && strings.HasSuffix(request.URL.Path, "/qemu/900/config"):
			writePVEData(response, map[string]any{"name": "console-vm", "description": "Organesson managed resource op-1"})
		case request.Method == http.MethodPost && strings.HasSuffix(request.URL.Path, "/qemu/900/vncproxy"):
			if request.URL.Query().Get("websocket") != "1" {
				t.Errorf("vncproxy did not request websocket mode")
			}
			ticketIssued = true
			writePVEData(response, map[string]any{"ticket": "PVEVNC:short-ticket", "port": "5900", "user": "root@pam"})
		case request.Method == http.MethodGet && strings.HasSuffix(request.URL.Path, "/qemu/900/vncwebsocket"):
			if !ticketIssued || request.URL.Query().Get("port") != "5900" || request.URL.Query().Get("vncticket") != "PVEVNC:short-ticket" {
				t.Errorf("incorrect VNC WebSocket ticket query: %s", request.URL.RawQuery)
			}
			socketAuthorized = true
			var connection *serverWebsocket.Conn
			var err error
			if connection, err = upgrader.Upgrade(response, request, nil); err != nil {
				t.Errorf("upgrade fake PVE WebSocket: %v", err)
				return
			}
			defer connection.Close()
			_ = connection.WriteMessage(serverWebsocket.BinaryMessage, []byte("frame"))
		default:
			t.Errorf("unexpected fake PVE request: %s %s", request.Method, request.URL.String())
			writePVEData(response, nil)
		}
	}))
	defer fakePVE.Close()

	var service *Service = New(config.ProxmoxConfiguration{
		APIURL: fakePVE.URL + "/api2/json", APITokenID: "test!organesson", APITokenSecret: "secret", InsecureSkipVerify: true,
	})
	var connection *websocket.Conn
	var password string
	var err error
	if connection, password, err = service.DialVMConsole(context.Background(), "pve1", "900", "op-1"); err != nil {
		t.Fatalf("dial managed VM console: %v", err)
	}
	defer connection.Close()
	if password != "PVEVNC:short-ticket" {
		t.Fatalf("unexpected console password: %q", password)
	}
	var messageType int
	var payload []byte
	if messageType, payload, err = connection.ReadMessage(); err != nil {
		t.Fatalf("read proxied test frame: %v", err)
	}
	if messageType != websocket.BinaryMessage || string(payload) != "frame" || !socketAuthorized {
		t.Fatalf("unexpected proxied console frame: type=%d payload=%q authorized=%t", messageType, payload, socketAuthorized)
	}
}
