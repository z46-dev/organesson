package proxmox

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/fasthttp/websocket"
	pve "github.com/luthermonson/go-proxmox"
)

type vncProxyResponse struct {
	Data struct {
		Ticket string          `json:"ticket"`
		Port   json.RawMessage `json:"port"`
		User   string          `json:"user"`
	} `json:"data"`
}

// DialVMConsole creates a short-lived PVE VNC ticket and opens the server-side WebSocket.
func (service *Service) DialVMConsole(ctx context.Context, node string, id string, operationKey string) (connection *websocket.Conn, password string, err error) {
	if service == nil || !service.Configured() {
		err = ErrNotConfigured
		return
	}
	if _, err = service.consoleVM(ctx, node, id, operationKey); err != nil {
		return
	}
	var ticketResponse vncProxyResponse
	var apiURL *url.URL
	if apiURL, err = url.Parse(service.settings.APIURL); err != nil {
		return
	}
	if service.settings.ConsoleNodeHostTemplate != "" {
		var consoleHost string = strings.ReplaceAll(service.settings.ConsoleNodeHostTemplate, "{node}", node)
		if apiURL.Port() == "" {
			apiURL.Host = consoleHost
		} else {
			apiURL.Host = net.JoinHostPort(consoleHost, apiURL.Port())
		}
	}
	var endpoint string = strings.TrimSuffix(apiURL.String(), "/") + "/nodes/" + url.PathEscape(node) + "/qemu/" + url.PathEscape(id) + "/vncproxy?websocket=1"
	var request *http.Request
	if request, err = http.NewRequestWithContext(ctx, http.MethodPost, endpoint, nil); err != nil {
		return
	}
	request.Header.Set("Authorization", "PVEAPIToken="+service.settings.APITokenID+"="+service.settings.APITokenSecret)
	var httpClient *http.Client = &http.Client{Timeout: 15 * time.Second}
	if httpClient.Transport, err = service.consoleTransport(); err != nil {
		return
	}
	var response *http.Response
	if response, err = httpClient.Do(request); err != nil {
		return
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		err = fmt.Errorf("Proxmox VNC proxy request returned HTTP %d", response.StatusCode)
		return
	}
	if err = json.NewDecoder(response.Body).Decode(&ticketResponse); err != nil {
		return
	}
	var port int
	if err = json.Unmarshal(ticketResponse.Data.Port, &port); err != nil {
		var portString string
		if err = json.Unmarshal(ticketResponse.Data.Port, &portString); err != nil {
			return
		}
		if port, err = strconv.Atoi(portString); err != nil {
			return
		}
	}
	if ticketResponse.Data.Ticket == "" || port < 1 || port > 65535 {
		err = errors.New("Proxmox returned an invalid VNC proxy ticket")
		return
	}
	apiURL.Path = "/api2/json/nodes/" + node + "/qemu/" + id + "/vncwebsocket"
	apiURL.RawQuery = url.Values{"port": {strconv.Itoa(port)}, "vncticket": {ticketResponse.Data.Ticket}}.Encode()
	var websocketURL string = "wss" + strings.TrimPrefix(apiURL.String(), "https")
	var headers http.Header = make(http.Header)
	headers.Set("Authorization", "PVEAPIToken="+service.settings.APITokenID+"="+service.settings.APITokenSecret)
	var dialer websocket.Dialer = websocket.Dialer{
		HandshakeTimeout: 15 * time.Second,
		Subprotocols:     []string{"binary"},
	}
	if dialer.TLSClientConfig, err = service.consoleTLSConfig(); err != nil {
		return
	}
	if connection, _, err = dialer.DialContext(ctx, websocketURL, headers); err != nil {
		return
	}
	password = ticketResponse.Data.Ticket
	return
}

func (service *Service) consoleVM(ctx context.Context, node string, id string, operationKey string) (vm *pve.VirtualMachine, err error) {
	var vmid int
	if vmid, err = parseVMID(id); err != nil {
		return
	}
	var client *pve.Client
	if client, err = newAPIClient(service.settings); err != nil {
		return
	}
	var pveNode *pve.Node
	if pveNode, err = client.Node(ctx, node); err != nil {
		return
	}
	if vm, err = pveNode.VirtualMachine(ctx, vmid); err != nil {
		return
	}
	err = verifyManagedVM(vm, operationKey)
	return
}

func (service *Service) consoleTLSConfig() (configuration *tls.Config, err error) {
	configuration = &tls.Config{MinVersion: tls.VersionTLS12, InsecureSkipVerify: service.settings.InsecureSkipVerify}
	if service.settings.RootCABundlePath != "" {
		var contents []byte
		if contents, err = os.ReadFile(service.settings.RootCABundlePath); err != nil {
			return
		}
		var roots *x509.CertPool = x509.NewCertPool()
		if !roots.AppendCertsFromPEM(contents) {
			err = errors.New("Proxmox CA bundle contains no valid certificates")
			return
		}
		configuration.RootCAs = roots
	}
	return
}

func (service *Service) consoleTransport() (transport *http.Transport, err error) {
	var tlsConfig *tls.Config
	if tlsConfig, err = service.consoleTLSConfig(); err != nil {
		return
	}
	transport = &http.Transport{TLSClientConfig: tlsConfig}
	return
}
